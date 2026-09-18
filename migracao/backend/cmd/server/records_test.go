// Testes deste arquivo exigem Postgres real — pulam (t.Skip) se
// SALTCORN_GO_TEST_DATABASE_URL não estiver definida. Cobrem GO-017: as
// rotas reais que substituíram a rota de exemplo/placeholder, com o mesmo
// rigor das demais tarefas (Postgres real, HTTP real via httptest,
// identidade delegada real assinada/verificada, não simulada).
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/jackc/pgx/v5"

	"github.com/vjuliani/saltcorn/migracao/backend/internal/identity"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/metadata"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/platform/cutover"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/platform/database"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/platform/outbox"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/platform/shutdown"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/platform/tenancy"
)

const testServiceIdentitySecret = "01234567890123456789012345678901" // 32 bytes, piso de tenancy.NewVerifier

func testDB(t *testing.T) *database.DB {
	t.Helper()
	dsn := os.Getenv("SALTCORN_GO_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("SALTCORN_GO_TEST_DATABASE_URL não definida — pulando teste que exige Postgres real")
	}
	db, err := database.Open(context.Background(), dsn)
	if err != nil {
		t.Fatalf("database.Open() erro inesperado: %v", err)
	}
	t.Cleanup(db.Close)
	return db
}

func sanitizeForSchema(name string) string {
	out := make([]rune, 0, len(name))
	for _, r := range name {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			out = append(out, r)
		} else {
			out = append(out, '_')
		}
	}
	return string(out)
}

// testFixture monta um tenant isolado com: schema de identidade
// (_sc_users), schema de metadados (_sc_tables/_sc_fields), uma tabela
// "widgets" de leitura/escrita pública com um campo "label", um usuário
// com o papel dado, e o registro de ownership de "tables.records" para
// OwnerGo — tudo que uma requisição HTTP real às rotas de GO-017 precisa
// para não ser recusada por um motivo alheio ao que o teste quer provar.
type testFixture struct {
	tenant  tenancy.Tenant
	userID  int
	guard   *cutover.Guard
	tracker *shutdown.Tracker
}

func newTestFixture(t *testing.T, db *database.DB, actorRole identity.RoleID) testFixture {
	t.Helper()
	ctx := context.Background()
	tenant := tenancy.Tenant(fmt.Sprintf("srv_test_%s", sanitizeForSchema(t.Name())))

	if err := db.WithTenant(ctx, "public", func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, fmt.Sprintf(`CREATE SCHEMA IF NOT EXISTS %s`, pgx.Identifier{string(tenant)}.Sanitize()))
		return err
	}); err != nil {
		t.Fatalf("criar schema de teste: %v", err)
	}
	t.Cleanup(func() {
		_ = db.WithTenant(context.Background(), "public", func(ctx context.Context, tx pgx.Tx) error {
			_, err := tx.Exec(ctx, fmt.Sprintf(`DROP SCHEMA IF EXISTS %s CASCADE`, pgx.Identifier{string(tenant)}.Sanitize()))
			return err
		})
		_ = db.WithTenant(context.Background(), "public", func(ctx context.Context, tx pgx.Tx) error {
			_, err := tx.Exec(ctx, "DELETE FROM _sc_capability_ownership WHERE tenant = $1 AND capability = $2", string(tenant), recordsCapability)
			return err
		})
	})

	var userID int
	if err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		if err := identity.EnsureSchema(ctx, tx); err != nil {
			return err
		}
		if err := metadata.EnsureSchema(ctx, database.AsTx(tx)); err != nil {
			return err
		}
		if err := outbox.EnsureSchema(ctx, tx); err != nil {
			return err
		}
		hash, err := identity.HashPassword("hunter2")
		if err != nil {
			return err
		}
		userID, err = identity.CreateUser(ctx, tx, "ator@example.com", hash, actorRole)
		if err != nil {
			return err
		}
		widgets, err := metadata.CreateTable(ctx, database.AsTx(tx), identity.RoleAdmin, "widgets", metadata.TableOptions{})
		if err != nil {
			return err
		}
		_, err = metadata.AddField(ctx, database.AsTx(tx), identity.RoleAdmin, widgets.ID, metadata.FieldDef{Name: "label", Type: metadata.FieldText, Required: true})
		return err
	}); err != nil {
		t.Fatalf("setup do fixture: %v", err)
	}

	if err := db.WithTenant(ctx, "public", func(ctx context.Context, tx pgx.Tx) error {
		return cutover.EnsureSchema(ctx, tx)
	}); err != nil {
		t.Fatalf("cutover.EnsureSchema: %v", err)
	}
	guard := cutover.NewGuard()
	if err := cutover.SwitchOwner(ctx, db, guard, tenant, recordsCapability, cutover.OwnerGo, 2*time.Second); err != nil {
		t.Fatalf("cutover.SwitchOwner: %v", err)
	}

	return testFixture{tenant: tenant, userID: userID, guard: guard, tracker: shutdown.NewTracker()}
}

