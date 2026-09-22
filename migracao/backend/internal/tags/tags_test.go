// Testes de internal/tags (GO-045) — exigem Postgres real, pulam
// (t.Skip) se SALTCORN_GO_TEST_DATABASE_URL não estiver definida, mesmo
// padrão de internal/library.
package tags

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/vjuliani/saltcorn/migracao/backend/internal/identity"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/metadata"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/platform/database"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/platform/tenancy"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/views"
)

func testDB(t *testing.T) *database.DB {
	t.Helper()
	dsn := os.Getenv("SALTCORN_GO_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("SALTCORN_GO_TEST_DATABASE_URL não definida — pulando teste que exige Postgres real")
	}
	db, err := database.Open(context.Background(), dsn)
	if err != nil {
		t.Fatalf("database.Open() erro inesperado: %v", err)
	}
	t.Cleanup(db.Close)
	return db
}

func sanitizeForSchema(name string) string {
	out := make([]rune, 0, len(name))
	for _, r := range name {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			out = append(out, r)
		} else {
			out = append(out, '_')
		}
	}
	return string(out)
}

// tagsFixture monta um tenant isolado com _sc_tables/_sc_views/_sc_tags
// prontos, uma tabela "posts" e uma view List sobre ela — o suficiente
// para AddEntry referenciar uma entidade real de cada tipo suportado.
func tagsFixture(t *testing.T) (db *database.DB, tenant tenancy.Tenant, tableID, viewID int) {
	t.Helper()
	db = testDB(t)
	ctx := context.Background()
	tenant = tenancy.Tenant(fmt.Sprintf("tags_test_%s", sanitizeForSchema(t.Name())))

	if err := db.WithTenant(ctx, "public", func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, fmt.Sprintf(`CREATE SCHEMA IF NOT EXISTS %s`, pgx.Identifier{string(tenant)}.Sanitize()))
		return err
	}); err != nil {
		t.Fatalf("criar schema de teste: %v", err)
	}
	t.Cleanup(func() {
		_ = db.WithTenant(context.Background(), "public", func(ctx context.Context, tx pgx.Tx) error {
			_, err := tx.Exec(ctx, fmt.Sprintf(`DROP SCHEMA IF EXISTS %s CASCADE`, pgx.Identifier{string(tenant)}.Sanitize()))
			return err
		})
	})

	if err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		if err := metadata.EnsureSchema(ctx, database.AsTx(tx)); err != nil {
			return err
		}
		if err := views.EnsureSchema(ctx, tx); err != nil {
			return err
		}
		if err := EnsureSchema(ctx, tx); err != nil {
			return err
		}
		table, err := metadata.CreateTable(ctx, database.AsTx(tx), identity.RoleAdmin, "posts", metadata.TableOptions{})
		if err != nil {
			return err
		}
		tableID = table.ID
		if _, err := metadata.AddField(ctx, database.AsTx(tx), identity.RoleAdmin, table.ID, metadata.FieldDef{Name: "title", Type: metadata.FieldText}); err != nil {
			return err
		}
		v, err := views.CreateView(ctx, tx, identity.RoleAdmin, "posts_list", table.ID, "List",
			map[string]any{"columns": []any{map[string]any{"type": "Field", "field_name": "title"}}},
			views.ViewOptions{},
		)
		if err != nil {
			return err
		}
		viewID = v.ID
		return nil
	}); err != nil {
		t.Fatalf("setup do fixture: %v", err)
	}
	return db, tenant, tableID, viewID
}

func TestCreateTag_IdempotentByName(t *testing.T) {
	db, tenant, _, _ := tagsFixture(t)
	ctx := context.Background()

	var first, second *Tag
	if err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		first, err = CreateTag(ctx, tx, identity.RoleAdmin, "important")
		return err
	}); err != nil {
		t.Fatalf("CreateTag (1ª vez): %v", err)
	}
	if err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		second, err = CreateTag(ctx, tx, identity.RoleAdmin, "important")
		return err
	}); err != nil {
		t.Fatalf("CreateTag (2ª vez): %v", err)
	}
	if first.ID != second.ID {
		t.Errorf("IDs = %d, %d — esperado o mesmo (idempotente por nome)", first.ID, second.ID)
	}

	if err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		all, err := ListTags(ctx, tx)
		if err != nil {
			return err
		}
		if len(all) != 1 {
			t.Errorf("tags = %d, esperado 1 (create idempotente não deveria duplicar)", len(all))
		}
		return nil
	}); err != nil {
		t.Fatalf("ListTags: %v", err)
	}
}

