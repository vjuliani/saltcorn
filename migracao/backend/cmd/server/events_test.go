// Testes deste arquivo exigem Postgres real — pulam (t.Skip) se
// SALTCORN_GO_TEST_DATABASE_URL não estiver definida. Cobrem GO-052 via
// HTTP real (POST .../events/{eventname}): autorização por nome de
// evento e idempotência — o despacho em si (TriggersForEvent/EmitEvent/
// escrita via Table.insertRow) já tem cobertura própria e mais profunda
// em internal/triggers/emitevent_test.go; aqui só a fronteira HTTP.
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
	"github.com/vjuliani/saltcorn/migracao/backend/internal/metadata"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/platform/cutover"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/platform/tenancy"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/triggers"
)

// buildEventsHandler replica a composição de middlewares de main.go para
// a rota de eventos (tenancy.Middleware + cutover.RequireOwnership) —
// mesma duplicação deliberada e pequena de buildRecordsHandler/
// buildEditorHandler.
func buildEventsHandler(t *testing.T, verifier *tenancy.Verifier, guard *cutover.Guard, next http.Handler) http.Handler {
	t.Helper()
	return tenancy.Middleware(verifier, cutover.RequireOwnership(guard, eventsCapability, next))
}

// dispatcherWithMarkAction devolve um *triggers.Dispatcher com uma ação
// nativa "mark" que só anota o mapa `fired` — suficiente para provar
// despacho/autorização/idempotência na fronteira HTTP sem precisar subir
// o host de plugins real (run_js_code já tem sua própria cobertura mais
// profunda em internal/triggers/emitevent_test.go, com Postgres E host
// reais).
func dispatcherWithMarkAction(fired *[]string) *triggers.Dispatcher {
	return &triggers.Dispatcher{Actions: map[string]triggers.ActionFunc{
		"mark": func(ctx context.Context, tx pgx.Tx, table metadata.Table, row map[string]any, cfg map[string]any) error {
			name, _ := cfg["name"].(string)
			*fired = append(*fired, name)
			return nil
		},
	}}
}

