// Testes de GET .../realtime/events (GO-028) — mesmo rigor de
// records_test.go: Postgres real, HTTP real via httptest, identidade
// delegada real assinada/verificada.
package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/vjuliani/saltcorn/migracao/backend/internal/identity"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/platform/cutover"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/platform/database"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/platform/tenancy"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/realtime"
)

// switchRealtimeOwnershipOnDB espelha o SwitchOwner(recordsCapability) já
// feito por newTestFixture, para a capacidade própria de GO-028 — as duas
// capacidades são cortadas independentemente (mesmo espírito granular de
// GO-009/019), então um teste de realtime precisa ligar a sua.
func switchRealtimeOwnershipOnDB(t *testing.T, db *database.DB, fx testFixture) {
	t.Helper()
	if err := cutover.SwitchOwner(context.Background(), db, fx.guard, fx.tenant, realtimeCapability, cutover.OwnerGo, 2*time.Second); err != nil {
		t.Fatalf("cutover.SwitchOwner(realtimeCapability): %v", err)
	}
}

func buildRealtimeHandler(t *testing.T, verifier *tenancy.Verifier, guard *cutover.Guard, next http.Handler) http.Handler {
	t.Helper()
	return tenancy.Middleware(verifier, cutover.RequireOwnership(guard, realtimeCapability, next))
}

