// Testes deste arquivo exigem Postgres real — pulam (t.Skip) se
// SALTCORN_GO_TEST_DATABASE_URL não estiver definida. Cobrem GO-019: o
// ciclo do editor via HTTP real (criar tabela/campo, criar view, salvar,
// reabrir, publicar, conflito de edição) — reaproveita os helpers de
// records_test.go (testDB, sanitizeForSchema, mintServiceIdentity,
// testServiceIdentitySecret), mesmo pacote.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/vjuliani/saltcorn/migracao/backend/internal/identity"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/metadata"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/platform/cutover"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/platform/database"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/platform/outbox"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/platform/shutdown"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/platform/tenancy"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/views"
)

// editorFixture monta um tenant isolado com identidade/metadados/outbox/
// views aplicados, um usuário admin, e ownership de tablesSchemaCapability
// e viewsCapability registrado para OwnerGo — tudo que as rotas de
// GO-019 precisam para não recusar por um motivo alheio ao que o teste
// quer provar.
type editorFixture struct {
	tenant   tenancy.Tenant
	adminID  int
	publicID int
	guard    *cutover.Guard
	tracker  *shutdown.Tracker
}

func newEditorFixture(t *testing.T, db *database.DB) editorFixture {
	t.Helper()
	ctx := context.Background()
	tenant := tenancy.Tenant(fmt.Sprintf("editor_test_%s", sanitizeForSchema(t.Name())))

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
			_, err := tx.Exec(ctx, "DELETE FROM _sc_capability_ownership WHERE tenant = $1 AND capability IN ($2, $3)",
				string(tenant), tablesSchemaCapability, viewsCapability)
			return err
		})
	})

	var adminID, publicID int
	if err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		if err := identity.EnsureSchema(ctx, tx); err != nil {
			return err
		}
		if err := metadata.EnsureSchema(ctx, tx); err != nil {
			return err
		}
		if err := outbox.EnsureSchema(ctx, tx); err != nil {
			return err
		}
		if err := views.EnsureSchema(ctx, tx); err != nil {
			return err
		}
		hash, err := identity.HashPassword("hunter2")
		if err != nil {
			return err
		}
		adminID, err = identity.CreateUser(ctx, tx, "admin@example.com", hash, identity.RoleAdmin)
		if err != nil {
			return err
		}
		publicID, err = identity.CreateUser(ctx, tx, "public@example.com", hash, identity.RolePublic)
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
	if err := cutover.SwitchOwner(ctx, db, guard, tenant, tablesSchemaCapability, cutover.OwnerGo, 2*time.Second); err != nil {
		t.Fatalf("cutover.SwitchOwner (schema): %v", err)
	}
	if err := cutover.SwitchOwner(ctx, db, guard, tenant, viewsCapability, cutover.OwnerGo, 2*time.Second); err != nil {
		t.Fatalf("cutover.SwitchOwner (views): %v", err)
	}

	return editorFixture{tenant: tenant, adminID: adminID, publicID: publicID, guard: guard, tracker: shutdown.NewTracker()}
}

func buildEditorHandler(t *testing.T, verifier *tenancy.Verifier, guard *cutover.Guard, capability string, next http.Handler) http.Handler {
	t.Helper()
	return tenancy.Middleware(verifier, cutover.RequireOwnership(guard, capability, next))
}

