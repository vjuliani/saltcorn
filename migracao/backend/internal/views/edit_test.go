// Testes de integração de edit.go — exigem Postgres real (mesmo t.Skip de
// commands_test.go/render_test.go/show_test.go).
package views

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/vjuliani/saltcorn/migracao/backend/internal/identity"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/records"
)

// editBooksConfiguration é a mesma forma real do pack guitars para um
// Edit view: um campo de texto ("edit"), um campo key ("select", com
// opções carregadas da tabela referenciada) e uma ação de submissão.
func editBooksConfiguration(actionName string) map[string]any {
	return map[string]any{
		"columns": []any{
			map[string]any{"type": "Field", "field_name": "title", "fieldview": "edit"},
			map[string]any{"type": "Field", "field_name": "author", "fieldview": "select"},
			map[string]any{"type": "Action", "action_name": actionName},
		},
	}
}

func TestCompileEditPlan_NewRecord(t *testing.T) {
	db := testDB(t)
	tenant, tableID := testFixture(t, db)
	authorAID, authorBID := booksWithAuthorJoin(t, db, tenant, tableID)

	var view View
	if err := db.WithTenant(context.Background(), tenant, func(ctx context.Context, tx pgx.Tx) error {
		created, err := CreateView(ctx, tx, identity.RoleAdmin, "createbook", tableID, "Edit", editBooksConfiguration("Save"), ViewOptions{})
		view = created
		return err
	}); err != nil {
		t.Fatalf("criar view: %v", err)
	}

	var plan *EditPlan
	if err := db.WithTenant(context.Background(), tenant, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		plan, err = CompileEditPlan(ctx, tx, identity.RoleAdmin, view.ID, 0)
		return err
	}); err != nil {
		t.Fatalf("CompileEditPlan() erro inesperado: %v", err)
	}

	if plan.RecordID != 0 || plan.Version != "" {
		t.Errorf("plan.RecordID/Version = %d/%q, esperado 0/\"\" (registro novo)", plan.RecordID, plan.Version)
	}
	if len(plan.Fields) != 2 {
		t.Fatalf("len(plan.Fields) = %d, esperado 2", len(plan.Fields))
	}
	if plan.Fields[0].Value != nil {
		t.Errorf("plan.Fields[0].Value = %v, esperado nil (registro novo)", plan.Fields[0].Value)
	}
	authorField := plan.Fields[1]
	if authorField.FieldName != "author" || len(authorField.Options) != 2 {
		t.Fatalf("plan.Fields[1] = %+v, esperado campo author com 2 opções", authorField)
	}
	byID := map[int]string{authorField.Options[0].ID: authorField.Options[0].Label, authorField.Options[1].ID: authorField.Options[1].Label}
	if byID[authorAID] != "Ursula K. Le Guin" || byID[authorBID] != "Frank Herbert" {
		t.Errorf("opções = %+v, esperado rótulos reais das duas autoras/autores", authorField.Options)
	}
	if plan.ActionName != "Save" {
		t.Errorf("plan.ActionName = %q, esperado Save", plan.ActionName)
	}
}

