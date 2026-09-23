// Testes deste arquivo exigem Postgres real — pulam (t.Skip) se
// SALTCORN_GO_TEST_DATABASE_URL não estiver definida. Cobrem GO-051 via
// HTTP real (GET .../render): o viewtemplate Feed e a view Edit aninhada
// — a lógica de domínio já tem cobertura própria em
// internal/views/{feed,nested}_test.go; aqui só a fronteira HTTP.
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
	"github.com/vjuliani/saltcorn/migracao/backend/internal/metadata"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/platform/database"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/platform/tenancy"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/records"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/views"
)

// TestRenderFeedHandler_RendersCardsViaShowView é a prova HTTP do
// critério de aceite: "guitar_feed renderiza os cards via show_guitar
// por linha" — aqui com books/showbook, mesmo mecanismo real.
func TestRenderFeedHandler_RendersCardsViaShowView(t *testing.T) {
	db := testDB(t)
	fx := newEditorFixture(t, db)
	verifier, err := tenancy.NewVerifier([]byte(testServiceIdentitySecret))
	if err != nil {
		t.Fatalf("NewVerifier: %v", err)
	}
	tableID := setupBooksWithRecords(t, db, fx.tenant)

	var feedView views.View
	if err := db.WithTenant(context.Background(), fx.tenant, func(ctx context.Context, tx pgx.Tx) error {
		if _, err := views.CreateView(ctx, tx, identity.RoleAdmin, "showbook", tableID, "Show", map[string]any{
			"columns": []any{map[string]any{"type": "Field", "field_name": "title", "fieldview": "as_text"}},
		}, views.ViewOptions{}); err != nil {
			return err
		}
		fv, err := views.CreateView(ctx, tx, identity.RoleAdmin, "bookfeed", tableID, "Feed", map[string]any{
			"show_view": "showbook",
		}, views.ViewOptions{})
		feedView = fv
		return err
	}); err != nil {
		t.Fatalf("preparar fixture: %v", err)
	}

	renderH := buildEditorHandler(t, verifier, fx.guard, viewsCapability, renderViewHandler(fx.tracker, db))
	adminToken := mintServiceIdentity(t, testServiceIdentitySecret, strconv.Itoa(fx.adminID), fx.tenant, time.Minute)

	req := httptest.NewRequest(http.MethodGet, "/v1/tenants/"+string(fx.tenant)+"/views/"+strconv.Itoa(feedView.ID)+"/render", nil)
	req.SetPathValue("tenant", string(fx.tenant))
	req.SetPathValue("id", strconv.Itoa(feedView.ID))
	req.Header.Set("Authorization", "Bearer "+adminToken)
	rec := httptest.NewRecorder()
	renderH.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("render Feed: status = %d, corpo = %s", rec.Code, rec.Body.String())
	}
	var resp renderFeedResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decodificar resposta: %v", err)
	}
	if len(resp.Cards) != 3 {
		t.Fatalf("len(resp.Cards) = %d, esperado 3 (setupBooksWithRecords semeia 3 livros)", len(resp.Cards))
	}
	for _, card := range resp.Cards {
		if _, ok := card.Show.Values["title"]; !ok {
			t.Errorf("card.Show.Values = %+v, esperado incluir \"title\"", card.Show.Values)
		}
	}
}