// TestEditorE2E_CreateTableViewSaveReopenPublish é o teste direto do
// critério de aceite de GO-019: "E2E cria tabela e view, salva, reabre e
// publica com dois papéis; conflito de edição é apresentado sem
// sobrescrever silenciosamente" — via HTTP real (Postgres real,
// ServiceIdentity real assinado/verificado), não um navegador (sem
// ferramenta de browser disponível neste ambiente, ver
// docs/migracao-go/execucoes/GO-018.md).
func TestEditorE2E_CreateTableViewSaveReopenPublish(t *testing.T) {
	db := testDB(t)
	fx := newEditorFixture(t, db)
	verifier, err := tenancy.NewVerifier([]byte(testServiceIdentitySecret))
	if err != nil {
		t.Fatalf("NewVerifier: %v", err)
	}
	adminToken := mintServiceIdentity(t, testServiceIdentitySecret, strconv.Itoa(fx.adminID), fx.tenant, time.Minute)
	publicToken := mintServiceIdentity(t, testServiceIdentitySecret, strconv.Itoa(fx.publicID), fx.tenant, time.Minute)

	tablesHandler := buildEditorHandler(t, verifier, fx.guard, tablesSchemaCapability, createTableHandler(fx.tracker, db))
	fieldsHandler := buildEditorHandler(t, verifier, fx.guard, tablesSchemaCapability, addFieldHandler(fx.tracker, db))
	createViewH := buildEditorHandler(t, verifier, fx.guard, viewsCapability, createViewHandler(fx.tracker, db))
	getViewH := buildEditorHandler(t, verifier, fx.guard, viewsCapability, getViewHandler(fx.tracker, db))
	updateViewH := buildEditorHandler(t, verifier, fx.guard, viewsCapability, updateViewHandler(fx.tracker, db))

	// 1. Criar tabela.
	tableReq := httptest.NewRequest(http.MethodPost, "/v1/tenants/"+string(fx.tenant)+"/tables", bytes.NewBufferString(`{"name":"books"}`))
	tableReq.SetPathValue("tenant", string(fx.tenant))
	tableReq.Header.Set("Authorization", "Bearer "+adminToken)
	tableRec := httptest.NewRecorder()
	tablesHandler.ServeHTTP(tableRec, tableReq)
	if tableRec.Code != http.StatusCreated {
		t.Fatalf("criar tabela: status = %d, corpo = %s", tableRec.Code, tableRec.Body.String())
	}
	var table tableResponse
	if err := json.Unmarshal(tableRec.Body.Bytes(), &table); err != nil {
		t.Fatalf("decodificar tabela: %v", err)
	}

	// 2. Criar campo.
	fieldReq := httptest.NewRequest(http.MethodPost, "/v1/tenants/"+string(fx.tenant)+"/tables/books/fields", bytes.NewBufferString(`{"name":"title","type":"text","required":true}`))
	fieldReq.SetPathValue("tenant", string(fx.tenant))
	fieldReq.SetPathValue("table", "books")
	fieldReq.Header.Set("Authorization", "Bearer "+adminToken)
	fieldRec := httptest.NewRecorder()
	fieldsHandler.ServeHTTP(fieldRec, fieldReq)
	if fieldRec.Code != http.StatusCreated {
		t.Fatalf("criar campo: status = %d, corpo = %s", fieldRec.Code, fieldRec.Body.String())
	}

	// 3. Criar view (não publicada — MinRole admin por padrão).
	createViewBody := `{"name":"booklist","table":"books","template":"List","configuration":{"above":[{"type":"blank","contents":"v1"}]}}`
	createReq := httptest.NewRequest(http.MethodPost, "/v1/tenants/"+string(fx.tenant)+"/views", bytes.NewBufferString(createViewBody))
	createReq.SetPathValue("tenant", string(fx.tenant))
	createReq.Header.Set("Authorization", "Bearer "+adminToken)
	createReq.Header.Set("Idempotency-Key", "create-view-1")
	createRec := httptest.NewRecorder()
	createViewH.ServeHTTP(createRec, createReq)
	if createRec.Code != http.StatusCreated {
		t.Fatalf("criar view: status = %d, corpo = %s", createRec.Code, createRec.Body.String())
	}
	var created viewResponse
	if err := json.Unmarshal(createRec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decodificar view criada: %v", err)
	}
	viewPath := "/v1/tenants/" + string(fx.tenant) + "/views/" + strconv.Itoa(created.ID)

	// 4. Antes de publicar: público não consegue reabrir (403).
	getPublicReq := httptest.NewRequest(http.MethodGet, viewPath, nil)
	getPublicReq.SetPathValue("tenant", string(fx.tenant))
	getPublicReq.SetPathValue("id", strconv.Itoa(created.ID))
	getPublicReq.Header.Set("Authorization", "Bearer "+publicToken)
	getPublicRec := httptest.NewRecorder()
	getViewH.ServeHTTP(getPublicRec, getPublicReq)
	if getPublicRec.Code != http.StatusForbidden {
		t.Fatalf("GET view (público, antes de publicar): status = %d, corpo = %s, esperado 403", getPublicRec.Code, getPublicRec.Body.String())
	}

	// 5. Salvar (editar configuration) — reaproveita o mesmo Version.
	saveBody := `{"_version":"` + created.Version + `","configuration":{"above":[{"type":"blank","contents":"v2"}]}}`
	saveReq := httptest.NewRequest(http.MethodPatch, viewPath, bytes.NewBufferString(saveBody))
	saveReq.SetPathValue("tenant", string(fx.tenant))
	saveReq.SetPathValue("id", strconv.Itoa(created.ID))
	saveReq.Header.Set("Authorization", "Bearer "+adminToken)
	saveReq.Header.Set("Idempotency-Key", "save-1")
	saveRec := httptest.NewRecorder()
	updateViewH.ServeHTTP(saveRec, saveReq)
	if saveRec.Code != http.StatusOK {
		t.Fatalf("salvar view: status = %d, corpo = %s", saveRec.Code, saveRec.Body.String())
	}
	var saved viewResponse
	if err := json.Unmarshal(saveRec.Body.Bytes(), &saved); err != nil {
		t.Fatalf("decodificar view salva: %v", err)
	}

	// 6. Reabrir (GET, como admin) — confirma que o salvamento persistiu.
	reopenReq := httptest.NewRequest(http.MethodGet, viewPath, nil)
	reopenReq.SetPathValue("tenant", string(fx.tenant))
	reopenReq.SetPathValue("id", strconv.Itoa(created.ID))
	reopenReq.Header.Set("Authorization", "Bearer "+adminToken)
	reopenRec := httptest.NewRecorder()
	getViewH.ServeHTTP(reopenRec, reopenReq)
	if reopenRec.Code != http.StatusOK {
		t.Fatalf("reabrir view: status = %d, corpo = %s", reopenRec.Code, reopenRec.Body.String())
	}
	var reopened viewResponse
	if err := json.Unmarshal(reopenRec.Body.Bytes(), &reopened); err != nil {
		t.Fatalf("decodificar view reaberta: %v", err)
	}
	aboveList, _ := reopened.Configuration["above"].([]any)
	if len(aboveList) != 1 {
		t.Fatalf("configuration reaberta = %+v, esperado 1 segmento", reopened.Configuration)
	}
	seg, _ := aboveList[0].(map[string]any)
	if seg["contents"] != "v2" {
		t.Errorf("conteúdo reaberto = %+v, esperado contents=v2 (o que foi salvo no passo 5)", seg)
	}

	// 7. Conflito de edição: uma segunda tentativa de salvar com o Version
	// ANTIGO (de antes do passo 5) deve ser rejeitada, não sobrescrever o
	// que já foi salvo — corpo diferente do passo 5, então é uma edição
	// de verdade (chave de idempotência diferente), não um replay.
	staleBody := `{"_version":"` + created.Version + `","configuration":{"above":[{"type":"blank","contents":"conflito, nunca deveria persistir"}]}}`
	staleReq := httptest.NewRequest(http.MethodPatch, viewPath, bytes.NewBufferString(staleBody))
	staleReq.SetPathValue("tenant", string(fx.tenant))
	staleReq.SetPathValue("id", strconv.Itoa(created.ID))
	staleReq.Header.Set("Authorization", "Bearer "+adminToken)
	staleReq.Header.Set("Idempotency-Key", "conflicting-save")
	staleRec := httptest.NewRecorder()
	updateViewH.ServeHTTP(staleRec, staleReq)
	if staleRec.Code != http.StatusConflict {
		t.Fatalf("salvar com _version obsoleto: status = %d, corpo = %s, esperado 409", staleRec.Code, staleRec.Body.String())
	}

	// 8. Publicar: PATCH baixando min_role para RolePublic — usa a Version
	// ATUAL (a do passo 5/6), não a obsoleta do passo 7 (que falhou e não
	// avançou a versão). Desde GO-020, publicar exige um layout executável
	// pelo runtime novo (ver internal/views/render.go) — os passos 3/5/7
	// usam de propósito o layout opaco de mock (`above`, GO-018/019, prova
	// que o transporte/concorrência não interpretam `configuration`); este
	// passo troca para o shape real e suportado (`layout.besides` com uma
	// coluna de campo direto) exatamente no momento de publicar, o
	// equivalente a "finalizar o layout antes de publicar" no editor real.
	publishBody := `{"_version":"` + saved.Version + `","min_role":` + strconv.Itoa(int(identity.RolePublic)) +
		`,"configuration":{"layout":{"besides":[{"contents":{"type":"Field","field_name":"title"}}]}}}`
	publishReq := httptest.NewRequest(http.MethodPatch, viewPath, bytes.NewBufferString(publishBody))
	publishReq.SetPathValue("tenant", string(fx.tenant))
	publishReq.SetPathValue("id", strconv.Itoa(created.ID))
	publishReq.Header.Set("Authorization", "Bearer "+adminToken)
	publishReq.Header.Set("Idempotency-Key", "publish-1")
	publishRec := httptest.NewRecorder()
	updateViewH.ServeHTTP(publishRec, publishReq)
	if publishRec.Code != http.StatusOK {
		t.Fatalf("publicar view: status = %d, corpo = %s", publishRec.Code, publishRec.Body.String())
	}

	// 9. Depois de publicar: público consegue reabrir — "publica com dois
	// papéis" comprovado de ponta a ponta.
	getPublicAfterReq := httptest.NewRequest(http.MethodGet, viewPath, nil)
	getPublicAfterReq.SetPathValue("tenant", string(fx.tenant))
	getPublicAfterReq.SetPathValue("id", strconv.Itoa(created.ID))
	getPublicAfterReq.Header.Set("Authorization", "Bearer "+publicToken)
	getPublicAfterRec := httptest.NewRecorder()
	getViewH.ServeHTTP(getPublicAfterRec, getPublicAfterReq)
	if getPublicAfterRec.Code != http.StatusOK {
		t.Fatalf("GET view (público, depois de publicar): status = %d, corpo = %s, esperado 200", getPublicAfterRec.Code, getPublicAfterRec.Body.String())
	}

	// 10. Confirma, com uma consulta final, que o conteúdo do "ataque" de
	// conflito (passo 7) nunca persistiu — nem antes nem depois de
	// publicar. A publicação (passo 8) trocou `configuration` para o
	// layout compatível; o conteúdo do passo 7 nunca existiu em nenhuma
	// versão persistida, então basta confirmar que a view final não é o
	// shape antigo (`above`) nem contém o texto do conflito em lugar algum.
	var finalCheck viewResponse
	if err := json.Unmarshal(getPublicAfterRec.Body.Bytes(), &finalCheck); err != nil {
		t.Fatalf("decodificar view final: %v", err)
	}
	if _, stillOldShape := finalCheck.Configuration["above"]; stillOldShape {
		t.Fatalf("configuration final ainda no shape antigo (above) — publicar (passo 8) deveria ter trocado para layout.besides: %+v", finalCheck.Configuration)
	}
	rawFinal, _ := json.Marshal(finalCheck.Configuration)
	if strings.Contains(string(rawFinal), "conflito, nunca deveria persistir") {
		t.Fatal("o conteúdo do conflito de edição persistiu — sobrescrita silenciosa!")
	}
}

