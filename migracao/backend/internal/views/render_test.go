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
		if _, err := metadata.AddField(ctx, database.AsTx(tx), identity.RoleAdmin, tableID, metadata.FieldDef{Name: "title", Type: metadata.FieldText, Required: true}); err != nil {
			return err
		}
		if _, err := metadata.AddField(ctx, database.AsTx(tx), identity.RoleAdmin, tableID, metadata.FieldDef{Name: "pages", Type: metadata.FieldInteger}); err != nil {
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

// booksWithAuthorJoin estende testFixture com uma tabela "authors"
// (campo de texto "name") e um campo FieldKey "author" em "books"
// referenciando-a, mais dois autores e dois livros — o fixture mínimo
// para exercitar JoinField (guitar_list/list_processed do pack guitars
// usam exatamente este padrão: um campo key + join_field "campo.remoto").
func booksWithAuthorJoin(t *testing.T, db *database.DB, tenant tenancy.Tenant, tableID int) (authorAID, authorBID int) {
	t.Helper()
	ctx := context.Background()
	if err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		// Escrita liberada a qualquer papel — necessário para
		// TestDeleteListRow_Success provar que o ActionMinRole=100 do
		// NÓ DE COLUNA (não a tabela) é quem decide se um ator público
		// pode excluir; com o padrão (MinRoleWrite=RoleAdmin) nenhum
		// ActionMinRole permissivo passaria da checagem de tabela em
		// records.DeleteRecordTx.
		if _, err := metadata.UpdateTablePermissions(ctx, database.AsTx(tx), identity.RoleAdmin, tableID, identity.RolePublic, identity.RolePublic); err != nil {
			return err
		}
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
		a, err := records.CreateRecord(ctx, tx, identity.RoleAdmin, "authors", map[string]any{"name": "Ursula K. Le Guin"}, nil)
		if err != nil {
			return err
		}
		authorAID = int(a["id"].(int32))
		b, err := records.CreateRecord(ctx, tx, identity.RoleAdmin, "authors", map[string]any{"name": "Frank Herbert"}, nil)
		if err != nil {
			return err
		}
		authorBID = int(b["id"].(int32))
		if _, err := records.CreateRecord(ctx, tx, identity.RoleAdmin, "books", map[string]any{"title": "The Left Hand of Darkness", "author": authorAID}, nil); err != nil {
			return err
		}
		if _, err := records.CreateRecord(ctx, tx, identity.RoleAdmin, "books", map[string]any{"title": "Dune", "author": authorBID}, nil); err != nil {
			return err
		}
		return nil
	}); err != nil {
		t.Fatalf("setup de join fixture: %v", err)
	}
	return authorAID, authorBID
}

func joinAndActionConfiguration() map[string]any {
	return map[string]any{
		"columns": []any{
			fieldColumn("title"),
			map[string]any{"type": "JoinField", "join_field": "author.name"},
			map[string]any{"type": "Action", "action_name": "Delete", "minRole": float64(100)},
		},
	}
}

func TestCompileListPlan_JoinFieldAndAction(t *testing.T) {
	db := testDB(t)
	tenant, tableID := testFixture(t, db)
	booksWithAuthorJoin(t, db, tenant, tableID)

	var view View
	if err := db.WithTenant(context.Background(), tenant, func(ctx context.Context, tx pgx.Tx) error {
		created, err := CreateView(ctx, tx, identity.RoleAdmin, "booklist", tableID, "List", joinAndActionConfiguration(), ViewOptions{})
		view = created
		return err
	}); err != nil {
		t.Fatalf("criar view: %v", err)
	}

	var plan *ListPlan
	if err := db.WithTenant(context.Background(), tenant, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		plan, _, err = CompileListPlan(ctx, tx, identity.RoleAdmin, view.ID, 10, 0)
		return err
	}); err != nil {
		t.Fatalf("CompileListPlan() erro inesperado: %v", err)
	}

	if len(plan.Columns) != 3 {
		t.Fatalf("len(plan.Columns) = %d, esperado 3", len(plan.Columns))
	}
	if plan.Columns[1].Kind != ListColumnJoinField || plan.Columns[1].FieldName != "author__name" {
		t.Fatalf("plan.Columns[1] = %+v, esperado join_field author__name", plan.Columns[1])
	}
	if plan.Columns[2].Kind != ListColumnAction || plan.Columns[2].ActionName != "Delete" {
		t.Fatalf("plan.Columns[2] = %+v, esperado ação Delete", plan.Columns[2])
	}
	if len(plan.Rows) != 2 {
		t.Fatalf("len(plan.Rows) = %d, esperado 2", len(plan.Rows))
	}
	if plan.Rows[0]["author__name"] != "Ursula K. Le Guin" {
		t.Errorf("plan.Rows[0][author__name] = %v, esperado Ursula K. Le Guin (join real, não inventado)", plan.Rows[0]["author__name"])
	}
}

