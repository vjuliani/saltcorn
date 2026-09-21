// Corpus de GO-044: UpdateTablePermissions.
package metadata

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/vjuliani/saltcorn/migracao/backend/internal/identity"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/platform/database"
)

func TestUpdateTablePermissions_ChangesMinRoles(t *testing.T) {
	db := testDB(t)
	tenant := testTenant(t, db)
	ctx := context.Background()

	var tableID int
	var versionBefore int64
	if err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		tbl, err := CreateTable(ctx, database.AsTx(tx), identity.RoleAdmin, "secrets", TableOptions{})
		if err != nil {
			return err
		}
		tableID = tbl.ID
		versionBefore, err = CurrentVersion(ctx, database.AsTx(tx))
		return err
	}); err != nil {
		t.Fatalf("setup: %v", err)
	}

	if err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		updated, err := UpdateTablePermissions(ctx, database.AsTx(tx), identity.RoleAdmin, tableID, identity.RoleAdmin, identity.RoleAdmin)
		if err != nil {
			return err
		}
		if updated.MinRoleRead != identity.RoleAdmin || updated.MinRoleWrite != identity.RoleAdmin {
			t.Errorf("permissões = %+v, esperado RoleAdmin/RoleAdmin", updated)
		}
		return nil
	}); err != nil {
		t.Fatalf("UpdateTablePermissions: %v", err)
	}

	if err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		tbl, err := GetTableByID(ctx, database.AsTx(tx), tableID)
		if err != nil {
			return err
		}
		if tbl.MinRoleRead != identity.RoleAdmin {
			t.Errorf("MinRoleRead persistido = %v, esperado RoleAdmin", tbl.MinRoleRead)
		}
		versionAfter, err := CurrentVersion(ctx, database.AsTx(tx))
		if err != nil {
			return err
		}
		if versionAfter != versionBefore+1 {
			t.Errorf("versão do catálogo = %d, esperado %d (incrementada em 1)", versionAfter, versionBefore+1)
		}
		return nil
	}); err != nil {
		t.Fatalf("verificação: %v", err)
	}
}

func TestUpdateTablePermissions_RequiresAdmin(t *testing.T) {
	db := testDB(t)
	tenant := testTenant(t, db)
	ctx := context.Background()

	var tableID int
	if err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		tbl, err := CreateTable(ctx, database.AsTx(tx), identity.RoleAdmin, "books", TableOptions{})
		tableID = tbl.ID
		return err
	}); err != nil {
		t.Fatalf("setup: %v", err)
	}

	err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		_, err := UpdateTablePermissions(ctx, database.AsTx(tx), identity.RolePublic, tableID, identity.RoleAdmin, identity.RoleAdmin)
		return err
	})
	if !errors.Is(err, ErrNotAuthorized) {
		t.Fatalf("err = %v, esperado ErrNotAuthorized", err)
	}
}

func TestUpdateTablePermissions_UnknownTable_ReturnsErrTableNotFound(t *testing.T) {
	db := testDB(t)
	tenant := testTenant(t, db)
	ctx := context.Background()

	err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		_, err := UpdateTablePermissions(ctx, database.AsTx(tx), identity.RoleAdmin, 999999, identity.RoleAdmin, identity.RoleAdmin)
		return err
	})
	if !errors.Is(err, ErrTableNotFound) {
		t.Fatalf("err = %v, esperado ErrTableNotFound", err)
	}
}
