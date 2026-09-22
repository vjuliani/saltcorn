// Testes de versionamento de linha (GO-045) — exigem Postgres real, pulam
// (t.Skip) se SALTCORN_GO_TEST_DATABASE_URL não estiver definida, mesmo
// padrão de compiler_test.go.
package records

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/vjuliani/saltcorn/migracao/backend/internal/identity"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/metadata"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/platform/database"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/platform/tenancy"
)

// createVersionedTable cria a tabela "posts" (Versioned=true) com um
// único campo de texto "title" — a fixture comum destes testes.
func createVersionedTable(t *testing.T, db *database.DB, tenant tenancy.Tenant) {
	t.Helper()
	ctx := context.Background()
	if err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		if err := metadata.EnsureSchema(ctx, database.AsTx(tx)); err != nil {
			return err
		}
		tbl, err := metadata.CreateTable(ctx, database.AsTx(tx), identity.RoleAdmin, "posts", metadata.TableOptions{Versioned: true})
		if err != nil {
			return err
		}
		_, err = metadata.AddField(ctx, database.AsTx(tx), identity.RoleAdmin, tbl.ID, metadata.FieldDef{Name: "title", Type: metadata.FieldText, Required: true})
		return err
	}); err != nil {
		t.Fatalf("setup de tabela versionada: %v", err)
	}
}

func TestCreateAndUpdate_VersionedTable_GrowsHistory(t *testing.T) {
	db := testDB(t)
	tenant := testTenant(t, db)
	ctx := context.Background()
	createVersionedTable(t, db, tenant)

	var id int
	var version string
	if err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		rec, err := CreateRecordTx(ctx, database.AsTx(tx), identity.RoleAdmin, "posts", map[string]any{"title": "v1"}, nil)
		if err != nil {
			return err
		}
		id = int(rec["id"].(int32))
		version = rec["_version"].(string)
		return nil
	}); err != nil {
		t.Fatalf("CreateRecordTx: %v", err)
	}

	if err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		versions, err := GetHistoryTx(ctx, database.AsTx(tx), identity.RolePublic, "posts", id)
		if err != nil {
			return err
		}
		if len(versions) != 1 {
			t.Fatalf("versões após create = %d, esperado 1", len(versions))
		}
		if versions[0].Version != 1 || versions[0].Record["title"] != "v1" {
			t.Errorf("versão 1 = %+v, esperado title=v1", versions[0])
		}
		if versions[0].RestoreOfVersion != nil {
			t.Errorf("RestoreOfVersion = %v, esperado nil (não é uma restauração)", *versions[0].RestoreOfVersion)
		}
		return nil
	}); err != nil {
		t.Fatalf("GetHistoryTx (após create): %v", err)
	}

	if err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		_, err := UpdateRecordTx(ctx, database.AsTx(tx), identity.RoleAdmin, "posts", id, version, map[string]any{"title": "v2"}, nil)
		return err
	}); err != nil {
		t.Fatalf("UpdateRecordTx: %v", err)
	}

	if err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		versions, err := GetHistoryTx(ctx, database.AsTx(tx), identity.RolePublic, "posts", id)
		if err != nil {
			return err
		}
		if len(versions) != 2 {
			t.Fatalf("versões após update = %d, esperado 2", len(versions))
		}
		// Mais recente primeiro (ORDER BY _history_version DESC).
		if versions[0].Version != 2 || versions[0].Record["title"] != "v2" {
			t.Errorf("versão mais recente = %+v, esperado version=2 title=v2", versions[0])
		}
		if versions[1].Version != 1 || versions[1].Record["title"] != "v1" {
			t.Errorf("versão mais antiga = %+v, esperado version=1 title=v1", versions[1])
		}
		return nil
	}); err != nil {
		t.Fatalf("GetHistoryTx (após update): %v", err)
	}
}

func TestGetHistoryTx_UnversionedTable_ReturnsError(t *testing.T) {
	db := testDB(t)
	tenant := testTenant(t, db)
	ctx := context.Background()

	if err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		if err := metadata.EnsureSchema(ctx, database.AsTx(tx)); err != nil {
			return err
		}
		tbl, err := metadata.CreateTable(ctx, database.AsTx(tx), identity.RoleAdmin, "posts", metadata.TableOptions{})
		if err != nil {
			return err
		}
		_, err = metadata.AddField(ctx, database.AsTx(tx), identity.RoleAdmin, tbl.ID, metadata.FieldDef{Name: "title", Type: metadata.FieldText})
		return err
	}); err != nil {
		t.Fatalf("setup: %v", err)
	}

	err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		_, err := GetHistoryTx(ctx, database.AsTx(tx), identity.RolePublic, "posts", 1)
		return err
	})
	if !errors.Is(err, ErrTableNotVersioned) {
		t.Fatalf("err = %v, esperado ErrTableNotVersioned", err)
	}
}

