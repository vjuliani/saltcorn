package cutover

import (
	"context"

	"github.com/jackc/pgx/v5"

	"github.com/vjuliani/saltcorn/migracao/backend/internal/platform/database"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/platform/tenancy"
)

// publicSchema é o schema onde vive _sc_capability_ownership — metadado de
// controle sobre o roteamento em si, não dado de um tenant (ver schema.go).
const publicSchema = tenancy.Tenant("public")

// LoadFromRegistry carrega no cache em memória da Guard todo o conteúdo
// persistido de _sc_capability_ownership — chamado uma vez na
// inicialização de cmd/server/cmd/worker, antes de aceitar
// requisições/jobs. A Guard é só em memória e reinicia zerada a cada
// processo novo; sem este carregamento de boot, um restart faria toda
// tenant/capacidade voltar a "sem owner registrado" (equivalente a
// OwnerLegacy) mesmo que o registro persistido diga "go" — comportamento
// seguro (nunca aceita escrita por engano), mas que exige este passo
// explícito para não parecer um rollback silencioso a cada restart.
func LoadFromRegistry(ctx context.Context, db *database.DB, guard *Guard) error {
	type row struct {
		tenant     string
		capability string
		owner      string
	}
	var rows []row
	err := db.WithTenant(ctx, publicSchema, func(ctx context.Context, tx pgx.Tx) error {
		result, err := tx.Query(ctx, "SELECT tenant, capability, owner FROM _sc_capability_ownership")
		if err != nil {
			return err
		}
		defer result.Close()
		for result.Next() {
			var r row
			if err := result.Scan(&r.tenant, &r.capability, &r.owner); err != nil {
				return err
			}
			rows = append(rows, r)
		}
		return result.Err()
	})
	if err != nil {
		return err
	}

	guard.mu.Lock()
	defer guard.mu.Unlock()
	for _, r := range rows {
		st := guard.stateFor(guardKey{tenancy.Tenant(r.tenant), r.capability})
		st.owner = Owner(r.owner)
	}
	return nil
}