// TestRenderEditHandler_NestedView_ExistingParent_IncludesEmbeddedRows é
// a prova HTTP do critério de aceite: "create_guitar renderiza... o
// sub-formulário edit_processed_embed embutido, filtrado pela relação da
// linha pai" — aqui com books/chapters, mesma relação 1:N real.
func TestRenderEditHandler_NestedView_ExistingParent_IncludesEmbeddedRows(t *testing.T) {
	db := testDB(t)
	fx := newEditorFixture(t, db)
	verifier, err := tenancy.NewVerifier([]byte(testServiceIdentitySecret))
	if err != nil {
		t.Fatalf("NewVerifier: %v", err)
	}

	var parentView views.View
	var bookID int
	if err := db.WithTenant(context.Background(), fx.tenant, func(ctx context.Context, tx pgx.Tx) error {
		books, err := metadata.CreateTable(ctx, database.AsTx(tx), identity.RoleAdmin, "books", metadata.TableOptions{})
		if err != nil {
			return err
		}
		if _, err := metadata.AddField(ctx, database.AsTx(tx), identity.RoleAdmin, books.ID, metadata.FieldDef{Name: "title", Type: metadata.FieldText, Required: true}); err != nil {
			return err
		}
		chapters, err := metadata.CreateTable(ctx, database.AsTx(tx), identity.RoleAdmin, "chapters", metadata.TableOptions{})
		if err != nil {
			return err
		}
		if _, err := metadata.AddField(ctx, database.AsTx(tx), identity.RoleAdmin, chapters.ID, metadata.FieldDef{Name: "heading", Type: metadata.FieldText, Required: true}); err != nil {
			return err
		}
		if _, err := metadata.AddField(ctx, database.AsTx(tx), identity.RoleAdmin, chapters.ID, metadata.FieldDef{Name: "book", Type: metadata.FieldKey, References: "books"}); err != nil {
			return err
		}
		if _, err := views.CreateView(ctx, tx, identity.RoleAdmin, "editchapter", chapters.ID, "Edit", map[string]any{
			"columns": []any{
				map[string]any{"type": "Field", "field_name": "heading", "fieldview": "edit"},
				map[string]any{"type": "Action", "action_name": "Save"},
			},
		}, views.ViewOptions{}); err != nil {
			return err
		}
		pv, err := views.CreateView(ctx, tx, identity.RoleAdmin, "createbook", books.ID, "Edit", map[string]any{
			"columns": []any{
				map[string]any{"type": "Field", "field_name": "title", "fieldview": "edit"},
				map[string]any{"type": "Action", "action_name": "Save"},
			},
			"layout": map[string]any{
				"above": []any{
					map[string]any{"type": "view", "view": "editchapter", "relation": ".books.chapters$book"},
				},
			},
		}, views.ViewOptions{})
		if err != nil {
			return err
		}
		parentView = pv

		bookRow, err := records.CreateRecord(ctx, tx, identity.RoleAdmin, "books", map[string]any{"title": "Dune"}, nil)
		if err != nil {
			return err
		}
		bookID = int(bookRow["id"].(int32))
		if _, err := records.CreateRecord(ctx, tx, identity.RoleAdmin, "chapters", map[string]any{"heading": "Chapter 1", "book": bookID}, nil); err != nil {
			return err
		}
		return nil
	}); err != nil {
		t.Fatalf("preparar fixture: %v", err)
	}

	renderH := buildEditorHandler(t, verifier, fx.guard, viewsCapability, renderViewHandler(fx.tracker, db))
	adminToken := mintServiceIdentity(t, testServiceIdentitySecret, strconv.Itoa(fx.adminID), fx.tenant, time.Minute)

	req := httptest.NewRequest(http.MethodGet, "/v1/tenants/"+string(fx.tenant)+"/views/"+strconv.Itoa(parentView.ID)+"/render?record="+strconv.Itoa(bookID), nil)
	req.SetPathValue("tenant", string(fx.tenant))
	req.SetPathValue("id", strconv.Itoa(parentView.ID))
	req.Header.Set("Authorization", "Bearer "+adminToken)
	rec := httptest.NewRecorder()
	renderH.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("render Edit (com embed): status = %d, corpo = %s", rec.Code, rec.Body.String())
	}
	var resp renderEditResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decodificar resposta: %v", err)
	}
	if len(resp.Nested) != 1 {
		t.Fatalf("len(resp.Nested) = %d, esperado 1", len(resp.Nested))
	}
	if resp.Nested[0].ViewName != "editchapter" || resp.Nested[0].FKField != "book" || resp.Nested[0].ParentID != bookID {
		t.Fatalf("resp.Nested[0] = %+v inesperado", resp.Nested[0])
	}
	if len(resp.Nested[0].Rows) != 1 {
		t.Fatalf("len(resp.Nested[0].Rows) = %d, esperado 1", len(resp.Nested[0].Rows))
	}
}
