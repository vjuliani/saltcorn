// Testes HTTP de GO-039: as rotas GET .../views/{id}/render (Show/Edit),
// POST .../views/{id}/submit (form_action) e
// DELETE .../views/{id}/rows/{recordId} (ação de coluna "Delete" de
// List) — reaproveita editorFixture/buildEditorHandler/
// setupBooksWithRecords de views_test.go/render_test.go, mesmo pacote.
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

	"github.com/vjuliani/saltcorn/migracao/backend/internal/identity"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/metadata"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/platform/database"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/platform/tenancy"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/records"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/views"
)

// setupBooksWithAuthor estende setupBooksWithRecords com uma tabela
// "authors" e um campo FieldKey "author" em "books" — o fixture HTTP
// mínimo para exercitar o fieldview "select" de Edit (opções reais) e o
// JoinField de List, mesmo padrão de booksWithAuthorJoin em
// internal/views/render_test.go.
func setupBooksWithAuthor(t *testing.T, db *database.DB, tenant tenancy.Tenant) (tableID, authorID int) {
	t.Helper()
	ctx := context.Background()
	if err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		table, err := metadata.CreateTable(ctx, database.AsTx(tx), identity.RoleAdmin, "books", metadata.TableOptions{})
		if err != nil {
			return err
		}
		tableID = table.ID
		if _, err := metadata.AddField(ctx, database.AsTx(tx), identity.RoleAdmin, tableID, metadata.FieldDef{Name: "title", Type: metadata.FieldText, Required: true}); err != nil {
			return err
		}
		authors, err := metadata.CreateTable(ctx, database.AsTx(tx), identity.RoleAdmin, "authors", metadata.TableOptions{})
		if err != nil {
			return err
		}
		if _, err := metadata.AddField(ctx, database.AsTx(tx), identity.RoleAdmin, authors.ID, metadata.FieldDef{Name: "name", Type: metadata.FieldText, Required: true}); err != nil {
			return err
		}
		if _, err := metadata.AddField(ctx, database.AsTx(tx), identity.RoleAdmin, tableID, metadata.FieldDef{Name: "author", Type: metadata.FieldKey, References: "authors"}); err != nil {
			return err
		}
		a, err := records.CreateRecord(ctx, tx, identity.RoleAdmin, "authors", map[string]any{"name": "Octavia E. Butler"}, nil)
		if err != nil {
			return err
		}
		authorID = int(a["id"].(int32))
		if _, err := records.CreateRecord(ctx, tx, identity.RoleAdmin, "books", map[string]any{"title": "Kindred", "author": authorID}, nil); err != nil {
			return err
		}
		return nil
	}); err != nil {
		t.Fatalf("setup de books/authors: %v", err)
	}
	return tableID, authorID
}

func editBookConfigurationHTTP() map[string]any {
	return map[string]any{
		"columns": []any{
			map[string]any{"type": "Field", "field_name": "title", "fieldview": "edit"},
			map[string]any{"type": "Field", "field_name": "author", "fieldview": "select"},
			map[string]any{"type": "Action", "action_name": "Save"},
		},
	}
}

func TestRenderViewHandler_ShowRendersRecordValues(t *testing.T) {
	db := testDB(t)
	fx := newEditorFixture(t, db)
	verifier, err := tenancy.NewVerifier([]byte(testServiceIdentitySecret))
	if err != nil {
		t.Fatalf("NewVerifier: %v", err)
	}
	tableID := setupBooksWithRecords(t, db, fx.tenant)

	var view views.View
	var bookID int
	if err := db.WithTenant(context.Background(), fx.tenant, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		view, err = views.CreateView(ctx, tx, identity.RoleAdmin, "showbook", tableID, "Show",
			map[string]any{"columns": []any{map[string]any{"type": "Field", "field_name": "title"}}}, views.ViewOptions{})
		if err != nil {
			return err
		}
		rows, err := records.Rows(ctx, tx, identity.RoleAdmin, records.Query{Table: "books", OrderBy: []records.OrderTerm{{Field: "id"}}, Limit: 1})
		if err != nil {
			return err
		}
		bookID = int(rows[0]["id"].(int32))
		return nil
	}); err != nil {
		t.Fatalf("preparar fixture: %v", err)
	}

	renderH := buildEditorHandler(t, verifier, fx.guard, viewsCapability, renderViewHandler(fx.tracker, db))
	adminToken := mintServiceIdentity(t, testServiceIdentitySecret, strconv.Itoa(fx.adminID), fx.tenant, time.Minute)

	req := httptest.NewRequest(http.MethodGet, "/v1/tenants/"+string(fx.tenant)+"/views/"+strconv.Itoa(view.ID)+"/render?record="+strconv.Itoa(bookID), nil)
	req.SetPathValue("tenant", string(fx.tenant))
	req.SetPathValue("id", strconv.Itoa(view.ID))
	req.Header.Set("Authorization", "Bearer "+adminToken)
	rec := httptest.NewRecorder()
	renderH.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("render Show: status = %d, corpo = %s", rec.Code, rec.Body.String())
	}
	var resp renderShowResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decodificar resposta: %v", err)
	}
	if resp.Values["title"] != "Dune" {
		t.Errorf("resp.Values[title] = %v, esperado Dune", resp.Values["title"])
	}
}

