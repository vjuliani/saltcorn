// Testes de view aninhada (GO-051) — exigem Postgres real (mesmo t.Skip
// de commands_test.go/edit_test.go).
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

// booksWithChapters cria "books" (pai, campo "title") e "chapters"
// (filha, campos "heading" e FieldKey "book" → books) — mesma forma real
// de guitars/processed do pack piloto guitars (relação 1:N) — devolve o
// id de um livro criado e o id do catálogo de "chapters", prontos para
// as views de teste montarem `.books.chapters$book`.
func booksWithChapters(t *testing.T, db *database.DB, tenant tenancy.Tenant, booksTableID int) (bookID int, chaptersTableID int) {
	t.Helper()
	ctx := context.Background()
	if err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		if _, err := metadata.AddField(ctx, database.AsTx(tx), identity.RoleAdmin, booksTableID, metadata.FieldDef{Name: "title", Type: metadata.FieldText, Required: true}); err != nil {
			return err
		}
		chapters, err := metadata.CreateTable(ctx, database.AsTx(tx), identity.RoleAdmin, "chapters", metadata.TableOptions{})
		if err != nil {
			return err
		}
		chaptersTableID = chapters.ID
		if _, err := metadata.AddField(ctx, database.AsTx(tx), identity.RoleAdmin, chapters.ID, metadata.FieldDef{Name: "heading", Type: metadata.FieldText, Required: true}); err != nil {
			return err
		}
		if _, err := metadata.AddField(ctx, database.AsTx(tx), identity.RoleAdmin, chapters.ID, metadata.FieldDef{Name: "book", Type: metadata.FieldKey, References: "books"}); err != nil {
			return err
		}
		book, err := records.CreateRecord(ctx, tx, identity.RoleAdmin, "books", map[string]any{"title": "Dune"}, nil)
		if err != nil {
			return err
		}
		bookID = int(book["id"].(int32))
		return nil
	}); err != nil {
		t.Fatalf("setup de fixture de view aninhada: %v", err)
	}
	return bookID, chaptersTableID
}

func chapterEditConfiguration(actionName string) map[string]any {
	return map[string]any{
		"columns": []any{
			map[string]any{"type": "Field", "field_name": "heading", "fieldview": "edit"},
			map[string]any{"type": "Action", "action_name": actionName},
		},
	}
}

func bookWithEmbedConfiguration(actionName string) map[string]any {
	return map[string]any{
		"columns": []any{
			map[string]any{"type": "Field", "field_name": "title", "fieldview": "edit"},
			map[string]any{"type": "Action", "action_name": actionName},
		},
		"layout": map[string]any{
			"above": []any{
				map[string]any{
					"type":     "view",
					"view":     "editchapter",
					"relation": ".books.chapters$book",
				},
			},
		},
	}
}

func TestCompileEditPlan_NestedView_NewParent_NoEmbed(t *testing.T) {
	db := testDB(t)
	tenant, tableID := testFixture(t, db)
	booksWithChapters(t, db, tenant, tableID)

	var parentView View
	if err := db.WithTenant(context.Background(), tenant, func(ctx context.Context, tx pgx.Tx) error {
		created, err := CreateView(ctx, tx, identity.RoleAdmin, "createbook", tableID, "Edit", bookWithEmbedConfiguration("Save"), ViewOptions{})
		parentView = created
		return err
	}); err != nil {
		t.Fatalf("criar view pai: %v", err)
	}

	var plan *EditPlan
	if err := db.WithTenant(context.Background(), tenant, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		plan, err = CompileEditPlan(ctx, tx, identity.RoleAdmin, parentView.ID, 0)
		return err
	}); err != nil {
		t.Fatalf("CompileEditPlan(registro novo) erro inesperado: %v", err)
	}
	if len(plan.Nested) != 0 {
		t.Fatalf("plan.Nested = %+v, esperado vazio (registro pai ainda não existe)", plan.Nested)
	}
}

