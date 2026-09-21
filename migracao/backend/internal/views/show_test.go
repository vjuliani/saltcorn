// Testes de integração de show.go — exigem Postgres real (mesmo t.Skip de
// commands_test.go/render_test.go).
package views

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/vjuliani/saltcorn/migracao/backend/internal/identity"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/records"
)

func TestCompileShowPlan_Success(t *testing.T) {
	db := testDB(t)
	tenant, tableID := testFixture(t, db)
	booksWithFields(t, db, tenant, tableID)

	var view View
	var bookID int
	if err := db.WithTenant(context.Background(), tenant, func(ctx context.Context, tx pgx.Tx) error {
		created, err := CreateView(ctx, tx, identity.RoleAdmin, "showbook", tableID, "Show", compatibleConfiguration(), ViewOptions{})
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

	var plan *ShowPlan
	if err := db.WithTenant(context.Background(), tenant, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		plan, err = CompileShowPlan(ctx, tx, identity.RoleAdmin, view.ID, bookID)
		return err
	}); err != nil {
		t.Fatalf("CompileShowPlan() erro inesperado: %v", err)
	}

	if len(plan.Columns) != 2 || plan.Columns[0].FieldName != "title" || plan.Columns[1].FieldName != "pages" {
		t.Fatalf("plan.Columns = %+v, esperado [title pages]", plan.Columns)
	}
	if plan.Values["title"] != "Dune" {
		t.Errorf("plan.Values[title] = %v, esperado Dune", plan.Values["title"])
	}
	if plan.RecordID != bookID {
		t.Errorf("plan.RecordID = %d, esperado %d", plan.RecordID, bookID)
	}
}

func TestCompileShowPlan_RecordNotFound(t *testing.T) {
	db := testDB(t)
	tenant, tableID := testFixture(t, db)
	booksWithFields(t, db, tenant, tableID)

	var view View
	if err := db.WithTenant(context.Background(), tenant, func(ctx context.Context, tx pgx.Tx) error {
		created, err := CreateView(ctx, tx, identity.RoleAdmin, "showbook", tableID, "Show", compatibleConfiguration(), ViewOptions{})
		view = created
		return err
	}); err != nil {
		t.Fatalf("criar view: %v", err)
	}

	err := db.WithTenant(context.Background(), tenant, func(ctx context.Context, tx pgx.Tx) error {
		_, err := CompileShowPlan(ctx, tx, identity.RoleAdmin, view.ID, 999999)
		return err
	})
	if !errors.Is(err, records.ErrRecordNotFound) {
		t.Fatalf("CompileShowPlan() erro = %v, esperado records.ErrRecordNotFound", err)
	}
}

func TestCompileShowPlan_WrongTemplateRejected(t *testing.T) {
	db := testDB(t)
	tenant, tableID := testFixture(t, db)
	booksWithFields(t, db, tenant, tableID)

	var view View
	if err := db.WithTenant(context.Background(), tenant, func(ctx context.Context, tx pgx.Tx) error {
		created, err := CreateView(ctx, tx, identity.RoleAdmin, "booklist", tableID, "List", compatibleConfiguration(), ViewOptions{})
		view = created
		return err
	}); err != nil {
		t.Fatalf("criar view: %v", err)
	}

	err := db.WithTenant(context.Background(), tenant, func(ctx context.Context, tx pgx.Tx) error {
		_, err := CompileShowPlan(ctx, tx, identity.RoleAdmin, view.ID, 1)
		return err
	})
	var target *UnsupportedLayoutError
	if !errors.As(err, &target) {
		t.Fatalf("CompileShowPlan() erro = %v, esperado *UnsupportedLayoutError (view é List, não Show)", err)
	}
}