func TestRenderViewHandler_ShowRequiresRecordParam(t *testing.T) {
	db := testDB(t)
	fx := newEditorFixture(t, db)
	verifier, err := tenancy.NewVerifier([]byte(testServiceIdentitySecret))
	if err != nil {
		t.Fatalf("NewVerifier: %v", err)
	}
	tableID := setupBooksWithRecords(t, db, fx.tenant)

	var view views.View
	if err := db.WithTenant(context.Background(), fx.tenant, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		view, err = views.CreateView(ctx, tx, identity.RoleAdmin, "showbook", tableID, "Show",
			map[string]any{"columns": []any{map[string]any{"type": "Field", "field_name": "title"}}}, views.ViewOptions{})
		return err
	}); err != nil {
		t.Fatalf("criar view: %v", err)
	}

	renderH := buildEditorHandler(t, verifier, fx.guard, viewsCapability, renderViewHandler(fx.tracker, db))
	adminToken := mintServiceIdentity(t, testServiceIdentitySecret, strconv.Itoa(fx.adminID), fx.tenant, time.Minute)

	req := httptest.NewRequest(http.MethodGet, "/v1/tenants/"+string(fx.tenant)+"/views/"+strconv.Itoa(view.ID)+"/render", nil)
	req.SetPathValue("tenant", string(fx.tenant))
	req.SetPathValue("id", strconv.Itoa(view.ID))
	req.Header.Set("Authorization", "Bearer "+adminToken)
	rec := httptest.NewRecorder()
	renderH.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("render Show sem ?record=: status = %d, corpo = %s, esperado 400", rec.Code, rec.Body.String())
	}
}

func TestRenderViewHandler_EditNewRecordHasSelectOptions(t *testing.T) {
	db := testDB(t)
	fx := newEditorFixture(t, db)
	verifier, err := tenancy.NewVerifier([]byte(testServiceIdentitySecret))
	if err != nil {
		t.Fatalf("NewVerifier: %v", err)
	}
	tableID, authorID := setupBooksWithAuthor(t, db, fx.tenant)

	var view views.View
	if err := db.WithTenant(context.Background(), fx.tenant, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		view, err = views.CreateView(ctx, tx, identity.RoleAdmin, "createbook", tableID, "Edit", editBookConfigurationHTTP(), views.ViewOptions{})
		return err
	}); err != nil {
		t.Fatalf("criar view: %v", err)
	}

	renderH := buildEditorHandler(t, verifier, fx.guard, viewsCapability, renderViewHandler(fx.tracker, db))
	adminToken := mintServiceIdentity(t, testServiceIdentitySecret, strconv.Itoa(fx.adminID), fx.tenant, time.Minute)

	req := httptest.NewRequest(http.MethodGet, "/v1/tenants/"+string(fx.tenant)+"/views/"+strconv.Itoa(view.ID)+"/render", nil)
	req.SetPathValue("tenant", string(fx.tenant))
	req.SetPathValue("id", strconv.Itoa(view.ID))
	req.Header.Set("Authorization", "Bearer "+adminToken)
	rec := httptest.NewRecorder()
	renderH.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("render Edit (novo): status = %d, corpo = %s", rec.Code, rec.Body.String())
	}
	var resp renderEditResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decodificar resposta: %v", err)
	}
	if resp.RecordID != 0 || resp.Version != "" {
		t.Errorf("resp.RecordID/Version = %d/%q, esperado 0/\"\"", resp.RecordID, resp.Version)
	}
	if len(resp.Fields) != 2 {
		t.Fatalf("len(resp.Fields) = %d, esperado 2", len(resp.Fields))
	}
	authorField := resp.Fields[1]
	if len(authorField.Options) != 1 || authorField.Options[0].ID != authorID || authorField.Options[0].Label != "Octavia E. Butler" {
		t.Errorf("authorField.Options = %+v, esperado 1 opção real (Octavia E. Butler)", authorField.Options)
	}
	if resp.ActionName != "Save" {
		t.Errorf("resp.ActionName = %q, esperado Save", resp.ActionName)
	}
}

