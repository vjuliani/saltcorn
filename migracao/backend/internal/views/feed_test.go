// Testes do viewtemplate Feed (GO-051) — exigem Postgres real (mesmo
// t.Skip de commands_test.go/edit_test.go).
package views

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/vjuliani/saltcorn/migracao/backend/internal/identity"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/metadata"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/platform/database"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/records"
)

func showBookConfiguration() map[string]any {
	return map[string]any{
		"columns": []any{
			map[string]any{"type": "Field", "field_name": "title", "fieldview": "as_text"},
		},
	}
}

func editBooksConfigurationTitleOnly(actionName string) map[string]any {
	return map[string]any{
		"columns": []any{
			map[string]any{"type": "Field", "field_name": "title", "fieldview": "edit"},
			map[string]any{"type": "Action", "action_name": actionName},
		},
	}
}

func recordsAddTitleField(ctx context.Context, tx pgx.Tx, tableID int) (*metadata.Field, error) {
	return metadata.AddField(ctx, database.AsTx(tx), identity.RoleAdmin, tableID, metadata.FieldDef{Name: "title", Type: metadata.FieldText, Required: true})
}

// TestCompileFeedPlan_RendersShowPerRow é a prova direta do critério de
// aceite: "guitar_feed renderiza os cards via show_guitar por linha" —
// aqui com books/showbook, mesmo mecanismo.
func TestCompileFeedPlan_RendersShowPerRow(t *testing.T) {
	db := testDB(t)
	tenant, tableID := testFixture(t, db)

	var showView, feedView, createView View
	if err := db.WithTenant(context.Background(), tenant, func(ctx context.Context, tx pgx.Tx) error {
		if _, err := recordsAddTitleField(ctx, tx, tableID); err != nil {
			return err
		}
		sv, err := CreateView(ctx, tx, identity.RoleAdmin, "showbook", tableID, "Show", showBookConfiguration(), ViewOptions{})
		if err != nil {
			return err
		}
		showView = sv
		cv, err := CreateView(ctx, tx, identity.RoleAdmin, "createbook", tableID, "Edit", editBooksConfigurationTitleOnly("Save"), ViewOptions{})
		if err != nil {
			return err
		}
		createView = cv
		fv, err := CreateView(ctx, tx, identity.RoleAdmin, "bookfeed", tableID, "Feed", map[string]any{
			"show_view":      "showbook",
			"view_to_create": "createbook",
			"order_field":    "id",
			"descending":     false,
		}, ViewOptions{})
		if err != nil {
			return err
		}
		feedView = fv
		if _, err := records.CreateRecord(ctx, tx, identity.RoleAdmin, "books", map[string]any{"title": "The Left Hand of Darkness"}, nil); err != nil {
			return err
		}
		if _, err := records.CreateRecord(ctx, tx, identity.RoleAdmin, "books", map[string]any{"title": "Dune"}, nil); err != nil {
			return err
		}
		return nil
	}); err != nil {
		t.Fatalf("preparar fixture: %v", err)
	}

	var plan *FeedPlan
	var hasMore bool
	if err := db.WithTenant(context.Background(), tenant, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		plan, hasMore, err = CompileFeedPlan(ctx, tx, identity.RoleAdmin, feedView.ID, 50, 0)
		return err
	}); err != nil {
		t.Fatalf("CompileFeedPlan() erro inesperado: %v", err)
	}
	if hasMore {
		t.Errorf("hasMore = true, esperado false (só 2 linhas, limit 50)")
	}
	if len(plan.Cards) != 2 {
		t.Fatalf("len(plan.Cards) = %d, esperado 2", len(plan.Cards))
	}
	titles := map[string]bool{}
	for _, card := range plan.Cards {
		if card.Show.ViewID != showView.ID {
			t.Errorf("card.Show.ViewID = %d, esperado %d (showbook)", card.Show.ViewID, showView.ID)
		}
		titles[card.Show.Values["title"].(string)] = true
	}
	if !titles["Dune"] || !titles["The Left Hand of Darkness"] {
		t.Errorf("titles = %v, esperado os dois livros reais", titles)
	}
	if plan.ViewToCreateID != createView.ID || plan.ViewToCreateName != "createbook" {
		t.Errorf("ViewToCreateID/Name = %d/%q, esperado %d/\"createbook\"", plan.ViewToCreateID, plan.ViewToCreateName, createView.ID)
	}
}

func TestCompileFeedPlan_Pagination(t *testing.T) {
	db := testDB(t)
	tenant, tableID := testFixture(t, db)

	var feedView View
	if err := db.WithTenant(context.Background(), tenant, func(ctx context.Context, tx pgx.Tx) error {
		if _, err := recordsAddTitleField(ctx, tx, tableID); err != nil {
			return err
		}
		if _, err := CreateView(ctx, tx, identity.RoleAdmin, "showbook", tableID, "Show", showBookConfiguration(), ViewOptions{}); err != nil {
			return err
		}
		fv, err := CreateView(ctx, tx, identity.RoleAdmin, "bookfeed", tableID, "Feed", map[string]any{"show_view": "showbook"}, ViewOptions{})
		if err != nil {
			return err
		}
		feedView = fv
		for i := 0; i < 3; i++ {
			if _, err := records.CreateRecord(ctx, tx, identity.RoleAdmin, "books", map[string]any{"title": "book"}, nil); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatalf("preparar fixture: %v", err)
	}

	var hasMore bool
	if err := db.WithTenant(context.Background(), tenant, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		_, hasMore, err = CompileFeedPlan(ctx, tx, identity.RoleAdmin, feedView.ID, 2, 0)
		return err
	}); err != nil {
		t.Fatalf("CompileFeedPlan() erro inesperado: %v", err)
	}
	if !hasMore {
		t.Errorf("hasMore = false, esperado true (3 linhas, limit 2)")
	}
}

func TestCompileFeedPlan_ShowViewMissing_IsUnsupportedLayoutError(t *testing.T) {
	db := testDB(t)
	tenant, tableID := testFixture(t, db)

	var feedView View
	if err := db.WithTenant(context.Background(), tenant, func(ctx context.Context, tx pgx.Tx) error {
		fv, err := CreateView(ctx, tx, identity.RoleAdmin, "bookfeed", tableID, "Feed", map[string]any{"show_view": "does_not_exist"}, ViewOptions{})
		feedView = fv
		return err
	}); err != nil {
		t.Fatalf("preparar fixture: %v", err)
	}

	err := db.WithTenant(context.Background(), tenant, func(ctx context.Context, tx pgx.Tx) error {
		_, _, err := CompileFeedPlan(ctx, tx, identity.RoleAdmin, feedView.ID, 50, 0)
		return err
	})
	if !errors.Is(err, ErrViewNotFound) {
		t.Fatalf("CompileFeedPlan com show_view inexistente erro = %v, esperado ErrViewNotFound", err)
	}
}
