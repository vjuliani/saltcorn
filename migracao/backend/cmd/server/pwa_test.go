// Testes de GO-053 — exigem Postgres real, pulam (t.Skip) se
// SALTCORN_GO_TEST_DATABASE_URL não estiver definida, mesmo padrão de
// events_test.go. Cobrem manifestHandler (sem identidade delegada) e
// shareHandlerHandler (reaproveita o MESMO Dispatcher.EmitEvent já
// testado em GO-052).
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/vjuliani/saltcorn/migracao/backend/internal/config"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/identity"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/platform/cutover"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/platform/tenancy"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/triggers"
)

func TestManifestHandler_NoShareTrigger_OmitsShareTarget(t *testing.T) {
	db := testDB(t)
	fx := newTestFixture(t, db, identity.RoleAdmin)
	if err := db.WithTenant(context.Background(), fx.tenant, func(ctx context.Context, tx pgx.Tx) error {
		return config.Set(ctx, tx, "site_name", "Guitars Shop")
	}); err != nil {
		t.Fatalf("config.Set: %v", err)
	}

	handler := manifestHandler(fx.tracker, db)
	req := httptest.NewRequest(http.MethodGet, "/v1/tenants/"+string(fx.tenant)+"/manifest", nil)
	req.SetPathValue("tenant", string(fx.tenant))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, corpo = %s", rec.Code, rec.Body.String())
	}

	var manifest pwaManifest
	if err := json.Unmarshal(rec.Body.Bytes(), &manifest); err != nil {
		t.Fatalf("decodificar manifesto: %v", err)
	}
	if manifest.Name != "Guitars Shop" || manifest.StartURL != "/" {
		t.Fatalf("manifesto = %+v", manifest)
	}
	if manifest.ShareTarget != nil {
		t.Fatalf("share_target presente sem nenhum trigger ReceiveMobileShareData: %+v", manifest.ShareTarget)
	}
}

// TestManifestHandler_WithShareTrigger_IncludesShareTarget prova o
// critério de aceite: um bloco share_target válido aparece quando existe
// um trigger ReceiveMobileShareData — apontando para o MESMO caminho que
// shareHandlerHandler serve.
func TestManifestHandler_WithShareTrigger_IncludesShareTarget(t *testing.T) {
	db := testDB(t)
	fx := newTestFixture(t, db, identity.RoleAdmin)
	if err := db.WithTenant(context.Background(), fx.tenant, func(ctx context.Context, tx pgx.Tx) error {
		_, err := triggers.CreateTrigger(ctx, tx, triggers.Trigger{When: "ReceiveMobileShareData", Action: "mark"})
		return err
	}); err != nil {
		t.Fatalf("CreateTrigger: %v", err)
	}

	handler := manifestHandler(fx.tracker, db)
	req := httptest.NewRequest(http.MethodGet, "/v1/tenants/"+string(fx.tenant)+"/manifest", nil)
	req.SetPathValue("tenant", string(fx.tenant))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, corpo = %s", rec.Code, rec.Body.String())
	}

	var manifest pwaManifest
	if err := json.Unmarshal(rec.Body.Bytes(), &manifest); err != nil {
		t.Fatalf("decodificar manifesto: %v", err)
	}
	if manifest.ShareTarget == nil {
		t.Fatal("share_target ausente apesar do trigger existir")
	}
	if manifest.ShareTarget.Action != shareTargetPath || manifest.ShareTarget.Method != "POST" {
		t.Fatalf("share_target = %+v", manifest.ShareTarget)
	}
}

// TestManifestHandler_NeverRequiresIdentity prova que a rota é
// deliberadamente pública — nenhum cabeçalho Authorization, ainda assim
// 200 (mesmo espírito de healthz/readyz, ver comentário de pwa.go).
func TestManifestHandler_NeverRequiresIdentity(t *testing.T) {
	db := testDB(t)
	fx := newTestFixture(t, db, identity.RoleAdmin)

	handler := manifestHandler(fx.tracker, db)
	req := httptest.NewRequest(http.MethodGet, "/v1/tenants/"+string(fx.tenant)+"/manifest", nil)
	req.SetPathValue("tenant", string(fx.tenant))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d sem Authorization, esperado 200 (rota pública)", rec.Code)
	}
}

