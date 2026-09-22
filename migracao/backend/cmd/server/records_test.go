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

	"github.com/vjuliani/saltcorn/migracao/backend/internal/config"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/identity"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/metadata"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/platform/cutover"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/platform/database"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/platform/outbox"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/platform/shutdown"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/platform/tenancy"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/triggers"
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
	// widgetsTableID (GO-040) é o ID de metadata da tabela "widgets" —
	// necessário para criar um trigger real via triggers.CreateTrigger nos
	// testes de disparo síncrono, que precisam de TableID.
	widgetsTableID int
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
	var widgetsTableID int
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
		if err := triggers.EnsureSchema(ctx, tx); err != nil {
			return err
		}
		if err := config.EnsureSchema(ctx, tx); err != nil {
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
		widgetsTableID = widgets.ID
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

	return testFixture{tenant: tenant, userID: userID, guard: guard, tracker: shutdown.NewTracker(), widgetsTableID: widgetsTableID}
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

// testDispatcher devolve um *triggers.Dispatcher sem Expression/RunJSCode
// (nenhum destes testes cria trigger algum na tabela "widgets" — TriggersFor
// nunca encontra nada para disparar, então nunca toca em Expression) — só
// o suficiente para createRecordHandler/updateRecordHandler/
// deleteRecordHandler terem um HooksFor não-nulo para chamar.
func testDispatcher() *triggers.Dispatcher {
	return &triggers.Dispatcher{Actions: triggers.BuiltinActions()}
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
	handler := buildRecordsHandler(t, verifier, fx.guard, createRecordHandler(fx.tracker, db, testDispatcher()))
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
	handler := buildRecordsHandler(t, verifier, fx.guard, createRecordHandler(fx.tracker, db, testDispatcher()))
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
	handler := buildRecordsHandler(t, verifier, fx.guard, createRecordHandler(fx.tracker, db, testDispatcher()))
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
	handler := buildRecordsHandler(t, verifier, fx.guard, createRecordHandler(fx.tracker, db, testDispatcher()))
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

// doCreateWidget cria um widget via createRecordHandler real (HTTP) e
// devolve o registro decodificado — helper comum aos testes de update/
// delete/trigger abaixo, que sempre precisam de um registro existente
// antes de exercitar a rota que estão testando.
func doCreateWidget(t *testing.T, verifier *tenancy.Verifier, fx testFixture, db *database.DB, token, label, idempotencyKey string) map[string]any {
	t.Helper()
	handler := buildRecordsHandler(t, verifier, fx.guard, createRecordHandler(fx.tracker, db, testDispatcher()))
	body := bytes.NewBufferString(fmt.Sprintf(`{"label":%q}`, label))
	req := httptest.NewRequest(http.MethodPost, "/v1/tenants/"+string(fx.tenant)+"/tables/widgets/records", body)
	req.SetPathValue("tenant", string(fx.tenant))
	req.SetPathValue("table", "widgets")
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Idempotency-Key", idempotencyKey)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("doCreateWidget: status = %d, corpo = %s, esperado 201", rec.Code, rec.Body.String())
	}
	var created map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("doCreateWidget: decodificar resposta: %v", err)
	}
	return created
}

func TestUpdateRecordHandler_Success(t *testing.T) {
	db := testDB(t)
	fx := newTestFixture(t, db, identity.RoleAdmin)
	verifier, err := tenancy.NewVerifier([]byte(testServiceIdentitySecret))
	if err != nil {
		t.Fatalf("NewVerifier: %v", err)
	}
	token := mintServiceIdentity(t, testServiceIdentitySecret, strconv.Itoa(fx.userID), fx.tenant, time.Minute)
	created := doCreateWidget(t, verifier, fx, db, token, "original", "update-success-create")
	id := int(created["id"].(float64))
	version := created["_version"].(string)

	handler := buildRecordsHandler(t, verifier, fx.guard, updateRecordHandler(fx.tracker, db, testDispatcher()))
	body := bytes.NewBufferString(fmt.Sprintf(`{"label":"atualizado","_version":%q}`, version))
	req := httptest.NewRequest(http.MethodPatch, fmt.Sprintf("/v1/tenants/%s/tables/widgets/records/%d", fx.tenant, id), body)
	req.SetPathValue("tenant", string(fx.tenant))
	req.SetPathValue("table", "widgets")
	req.SetPathValue("id", strconv.Itoa(id))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Idempotency-Key", "update-success-1")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, corpo = %s, esperado 200", rec.Code, rec.Body.String())
	}
	var updated map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &updated); err != nil {
		t.Fatalf("decodificar resposta: %v", err)
	}
	if updated["label"] != "atualizado" {
		t.Errorf("label = %v, esperado \"atualizado\"", updated["label"])
	}
}

