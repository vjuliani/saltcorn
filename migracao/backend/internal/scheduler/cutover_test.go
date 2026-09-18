// Prova o critério de aceite "scheduler antigo é desativado por escopo"
// — reaproveitando o MESMO mecanismo de ownership de GO-009
// (internal/platform/cutover), já testado genericamente por
// TestOwnerOf_DefaultsToLegacy e concretamente pela mesma âncora em
// internal/pluginhost/cutover_test.go e
// internal/expression/fixture_test.go.
package scheduler

import (
	"context"
	"fmt"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/vjuliani/saltcorn/migracao/backend/internal/platform/cutover"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/platform/tenancy"
)

func TestSchedulerCapability_DefaultsToLegacy(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	tenant := tenancy.Tenant(fmt.Sprintf("scheduler_cutover_%s", sanitizeForSchema(t.Name())))

	if err := db.WithTenant(ctx, "public", func(ctx context.Context, tx pgx.Tx) error {
		return cutover.EnsureSchema(ctx, tx)
	}); err != nil {
		t.Fatalf("cutover.EnsureSchema: %v", err)
	}
	t.Cleanup(func() {
		_ = db.WithTenant(context.Background(), "public", func(ctx context.Context, tx pgx.Tx) error {
			_, err := tx.Exec(ctx, "DELETE FROM _sc_capability_ownership WHERE tenant = $1 AND capability = $2", string(tenant), Capability)
			return err
		})
	})

	var owner cutover.Owner
	if err := db.WithTenant(ctx, "public", func(ctx context.Context, tx pgx.Tx) error {
		var err error
		owner, err = cutover.OwnerOf(ctx, tx, tenant, Capability)
		return err
	}); err != nil {
		t.Fatalf("cutover.OwnerOf: %v", err)
	}
	if owner != cutover.OwnerLegacy {
		t.Fatalf("owner de %q para um tenant nunca configurado = %q, esperado OwnerLegacy — o scheduler antigo NUNCA deveria perder responsabilidade por omissão", Capability, owner)
	}

	guard := cutover.NewGuard()
	if _, err := guard.Begin(tenant, Capability); err == nil {
		t.Fatal("guard.Begin() permitiu admissão para uma capacidade nunca trocada para OwnerGo — deveria recusar (fail closed)")
	}
}