func TestCompileEditPlan_ExistingRecord(t *testing.T) {
	db := testDB(t)
	tenant, tableID := testFixture(t, db)
	booksWithAuthorJoin(t, db, tenant, tableID)

	var view View
	var bookID int
	if err := db.WithTenant(context.Background(), tenant, func(ctx context.Context, tx pgx.Tx) error {
		created, err := CreateView(ctx, tx, identity.RoleAdmin, "editbook", tableID, "Edit", editBooksConfiguration("Save"), ViewOptions{})
		view = created
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

	var plan *EditPlan
	if err := db.WithTenant(context.Background(), tenant, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		plan, err = CompileEditPlan(ctx, tx, identity.RoleAdmin, view.ID, bookID)
		return err
	}); err != nil {
		t.Fatalf("CompileEditPlan() erro inesperado: %v", err)
	}

	if plan.RecordID != bookID || plan.Version == "" {
		t.Errorf("plan.RecordID/Version = %d/%q, esperado %d/(não vazio)", plan.RecordID, plan.Version, bookID)
	}
	if plan.Fields[0].Value != "The Left Hand of Darkness" {
		t.Errorf("plan.Fields[0].Value = %v, esperado o título já gravado", plan.Fields[0].Value)
	}
}

func TestSubmitEditView_CreatesRecordAndDefaultsToReload(t *testing.T) {
	db := testDB(t)
	tenant, tableID := testFixture(t, db)
	authorAID, _ := booksWithAuthorJoin(t, db, tenant, tableID)

	var view View
	if err := db.WithTenant(context.Background(), tenant, func(ctx context.Context, tx pgx.Tx) error {
		created, err := CreateView(ctx, tx, identity.RoleAdmin, "createbook", tableID, "Edit", editBooksConfiguration("Save"), ViewOptions{})
		view = created
		return err
	}); err != nil {
		t.Fatalf("criar view: %v", err)
	}

	var result *EditSubmitResult
	if err := db.WithTenant(context.Background(), tenant, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		result, err = SubmitEditView(ctx, tx, identity.RoleAdmin, view.ID, 0, "", map[string]any{"title": "Neuromancer", "author": authorAID})
		return err
	}); err != nil {
		t.Fatalf("SubmitEditView() erro inesperado: %v", err)
	}

	if result.Record["title"] != "Neuromancer" {
		t.Errorf("record[title] = %v, esperado Neuromancer", result.Record["title"])
	}
	if result.Navigate.Type != "reload" {
		t.Errorf("Navigate.Type = %q, esperado \"reload\" (destination_type ausente)", result.Navigate.Type)
	}

	// Prova que a escrita é REAL, não só a resposta — relendo a tabela.
	if err := db.WithTenant(context.Background(), tenant, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := records.Rows(ctx, tx, identity.RoleAdmin, records.Query{Table: "books", Where: records.Eq{Field: "title", Value: "Neuromancer"}})
		if err != nil {
			return err
		}
		if len(rows) != 1 {
			t.Errorf("len(rows) = %d, esperado 1 registro persistido", len(rows))
		}
		return nil
	}); err != nil {
		t.Fatalf("reler tabela: %v", err)
	}
}