func TestUpdateRecordHandler_MissingVersionRejected(t *testing.T) {
	db := testDB(t)
	fx := newTestFixture(t, db, identity.RoleAdmin)
	verifier, err := tenancy.NewVerifier([]byte(testServiceIdentitySecret))
	if err != nil {
		t.Fatalf("NewVerifier: %v", err)
	}
	token := mintServiceIdentity(t, testServiceIdentitySecret, strconv.Itoa(fx.userID), fx.tenant, time.Minute)
	created := doCreateWidget(t, verifier, fx, db, token, "original", "update-missing-version-create")
	id := int(created["id"].(float64))

	handler := buildRecordsHandler(t, verifier, fx.guard, updateRecordHandler(fx.tracker, db, testDispatcher()))
	body := bytes.NewBufferString(`{"label":"atualizado"}`)
	req := httptest.NewRequest(http.MethodPatch, fmt.Sprintf("/v1/tenants/%s/tables/widgets/records/%d", fx.tenant, id), body)
	req.SetPathValue("tenant", string(fx.tenant))
	req.SetPathValue("table", "widgets")
	req.SetPathValue("id", strconv.Itoa(id))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Idempotency-Key", "update-missing-version-1")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, corpo = %s, esperado 400", rec.Code, rec.Body.String())
	}
}

func TestUpdateRecordHandler_StaleVersionConflict(t *testing.T) {
	db := testDB(t)
	fx := newTestFixture(t, db, identity.RoleAdmin)
	verifier, err := tenancy.NewVerifier([]byte(testServiceIdentitySecret))
	if err != nil {
		t.Fatalf("NewVerifier: %v", err)
	}
	token := mintServiceIdentity(t, testServiceIdentitySecret, strconv.Itoa(fx.userID), fx.tenant, time.Minute)
	created := doCreateWidget(t, verifier, fx, db, token, "original", "update-stale-create")
	id := int(created["id"].(float64))
	staleVersion := created["_version"].(string)

	handler := buildRecordsHandler(t, verifier, fx.guard, updateRecordHandler(fx.tracker, db, testDispatcher()))

	// Primeira atualização com a versão correta: avança a versão.
	body1 := bytes.NewBufferString(fmt.Sprintf(`{"label":"primeira","_version":%q}`, staleVersion))
	req1 := httptest.NewRequest(http.MethodPatch, fmt.Sprintf("/v1/tenants/%s/tables/widgets/records/%d", fx.tenant, id), body1)
	req1.SetPathValue("tenant", string(fx.tenant))
	req1.SetPathValue("table", "widgets")
	req1.SetPathValue("id", strconv.Itoa(id))
	req1.Header.Set("Authorization", "Bearer "+token)
	req1.Header.Set("Idempotency-Key", "update-stale-1")
	rec1 := httptest.NewRecorder()
	handler.ServeHTTP(rec1, req1)
	if rec1.Code != http.StatusOK {
		t.Fatalf("primeira atualização: status = %d, corpo = %s, esperado 200", rec1.Code, rec1.Body.String())
	}

	// Segunda tentativa com a MESMA versão antiga: a versão já avançou.
	body2 := bytes.NewBufferString(fmt.Sprintf(`{"label":"segunda","_version":%q}`, staleVersion))
	req2 := httptest.NewRequest(http.MethodPatch, fmt.Sprintf("/v1/tenants/%s/tables/widgets/records/%d", fx.tenant, id), body2)
	req2.SetPathValue("tenant", string(fx.tenant))
	req2.SetPathValue("table", "widgets")
	req2.SetPathValue("id", strconv.Itoa(id))
	req2.Header.Set("Authorization", "Bearer "+token)
	req2.Header.Set("Idempotency-Key", "update-stale-2")
	rec2 := httptest.NewRecorder()
	handler.ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusConflict {
		t.Fatalf("segunda atualização: status = %d, corpo = %s, esperado 409", rec2.Code, rec2.Body.String())
	}
}

