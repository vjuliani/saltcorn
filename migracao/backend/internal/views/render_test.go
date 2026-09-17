// Testes de integração de render.go — exigem Postgres real (mesmo t.Skip
// de commands_test.go). Cobrem o caminho completo: catálogo real
// (internal/metadata) + registros reais (internal/records) + a view.
package views

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/vjuliani/saltcorn/migracao/backend/internal/identity"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/metadata"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/platform/database"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/platform/tenancy"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/records"
)

// booksWithFields estende testFixture com os campos title/pages e três
// registros — o "oráculo" mínimo para provar que CompileListPlan produz
// as MESMAS linhas/ordem que existem na tabela, não um resultado
// fabricado (mesmo espírito do fixture "books" citado na matriz GO-001
// como referência de correção).
func booksWithFields(t *testing.T, db *database.DB, tenant tenancy.Tenant, tableID int) {
	t.Helper()
	ctx := context.Background()
	if err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		if _, err := metadata.AddField(ctx, tx, identity.RoleAdmin, tableID, metadata.FieldDef{Name: "title", Type: metadata.FieldText, Required: true}); err != nil {
			return err
		}
		if _, err := metadata.AddField(ctx, tx, identity.RoleAdmin, tableID, metadata.FieldDef{Name: "pages", Type: metadata.FieldInteger}); err != nil {
			return err
		}
		for _, book := range []map[string]any{
			{"title": "Dune", "pages": 412},
			{"title": "Foundation", "pages": 255},
			{"title": "Neuromancer", "pages": 271},
		} {
			if _, err := records.CreateRecord(ctx, tx, identity.RoleAdmin, "books", book, nil); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatalf("setup de campos/registros: %v", err)
	}
}

func TestCompileListPlan_Success(t *testing.T) {
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

	var plan *ListPlan
	var hasMore bool
	if err := db.WithTenant(context.Background(), tenant, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		plan, hasMore, err = CompileListPlan(ctx, tx, identity.RoleAdmin, view.ID, 2, 0)
		return err
	}); err != nil {
		t.Fatalf("CompileListPlan() erro inesperado: %v", err)
	}

	if len(plan.Columns) != 2 || plan.Columns[0].FieldName != "title" || plan.Columns[1].FieldName != "pages" {
		t.Fatalf("plan.Columns = %+v, esperado [title pages]", plan.Columns)
	}
	if len(plan.Rows) != 2 {
		t.Fatalf("len(plan.Rows) = %d, esperado 2 (limit=2 de 3 registros)", len(plan.Rows))
	}
	if !hasMore {
		t.Error("hasMore = false, esperado true (há um 3º registro)")
	}
	if plan.Rows[0]["title"] != "Dune" {
		t.Errorf("plan.Rows[0][title] = %v, esperado Dune (ordem natural por id)", plan.Rows[0]["title"])
	}
}

func TestCompileListPlan_UnsupportedTemplateRejected(t *testing.T) {
	db := testDB(t)
	tenant, tableID := testFixture(t, db)
	booksWithFields(t, db, tenant, tableID)

	var view View
	if err := db.WithTenant(context.Background(), tenant, func(ctx context.Context, tx pgx.Tx) error {
		created, err := CreateView(ctx, tx, identity.RoleAdmin, "showbook", tableID, "Show", map[string]any{}, ViewOptions{})
		view = created
		return err
	}); err != nil {
		t.Fatalf("criar view: %v", err)
	}

	err := db.WithTenant(context.Background(), tenant, func(ctx context.Context, tx pgx.Tx) error {
		_, _, err := CompileListPlan(ctx, tx, identity.RoleAdmin, view.ID, 10, 0)
		return err
	})
	var target *UnsupportedLayoutError
	if !errors.As(err, &target) {
		t.Fatalf("CompileListPlan() erro = %v, esperado *UnsupportedLayoutError", err)
	}
}

func TestCompileListPlan_DeniedForUnauthorizedActor(t *testing.T) {
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
		_, _, err := CompileListPlan(ctx, tx, identity.RolePublic, view.ID, 10, 0)
		return err
	})
	if !errors.Is(err, ErrNotAuthorized) {
		t.Fatalf("CompileListPlan() erro = %v, esperado ErrNotAuthorized (view ainda não publicada)", err)
	}
}

func TestUpdateView_PublishBlocksIncompatibleLayout(t *testing.T) {
	db := testDB(t)
	tenant, tableID := testFixture(t, db)
	booksWithFields(t, db, tenant, tableID)

	var view View
	if err := db.WithTenant(context.Background(), tenant, func(ctx context.Context, tx pgx.Tx) error {
		created, err := CreateView(ctx, tx, identity.RoleAdmin, "showbook", tableID, "Show", map[string]any{}, ViewOptions{})
		view = created
		return err
	}); err != nil {
		t.Fatalf("criar view: %v", err)
	}

	publicRole := identity.RolePublic
	err := db.WithTenant(context.Background(), tenant, func(ctx context.Context, tx pgx.Tx) error {
		_, err := UpdateView(ctx, tx, identity.RoleAdmin, view.ID, view.Version, ViewUpdate{MinRole: &publicRole})
		return err
	})
	var target *UnsupportedLayoutError
	if !errors.As(err, &target) {
		t.Fatalf("UpdateView() (publicar view incompatível) erro = %v, esperado *UnsupportedLayoutError", err)
	}

	// A view permanece admin-only — o bloqueio não deixou um estado
	// "meio publicado".
	var reread View
	if err := db.WithTenant(context.Background(), tenant, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		reread, err = GetView(ctx, tx, identity.RoleAdmin, view.ID)
		return err
	}); err != nil {
		t.Fatalf("reler view: %v", err)
	}
	if reread.MinRole != identity.RoleAdmin {
		t.Errorf("view.MinRole = %v após publicação bloqueada, esperado permanecer RoleAdmin", reread.MinRole)
	}
}

func TestUpdateView_PublishAllowsCompatibleLayout(t *testing.T) {
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

	publicRole := identity.RolePublic
	if err := db.WithTenant(context.Background(), tenant, func(ctx context.Context, tx pgx.Tx) error {
		_, err := UpdateView(ctx, tx, identity.RoleAdmin, view.ID, view.Version, ViewUpdate{MinRole: &publicRole})
		return err
	}); err != nil {
		t.Fatalf("UpdateView() (publicar view compatível) erro inesperado: %v", err)
	}

	if err := db.WithTenant(context.Background(), tenant, func(ctx context.Context, tx pgx.Tx) error {
		_, _, err := CompileListPlan(ctx, tx, identity.RolePublic, view.ID, 10, 0)
		return err
	}); err != nil {
		t.Fatalf("CompileListPlan() como público após publicar: %v", err)
	}
}
