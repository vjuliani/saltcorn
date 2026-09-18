// Package lease implementa exclusividade MULTI-PROCESSO por nome de job,
// com expiração explícita (TTL) — o que falta em internal/platform/cutover
// para o critério de aceite de GO-025 ("dois workers não executam
// simultaneamente job exclusivo"): cutover.Guard é uma guarda em memória
// de UM processo (a troca de ownership legado↔Go), não uma coordenação
// entre múltiplas instâncias do processo `worker` rodando ao mesmo tempo —
// cada instância teria sua própria Guard, ambas "Go owner", sem nada
// impedindo as duas de rodar o MESMO job simultaneamente.
//
// Divergência deliberada do legado (packages/server/serve.js:443-494,
// eleição de líder via pg_try_advisory_lock(11565) sem TTL — o lock só
// solta quando a conexão morre, não testável deterministicamente sem
// matar uma conexão de verdade): este pacote usa uma TABELA
// (_sc_leases) com expiração explícita por timestamp, reivindicada por um
// UPSERT atômico condicional — testável sem depender do ciclo de vida de
// uma conexão TCP, e com uma semântica de expiração precisa e observável.
package lease

import (
	"context"

	"github.com/jackc/pgx/v5"
)

const createLeasesTableSQL = `
CREATE TABLE IF NOT EXISTS _sc_leases (
	name text PRIMARY KEY,
	owner_id text NOT NULL,
	acquired_at timestamptz NOT NULL DEFAULT now(),
	expires_at timestamptz NOT NULL
)`

// EnsureSchema cria a tabela de leases, idempotente — chamar dentro de
// db.WithTenant, uma vez por tenant (mesmo padrão de internal/platform/outbox,
// internal/triggers) — leases são por tenant porque um job exclusivo (ex.:
// o scheduler de triggers agendados) é por tenant, não global ao processo.
func EnsureSchema(ctx context.Context, tx pgx.Tx) error {
	_, err := tx.Exec(ctx, createLeasesTableSQL)
	return err
}
