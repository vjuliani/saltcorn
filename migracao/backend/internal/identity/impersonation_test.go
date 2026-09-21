// Corpus de GO-044: auditoria de impersonação (StartImpersonation/
// EndImpersonation/GetImpersonation).
package identity

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"
)

func TestStartImpersonation_RequiresAdmin(t *testing.T) {
	db := testDB(t)
	tenant := testTenant(t, db, "a")
	ctx := context.Background()

	var adminID, targetID int
	if err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		adminID = mustCreateUser(t, ctx, tx, "admin@example.com", RoleAdmin)
		targetID = mustCreateUser(t, ctx, tx, "target@example.com", RoleID(80))
		return nil
	}); err != nil {
		t.Fatalf("setup: %v", err)
	}

	err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		_, err := StartImpersonation(ctx, tx, RolePublic, adminID, targetID)
		return err
	})
	if !errors.Is(err, ErrNotAuthorized) {
		t.Fatalf("err = %v, esperado ErrNotAuthorized", err)
	}
}

func TestStartImpersonation_RejectsImpersonatingSelf(t *testing.T) {
	db := testDB(t)
	tenant := testTenant(t, db, "a")
	ctx := context.Background()

	var adminID int
	if err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		adminID = mustCreateUser(t, ctx, tx, "admin2@example.com", RoleAdmin)
		return nil
	}); err != nil {
		t.Fatalf("setup: %v", err)
	}

	err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		_, err := StartImpersonation(ctx, tx, RoleAdmin, adminID, adminID)
		return err
	})
	if !errors.Is(err, ErrCannotImpersonateSelf) {
		t.Fatalf("err = %v, esperado ErrCannotImpersonateSelf", err)
	}
}

func TestStartImpersonation_UnknownTarget_ReturnsErrUserNotFound(t *testing.T) {
	db := testDB(t)
	tenant := testTenant(t, db, "a")
	ctx := context.Background()

	var adminID int
	if err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		adminID = mustCreateUser(t, ctx, tx, "admin3@example.com", RoleAdmin)
		return nil
	}); err != nil {
		t.Fatalf("setup: %v", err)
	}

	err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		_, err := StartImpersonation(ctx, tx, RoleAdmin, adminID, 999999)
		return err
	})
	if !errors.Is(err, ErrUserNotFound) {
		t.Fatalf("err = %v, esperado ErrUserNotFound", err)
	}
}

// TestStartImpersonation_RecordsAuditTrail é o teste direto da divergência
// deliberada mais forte que o legado (que não registra NADA sobre
// "become-user"): confirma que início/fim ficam persistidos e corretos.
func TestStartImpersonation_RecordsAuditTrail(t *testing.T) {
	db := testDB(t)
	tenant := testTenant(t, db, "a")
	ctx := context.Background()

	var adminID, targetID int
	if err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		adminID = mustCreateUser(t, ctx, tx, "admin4@example.com", RoleAdmin)
		targetID = mustCreateUser(t, ctx, tx, "target4@example.com", RoleID(80))
		return nil
	}); err != nil {
		t.Fatalf("setup: %v", err)
	}

	var logID int
	if err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		logID, err = StartImpersonation(ctx, tx, RoleAdmin, adminID, targetID)
		return err
	}); err != nil {
		t.Fatalf("StartImpersonation: %v", err)
	}
	if logID == 0 {
		t.Fatal("logID = 0, esperado atribuído")
	}

	if err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		rec, err := GetImpersonation(ctx, tx, logID)
		if err != nil {
			return err
		}
		if rec.AdminUserID != adminID || rec.TargetUserID != targetID {
			t.Errorf("rec = %+v, esperado admin=%d target=%d", rec, adminID, targetID)
		}
		if !rec.StillActive {
			t.Error("StillActive = false logo após StartImpersonation, esperado true")
		}
		if rec.StartedAt == "" {
			t.Error("StartedAt vazio")
		}
		return nil
	}); err != nil {
		t.Fatalf("GetImpersonation (ativo): %v", err)
	}

	if err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		return EndImpersonation(ctx, tx, logID)
	}); err != nil {
		t.Fatalf("EndImpersonation: %v", err)
	}

	if err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		rec, err := GetImpersonation(ctx, tx, logID)
		if err != nil {
			return err
		}
		if rec.StillActive {
			t.Error("StillActive = true após EndImpersonation, esperado false")
		}
		if rec.EndedAt == "" {
			t.Error("EndedAt vazio após EndImpersonation")
		}
		return nil
	}); err != nil {
		t.Fatalf("GetImpersonation (encerrado): %v", err)
	}
}

func TestEndImpersonation_IdempotentForUnknownOrAlreadyEnded(t *testing.T) {
	db := testDB(t)
	tenant := testTenant(t, db, "a")
	ctx := context.Background()

	// logID inexistente: no-op, nunca erro.
	if err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		return EndImpersonation(ctx, tx, 999999)
	}); err != nil {
		t.Fatalf("EndImpersonation(logID inexistente): %v", err)
	}

	var adminID, targetID, logID int
	if err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		adminID = mustCreateUser(t, ctx, tx, "admin5@example.com", RoleAdmin)
		targetID = mustCreateUser(t, ctx, tx, "target5@example.com", RoleID(80))
		var err error
		logID, err = StartImpersonation(ctx, tx, RoleAdmin, adminID, targetID)
		return err
	}); err != nil {
		t.Fatalf("setup: %v", err)
	}

	for i := 0; i < 2; i++ {
		if err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
			return EndImpersonation(ctx, tx, logID)
		}); err != nil {
			t.Fatalf("EndImpersonation (chamada %d): %v", i+1, err)
		}
	}
}

func TestGetImpersonation_UnknownLogID_ReturnsErrImpersonationNotFound(t *testing.T) {
	db := testDB(t)
	tenant := testTenant(t, db, "a")
	ctx := context.Background()

	err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		_, err := GetImpersonation(ctx, tx, 999999)
		return err
	})
	if !errors.Is(err, ErrImpersonationNotFound) {
		t.Fatalf("err = %v, esperado ErrImpersonationNotFound", err)
	}
}