func mintServiceIdentity(t *testing.T, secret string, sub string, tenant tenancy.Tenant, ttl time.Duration) string {
	t.Helper()
	now := time.Now()
	claims := jwt.MapClaims{
		"sub":    sub,
		"tenant": string(tenant),
		"iat":    now.Unix(),
		"exp":    now.Add(ttl).Unix(),
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, err := token.SignedString([]byte(secret))
	if err != nil {
		t.Fatalf("mintServiceIdentity: %v", err)
	}
	return signed
}

// buildRecordsHandler replica a composição de middlewares de main.go para
// as rotas de registros (tenancy.Middleware + cutover.RequireOwnership,
// sem telemetry.Middleware — irrelevante para o que este teste verifica) —
// duplicação deliberada e pequena (2 linhas), não uma extração de main.go,
// para manter cmd/server sem indireção só para ser testável.
func buildRecordsHandler(t *testing.T, verifier *tenancy.Verifier, guard *cutover.Guard, next http.Handler) http.Handler {
	t.Helper()
	return tenancy.Middleware(verifier, cutover.RequireOwnership(guard, recordsCapability, next))
}

func TestListRecordsHandler_Success(t *testing.T) {
	db := testDB(t)
	fx := newTestFixture(t, db, identity.RolePublic)
	verifier, err := tenancy.NewVerifier([]byte(testServiceIdentitySecret))
	if err != nil {
		t.Fatalf("NewVerifier: %v", err)
	}

	if err := db.WithTenant(context.Background(), fx.tenant, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, "INSERT INTO widgets (label) VALUES ('a'), ('b')")
		return err
	}); err != nil {
		t.Fatalf("seed de widgets: %v", err)
	}

	handler := buildRecordsHandler(t, verifier, fx.guard, listRecordsHandler(fx.tracker, db))
	token := mintServiceIdentity(t, testServiceIdentitySecret, strconv.Itoa(fx.userID), fx.tenant, time.Minute)

	req := httptest.NewRequest(http.MethodGet, "/v1/tenants/"+string(fx.tenant)+"/tables/widgets/records", nil)
	req.SetPathValue("tenant", string(fx.tenant))
	req.SetPathValue("table", "widgets")
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, corpo = %s, esperado 200", rec.Code, rec.Body.String())
	}
	var body struct {
		Items      []map[string]any `json:"items"`
		NextCursor *string          `json:"next_cursor"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decodificar resposta: %v", err)
	}
	if len(body.Items) != 2 {
		t.Fatalf("len(items) = %d, esperado 2: %v", len(body.Items), body.Items)
	}
	if body.NextCursor != nil {
		t.Errorf("next_cursor = %v, esperado nil (só 2 itens, limit padrão bem maior)", *body.NextCursor)
	}
}

// TestListRecordsHandler_InvalidSignatureRejected é a prova, ao nível de
// HTTP real, de "identidade forjada é rejeitada" (critério de aceite de
// GO-017): um token com assinatura inválida (segredo diferente) nunca
// alcança a lógica de negócio.
func TestListRecordsHandler_InvalidSignatureRejected(t *testing.T) {
	db := testDB(t)
	fx := newTestFixture(t, db, identity.RolePublic)
	verifier, err := tenancy.NewVerifier([]byte(testServiceIdentitySecret))
	if err != nil {
		t.Fatalf("NewVerifier: %v", err)
	}
	handler := buildRecordsHandler(t, verifier, fx.guard, listRecordsHandler(fx.tracker, db))

	forged := mintServiceIdentity(t, "outro-segredo-de-32-bytes-aqui!!", strconv.Itoa(fx.userID), fx.tenant, time.Minute)
	req := httptest.NewRequest(http.MethodGet, "/v1/tenants/"+string(fx.tenant)+"/tables/widgets/records", nil)
	req.SetPathValue("tenant", string(fx.tenant))
	req.SetPathValue("table", "widgets")
	req.Header.Set("Authorization", "Bearer "+forged)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, corpo = %s, esperado 401", rec.Code, rec.Body.String())
	}
}

// TestListRecordsHandler_TenantMismatchRejected prova a segunda metade de
// "identidade forjada é rejeitada": token válido, mas para OUTRO tenant.
func TestListRecordsHandler_TenantMismatchRejected(t *testing.T) {
	db := testDB(t)
	fx := newTestFixture(t, db, identity.RolePublic)
	verifier, err := tenancy.NewVerifier([]byte(testServiceIdentitySecret))
	if err != nil {
		t.Fatalf("NewVerifier: %v", err)
	}
	handler := buildRecordsHandler(t, verifier, fx.guard, listRecordsHandler(fx.tracker, db))

	tokenForOtherTenant := mintServiceIdentity(t, testServiceIdentitySecret, strconv.Itoa(fx.userID), "outro-tenant", time.Minute)
	req := httptest.NewRequest(http.MethodGet, "/v1/tenants/"+string(fx.tenant)+"/tables/widgets/records", nil)
	req.SetPathValue("tenant", string(fx.tenant))
	req.SetPathValue("table", "widgets")
	req.Header.Set("Authorization", "Bearer "+tokenForOtherTenant)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, corpo = %s, esperado 403", rec.Code, rec.Body.String())
	}
}

func TestCreateRecordHandler_Success(t *testing.T) {
	db := testDB(t)
	fx := newTestFixture(t, db, identity.RoleAdmin)
	verifier, err := tenancy.NewVerifier([]byte(testServiceIdentitySecret))
	if err != nil {
		t.Fatalf("NewVerifier: %v", err)
	}
	handler := buildRecordsHandler(t, verifier, fx.guard, createRecordHandler(fx.tracker, db))
	token := mintServiceIdentity(t, testServiceIdentitySecret, strconv.Itoa(fx.userID), fx.tenant, time.Minute)

	body := bytes.NewBufferString(`{"label":"novo widget"}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/tenants/"+string(fx.tenant)+"/tables/widgets/records", body)
	req.SetPathValue("tenant", string(fx.tenant))
	req.SetPathValue("table", "widgets")
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Idempotency-Key", "create-widget-1")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, corpo = %s, esperado 201", rec.Code, rec.Body.String())
	}
	var created map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decodificar resposta: %v", err)
	}
	if created["label"] != "novo widget" {
		t.Errorf("label = %v, esperado \"novo widget\"", created["label"])
	}
}