func TestDeleteRecordHandler_Success(t *testing.T) {
	db := testDB(t)
	fx := newTestFixture(t, db, identity.RoleAdmin)
	verifier, err := tenancy.NewVerifier([]byte(testServiceIdentitySecret))
	if err != nil {
		t.Fatalf("NewVerifier: %v", err)
	}
	token := mintServiceIdentity(t, testServiceIdentitySecret, strconv.Itoa(fx.userID), fx.tenant, time.Minute)
	created := doCreateWidget(t, verifier, fx, db, token, "descartável", "delete-success-create")
	id := int(created["id"].(float64))
	version := created["_version"].(string)

	handler := buildRecordsHandler(t, verifier, fx.guard, deleteRecordHandler(fx.tracker, db, testDispatcher()))
	req := httptest.NewRequest(http.MethodDelete, fmt.Sprintf("/v1/tenants/%s/tables/widgets/records/%d?version=%s", fx.tenant, id, version), nil)
	req.SetPathValue("tenant", string(fx.tenant))
	req.SetPathValue("table", "widgets")
	req.SetPathValue("id", strconv.Itoa(id))
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, corpo = %s, esperado 204", rec.Code, rec.Body.String())
	}

	if err := db.WithTenant(context.Background(), fx.tenant, func(ctx context.Context, tx pgx.Tx) error {
		var count int
		if err := tx.QueryRow(ctx, "SELECT count(*) FROM widgets WHERE id = $1", id).Scan(&count); err != nil {
			return err
		}
		if count != 0 {
			t.Errorf("widgets com id=%d = %d, esperado 0 após delete", id, count)
		}
		return nil
	}); err != nil {
		t.Fatalf("verificação pós-delete: %v", err)
	}
}

func TestDeleteRecordHandler_MissingVersionRejected(t *testing.T) {
	db := testDB(t)
	fx := newTestFixture(t, db, identity.RoleAdmin)
	verifier, err := tenancy.NewVerifier([]byte(testServiceIdentitySecret))
	if err != nil {
		t.Fatalf("NewVerifier: %v", err)
	}
	token := mintServiceIdentity(t, testServiceIdentitySecret, strconv.Itoa(fx.userID), fx.tenant, time.Minute)
	created := doCreateWidget(t, verifier, fx, db, token, "descartável", "delete-missing-version-create")
	id := int(created["id"].(float64))

	handler := buildRecordsHandler(t, verifier, fx.guard, deleteRecordHandler(fx.tracker, db, testDispatcher()))
	req := httptest.NewRequest(http.MethodDelete, fmt.Sprintf("/v1/tenants/%s/tables/widgets/records/%d", fx.tenant, id), nil)
	req.SetPathValue("tenant", string(fx.tenant))
	req.SetPathValue("table", "widgets")
	req.SetPathValue("id", strconv.Itoa(id))
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, corpo = %s, esperado 400", rec.Code, rec.Body.String())
	}
}

func TestDeleteRecordHandler_AlreadyDeletedNotFound(t *testing.T) {
	db := testDB(t)
	fx := newTestFixture(t, db, identity.RoleAdmin)
	verifier, err := tenancy.NewVerifier([]byte(testServiceIdentitySecret))
	if err != nil {
		t.Fatalf("NewVerifier: %v", err)
	}
	token := mintServiceIdentity(t, testServiceIdentitySecret, strconv.Itoa(fx.userID), fx.tenant, time.Minute)
	created := doCreateWidget(t, verifier, fx, db, token, "descartável", "delete-twice-create")
	id := int(created["id"].(float64))
	version := created["_version"].(string)

	handler := buildRecordsHandler(t, verifier, fx.guard, deleteRecordHandler(fx.tracker, db, testDispatcher()))
	url := fmt.Sprintf("/v1/tenants/%s/tables/widgets/records/%d?version=%s", fx.tenant, id, version)

	req1 := httptest.NewRequest(http.MethodDelete, url, nil)
	req1.SetPathValue("tenant", string(fx.tenant))
	req1.SetPathValue("table", "widgets")
	req1.SetPathValue("id", strconv.Itoa(id))
	req1.Header.Set("Authorization", "Bearer "+token)
	rec1 := httptest.NewRecorder()
	handler.ServeHTTP(rec1, req1)
	if rec1.Code != http.StatusNoContent {
		t.Fatalf("primeiro delete: status = %d, corpo = %s, esperado 204", rec1.Code, rec1.Body.String())
	}

	req2 := httptest.NewRequest(http.MethodDelete, url, nil)
	req2.SetPathValue("tenant", string(fx.tenant))
	req2.SetPathValue("table", "widgets")
	req2.SetPathValue("id", strconv.Itoa(id))
	req2.Header.Set("Authorization", "Bearer "+token)
	rec2 := httptest.NewRecorder()
	handler.ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusNotFound {
		t.Fatalf("segundo delete: status = %d, corpo = %s, esperado 404", rec2.Code, rec2.Body.String())
	}
}