func TestDeleteListRow_Success(t *testing.T) {
	db := testDB(t)
	tenant, tableID := testFixture(t, db)
	booksWithAuthorJoin(t, db, tenant, tableID)

	var view View
	var bookID int
	var bookVersion string
	if err := db.WithTenant(context.Background(), tenant, func(ctx context.Context, tx pgx.Tx) error {
		// MinRole público: sem publicar, GetView já rejeitaria o ator
		// RolePublic antes mesmo de chegar ao ActionMinRole da coluna —
		// este teste prova especificamente que ActionMinRole=100 (o
		// nó de coluna "Delete" do pack guitars) permite um ator sem
		// nenhum papel especial, não a checagem de leitura da view.
		created, err := CreateView(ctx, tx, identity.RoleAdmin, "booklist", tableID, "List", joinAndActionConfiguration(), ViewOptions{MinRole: identity.RolePublic})
		view = created
		if err != nil {
			return err
		}
		rows, err := records.Rows(ctx, tx, identity.RoleAdmin, records.Query{Table: "books", Limit: 1})
		if err != nil {
			return err
		}
		bookID = int(rows[0]["id"].(int32))
		bookVersion, _ = rows[0]["_version"].(string)
		return nil
	}); err != nil {
		t.Fatalf("preparar fixture: %v", err)
	}

	if err := db.WithTenant(context.Background(), tenant, func(ctx context.Context, tx pgx.Tx) error {
		return DeleteListRow(ctx, tx, identity.RolePublic, view.ID, bookID, bookVersion)
	}); err != nil {
		t.Fatalf("DeleteListRow() erro inesperado (minRole=100 permite ator público): %v", err)
	}

	if err := db.WithTenant(context.Background(), tenant, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := records.Rows(ctx, tx, identity.RoleAdmin, records.Query{Table: "books", Where: records.Eq{Field: "id", Value: bookID}})
		if err != nil {
			return err
		}
		if len(rows) != 0 {
			t.Errorf("registro %d ainda existe após DeleteListRow", bookID)
		}
		return nil
	}); err != nil {
		t.Fatalf("reler tabela: %v", err)
	}
}

func TestDeleteListRow_RejectsViewWithoutDeleteAction(t *testing.T) {
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
		return DeleteListRow(ctx, tx, identity.RoleAdmin, view.ID, 1, "")
	})
	var target *UnsupportedLayoutError
	if !errors.As(err, &target) {
		t.Fatalf("DeleteListRow() erro = %v, esperado *UnsupportedLayoutError (view sem ação Delete)", err)
	}
}

// TestDeleteListRow_RejectsBelowActionMinRole prova a granularidade
// ADICIONAL do ActionMinRole do nó de coluna: mesmo com a TABELA liberada
// para escrita pública (booksWithAuthorJoin), uma ação "Delete" sem
// `minRole` explícito usa o padrão seguro (identity.RoleAdmin) — um ator
// público continua sem poder excluir, porque o nó de coluna em si não
// concedeu isso.
func TestDeleteListRow_RejectsBelowActionMinRole(t *testing.T) {
	db := testDB(t)
	tenant, tableID := testFixture(t, db)
	booksWithAuthorJoin(t, db, tenant, tableID)

	conf := map[string]any{
		"columns": []any{
			fieldColumn("title"),
			map[string]any{"type": "Action", "action_name": "Delete"}, // sem minRole -> default RoleAdmin
		},
	}
	var view View
	var bookID int
	var version string
	if err := db.WithTenant(context.Background(), tenant, func(ctx context.Context, tx pgx.Tx) error {
		created, err := CreateView(ctx, tx, identity.RoleAdmin, "booklist", tableID, "List", conf, ViewOptions{MinRole: identity.RolePublic})
		view = created
		if err != nil {
			return err
		}
		rows, err := records.Rows(ctx, tx, identity.RoleAdmin, records.Query{Table: "books", Limit: 1})
		if err != nil {
			return err
		}
		bookID = int(rows[0]["id"].(int32))
		version, _ = rows[0]["_version"].(string)
		return nil
	}); err != nil {
		t.Fatalf("preparar fixture: %v", err)
	}

	err := db.WithTenant(context.Background(), tenant, func(ctx context.Context, tx pgx.Tx) error {
		return DeleteListRow(ctx, tx, identity.RolePublic, view.ID, bookID, version)
	})
	if !errors.Is(err, records.ErrNotAuthorized) {
		t.Fatalf("DeleteListRow() erro = %v, esperado records.ErrNotAuthorized (ActionMinRole default RoleAdmin)", err)
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