// TestCreateRecordHandler_RetryDoesNotDuplicate é o teste direto do
// critério de aceite "retries não duplicam comandos": duas requisições
// idênticas (mesma Idempotency-Key, mesmo corpo) resultam em UM só
// registro — a segunda é a reprodução do outbox (GO-014), não uma
// segunda execução de CreateRecord.
func TestCreateRecordHandler_RetryDoesNotDuplicate(t *testing.T) {
	db := testDB(t)
	fx := newTestFixture(t, db, identity.RoleAdmin)
	verifier, err := tenancy.NewVerifier([]byte(testServiceIdentitySecret))
	if err != nil {
		t.Fatalf("NewVerifier: %v", err)
	}
	handler := buildRecordsHandler(t, verifier, fx.guard, createRecordHandler(fx.tracker, db))
	token := mintServiceIdentity(t, testServiceIdentitySecret, strconv.Itoa(fx.userID), fx.tenant, time.Minute)

	doRequest := func() *httptest.ResponseRecorder {
		body := bytes.NewBufferString(`{"label":"retry widget"}`)
		req := httptest.NewRequest(http.MethodPost, "/v1/tenants/"+string(fx.tenant)+"/tables/widgets/records", body)
		req.SetPathValue("tenant", string(fx.tenant))
		req.SetPathValue("table", "widgets")
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Idempotency-Key", "retry-key-1")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		return rec
	}

	first := doRequest()
	if first.Code != http.StatusCreated {
		t.Fatalf("primeira requisição: status = %d, corpo = %s", first.Code, first.Body.String())
	}
	var firstBody map[string]any
	_ = json.Unmarshal(first.Body.Bytes(), &firstBody)

	second := doRequest()
	if second.Code != http.StatusCreated {
		t.Fatalf("segunda requisição (retry): status = %d, corpo = %s", second.Code, second.Body.String())
	}
	var secondBody map[string]any
	_ = json.Unmarshal(second.Body.Bytes(), &secondBody)

	if firstBody["id"] != secondBody["id"] {
		t.Errorf("retry criou um registro diferente: primeiro id=%v, segundo id=%v", firstBody["id"], secondBody["id"])
	}

	var count int
	if err := db.WithTenant(context.Background(), fx.tenant, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, "SELECT count(*) FROM widgets WHERE label = 'retry widget'").Scan(&count)
	}); err != nil {
		t.Fatalf("contar widgets: %v", err)
	}
	if count != 1 {
		t.Errorf("count(widgets label='retry widget') = %d, esperado 1 (retry não deveria duplicar)", count)
	}
}