func TestSubmitEditView_UpdatesExistingRecord(t *testing.T) {
	db := testDB(t)
	tenant, tableID := testFixture(t, db)
	booksWithAuthorJoin(t, db, tenant, tableID)

	var view View
	var bookID int
	var version string
	if err := db.WithTenant(context.Background(), tenant, func(ctx context.Context, tx pgx.Tx) error {
		created, err := CreateView(ctx, tx, identity.RoleAdmin, "editbook", tableID, "Edit", editBooksConfiguration("Save"), ViewOptions{})
		view = created
		if err != nil {
			return err
		}
		plan, err := CompileEditPlan(ctx, tx, identity.RoleAdmin, view.ID, 0)
		_ = plan
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

	if err := db.WithTenant(context.Background(), tenant, func(ctx context.Context, tx pgx.Tx) error {
		_, err := SubmitEditView(ctx, tx, identity.RoleAdmin, view.ID, bookID, version, map[string]any{"title": "The Left Hand of Darkness (revised)"})
		return err
	}); err != nil {
		t.Fatalf("SubmitEditView() (update) erro inesperado: %v", err)
	}

	if err := db.WithTenant(context.Background(), tenant, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := records.Rows(ctx, tx, identity.RoleAdmin, records.Query{Table: "books", Where: records.Eq{Field: "id", Value: bookID}})
		if err != nil {
			return err
		}
		if rows[0]["title"] != "The Left Hand of Darkness (revised)" {
			t.Errorf("title = %v, esperado o valor atualizado", rows[0]["title"])
		}
		return nil
	}); err != nil {
		t.Fatalf("reler tabela: %v", err)
	}
}

func TestSubmitEditView_StaleVersionConflict(t *testing.T) {
	db := testDB(t)
	tenant, tableID := testFixture(t, db)
	booksWithAuthorJoin(t, db, tenant, tableID)

	var view View
	var bookID int
	if err := db.WithTenant(context.Background(), tenant, func(ctx context.Context, tx pgx.Tx) error {
		created, err := CreateView(ctx, tx, identity.RoleAdmin, "editbook", tableID, "Edit", editBooksConfiguration("Save"), ViewOptions{})
		view = created
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

	err := db.WithTenant(context.Background(), tenant, func(ctx context.Context, tx pgx.Tx) error {
		_, err := SubmitEditView(ctx, tx, identity.RoleAdmin, view.ID, bookID, "obsoleta-nao-bate", map[string]any{"title": "x"})
		return err
	})
	if !errors.Is(err, records.ErrVersionConflict) {
		t.Fatalf("SubmitEditView() erro = %v, esperado records.ErrVersionConflict", err)
	}
}

func TestSubmitEditView_RejectsFieldNotInView(t *testing.T) {
	db := testDB(t)
	tenant, tableID := testFixture(t, db)
	booksWithAuthorJoin(t, db, tenant, tableID)

	var view View
	if err := db.WithTenant(context.Background(), tenant, func(ctx context.Context, tx pgx.Tx) error {
		// View com só "title" editável — "author" NÃO faz parte dela.
		conf := map[string]any{
			"columns": []any{
				map[string]any{"type": "Field", "field_name": "title", "fieldview": "edit"},
				map[string]any{"type": "Action", "action_name": "Save"},
			},
		}
		created, err := CreateView(ctx, tx, identity.RoleAdmin, "titleonly", tableID, "Edit", conf, ViewOptions{})
		view = created
		return err
	}); err != nil {
		t.Fatalf("criar view: %v", err)
	}

	err := db.WithTenant(context.Background(), tenant, func(ctx context.Context, tx pgx.Tx) error {
		_, err := SubmitEditView(ctx, tx, identity.RoleAdmin, view.ID, 0, "", map[string]any{"title": "x", "author": 1})
		return err
	})
	var target *UnsupportedLayoutError
	if !errors.As(err, &target) {
		t.Fatalf("SubmitEditView() erro = %v, esperado *UnsupportedLayoutError (author fora da view)", err)
	}
}

func TestSubmitEditView_NavigateBackToReferer(t *testing.T) {
	db := testDB(t)
	tenant, tableID := testFixture(t, db)
	authorAID, _ := booksWithAuthorJoin(t, db, tenant, tableID)

	var view View
	if err := db.WithTenant(context.Background(), tenant, func(ctx context.Context, tx pgx.Tx) error {
		conf := editBooksConfiguration("Save")
		conf["destination_type"] = "Back to referer"
		created, err := CreateView(ctx, tx, identity.RoleAdmin, "createbook", tableID, "Edit", conf, ViewOptions{})
		view = created
		return err
	}); err != nil {
		t.Fatalf("criar view: %v", err)
	}

	var result *EditSubmitResult
	if err := db.WithTenant(context.Background(), tenant, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		result, err = SubmitEditView(ctx, tx, identity.RoleAdmin, view.ID, 0, "", map[string]any{"title": "x", "author": authorAID})
		return err
	}); err != nil {
		t.Fatalf("SubmitEditView() erro inesperado: %v", err)
	}
	if result.Navigate.Type != "referer" {
		t.Errorf("Navigate.Type = %q, esperado \"referer\"", result.Navigate.Type)
	}
}

func TestSubmitEditView_NavigateToNamedView(t *testing.T) {
	db := testDB(t)
	tenant, tableID := testFixture(t, db)
	authorAID, _ := booksWithAuthorJoin(t, db, tenant, tableID)

	var view View
	if err := db.WithTenant(context.Background(), tenant, func(ctx context.Context, tx pgx.Tx) error {
		conf := editBooksConfiguration("Save")
		conf["destination_type"] = "View"
		conf["view_when_done"] = "booklist"
		created, err := CreateView(ctx, tx, identity.RoleAdmin, "createbook", tableID, "Edit", conf, ViewOptions{})
		view = created
		return err
	}); err != nil {
		t.Fatalf("criar view: %v", err)
	}

	var result *EditSubmitResult
	if err := db.WithTenant(context.Background(), tenant, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		result, err = SubmitEditView(ctx, tx, identity.RoleAdmin, view.ID, 0, "", map[string]any{"title": "x", "author": authorAID})
		return err
	}); err != nil {
		t.Fatalf("SubmitEditView() erro inesperado: %v", err)
	}
	if result.Navigate.Type != "view" || result.Navigate.ViewName != "booklist" {
		t.Errorf("Navigate = %+v, esperado {view booklist}", result.Navigate)
	}
}
