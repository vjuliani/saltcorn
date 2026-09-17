// Prova o critério de aceite "plugin transacional incompatível mantém
// operação integral no legado" — reaproveitando o MESMO mecanismo de
// ownership de GO-009 (internal/platform/cutover), já testado
// genericamente por TestOwnerOf_DefaultsToLegacy. Este teste é a âncora
// concreta para o pacote pluginhost: a capacidade que uma expressão/
// plugin usaria (ExpressionCapability) nunca é servida por este host a
// menos que uma troca explícita (cutover.SwitchOwner) já tenha
// acontecido — o padrão seguro "nenhuma tenant+capacidade passa a ser
// servida por Go por omissão" também vale para plugins, não só para
// tabelas/views.
package pluginhost

import (
	"context"
	"fmt"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/vjuliani/saltcorn/migracao/backend/internal/platform/cutover"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/platform/tenancy"
)

func TestExpressionCapability_DefaultsToLegacy(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	tenant := tenancy.Tenant(fmt.Sprintf("pluginhost_cutover_%s", sanitizeForSchema(t.Name())))

	if err := db.WithTenant(ctx, "public", func(ctx context.Context, tx pgx.Tx) error {
		return cutover.EnsureSchema(ctx, tx)
	}); err != nil {
		t.Fatalf("cutover.EnsureSchema: %v", err)
	}
	t.Cleanup(func() {
		_ = db.WithTenant(context.Background(), "public", func(ctx context.Context, tx pgx.Tx) error {
			_, err := tx.Exec(ctx, "DELETE FROM _sc_capability_ownership WHERE tenant = $1 AND capability = $2", string(tenant), ExpressionCapability)
			return err
		})
	})

	var owner cutover.Owner
	if err := db.WithTenant(ctx, "public", func(ctx context.Context, tx pgx.Tx) error {
		var err error
		owner, err = cutover.OwnerOf(ctx, tx, tenant, ExpressionCapability)
		return err
	}); err != nil {
		t.Fatalf("cutover.OwnerOf: %v", err)
	}
	if owner != cutover.OwnerLegacy {
		t.Fatalf("owner de %q para um tenant nunca configurado = %q, esperado OwnerLegacy — um plugin incompatível NUNCA deveria ser roteado para o host novo por omissão", ExpressionCapability, owner)
	}

	// Uma guard que nunca recebeu LoadFromRegistry/SwitchOwner para esta
	// capacidade também recusa admissão — a MESMA guarda que já protege
	// tables.records/schema/views (GO-009/017/019/020), aplicada aqui.
	guard := cutover.NewGuard()
	_, err := guard.Begin(tenant, ExpressionCapability)
	if err == nil {
		t.Fatal("guard.Begin() permitiu admissão para uma capacidade nunca trocada para OwnerGo — deveria recusar (fail closed)")
	}
}
