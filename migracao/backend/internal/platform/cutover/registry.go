package cutover

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"

	"github.com/vjuliani/saltcorn/migracao/backend/internal/platform/tenancy"
)

// OwnerOf lê o proprietário de escrita registrado para tenant+capability.
// Sem registro explícito, retorna OwnerLegacy — o padrão seguro (ADR-0006):
// nenhuma tenant+capacidade passa a ser servida por Go por omissão.
func OwnerOf(ctx context.Context, tx pgx.Tx, tenant tenancy.Tenant, capability string) (Owner, error) {
	var owner string
	err := tx.QueryRow(ctx,
		"SELECT owner FROM _sc_capability_ownership WHERE tenant = $1 AND capability = $2",
		string(tenant), capability,
	).Scan(&owner)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return OwnerLegacy, nil
		}
		return "", err
	}
	return Owner(owner), nil
}

// SetOwner grava o proprietário de escrita para tenant+capability
// (upsert). Não drena nem verifica trabalho em curso — isso é
// responsabilidade de SwitchOwner, que orquestra drenar-então-trocar.
// Chamar SetOwner diretamente (fora de SwitchOwner) troca o registro sem
// nenhuma garantia sobre trabalho em curso sob o owner antigo.
func SetOwner(ctx context.Context, tx pgx.Tx, tenant tenancy.Tenant, capability string, owner Owner) error {
	_, err := tx.Exec(ctx, `
		INSERT INTO _sc_capability_ownership (tenant, capability, owner, updated_at)
		VALUES ($1, $2, $3, now())
		ON CONFLICT (tenant, capability)
		DO UPDATE SET owner = EXCLUDED.owner, updated_at = EXCLUDED.updated_at
	`, string(tenant), capability, string(owner))
	return err
}