func TestCreateTableHandler_RequiresAdmin(t *testing.T) {
	db := testDB(t)
	fx := newEditorFixture(t, db)
	verifier, err := tenancy.NewVerifier([]byte(testServiceIdentitySecret))
	if err != nil {
		t.Fatalf("NewVerifier: %v", err)
	}
	handler := buildEditorHandler(t, verifier, fx.guard, tablesSchemaCapability, createTableHandler(fx.tracker, db))
	token := mintServiceIdentity(t, testServiceIdentitySecret, strconv.Itoa(fx.publicID), fx.tenant, time.Minute)

	req := httptest.NewRequest(http.MethodPost, "/v1/tenants/"+string(fx.tenant)+"/tables", bytes.NewBufferString(`{"name":"widgets"}`))
	req.SetPathValue("tenant", string(fx.tenant))
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("criar tabela com ator público: status = %d, corpo = %s, esperado 403", rec.Code, rec.Body.String())
	}
}

func TestCreateViewHandler_RetryDoesNotDuplicate(t *testing.T) {
	db := testDB(t)
	fx := newEditorFixture(t, db)
	ctx := context.Background()
	if err := db.WithTenant(ctx, fx.tenant, func(ctx context.Context, tx pgx.Tx) error {
		_, err := metadata.CreateTable(ctx, tx, identity.RoleAdmin, "books", metadata.TableOptions{})
		return err
	}); err != nil {
		t.Fatalf("criar tabela books: %v", err)
	}

	verifier, err := tenancy.NewVerifier([]byte(testServiceIdentitySecret))
	if err != nil {
		t.Fatalf("NewVerifier: %v", err)
	}
	handler := buildEditorHandler(t, verifier, fx.guard, viewsCapability, createViewHandler(fx.tracker, db))
	token := mintServiceIdentity(t, testServiceIdentitySecret, strconv.Itoa(fx.adminID), fx.tenant, time.Minute)

	doPost := func() *httptest.ResponseRecorder {
		body := `{"name":"retryview","table":"books","template":"List","configuration":{}}`
		req := httptest.NewRequest(http.MethodPost, "/v1/tenants/"+string(fx.tenant)+"/views", bytes.NewBufferString(body))
		req.SetPathValue("tenant", string(fx.tenant))
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Idempotency-Key", "retry-create-view")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		return rec
	}

	first := doPost()
	second := doPost()
	if first.Code != http.StatusCreated || second.Code != http.StatusCreated {
		t.Fatalf("status: primeiro=%d segundo=%d, corpos: %s / %s", first.Code, second.Code, first.Body.String(), second.Body.String())
	}
	var firstBody, secondBody viewResponse
	_ = json.Unmarshal(first.Body.Bytes(), &firstBody)
	_ = json.Unmarshal(second.Body.Bytes(), &secondBody)
	if firstBody.ID != secondBody.ID {
		t.Errorf("retry criou uma view diferente: primeiro id=%d, segundo id=%d", firstBody.ID, secondBody.ID)
	}

	var count int
	if err := db.WithTenant(ctx, fx.tenant, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, "SELECT count(*) FROM _sc_views WHERE name = 'retryview'").Scan(&count)
	}); err != nil {
		t.Fatalf("contar views: %v", err)
	}
	if count != 1 {
		t.Errorf("count(_sc_views name='retryview') = %d, esperado 1 (retry não deveria duplicar)", count)
	}
}