func TestCompileEditPlan_NestedView_ExistingParent_ListsChildren(t *testing.T) {
	db := testDB(t)
	tenant, tableID := testFixture(t, db)
	bookID, chaptersTableID := booksWithChapters(t, db, tenant, tableID)

	var parentView, childView View
	if err := db.WithTenant(context.Background(), tenant, func(ctx context.Context, tx pgx.Tx) error {
		child, err := CreateView(ctx, tx, identity.RoleAdmin, "editchapter", chaptersTableID, "Edit", chapterEditConfiguration("Save"), ViewOptions{})
		if err != nil {
			return err
		}
		childView = child
		parent, err := CreateView(ctx, tx, identity.RoleAdmin, "createbook", tableID, "Edit", bookWithEmbedConfiguration("Save"), ViewOptions{})
		if err != nil {
			return err
		}
		parentView = parent
		if _, err := records.CreateRecord(ctx, tx, identity.RoleAdmin, "chapters", map[string]any{"heading": "Chapter 1", "book": bookID}, nil); err != nil {
			return err
		}
		if _, err := records.CreateRecord(ctx, tx, identity.RoleAdmin, "chapters", map[string]any{"heading": "Chapter 2", "book": bookID}, nil); err != nil {
			return err
		}
		return nil
	}); err != nil {
		t.Fatalf("preparar fixture: %v", err)
	}

	var plan *EditPlan
	if err := db.WithTenant(context.Background(), tenant, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		plan, err = CompileEditPlan(ctx, tx, identity.RoleAdmin, parentView.ID, bookID)
		return err
	}); err != nil {
		t.Fatalf("CompileEditPlan(registro existente) erro inesperado: %v", err)
	}
	if len(plan.Nested) != 1 {
		t.Fatalf("len(plan.Nested) = %d, esperado 1", len(plan.Nested))
	}
	n := plan.Nested[0]
	if n.ViewID != childView.ID || n.ViewName != "editchapter" || n.ChildTable != "chapters" || n.FKField != "book" || n.ParentID != bookID {
		t.Fatalf("NestedEditPlan = %+v inesperado", n)
	}
	if len(n.Rows) != 2 {
		t.Fatalf("len(n.Rows) = %d, esperado 2 (dois capítulos)", len(n.Rows))
	}
	headings := map[string]bool{}
	for _, row := range n.Rows {
		for _, f := range row.Fields {
			if f.FieldName == "heading" {
				headings[f.Value.(string)] = true
			}
		}
		if row.ActionName != "Save" {
			t.Errorf("row.ActionName = %q, esperado Save", row.ActionName)
		}
	}
	if !headings["Chapter 1"] || !headings["Chapter 2"] {
		t.Errorf("headings = %v, esperado ambos os capítulos reais", headings)
	}
}

func TestCompileEditPlan_NestedView_RelationMismatch_IsUnsupportedLayoutError(t *testing.T) {
	db := testDB(t)
	tenant, tableID := testFixture(t, db)
	bookID, chaptersTableID := booksWithChapters(t, db, tenant, tableID)

	badConfig := map[string]any{
		"columns": []any{
			map[string]any{"type": "Field", "field_name": "title", "fieldview": "edit"},
			map[string]any{"type": "Action", "action_name": "Save"},
		},
		"layout": map[string]any{
			"above": []any{
				map[string]any{"type": "view", "view": "editchapter", "relation": ".wrongtable.chapters$book"},
			},
		},
	}

	var parentView View
	if err := db.WithTenant(context.Background(), tenant, func(ctx context.Context, tx pgx.Tx) error {
		if _, err := CreateView(ctx, tx, identity.RoleAdmin, "editchapter", chaptersTableID, "Edit", chapterEditConfiguration("Save"), ViewOptions{}); err != nil {
			return err
		}
		created, err := CreateView(ctx, tx, identity.RoleAdmin, "createbook", tableID, "Edit", badConfig, ViewOptions{})
		parentView = created
		return err
	}); err != nil {
		t.Fatalf("preparar fixture: %v", err)
	}

	err := db.WithTenant(context.Background(), tenant, func(ctx context.Context, tx pgx.Tx) error {
		_, err := CompileEditPlan(ctx, tx, identity.RoleAdmin, parentView.ID, bookID)
		return err
	})
	var unsupported *UnsupportedLayoutError
	if !errors.As(err, &unsupported) {
		t.Fatalf("CompileEditPlan com relation incompatível erro = %v, esperado *UnsupportedLayoutError", err)
	}
}