func TestSubmitViewHandler_CreatesRecordAndReturns201WithNavigate(t *testing.T) {
	db := testDB(t)
	fx := newEditorFixture(t, db)
	verifier, err := tenancy.NewVerifier([]byte(testServiceIdentitySecret))
	if err != nil {
		t.Fatalf("NewVerifier: %v", err)
	}
	tableID, authorID := setupBooksWithAuthor(t, db, fx.tenant)

	var view views.View
	if err := db.WithTenant(context.Background(), fx.tenant, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		view, err = views.CreateView(ctx, tx, identity.RoleAdmin, "createbook", tableID, "Edit", editBookConfigurationHTTP(), views.ViewOptions{})
		return err
	}); err != nil {
		t.Fatalf("criar view: %v", err)
	}

	submitH := buildEditorHandler(t, verifier, fx.guard, viewsCapability, submitViewHandler(fx.tracker, db))
	adminToken := mintServiceIdentity(t, testServiceIdentitySecret, strconv.Itoa(fx.adminID), fx.tenant, time.Minute)

	body := `{"values":{"title":"Parable of the Sower","author":` + strconv.Itoa(authorID) + `}}`
	req := httptest.NewRequest(http.MethodPost, "/v1/tenants/"+string(fx.tenant)+"/views/"+strconv.Itoa(view.ID)+"/submit", bytes.NewBufferString(body))
	req.SetPathValue("tenant", string(fx.tenant))
	req.SetPathValue("id", strconv.Itoa(view.ID))
	req.Header.Set("Authorization", "Bearer "+adminToken)
	req.Header.Set("Idempotency-Key", "create-book-1")
	rec := httptest.NewRecorder()
	submitH.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("submit (criar): status = %d, corpo = %s, esperado 201", rec.Code, rec.Body.String())
	}
	var resp submitViewResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decodificar resposta: %v", err)
	}
	if resp.Record["title"] != "Parable of the Sower" {
		t.Errorf("record[title] = %v, esperado Parable of the Sower", resp.Record["title"])
	}
	if resp.Navigate.Type != "reload" {
		t.Errorf("Navigate.Type = %q, esperado reload (destination_type ausente)", resp.Navigate.Type)
	}
}

func TestSubmitViewHandler_UpdateRequiresVersion(t *testing.T) {
	db := testDB(t)
	fx := newEditorFixture(t, db)
	verifier, err := tenancy.NewVerifier([]byte(testServiceIdentitySecret))
	if err != nil {
		t.Fatalf("NewVerifier: %v", err)
	}
	tableID, _ := setupBooksWithAuthor(t, db, fx.tenant)

	var view views.View
	if err := db.WithTenant(context.Background(), fx.tenant, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		view, err = views.CreateView(ctx, tx, identity.RoleAdmin, "editbook", tableID, "Edit", editBookConfigurationHTTP(), views.ViewOptions{})
		return err
	}); err != nil {
		t.Fatalf("criar view: %v", err)
	}

	submitH := buildEditorHandler(t, verifier, fx.guard, viewsCapability, submitViewHandler(fx.tracker, db))
	adminToken := mintServiceIdentity(t, testServiceIdentitySecret, strconv.Itoa(fx.adminID), fx.tenant, time.Minute)

	body := `{"record_id":1,"values":{"title":"x"}}`
	req := httptest.NewRequest(http.MethodPost, "/v1/tenants/"+string(fx.tenant)+"/views/"+strconv.Itoa(view.ID)+"/submit", bytes.NewBufferString(body))
	req.SetPathValue("tenant", string(fx.tenant))
	req.SetPathValue("id", strconv.Itoa(view.ID))
	req.Header.Set("Authorization", "Bearer "+adminToken)
	req.Header.Set("Idempotency-Key", "update-book-1")
	rec := httptest.NewRecorder()
	submitH.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("submit (update sem _version): status = %d, corpo = %s, esperado 400", rec.Code, rec.Body.String())
	}
}