// TestCreateRecordHandler_SameKeyDifferentPayloadConflict é a outra
// metade do critério de aceite de idempotência (GO-014, reexercitado aqui
// via HTTP): mesma chave, payload diferente → 409, nunca um segundo
// registro criado.
func TestCreateRecordHandler_SameKeyDifferentPayloadConflict(t *testing.T) {
	db := testDB(t)
	fx := newTestFixture(t, db, identity.RoleAdmin)
	verifier, err := tenancy.NewVerifier([]byte(testServiceIdentitySecret))
	if err != nil {
		t.Fatalf("NewVerifier: %v", err)
	}
	handler := buildRecordsHandler(t, verifier, fx.guard, createRecordHandler(fx.tracker, db))
	token := mintServiceIdentity(t, testServiceIdentitySecret, strconv.Itoa(fx.userID), fx.tenant, time.Minute)

	post := func(label string) *httptest.ResponseRecorder {
		body := bytes.NewBufferString(`{"label":"` + label + `"}`)
		req := httptest.NewRequest(http.MethodPost, "/v1/tenants/"+string(fx.tenant)+"/tables/widgets/records", body)
		req.SetPathValue("tenant", string(fx.tenant))
		req.SetPathValue("table", "widgets")
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Idempotency-Key", "conflict-key-1")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		return rec
	}

	if rec := post("primeiro"); rec.Code != http.StatusCreated {
		t.Fatalf("primeira requisição: status = %d, corpo = %s", rec.Code, rec.Body.String())
	}
	rec := post("segundo, payload diferente")
	if rec.Code != http.StatusConflict {
		t.Fatalf("segunda requisição (payload diferente, mesma chave): status = %d, corpo = %s, esperado 409", rec.Code, rec.Body.String())
	}
}

