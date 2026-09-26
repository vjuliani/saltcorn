// Testes deste arquivo cobrem o caminho SQLite de GO-041 (cmd/server/
// sqlite.go) — sem Postgres, sem cutover/ownership (ver comentário de
// main.go: cutover é um conceito de migração gradual Node/Go que não se
// aplica ao modo desktop, Go-only desde o primeiro dia). Prova real via
// HTTP (httptest), não uma chamada direta às funções de internal/*: o
// critério de aceite é "cmd/server sobe e serve tráfego real contra um
// tenant em arquivo SQLite".
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/vjuliani/saltcorn/migracao/backend/internal/identity"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/metadata"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/platform/database"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/platform/outbox"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/platform/shutdown"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/platform/sqlite"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/platform/tenancy"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/records"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/triggers"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/views"
)

type sqliteFixture struct {
	db      *sqlite.DB
	tenant  tenancy.Tenant
	userID  int
	tracker *shutdown.Tracker
}

func newSQLiteFixture(t *testing.T, actorRole identity.RoleID) sqliteFixture {
	t.Helper()
	dir := t.TempDir()
	db, err := sqlite.Open(dir)
	if err != nil {
		t.Fatalf("sqlite.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	tenant := tenancy.Tenant("srv_sqlite_test")
	var userID int
	if err := db.WithTenant(context.Background(), tenant, func(ctx context.Context, tx database.Tx) error {
		if err := identity.EnsureSchemaTx(ctx, tx); err != nil {
			return err
		}
		if err := metadata.EnsureSchema(ctx, tx); err != nil {
			return err
		}
		if err := outbox.EnsureSchemaTx(ctx, tx); err != nil {
			return err
		}
		if err := views.EnsureSchemaTx(ctx, tx); err != nil {
			return err
		}
		if err := triggers.EnsureSchemaTx(ctx, tx); err != nil {
			return err
		}
		id, err := identity.CreateUserTx(ctx, tx, "ator@example.com", "hash", actorRole)
		userID = id
		return err
	}); err != nil {
		t.Fatalf("setup do fixture SQLite: %v", err)
	}

	return sqliteFixture{db: db, tenant: tenant, userID: userID, tracker: shutdown.NewTracker()}
}

// buildSQLiteHandler replica a composição de middlewares de main.go para
// o modo SQLite (tenancy.Middleware, SEM cutover.RequireOwnership) —
// mesma duplicação deliberada e pequena de buildRecordsHandler.
func buildSQLiteHandler(t *testing.T, verifier *tenancy.Verifier, next http.Handler) http.Handler {
	t.Helper()
	return tenancy.Middleware(verifier, next)
}

func TestSQLite_TablesFieldsRecordsViewsEndToEnd(t *testing.T) {
	fx := newSQLiteFixture(t, identity.RoleAdmin)
	verifier, err := tenancy.NewVerifier([]byte(testServiceIdentitySecret))
	if err != nil {
		t.Fatalf("NewVerifier: %v", err)
	}
	token := mintServiceIdentity(t, testServiceIdentitySecret, strconv.Itoa(fx.userID), fx.tenant, time.Minute)

	doJSON := func(handler http.Handler, method, path string, pathValues map[string]string, headers map[string]string, body any) *httptest.ResponseRecorder {
		t.Helper()
		var reader *bytes.Reader
		if body != nil {
			b, err := json.Marshal(body)
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			reader = bytes.NewReader(b)
		} else {
			reader = bytes.NewReader(nil)
		}
		req := httptest.NewRequest(method, path, reader)
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")
		for k, v := range headers {
			req.Header.Set(k, v)
		}
		for k, v := range pathValues {
			req.SetPathValue(k, v)
		}
		req.SetPathValue("tenant", string(fx.tenant))
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		return rec
	}

	// 1. Identidade: getActor via o caminho SQLite.
	actorHandler := buildSQLiteHandler(t, verifier, sqliteGetActorHandler(fx.tracker, fx.db))
	rec := doJSON(actorHandler, http.MethodGet, "/v1/tenants/"+string(fx.tenant)+"/actor", nil, nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("getActor: status = %d, corpo = %s", rec.Code, rec.Body.String())
	}
	var actor actorResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &actor); err != nil {
		t.Fatalf("decodificar actor: %v", err)
	}
	if actor.ID != fx.userID || actor.RoleID != int(identity.RoleAdmin) {
		t.Fatalf("actor = %+v, esperado id=%d role=%d", actor, fx.userID, identity.RoleAdmin)
	}

	// 2. Tabela + campo, via HTTP real.
	createTableH := buildSQLiteHandler(t, verifier, sqliteCreateTableHandler(fx.tracker, fx.db))
	rec = doJSON(createTableH, http.MethodPost, "/v1/tenants/"+string(fx.tenant)+"/tables", nil, nil, createTableRequest{Name: "notes"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("createTable: status = %d, corpo = %s", rec.Code, rec.Body.String())
	}
	var table tableResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &table)

	addFieldH := buildSQLiteHandler(t, verifier, sqliteAddFieldHandler(fx.tracker, fx.db))
	rec = doJSON(addFieldH, http.MethodPost, "/v1/tenants/"+string(fx.tenant)+"/tables/notes/fields", map[string]string{"table": "notes"}, nil,
		addFieldRequest{Name: "title", Type: "text", Required: true})
	if rec.Code != http.StatusCreated {
		t.Fatalf("addField: status = %d, corpo = %s", rec.Code, rec.Body.String())
	}

	// 3. Registro: create, list, update, delete — via HTTP real.
	createRecH := buildSQLiteHandler(t, verifier, sqliteCreateRecordHandler(fx.tracker, fx.db, &triggers.Dispatcher{}))
	rec = doJSON(createRecH, http.MethodPost, "/v1/tenants/"+string(fx.tenant)+"/tables/notes/records", map[string]string{"table": "notes"},
		map[string]string{"Idempotency-Key": "create-1"}, map[string]any{"title": "primeira nota"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("createRecord: status = %d, corpo = %s", rec.Code, rec.Body.String())
	}
	var created map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &created)
	recordID := int(created["id"].(float64))
	version, _ := created["_version"].(string)

	listRecH := buildSQLiteHandler(t, verifier, sqliteListRecordsHandler(fx.tracker, fx.db))
	rec = doJSON(listRecH, http.MethodGet, "/v1/tenants/"+string(fx.tenant)+"/tables/notes/records", map[string]string{"table": "notes"}, nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("listRecords: status = %d, corpo = %s", rec.Code, rec.Body.String())
	}
	var page struct {
		Items []map[string]any `json:"items"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &page)
	if len(page.Items) != 1 || page.Items[0]["title"] != "primeira nota" {
		t.Fatalf("listRecords: %+v", page)
	}

	updateRecH := buildSQLiteHandler(t, verifier, sqliteUpdateRecordHandler(fx.tracker, fx.db, &triggers.Dispatcher{}))
	rec = doJSON(updateRecH, http.MethodPatch, "/v1/tenants/"+string(fx.tenant)+"/tables/notes/records/"+strconv.Itoa(recordID),
		map[string]string{"table": "notes", "id": strconv.Itoa(recordID)}, map[string]string{"Idempotency-Key": "update-1"},
		map[string]any{"title": "nota atualizada", "_version": version})
	if rec.Code != http.StatusOK {
		t.Fatalf("updateRecord: status = %d, corpo = %s", rec.Code, rec.Body.String())
	}
	var updated map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &updated)
	if updated["title"] != "nota atualizada" {
		t.Fatalf("updateRecord: %+v", updated)
	}
	newVersion, _ := updated["_version"].(string)

	deleteRecH := buildSQLiteHandler(t, verifier, sqliteDeleteRecordHandler(fx.tracker, fx.db, &triggers.Dispatcher{}))
	rec = doJSON(deleteRecH, http.MethodDelete, "/v1/tenants/"+string(fx.tenant)+"/tables/notes/records/"+strconv.Itoa(recordID)+"?version="+newVersion,
		map[string]string{"table": "notes", "id": strconv.Itoa(recordID)}, nil, nil)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("deleteRecord: status = %d, corpo = %s", rec.Code, rec.Body.String())
	}

	// 4. View: create, list, render (List/Show/Edit), submit — via HTTP real.
	// Recria um registro para a view List/Edit renderizarem sobre algo real.
	rec = doJSON(createRecH, http.MethodPost, "/v1/tenants/"+string(fx.tenant)+"/tables/notes/records", map[string]string{"table": "notes"},
		map[string]string{"Idempotency-Key": "create-2"}, map[string]any{"title": "nota da view"})
	_ = json.Unmarshal(rec.Body.Bytes(), &created)
	recordID = int(created["id"].(float64))
	version, _ = created["_version"].(string)

	createViewH := buildSQLiteHandler(t, verifier, sqliteCreateViewHandler(fx.tracker, fx.db))
	rec = doJSON(createViewH, http.MethodPost, "/v1/tenants/"+string(fx.tenant)+"/views", nil, map[string]string{"Idempotency-Key": "view-1"},
		createViewRequest{Name: "notelist", TableName: "notes", Template: "List", Configuration: map[string]any{
			"columns": []any{map[string]any{"type": "Field", "field_name": "title"}},
		}})
	if rec.Code != http.StatusCreated {
		t.Fatalf("createView (List): status = %d, corpo = %s", rec.Code, rec.Body.String())
	}
	var listView viewResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &listView)

	minRoleAdmin := int(identity.RoleAdmin)
	rec = doJSON(createViewH, http.MethodPost, "/v1/tenants/"+string(fx.tenant)+"/views", nil, map[string]string{"Idempotency-Key": "view-2"},
		createViewRequest{Name: "editnote", TableName: "notes", Template: "Edit", MinRole: &minRoleAdmin, Configuration: map[string]any{
			"columns": []any{
				map[string]any{"type": "Field", "field_name": "title", "fieldview": "edit"},
				map[string]any{"type": "Action", "action_name": "Save"},
			},
		}})
	if rec.Code != http.StatusCreated {
		t.Fatalf("createView (Edit): status = %d, corpo = %s", rec.Code, rec.Body.String())
	}
	var editView viewResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &editView)

	listViewsH := buildSQLiteHandler(t, verifier, sqliteListViewsHandler(fx.tracker, fx.db))
	rec = doJSON(listViewsH, http.MethodGet, "/v1/tenants/"+string(fx.tenant)+"/views", nil, nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("listViews: status = %d, corpo = %s", rec.Code, rec.Body.String())
	}
	var allViews []viewResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &allViews)
	if len(allViews) != 2 {
		t.Fatalf("listViews: %d views, esperado 2", len(allViews))
	}

	renderH := buildSQLiteHandler(t, verifier, sqliteRenderViewHandler(fx.tracker, fx.db))
	rec = doJSON(renderH, http.MethodGet, "/v1/tenants/"+string(fx.tenant)+"/views/"+strconv.Itoa(listView.ID)+"/render",
		map[string]string{"id": strconv.Itoa(listView.ID)}, nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("renderView (List): status = %d, corpo = %s", rec.Code, rec.Body.String())
	}
	var listPlan renderListResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &listPlan); err != nil {
		t.Fatalf("decodificar renderListResponse: %v", err)
	}
	if len(listPlan.Rows) != 1 || listPlan.Rows[0]["title"] != "nota da view" {
		t.Fatalf("renderView (List): %+v", listPlan)
	}

	rec = doJSON(renderH, http.MethodGet, "/v1/tenants/"+string(fx.tenant)+"/views/"+strconv.Itoa(editView.ID)+"/render?record="+strconv.Itoa(recordID),
		map[string]string{"id": strconv.Itoa(editView.ID)}, nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("renderView (Edit): status = %d, corpo = %s", rec.Code, rec.Body.String())
	}
	var editPlan renderEditResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &editPlan); err != nil {
		t.Fatalf("decodificar renderEditResponse: %v", err)
	}
	if len(editPlan.Fields) != 1 || editPlan.Fields[0].Value != "nota da view" {
		t.Fatalf("renderView (Edit): %+v", editPlan)
	}

	submitH := buildSQLiteHandler(t, verifier, sqliteSubmitViewHandler(fx.tracker, fx.db))
	rec = doJSON(submitH, http.MethodPost, "/v1/tenants/"+string(fx.tenant)+"/views/"+strconv.Itoa(editView.ID)+"/submit",
		map[string]string{"id": strconv.Itoa(editView.ID)}, map[string]string{"Idempotency-Key": "submit-1"},
		submitViewRequest{RecordID: recordID, Version: version, Values: map[string]any{"title": "editada via view"}})
	if rec.Code != http.StatusOK {
		t.Fatalf("submitView: status = %d, corpo = %s", rec.Code, rec.Body.String())
	}
	var submitResp submitViewResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &submitResp); err != nil {
		t.Fatalf("decodificar submitViewResponse: %v", err)
	}
	if submitResp.Record["title"] != "editada via view" {
		t.Fatalf("submitView: %+v", submitResp)
	}
}

// TestSQLite_SameIdempotencyKey_DoesNotDuplicate prova que a idempotência
// (outbox.DoTx) funciona identicamente no caminho SQLite — mesma garantia
// já provada contra Postgres em records_test.go.
func TestSQLite_SameIdempotencyKey_DoesNotDuplicate(t *testing.T) {
	fx := newSQLiteFixture(t, identity.RoleAdmin)
	verifier, err := tenancy.NewVerifier([]byte(testServiceIdentitySecret))
	if err != nil {
		t.Fatalf("NewVerifier: %v", err)
	}
	token := mintServiceIdentity(t, testServiceIdentitySecret, strconv.Itoa(fx.userID), fx.tenant, time.Minute)

	if err := fx.db.WithTenant(context.Background(), fx.tenant, func(ctx context.Context, tx database.Tx) error {
		table, err := metadata.CreateTable(ctx, tx, identity.RoleAdmin, "widgets", metadata.TableOptions{})
		if err != nil {
			return err
		}
		_, err = metadata.AddField(ctx, tx, identity.RoleAdmin, table.ID, metadata.FieldDef{Name: "label", Type: metadata.FieldText, Required: true})
		return err
	}); err != nil {
		t.Fatalf("setup: %v", err)
	}

	handler := buildSQLiteHandler(t, verifier, sqliteCreateRecordHandler(fx.tracker, fx.db, &triggers.Dispatcher{}))
	do := func() *httptest.ResponseRecorder {
		body, _ := json.Marshal(map[string]any{"label": "gizmo"})
		req := httptest.NewRequest(http.MethodPost, "/v1/tenants/"+string(fx.tenant)+"/tables/widgets/records", bytes.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Idempotency-Key", "retry-key")
		req.SetPathValue("tenant", string(fx.tenant))
		req.SetPathValue("table", "widgets")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		return rec
	}
	first := do()
	second := do()
	if first.Code != http.StatusCreated || second.Code != http.StatusCreated {
		t.Fatalf("status: first=%d second=%d", first.Code, second.Code)
	}
	if first.Body.String() != second.Body.String() {
		t.Fatalf("a segunda chamada com a MESMA Idempotency-Key deveria devolver o MESMO resultado: %s vs %s", first.Body.String(), second.Body.String())
	}

	if err := fx.db.WithTenant(context.Background(), fx.tenant, func(ctx context.Context, tx database.Tx) error {
		rows, err := records.RowsTx(ctx, tx, identity.RoleAdmin, records.Query{Table: "widgets"})
		if err != nil {
			return err
		}
		if len(rows) != 1 {
			t.Fatalf("linhas em widgets = %d, esperado exatamente 1 — a 2ª chamada nunca deveria duplicar a criação", len(rows))
		}
		return nil
	}); err != nil {
		t.Fatalf("verificação de não-duplicação: %v", err)
	}
}

// TestSQLite_CreateRecordHandler_DispatchesRealTrigger prova o Aceite de
// GO-055 literalmente: "cmd/server em modo SQLite dispara triggers reais
// (Dispatcher.HooksFor) a partir de uma escrita HTTP de registro, sem
// hooks=nil" — uma escrita HTTP real via sqliteCreateRecordHandler,
// nunca uma chamada direta a HooksForTx/RunOneTx.
func TestSQLite_CreateRecordHandler_DispatchesRealTrigger(t *testing.T) {
	fx := newSQLiteFixture(t, identity.RoleAdmin)
	verifier, err := tenancy.NewVerifier([]byte(testServiceIdentitySecret))
	if err != nil {
		t.Fatalf("NewVerifier: %v", err)
	}
	token := mintServiceIdentity(t, testServiceIdentitySecret, strconv.Itoa(fx.userID), fx.tenant, time.Minute)

	var tableID int
	if err := fx.db.WithTenant(context.Background(), fx.tenant, func(ctx context.Context, tx database.Tx) error {
		table, err := metadata.CreateTable(ctx, tx, identity.RoleAdmin, "posts", metadata.TableOptions{})
		if err != nil {
			return err
		}
		tableID = table.ID
		if _, err := metadata.AddField(ctx, tx, identity.RoleAdmin, tableID, metadata.FieldDef{Name: "title", Type: metadata.FieldText, Required: true}); err != nil {
			return err
		}
		_, err = triggers.CreateTriggerTx(ctx, tx, triggers.Trigger{TableID: tableID, When: triggers.WhenInsert, Action: "mark"})
		return err
	}); err != nil {
		t.Fatalf("setup: %v", err)
	}

	var fired []string
	dispatcher := &triggers.Dispatcher{Actions: map[string]triggers.ActionFuncTx{
		"mark": func(ctx context.Context, tx database.Tx, table metadata.Table, row map[string]any, config map[string]any) error {
			fired = append(fired, fmt.Sprint(row["title"]))
			return nil
		},
	}}

	handler := buildSQLiteHandler(t, verifier, sqliteCreateRecordHandler(fx.tracker, fx.db, dispatcher))
	body, _ := json.Marshal(map[string]any{"title": "disparado via HTTP"})
	req := httptest.NewRequest(http.MethodPost, "/v1/tenants/"+string(fx.tenant)+"/tables/posts/records", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Idempotency-Key", "trigger-http-1")
	req.SetPathValue("tenant", string(fx.tenant))
	req.SetPathValue("table", "posts")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("createRecord: status = %d, corpo = %s", rec.Code, rec.Body.String())
	}

	if len(fired) != 1 || fired[0] != "disparado via HTTP" {
		t.Fatalf("trigger não disparou a partir da escrita HTTP: %v", fired)
	}
}