func postShareHandler(t *testing.T, handler http.Handler, tenant tenancy.Tenant, token, idempotencyKey string, body map[string]any) *httptest.ResponseRecorder {
	t.Helper()
	b, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal do corpo: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/v1/tenants/"+string(tenant)+"/share-handler", bytes.NewReader(b))
	req.SetPathValue("tenant", string(tenant))
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	req.Header.Set("Content-Type", "application/json")
	if idempotencyKey != "" {
		req.Header.Set("Idempotency-Key", idempotencyKey)
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}

// TestShareHandlerHandler_DispatchesReceiveMobileShareDataEndToEnd prova
// o Aceite: POST .../share-handler chama Dispatcher.EmitEvent(
// "ReceiveMobileShareData", ...) de ponta a ponta — mesma prova de
// receive_share_trigger já validada em GO-052, agora acionável também por
// este caminho HTTP dedicado.
func TestShareHandlerHandler_DispatchesReceiveMobileShareDataEndToEnd(t *testing.T) {
	db := testDB(t)
	fx := newTestFixture(t, db, identity.RoleAdmin)
	verifier, err := tenancy.NewVerifier([]byte(testServiceIdentitySecret))
	if err != nil {
		t.Fatalf("NewVerifier: %v", err)
	}
	if err := db.WithTenant(context.Background(), fx.tenant, func(ctx context.Context, tx pgx.Tx) error {
		_, err := triggers.CreateTrigger(ctx, tx, triggers.Trigger{
			When: "ReceiveMobileShareData", Action: "mark", Configuration: map[string]any{"name": "shared"},
		})
		return err
	}); err != nil {
		t.Fatalf("CreateTrigger: %v", err)
	}
	if err := cutover.SwitchOwner(context.Background(), db, fx.guard, fx.tenant, eventsCapability, cutover.OwnerGo, 2*time.Second); err != nil {
		t.Fatalf("cutover.SwitchOwner (events): %v", err)
	}

	var fired []string
	handler := buildEventsHandler(t, verifier, fx.guard, shareHandlerHandler(fx.tracker, db, dispatcherWithMarkAction(&fired)))
	token := mintServiceIdentity(t, testServiceIdentitySecret, strconv.Itoa(fx.userID), fx.tenant, time.Minute)

	rec := postShareHandler(t, handler, fx.tenant, token, "share-1", map[string]any{"title": "olhem isso", "url": "https://example.com"})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, corpo = %s", rec.Code, rec.Body.String())
	}
	var resp emitEventResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decodificar resposta: %v", err)
	}
	if resp.Fired != 1 || len(fired) != 1 || fired[0] != "shared" {
		t.Fatalf("fired resposta=%d dispatcher=%v, esperado 1/[shared]", resp.Fired, fired)
	}

	// Idempotência: repetir a MESMA chave nunca dispara de novo.
	rec2 := postShareHandler(t, handler, fx.tenant, token, "share-1", map[string]any{"title": "olhem isso", "url": "https://example.com"})
	if rec2.Code != http.StatusOK {
		t.Fatalf("status (repetição) = %d", rec2.Code)
	}
	if len(fired) != 1 {
		t.Fatalf("repetir a MESMA Idempotency-Key disparou de novo: %v", fired)
	}
}

func TestShareHandlerHandler_NoTrigger_ReturnsNotFound(t *testing.T) {
	db := testDB(t)
	fx := newTestFixture(t, db, identity.RoleAdmin)
	verifier, err := tenancy.NewVerifier([]byte(testServiceIdentitySecret))
	if err != nil {
		t.Fatalf("NewVerifier: %v", err)
	}
	if err := cutover.SwitchOwner(context.Background(), db, fx.guard, fx.tenant, eventsCapability, cutover.OwnerGo, 2*time.Second); err != nil {
		t.Fatalf("cutover.SwitchOwner (events): %v", err)
	}

	var fired []string
	handler := buildEventsHandler(t, verifier, fx.guard, shareHandlerHandler(fx.tracker, db, dispatcherWithMarkAction(&fired)))
	token := mintServiceIdentity(t, testServiceIdentitySecret, strconv.Itoa(fx.userID), fx.tenant, time.Minute)

	rec := postShareHandler(t, handler, fx.tenant, token, "share-2", map[string]any{"title": "sem trigger"})
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, esperado 404 (sharing_not_enabled)", rec.Code)
	}
	if len(fired) != 0 {
		t.Fatalf("dispatcher chamado sem nenhum trigger registrado: %v", fired)
	}
}

func TestShareHandlerHandler_PublicRole_RequiresLogin(t *testing.T) {
	db := testDB(t)
	fx := newTestFixture(t, db, identity.RolePublic)
	verifier, err := tenancy.NewVerifier([]byte(testServiceIdentitySecret))
	if err != nil {
		t.Fatalf("NewVerifier: %v", err)
	}
	if err := db.WithTenant(context.Background(), fx.tenant, func(ctx context.Context, tx pgx.Tx) error {
		_, err := triggers.CreateTrigger(ctx, tx, triggers.Trigger{When: "ReceiveMobileShareData", Action: "mark"})
		return err
	}); err != nil {
		t.Fatalf("CreateTrigger: %v", err)
	}
	if err := cutover.SwitchOwner(context.Background(), db, fx.guard, fx.tenant, eventsCapability, cutover.OwnerGo, 2*time.Second); err != nil {
		t.Fatalf("cutover.SwitchOwner (events): %v", err)
	}

	var fired []string
	handler := buildEventsHandler(t, verifier, fx.guard, shareHandlerHandler(fx.tracker, db, dispatcherWithMarkAction(&fired)))
	token := mintServiceIdentity(t, testServiceIdentitySecret, strconv.Itoa(fx.userID), fx.tenant, time.Minute)

	rec := postShareHandler(t, handler, fx.tenant, token, "share-3", map[string]any{"title": "anonimo"})
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, esperado 401 (login_required)", rec.Code)
	}
	if len(fired) != 0 {
		t.Fatalf("dispatcher chamado por um ator público: %v", fired)
	}
}