func postEmitEvent(t *testing.T, handler http.Handler, tenant tenancy.Tenant, token, eventName, idempotencyKey string, payload map[string]any) *httptest.ResponseRecorder {
	t.Helper()
	body, err := json.Marshal(map[string]any{"payload": payload})
	if err != nil {
		t.Fatalf("marshal do corpo: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/v1/tenants/"+string(tenant)+"/events/"+eventName, bytes.NewReader(body))
	req.SetPathValue("tenant", string(tenant))
	req.SetPathValue("eventname", eventName)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	if idempotencyKey != "" {
		req.Header.Set("Idempotency-Key", idempotencyKey)
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}

func TestEmitEventHandler_ReceiveMobileShareData_AlwaysAllowed_DispatchesTrigger(t *testing.T) {
	db := testDB(t)
	fx := newTestFixture(t, db, identity.RoleAdmin)
	verifier, err := tenancy.NewVerifier([]byte(testServiceIdentitySecret))
	if err != nil {
		t.Fatalf("NewVerifier: %v", err)
	}
	if err := db.WithTenant(context.Background(), fx.tenant, func(ctx context.Context, tx pgx.Tx) error {
		_, err := triggers.CreateTrigger(ctx, tx, triggers.Trigger{
			When: "ReceiveMobileShareData", Action: "mark", Configuration: map[string]any{"name": "ok"},
		})
		return err
	}); err != nil {
		t.Fatalf("CreateTrigger: %v", err)
	}
	if err := cutover.SwitchOwner(context.Background(), db, fx.guard, fx.tenant, eventsCapability, cutover.OwnerGo, 2*time.Second); err != nil {
		t.Fatalf("cutover.SwitchOwner (events): %v", err)
	}

	var fired []string
	handler := buildEventsHandler(t, verifier, fx.guard, emitEventHandler(fx.tracker, db, dispatcherWithMarkAction(&fired)))
	token := mintServiceIdentity(t, testServiceIdentitySecret, strconv.Itoa(fx.userID), fx.tenant, time.Minute)

	// Nenhuma configuração mobile_emit_allowed_events foi gravada — mas
	// "ReceiveMobileShareData" é sempre permitido para um ator autenticado
	// (mesma exceção do legado, routes/api.ts).
	rec := postEmitEvent(t, handler, fx.tenant, token, "ReceiveMobileShareData", "evt-1", map[string]any{"files": []any{}})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, corpo = %s, esperado 200", rec.Code, rec.Body.String())
	}
	var resp struct {
		Fired int `json:"fired"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decodificar resposta: %v", err)
	}
	if resp.Fired != 1 {
		t.Errorf("fired = %d, esperado 1", resp.Fired)
	}
	if len(fired) != 1 || fired[0] != "ok" {
		t.Errorf("fired (dispatcher) = %v, esperado [\"ok\"]", fired)
	}
}

func TestEmitEventHandler_ArbitraryEventName_RequiresAllowedEventsConfig(t *testing.T) {
	db := testDB(t)
	fx := newTestFixture(t, db, identity.RoleAdmin)
	verifier, err := tenancy.NewVerifier([]byte(testServiceIdentitySecret))
	if err != nil {
		t.Fatalf("NewVerifier: %v", err)
	}
	if err := db.WithTenant(context.Background(), fx.tenant, func(ctx context.Context, tx pgx.Tx) error {
		_, err := triggers.CreateTrigger(ctx, tx, triggers.Trigger{When: "MeuEvento", Action: "mark", Configuration: map[string]any{"name": "ok"}})
		return err
	}); err != nil {
		t.Fatalf("CreateTrigger: %v", err)
	}
	if err := cutover.SwitchOwner(context.Background(), db, fx.guard, fx.tenant, eventsCapability, cutover.OwnerGo, 2*time.Second); err != nil {
		t.Fatalf("cutover.SwitchOwner (events): %v", err)
	}

	var fired []string
	handler := buildEventsHandler(t, verifier, fx.guard, emitEventHandler(fx.tracker, db, dispatcherWithMarkAction(&fired)))
	token := mintServiceIdentity(t, testServiceIdentitySecret, strconv.Itoa(fx.userID), fx.tenant, time.Minute)

	// Sem mobile_emit_allowed_events configurado: 403, nunca dispara.
	rec := postEmitEvent(t, handler, fx.tenant, token, "MeuEvento", "evt-2", nil)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, corpo = %s, esperado 403 sem config", rec.Code, rec.Body.String())
	}
	if len(fired) != 0 {
		t.Fatalf("fired = %v, esperado vazio (evento não autorizado nunca deveria disparar)", fired)
	}

	// Configurar mobile_emit_allowed_events com "MeuEvento": agora permitido.
	if err := db.WithTenant(context.Background(), fx.tenant, func(ctx context.Context, tx pgx.Tx) error {
		return config.Set(ctx, tx, mobileEmitAllowedEventsConfigKey, []any{"MeuEvento"})
	}); err != nil {
		t.Fatalf("config.Set: %v", err)
	}
	rec = postEmitEvent(t, handler, fx.tenant, token, "MeuEvento", "evt-3", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, corpo = %s, esperado 200 com config", rec.Code, rec.Body.String())
	}
	if len(fired) != 1 {
		t.Fatalf("fired = %v, esperado 1 disparo depois de configurado", fired)
	}
}

func TestEmitEventHandler_MissingIdempotencyKey_Is400(t *testing.T) {
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
	handler := buildEventsHandler(t, verifier, fx.guard, emitEventHandler(fx.tracker, db, dispatcherWithMarkAction(&fired)))
	token := mintServiceIdentity(t, testServiceIdentitySecret, strconv.Itoa(fx.userID), fx.tenant, time.Minute)

	rec := postEmitEvent(t, handler, fx.tenant, token, "ReceiveMobileShareData", "", nil)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, corpo = %s, esperado 400 sem Idempotency-Key", rec.Code, rec.Body.String())
	}
}

// TestEmitEventHandler_SameIdempotencyKey_NeverFiresTwice é o critério de
// aceite "a capacidade de escrita... tem sua própria política de
// idempotência (nunca duas escritas para o mesmo evento)" na fronteira
// HTTP: um retry de rede da MESMA chamada (mesma Idempotency-Key, mesmo
// corpo) nunca dispara o trigger uma segunda vez.
func TestEmitEventHandler_SameIdempotencyKey_NeverFiresTwice(t *testing.T) {
	db := testDB(t)
	fx := newTestFixture(t, db, identity.RoleAdmin)
	verifier, err := tenancy.NewVerifier([]byte(testServiceIdentitySecret))
	if err != nil {
		t.Fatalf("NewVerifier: %v", err)
	}
	if err := db.WithTenant(context.Background(), fx.tenant, func(ctx context.Context, tx pgx.Tx) error {
		_, err := triggers.CreateTrigger(ctx, tx, triggers.Trigger{
			When: "ReceiveMobileShareData", Action: "mark", Configuration: map[string]any{"name": "ok"},
		})
		return err
	}); err != nil {
		t.Fatalf("CreateTrigger: %v", err)
	}
	if err := cutover.SwitchOwner(context.Background(), db, fx.guard, fx.tenant, eventsCapability, cutover.OwnerGo, 2*time.Second); err != nil {
		t.Fatalf("cutover.SwitchOwner (events): %v", err)
	}

	var fired []string
	handler := buildEventsHandler(t, verifier, fx.guard, emitEventHandler(fx.tracker, db, dispatcherWithMarkAction(&fired)))
	token := mintServiceIdentity(t, testServiceIdentitySecret, strconv.Itoa(fx.userID), fx.tenant, time.Minute)

	rec1 := postEmitEvent(t, handler, fx.tenant, token, "ReceiveMobileShareData", "retry-key", map[string]any{"files": []any{}})
	if rec1.Code != http.StatusOK {
		t.Fatalf("1ª chamada: status = %d, corpo = %s", rec1.Code, rec1.Body.String())
	}
	rec2 := postEmitEvent(t, handler, fx.tenant, token, "ReceiveMobileShareData", "retry-key", map[string]any{"files": []any{}})
	if rec2.Code != http.StatusOK {
		t.Fatalf("2ª chamada (retry): status = %d, corpo = %s", rec2.Code, rec2.Body.String())
	}
	if len(fired) != 1 {
		t.Fatalf("fired = %v (len=%d), esperado exatamente 1 — a 2ª chamada com a MESMA Idempotency-Key nunca deveria disparar de novo", fired, len(fired))
	}
}