func TestAddEntry_TableAndView_IdempotentAndListable(t *testing.T) {
	db, tenant, tableID, viewID := tagsFixture(t)
	ctx := context.Background()

	var tagID int
	if err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		tag, err := CreateTag(ctx, tx, identity.RoleAdmin, "piloto")
		if err != nil {
			return err
		}
		tagID = tag.ID
		if _, err := AddEntry(ctx, tx, identity.RoleAdmin, tagID, TagEntryRef{TableID: &tableID}); err != nil {
			return err
		}
		if _, err := AddEntry(ctx, tx, identity.RoleAdmin, tagID, TagEntryRef{ViewID: &viewID}); err != nil {
			return err
		}
		// Repetir a MESMA associação de tabela — idempotente, não duplica.
		if _, err := AddEntry(ctx, tx, identity.RoleAdmin, tagID, TagEntryRef{TableID: &tableID}); err != nil {
			return err
		}
		return nil
	}); err != nil {
		t.Fatalf("setup: %v", err)
	}

	if err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		entries, err := ListEntries(ctx, tx, tagID)
		if err != nil {
			return err
		}
		if len(entries) != 2 {
			t.Fatalf("entries = %d, esperado 2 (table+view, repetição não duplica)", len(entries))
		}
		var sawTable, sawView bool
		for _, e := range entries {
			if e.TableID != nil && *e.TableID == tableID {
				sawTable = true
			}
			if e.ViewID != nil && *e.ViewID == viewID {
				sawView = true
			}
		}
		if !sawTable || !sawView {
			t.Errorf("entries = %+v, esperado uma para tableID=%d e outra para viewID=%d", entries, tableID, viewID)
		}
		return nil
	}); err != nil {
		t.Fatalf("ListEntries: %v", err)
	}
}

func TestAddEntry_InvalidRef_RejectsAmbiguous(t *testing.T) {
	db, tenant, tableID, viewID := tagsFixture(t)
	ctx := context.Background()

	err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		tag, err := CreateTag(ctx, tx, identity.RoleAdmin, "ambigua")
		if err != nil {
			return err
		}
		_, err = AddEntry(ctx, tx, identity.RoleAdmin, tag.ID, TagEntryRef{})
		return err
	})
	if !errors.Is(err, ErrInvalidEntry) {
		t.Fatalf("err (nenhuma entidade) = %v, esperado ErrInvalidEntry", err)
	}

	err = db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		tag, err := CreateTag(ctx, tx, identity.RoleAdmin, "ambigua2")
		if err != nil {
			return err
		}
		_, err = AddEntry(ctx, tx, identity.RoleAdmin, tag.ID, TagEntryRef{TableID: &tableID, ViewID: &viewID})
		return err
	})
	if !errors.Is(err, ErrInvalidEntry) {
		t.Fatalf("err (duas entidades) = %v, esperado ErrInvalidEntry", err)
	}
}

func TestDeleteTag_CascadesEntries(t *testing.T) {
	db, tenant, tableID, _ := tagsFixture(t)
	ctx := context.Background()

	var tagID int
	if err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		tag, err := CreateTag(ctx, tx, identity.RoleAdmin, "descartavel")
		if err != nil {
			return err
		}
		tagID = tag.ID
		_, err = AddEntry(ctx, tx, identity.RoleAdmin, tagID, TagEntryRef{TableID: &tableID})
		return err
	}); err != nil {
		t.Fatalf("setup: %v", err)
	}

	if err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		return DeleteTag(ctx, tx, identity.RoleAdmin, tagID)
	}); err != nil {
		t.Fatalf("DeleteTag: %v", err)
	}

	if err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		var count int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM _sc_tag_entries WHERE tag_id = $1`, tagID).Scan(&count); err != nil {
			return err
		}
		if count != 0 {
			t.Errorf("entries remanescentes = %d, esperado 0 (ON DELETE CASCADE)", count)
		}
		_, err := GetTagByID(ctx, tx, tagID)
		if !errors.Is(err, ErrTagNotFound) {
			t.Errorf("GetTagByID após delete = %v, esperado ErrTagNotFound", err)
		}
		return nil
	}); err != nil {
		t.Fatalf("verificação: %v", err)
	}
}

func TestCreateTag_RequiresAdmin(t *testing.T) {
	db, tenant, _, _ := tagsFixture(t)
	ctx := context.Background()

	err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		_, err := CreateTag(ctx, tx, identity.RolePublic, "sem-permissao")
		return err
	})
	if !errors.Is(err, ErrNotAuthorized) {
		t.Fatalf("err = %v, esperado ErrNotAuthorized", err)
	}
}