// TestCreateRecordHandler_ValidateTriggerAbortsWrite_HTTP é o critério de
// aceite central de GO-040: uma requisição HTTP real de criação de
// registro dispara um trigger síncrono NA MESMA TRANSAÇÃO — aqui, um
// trigger WhenValidate cuja ação (webhook sem "url" configurado) falha
// deliberadamente, provando que o erro do Dispatcher realmente aborta a
// escrita inteira (nunca um trigger "melhor esforço" que loga e deixa o
// registro ser criado mesmo assim).
func TestCreateRecordHandler_ValidateTriggerAbortsWrite_HTTP(t *testing.T) {
	db := testDB(t)
	fx := newTestFixture(t, db, identity.RoleAdmin)
	verifier, err := tenancy.NewVerifier([]byte(testServiceIdentitySecret))
	if err != nil {
		t.Fatalf("NewVerifier: %v", err)
	}
	if err := db.WithTenant(context.Background(), fx.tenant, func(ctx context.Context, tx pgx.Tx) error {
		_, err := triggers.CreateTrigger(ctx, tx, triggers.Trigger{
			TableID: fx.widgetsTableID, When: triggers.WhenValidate, Action: triggers.ActionWebhook,
			Configuration: map[string]any{}, // sem "url" — webhookAction falha de propósito
		})
		return err
	}); err != nil {
		t.Fatalf("CreateTrigger: %v", err)
	}

	token := mintServiceIdentity(t, testServiceIdentitySecret, strconv.Itoa(fx.userID), fx.tenant, time.Minute)
	dispatcher := &triggers.Dispatcher{Actions: triggers.BuiltinActions()}
	handler := buildRecordsHandler(t, verifier, fx.guard, createRecordHandler(fx.tracker, db, dispatcher))

	body := bytes.NewBufferString(`{"label":"deveria falhar"}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/tenants/"+string(fx.tenant)+"/tables/widgets/records", body)
	req.SetPathValue("tenant", string(fx.tenant))
	req.SetPathValue("table", "widgets")
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Idempotency-Key", "validate-aborts-1")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code == http.StatusCreated {
		t.Fatalf("status = 201, esperado uma falha (o trigger Validate deveria ter abortado a escrita)")
	}

	if err := db.WithTenant(context.Background(), fx.tenant, func(ctx context.Context, tx pgx.Tx) error {
		var count int
		if err := tx.QueryRow(ctx, "SELECT count(*) FROM widgets WHERE label = 'deveria falhar'").Scan(&count); err != nil {
			return err
		}
		if count != 0 {
			t.Errorf("widgets com label='deveria falhar' = %d, esperado 0 — Validate deveria ter abortado", count)
		}
		return nil
	}); err != nil {
		t.Fatalf("verificação: %v", err)
	}
}

// TestCreateRecordHandler_InsertTriggerEnqueuesEmail_HTTP prova o segundo
// lado do mesmo critério de aceite: um trigger WhenInsert (ação
// send_email) disparado por uma criação HTTP real enfileira o e-mail em
// _sc_outbox NA MESMA TRANSAÇÃO da escrita (visível assim que o handler
// devolve 201, sem esperar nenhum passo assíncrono) — inclusive
// interpolando {{label}} com o valor real da linha recém-criada.
func TestCreateRecordHandler_InsertTriggerEnqueuesEmail_HTTP(t *testing.T) {
	db := testDB(t)
	fx := newTestFixture(t, db, identity.RoleAdmin)
	verifier, err := tenancy.NewVerifier([]byte(testServiceIdentitySecret))
	if err != nil {
		t.Fatalf("NewVerifier: %v", err)
	}
	if err := db.WithTenant(context.Background(), fx.tenant, func(ctx context.Context, tx pgx.Tx) error {
		_, err := triggers.CreateTrigger(ctx, tx, triggers.Trigger{
			TableID: fx.widgetsTableID, When: triggers.WhenInsert, Action: triggers.ActionSendEmail,
			Configuration: map[string]any{"to": "dest@example.com", "subject": "Novo: {{label}}"},
		})
		return err
	}); err != nil {
		t.Fatalf("CreateTrigger: %v", err)
	}

	token := mintServiceIdentity(t, testServiceIdentitySecret, strconv.Itoa(fx.userID), fx.tenant, time.Minute)
	dispatcher := &triggers.Dispatcher{Actions: triggers.BuiltinActions()}
	created := doCreateWidgetWithDispatcher(t, verifier, fx, db, dispatcher, token, "gizmo", "insert-enqueues-email-1")
	_ = created

	if err := db.WithTenant(context.Background(), fx.tenant, func(ctx context.Context, tx pgx.Tx) error {
		var subject string
		err := tx.QueryRow(ctx, `SELECT payload_json->>'subject' FROM _sc_outbox WHERE event_type = $1 ORDER BY id DESC LIMIT 1`, "notify.email").Scan(&subject)
		if err != nil {
			return fmt.Errorf("consultar _sc_outbox: %w", err)
		}
		if subject != "Novo: gizmo" {
			t.Errorf("subject enfileirado = %q, esperado \"Novo: gizmo\"", subject)
		}
		return nil
	}); err != nil {
		t.Fatalf("verificação: %v", err)
	}
}

// doCreateWidgetWithDispatcher é como doCreateWidget, mas com um
// Dispatcher explícito — os testes de trigger precisam do MESMO
// Dispatcher usado para registrar o trigger (testDispatcher() sozinho não
// carrega o catálogo de ações real).
func doCreateWidgetWithDispatcher(t *testing.T, verifier *tenancy.Verifier, fx testFixture, db *database.DB, dispatcher *triggers.Dispatcher, token, label, idempotencyKey string) map[string]any {
	t.Helper()
	handler := buildRecordsHandler(t, verifier, fx.guard, createRecordHandler(fx.tracker, db, dispatcher))
	body := bytes.NewBufferString(fmt.Sprintf(`{"label":%q}`, label))
	req := httptest.NewRequest(http.MethodPost, "/v1/tenants/"+string(fx.tenant)+"/tables/widgets/records", body)
	req.SetPathValue("tenant", string(fx.tenant))
	req.SetPathValue("table", "widgets")
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Idempotency-Key", idempotencyKey)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("doCreateWidgetWithDispatcher: status = %d, corpo = %s, esperado 201", rec.Code, rec.Body.String())
	}
	var created map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("doCreateWidgetWithDispatcher: decodificar resposta: %v", err)
	}
	return created
}

// TestUpdateRecordHandler_ValidateTriggerAbortsWrite_HTTP prova que
// updateRecordHandler também está ligado ao MESMO Dispatcher (não só
// createRecordHandler) — um trigger WhenValidate cuja ação falha
// deliberadamente aborta um PATCH real, e o valor antigo do campo
// permanece intacto.
func TestUpdateRecordHandler_ValidateTriggerAbortsWrite_HTTP(t *testing.T) {
	db := testDB(t)
	fx := newTestFixture(t, db, identity.RoleAdmin)
	verifier, err := tenancy.NewVerifier([]byte(testServiceIdentitySecret))
	if err != nil {
		t.Fatalf("NewVerifier: %v", err)
	}
	token := mintServiceIdentity(t, testServiceIdentitySecret, strconv.Itoa(fx.userID), fx.tenant, time.Minute)
	dispatcher := &triggers.Dispatcher{Actions: triggers.BuiltinActions()}
	created := doCreateWidgetWithDispatcher(t, verifier, fx, db, dispatcher, token, "original", "update-validate-create")
	id := int(created["id"].(float64))
	version := created["_version"].(string)

	if err := db.WithTenant(context.Background(), fx.tenant, func(ctx context.Context, tx pgx.Tx) error {
		_, err := triggers.CreateTrigger(ctx, tx, triggers.Trigger{
			TableID: fx.widgetsTableID, When: triggers.WhenValidate, Action: triggers.ActionWebhook,
			Configuration: map[string]any{}, // sem "url" — webhookAction falha de propósito
		})
		return err
	}); err != nil {
		t.Fatalf("CreateTrigger: %v", err)
	}

	handler := buildRecordsHandler(t, verifier, fx.guard, updateRecordHandler(fx.tracker, db, dispatcher))
	body := bytes.NewBufferString(fmt.Sprintf(`{"label":"nao deveria gravar","_version":%q}`, version))
	req := httptest.NewRequest(http.MethodPatch, fmt.Sprintf("/v1/tenants/%s/tables/widgets/records/%d", fx.tenant, id), body)
	req.SetPathValue("tenant", string(fx.tenant))
	req.SetPathValue("table", "widgets")
	req.SetPathValue("id", strconv.Itoa(id))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Idempotency-Key", "update-validate-1")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code == http.StatusOK {
		t.Fatalf("status = 200, esperado uma falha (o trigger Validate deveria ter abortado o update)")
	}

	if err := db.WithTenant(context.Background(), fx.tenant, func(ctx context.Context, tx pgx.Tx) error {
		var label string
		if err := tx.QueryRow(ctx, "SELECT label FROM widgets WHERE id = $1", id).Scan(&label); err != nil {
			return err
		}
		if label != "original" {
			t.Errorf("label = %q, esperado \"original\" (update deveria ter sido abortado)", label)
		}
		return nil
	}); err != nil {
		t.Fatalf("verificação: %v", err)
	}
}