func TestDeleteViewRowHandler_Success(t *testing.T) {
	db := testDB(t)
	fx := newEditorFixture(t, db)
	verifier, err := tenancy.NewVerifier([]byte(testServiceIdentitySecret))
	if err != nil {
		t.Fatalf("NewVerifier: %v", err)
	}
	tableID := setupBooksWithRecords(t, db, fx.tenant)

	var view views.View
	var bookID int
	var version string
	if err := db.WithTenant(context.Background(), fx.tenant, func(ctx context.Context, tx pgx.Tx) error {
		conf := map[string]any{
			"columns": []any{
				map[string]any{"type": "Field", "field_name": "title"},
				map[string]any{"type": "Action", "action_name": "Delete", "minRole": float64(1)},
			},
		}
		var err error
		view, err = views.CreateView(ctx, tx, identity.RoleAdmin, "booklist", tableID, "List", conf, views.ViewOptions{})
		if err != nil {
			return err
		}
		rows, err := records.Rows(ctx, tx, identity.RoleAdmin, records.Query{Table: "books", OrderBy: []records.OrderTerm{{Field: "id"}}, Limit: 1})
		if err != nil {
			return err
		}
		bookID = int(rows[0]["id"].(int32))
		version, _ = rows[0]["_version"].(string)
		return nil
	}); err != nil {
		t.Fatalf("preparar fixture: %v", err)
	}

	deleteH := buildEditorHandler(t, verifier, fx.guard, viewsCapability, deleteViewRowHandler(fx.tracker, db))
	adminToken := mintServiceIdentity(t, testServiceIdentitySecret, strconv.Itoa(fx.adminID), fx.tenant, time.Minute)

	req := httptest.NewRequest(http.MethodDelete, "/v1/tenants/"+string(fx.tenant)+"/views/"+strconv.Itoa(view.ID)+"/rows/"+strconv.Itoa(bookID)+"?version="+version, nil)
	req.SetPathValue("tenant", string(fx.tenant))
	req.SetPathValue("id", strconv.Itoa(view.ID))
	req.SetPathValue("recordId", strconv.Itoa(bookID))
	req.Header.Set("Authorization", "Bearer "+adminToken)
	rec := httptest.NewRecorder()
	deleteH.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("delete row: status = %d, corpo = %s, esperado 204", rec.Code, rec.Body.String())
	}

	if err := db.WithTenant(context.Background(), fx.tenant, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := records.Rows(ctx, tx, identity.RoleAdmin, records.Query{Table: "books", Where: records.Eq{Field: "id", Value: bookID}})
		if err != nil {
			return err
		}
		if len(rows) != 0 {
			t.Errorf("registro %d ainda existe após DELETE", bookID)
		}
		return nil
	}); err != nil {
		t.Fatalf("reler tabela: %v", err)
	}
}

func TestDeleteViewRowHandler_RequiresVersionParam(t *testing.T) {
	db := testDB(t)
	fx := newEditorFixture(t, db)
	verifier, err := tenancy.NewVerifier([]byte(testServiceIdentitySecret))
	if err != nil {
		t.Fatalf("NewVerifier: %v", err)
	}
	tableID := setupBooksWithRecords(t, db, fx.tenant)

	var view views.View
	if err := db.WithTenant(context.Background(), fx.tenant, func(ctx context.Context, tx pgx.Tx) error {
		conf := map[string]any{
			"columns": []any{
				map[string]any{"type": "Field", "field_name": "title"},
				map[string]any{"type": "Action", "action_name": "Delete"},
			},
		}
		var err error
		view, err = views.CreateView(ctx, tx, identity.RoleAdmin, "booklist", tableID, "List", conf, views.ViewOptions{})
		return err
	}); err != nil {
		t.Fatalf("criar view: %v", err)
	}

	deleteH := buildEditorHandler(t, verifier, fx.guard, viewsCapability, deleteViewRowHandler(fx.tracker, db))
	adminToken := mintServiceIdentity(t, testServiceIdentitySecret, strconv.Itoa(fx.adminID), fx.tenant, time.Minute)

	req := httptest.NewRequest(http.MethodDelete, "/v1/tenants/"+string(fx.tenant)+"/views/"+strconv.Itoa(view.ID)+"/rows/1", nil)
	req.SetPathValue("tenant", string(fx.tenant))
	req.SetPathValue("id", strconv.Itoa(view.ID))
	req.SetPathValue("recordId", "1")
	req.Header.Set("Authorization", "Bearer "+adminToken)
	rec := httptest.NewRecorder()
	deleteH.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("delete row sem ?version=: status = %d, corpo = %s, esperado 400", rec.Code, rec.Body.String())
	}
}