func TestCreateRecordHandler_MissingIdempotencyKeyRejected(t *testing.T) {
	db := testDB(t)
	fx := newTestFixture(t, db, identity.RolePublic)
	verifier, err := tenancy.NewVerifier([]byte(testServiceIdentitySecret))
	if err != nil {
		t.Fatalf("NewVerifier: %v", err)
	}
	handler := buildRecordsHandler(t, verifier, fx.guard, createRecordHandler(fx.tracker, db))
	token := mintServiceIdentity(t, testServiceIdentitySecret, strconv.Itoa(fx.userID), fx.tenant, time.Minute)

	body := bytes.NewBufferString(`{"label":"sem chave"}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/tenants/"+string(fx.tenant)+"/tables/widgets/records", body)
	req.SetPathValue("tenant", string(fx.tenant))
	req.SetPathValue("table", "widgets")
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, corpo = %s, esperado 400", rec.Code, rec.Body.String())
	}
}

// TestListRecordsHandler_UnauthorizedTableRejected prova que o papel do
// ator é resolvido de verdade (FindUserByID), não confiado de fora: um
// ator público não lê uma tabela admin-only.
func TestListRecordsHandler_UnauthorizedTableRejected(t *testing.T) {
	db := testDB(t)
	fx := newTestFixture(t, db, identity.RolePublic)
	ctx := context.Background()

	if err := db.WithTenant(ctx, fx.tenant, func(ctx context.Context, tx pgx.Tx) error {
		_, err := metadata.CreateTable(ctx, database.AsTx(tx), identity.RoleAdmin, "secrets", metadata.TableOptions{MinRoleRead: identity.RoleAdmin, MinRoleWrite: identity.RoleAdmin})
		return err
	}); err != nil {
		t.Fatalf("criar tabela secrets: %v", err)
	}

	verifier, err := tenancy.NewVerifier([]byte(testServiceIdentitySecret))
	if err != nil {
		t.Fatalf("NewVerifier: %v", err)
	}
	handler := buildRecordsHandler(t, verifier, fx.guard, listRecordsHandler(fx.tracker, db))
	token := mintServiceIdentity(t, testServiceIdentitySecret, strconv.Itoa(fx.userID), fx.tenant, time.Minute)

	req := httptest.NewRequest(http.MethodGet, "/v1/tenants/"+string(fx.tenant)+"/tables/secrets/records", nil)
	req.SetPathValue("tenant", string(fx.tenant))
	req.SetPathValue("table", "secrets")
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, corpo = %s, esperado 403", rec.Code, rec.Body.String())
	}
}

func TestGetActorHandler_Success(t *testing.T) {
	db := testDB(t)
	fx := newTestFixture(t, db, identity.RoleID(80))
	verifier, err := tenancy.NewVerifier([]byte(testServiceIdentitySecret))
	if err != nil {
		t.Fatalf("NewVerifier: %v", err)
	}
	handler := tenancy.Middleware(verifier, getActorHandler(fx.tracker, db))
	token := mintServiceIdentity(t, testServiceIdentitySecret, strconv.Itoa(fx.userID), fx.tenant, time.Minute)

	req := httptest.NewRequest(http.MethodGet, "/v1/tenants/"+string(fx.tenant)+"/actor", nil)
	req.SetPathValue("tenant", string(fx.tenant))
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, corpo = %s, esperado 200", rec.Code, rec.Body.String())
	}
	var body struct {
		ID     int `json:"id"`
		RoleID int `json:"role_id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decodificar resposta: %v", err)
	}
	if body.ID != fx.userID || body.RoleID != 80 {
		t.Errorf("body = %+v, esperado id=%d role_id=80", body, fx.userID)
	}
}