func TestRestoreRowVersionTx_RestoresOldValueAndAppendsNewHistoryEntry(t *testing.T) {
	db := testDB(t)
	tenant := testTenant(t, db)
	ctx := context.Background()
	createVersionedTable(t, db, tenant)

	var id int
	var version string
	if err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		rec, err := CreateRecordTx(ctx, database.AsTx(tx), identity.RoleAdmin, "posts", map[string]any{"title": "original"}, nil)
		if err != nil {
			return err
		}
		id = int(rec["id"].(int32))
		version = rec["_version"].(string)
		return nil
	}); err != nil {
		t.Fatalf("CreateRecordTx: %v", err)
	}

	if err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		_, err := UpdateRecordTx(ctx, database.AsTx(tx), identity.RoleAdmin, "posts", id, version, map[string]any{"title": "editado"}, nil)
		return err
	}); err != nil {
		t.Fatalf("UpdateRecordTx: %v", err)
	}

	// Restaura a versão 1 (title="original").
	if err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		restored, err := RestoreRowVersionTx(ctx, database.AsTx(tx), identity.RoleAdmin, "posts", id, 1, nil)
		if err != nil {
			return err
		}
		if restored["title"] != "original" {
			t.Errorf("title após restore = %v, esperado \"original\"", restored["title"])
		}
		return nil
	}); err != nil {
		t.Fatalf("RestoreRowVersionTx: %v", err)
	}

	if err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		var title string
		err := tx.QueryRow(ctx, `SELECT title FROM posts WHERE id = $1`, id).Scan(&title)
		if err != nil {
			return err
		}
		if title != "original" {
			t.Errorf("title na tabela principal = %q, esperado \"original\"", title)
		}
		return nil
	}); err != nil {
		t.Fatalf("verificação: %v", err)
	}

	// Restaurar é ADITIVO: 3 entradas de histórico (create, update, restore).
	if err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		versions, err := GetHistoryTx(ctx, database.AsTx(tx), identity.RolePublic, "posts", id)
		if err != nil {
			return err
		}
		if len(versions) != 3 {
			t.Fatalf("versões após restore = %d, esperado 3 (create+update+restore)", len(versions))
		}
		latest := versions[0]
		if latest.Version != 3 || latest.Record["title"] != "original" {
			t.Errorf("versão mais recente = %+v, esperado version=3 title=original", latest)
		}
		if latest.RestoreOfVersion == nil || *latest.RestoreOfVersion != 1 {
			t.Errorf("RestoreOfVersion da versão 3 = %v, esperado apontar para 1", latest.RestoreOfVersion)
		}
		return nil
	}); err != nil {
		t.Fatalf("GetHistoryTx (após restore): %v", err)
	}
}

func TestDeleteRecordTx_VersionedTable_DoesNotAddHistoryEntry(t *testing.T) {
	// Achado real de leitura do legado (models/table.ts, deleteRows nunca
	// chama insert_history_row) — este teste é a regressão desse achado:
	// se alguém "corrigir" DeleteRecordTx para gravar histórico
	// (aparentemente mais completo, mas divergente do comportamento real
	// do legado), este teste falha.
	db := testDB(t)
	tenant := testTenant(t, db)
	ctx := context.Background()
	createVersionedTable(t, db, tenant)

	var id int
	var version string
	if err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		rec, err := CreateRecordTx(ctx, database.AsTx(tx), identity.RoleAdmin, "posts", map[string]any{"title": "a apagar"}, nil)
		if err != nil {
			return err
		}
		id = int(rec["id"].(int32))
		version = rec["_version"].(string)
		return nil
	}); err != nil {
		t.Fatalf("CreateRecordTx: %v", err)
	}

	if err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		return DeleteRecordTx(ctx, database.AsTx(tx), identity.RoleAdmin, "posts", id, version, nil)
	}); err != nil {
		t.Fatalf("DeleteRecordTx: %v", err)
	}

	if err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		var count int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM posts__history WHERE id = $1`, id).Scan(&count); err != nil {
			return err
		}
		if count != 1 {
			t.Errorf("linhas de histórico = %d, esperado 1 (só o create — delete não grava novo snapshot)", count)
		}
		return nil
	}); err != nil {
		t.Fatalf("verificação: %v", err)
	}
}

func TestAddField_VersionedTable_PropagatesColumnToHistory(t *testing.T) {
	db := testDB(t)
	tenant := testTenant(t, db)
	ctx := context.Background()

	var tableID int
	if err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		if err := metadata.EnsureSchema(ctx, database.AsTx(tx)); err != nil {
			return err
		}
		tbl, err := metadata.CreateTable(ctx, database.AsTx(tx), identity.RoleAdmin, "posts", metadata.TableOptions{Versioned: true})
		if err != nil {
			return err
		}
		tableID = tbl.ID
		_, err = metadata.AddField(ctx, database.AsTx(tx), identity.RoleAdmin, tbl.ID, metadata.FieldDef{Name: "title", Type: metadata.FieldText, Required: true})
		return err
	}); err != nil {
		t.Fatalf("setup: %v", err)
	}

	// Campo novo ADICIONADO depois — deve propagar para posts__history.
	if err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		_, err := metadata.AddField(ctx, database.AsTx(tx), identity.RoleAdmin, tableID, metadata.FieldDef{Name: "views", Type: metadata.FieldInteger})
		return err
	}); err != nil {
		t.Fatalf("AddField (views): %v", err)
	}

	var id int
	if err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		rec, err := CreateRecordTx(ctx, database.AsTx(tx), identity.RoleAdmin, "posts", map[string]any{"title": "x", "views": int64(5)}, nil)
		if err != nil {
			return err
		}
		id = int(rec["id"].(int32))
		return nil
	}); err != nil {
		t.Fatalf("CreateRecordTx: %v", err)
	}

	if err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		versions, err := GetHistoryTx(ctx, database.AsTx(tx), identity.RolePublic, "posts", id)
		if err != nil {
			return err
		}
		if len(versions) != 1 {
			t.Fatalf("versões = %d, esperado 1", len(versions))
		}
		if v, ok := versions[0].Record["views"].(int32); !ok || v != 5 {
			t.Errorf("views no histórico = %v (%T), esperado 5", versions[0].Record["views"], versions[0].Record["views"])
		}
		return nil
	}); err != nil {
		t.Fatalf("GetHistoryTx: %v", err)
	}
}