func TestRealtimeEventsHandler_ReturnsEventsAddressedToActor(t *testing.T) {
	db := testDB(t)
	fx := newTestFixture(t, db, identity.RolePublic)
	switchRealtimeOwnershipOnDB(t, db, fx)
	verifier, err := tenancy.NewVerifier([]byte(testServiceIdentitySecret))
	if err != nil {
		t.Fatalf("NewVerifier: %v", err)
	}

	if err := db.WithTenant(context.Background(), fx.tenant, func(ctx context.Context, tx pgx.Tx) error {
		if err := realtime.EnsureSchema(ctx, tx); err != nil {
			return err
		}
		if _, err := realtime.Publish(ctx, tx, realtime.AudienceUsers, []int{fx.userID}, map[string]any{"type": "notification", "title": "Olá"}); err != nil {
			return err
		}
		_, err := realtime.Publish(ctx, tx, realtime.AudienceUsers, []int{fx.userID + 999}, map[string]any{"type": "notification", "title": "Não é seu"})
		return err
	}); err != nil {
		t.Fatalf("seed de eventos: %v", err)
	}

	handler := buildRealtimeHandler(t, verifier, fx.guard, realtimeEventsHandler(fx.tracker, db))
	token := mintServiceIdentity(t, testServiceIdentitySecret, strconv.Itoa(fx.userID), fx.tenant, time.Minute)

	req := httptest.NewRequest(http.MethodGet, "/v1/tenants/"+string(fx.tenant)+"/realtime/events", nil)
	req.SetPathValue("tenant", string(fx.tenant))
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, corpo = %s, esperado 200", rec.Code, rec.Body.String())
	}
	var body struct {
		Items []struct {
			ID       int64          `json:"id"`
			Audience string         `json:"audience"`
			Payload  map[string]any `json:"payload"`
		} `json:"items"`
		NextAfter int64 `json:"next_after"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decodificar resposta: %v", err)
	}
	if len(body.Items) != 1 {
		t.Fatalf("len(items) = %d, esperado 1 (só o evento endereçado a este ator, nunca o de outro usuário)", len(body.Items))
	}
	if body.Items[0].Payload["title"] != "Olá" {
		t.Errorf("payload.title = %v, esperado \"Olá\"", body.Items[0].Payload["title"])
	}
	if body.NextAfter != body.Items[0].ID {
		t.Errorf("next_after = %d, esperado igual ao id do único evento (%d)", body.NextAfter, body.Items[0].ID)
	}
}

// TestRealtimeEventsHandler_AfterCursor_ExcludesAlreadyDelivered prova o
// contrato de retomada que o BFF usa entre um poll e o seguinte (ver
// realtime.go): repetir a requisição com `after=<next_after>` da resposta
// anterior nunca reentrega o mesmo evento.
func TestRealtimeEventsHandler_AfterCursor_ExcludesAlreadyDelivered(t *testing.T) {
	db := testDB(t)
	fx := newTestFixture(t, db, identity.RolePublic)
	switchRealtimeOwnershipOnDB(t, db, fx)
	verifier, err := tenancy.NewVerifier([]byte(testServiceIdentitySecret))
	if err != nil {
		t.Fatalf("NewVerifier: %v", err)
	}

	if err := db.WithTenant(context.Background(), fx.tenant, func(ctx context.Context, tx pgx.Tx) error {
		if err := realtime.EnsureSchema(ctx, tx); err != nil {
			return err
		}
		_, err := realtime.Publish(ctx, tx, realtime.AudienceBroadcast, nil, map[string]any{"seq": 1})
		return err
	}); err != nil {
		t.Fatalf("seed de eventos: %v", err)
	}

	handler := buildRealtimeHandler(t, verifier, fx.guard, realtimeEventsHandler(fx.tracker, db))
	token := mintServiceIdentity(t, testServiceIdentitySecret, strconv.Itoa(fx.userID), fx.tenant, time.Minute)

	doRequest := func(after string) *httptest.ResponseRecorder {
		url := "/v1/tenants/" + string(fx.tenant) + "/realtime/events"
		if after != "" {
			url += "?after=" + after
		}
		req := httptest.NewRequest(http.MethodGet, url, nil)
		req.SetPathValue("tenant", string(fx.tenant))
		req.Header.Set("Authorization", "Bearer "+token)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		return rec
	}

	first := doRequest("")
	if first.Code != http.StatusOK {
		t.Fatalf("primeira requisição: status = %d, corpo = %s", first.Code, first.Body.String())
	}
	var firstBody struct {
		Items     []map[string]any `json:"items"`
		NextAfter int64            `json:"next_after"`
	}
	if err := json.Unmarshal(first.Body.Bytes(), &firstBody); err != nil {
		t.Fatalf("decodificar primeira resposta: %v", err)
	}
	if len(firstBody.Items) != 1 {
		t.Fatalf("primeira requisição: len(items) = %d, esperado 1", len(firstBody.Items))
	}

	second := doRequest(strconv.FormatInt(firstBody.NextAfter, 10))
	if second.Code != http.StatusOK {
		t.Fatalf("segunda requisição: status = %d, corpo = %s", second.Code, second.Body.String())
	}
	var secondBody struct {
		Items     []map[string]any `json:"items"`
		NextAfter int64            `json:"next_after"`
	}
	if err := json.Unmarshal(second.Body.Bytes(), &secondBody); err != nil {
		t.Fatalf("decodificar segunda resposta: %v", err)
	}
	if len(secondBody.Items) != 0 {
		t.Fatalf("segunda requisição (after=%d): len(items) = %d, esperado 0 (evento já entregue)", firstBody.NextAfter, len(secondBody.Items))
	}
	if secondBody.NextAfter != firstBody.NextAfter {
		t.Errorf("next_after da página vazia = %d, esperado igual ao cursor enviado (%d)", secondBody.NextAfter, firstBody.NextAfter)
	}
}

func TestRealtimeEventsHandler_WithoutOwnershipCutover_Rejected(t *testing.T) {
	db := testDB(t)
	fx := newTestFixture(t, db, identity.RolePublic)
	// Deliberadamente NÃO chama switchRealtimeOwnershipOnDB — a
	// capacidade nunca foi cortada para Go neste tenant.
	verifier, err := tenancy.NewVerifier([]byte(testServiceIdentitySecret))
	if err != nil {
		t.Fatalf("NewVerifier: %v", err)
	}
	handler := buildRealtimeHandler(t, verifier, fx.guard, realtimeEventsHandler(fx.tracker, db))
	token := mintServiceIdentity(t, testServiceIdentitySecret, strconv.Itoa(fx.userID), fx.tenant, time.Minute)

	req := httptest.NewRequest(http.MethodGet, "/v1/tenants/"+string(fx.tenant)+"/realtime/events", nil)
	req.SetPathValue("tenant", string(fx.tenant))
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code == http.StatusOK {
		t.Fatalf("status = %d, esperado uma rejeição (capacidade não pertence a Go neste tenant)", rec.Code)
	}
}
