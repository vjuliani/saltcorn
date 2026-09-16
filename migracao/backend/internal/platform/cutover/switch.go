package cutover

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/vjuliani/saltcorn/migracao/backend/internal/platform/database"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/platform/tenancy"
)

// SwitchOwner troca o proprietário de escrita de tenant+capability,
// drenando trabalho em curso antes de persistir a troca — a operação por
// trás dos critérios de aceite "rollback de rota" e "requisições em
// andamento são drenadas" de GO-009. A ordem é a garantia de corretude
// deste pacote:
//
//  1. Bloqueia admissão nova para a chave imediatamente (Guard.Drain) e
//     espera todo trabalho já admitido, sob o owner ANTIGO, terminar.
//  2. Só depois de confirmado o dreno, persiste o novo owner no registro
//     (SetOwner) — nunca antes, ou uma leitura concorrente do registro
//     veria o owner novo enquanto trabalho do owner antigo ainda roda.
//  3. Atualiza o cache em memória da Guard e libera admissão nova, numa
//     única seção crítica (resumeWithOwner), agora sob o owner novo.
//
// Se o passo 1 estourar drainTimeout, a troca é abortada sem nenhuma
// escrita no registro, e a chave permanece drenando (Begin continua
// recusando trabalho novo) até uma chamada de SwitchOwner bem-sucedida —
// nunca libera admissão sob um estado não confirmado. Se o passo 2 falhar,
// mesma coisa: a chave fica travada em drenagem, não retorna ao owner
// antigo silenciosamente (README §3 item 6: "retorno de tráfego sozinho
// não reverte dados" — o mesmo cuidado se aplica aqui, na direção
// inversa: não reabrir admissão sem confirmar o que foi persistido).
func SwitchOwner(ctx context.Context, db *database.DB, guard *Guard, tenant tenancy.Tenant, capability string, newOwner Owner, drainTimeout time.Duration) error {
	drainCtx, cancel := context.WithTimeout(ctx, drainTimeout)
	defer cancel()
	if err := guard.Drain(drainCtx, tenant, capability); err != nil {
		return fmt.Errorf("cutover: drenar %s/%s antes de trocar owner: %w", tenant, capability, err)
	}

	if err := db.WithTenant(ctx, publicSchema, func(ctx context.Context, tx pgx.Tx) error {
		return SetOwner(ctx, tx, tenant, capability, newOwner)
	}); err != nil {
		return fmt.Errorf("cutover: persistir novo owner de %s/%s: %w", tenant, capability, err)
	}

	guard.resumeWithOwner(tenant, capability, newOwner)
	return nil
}
