// Package outbox implementa idempotência de escrita e o padrão outbox
// transacional (GO-014): Do executa uma operação e grava sua chave de
// idempotência e os eventos que ela produz na MESMA transação Postgres do
// efeito em si — nunca como uma escrita separada. Isso é o que torna as
// duas metades do critério de aceite verdadeiras de graça, pela própria
// atomicidade do Postgres:
//
//   - Se a transação do chamador não commitar (a operação falha, o
//     chamador aborta, o processo cai antes do commit), NADA persiste —
//     nem o efeito, nem a chave, nem o evento. Nada foi "confirmado", então
//     nada é "perdido": uma nova tentativa encontra o catálogo limpo e
//     tenta de novo.
//   - Se a transação commitar, efeito + chave + evento ficam gravados
//     juntos. Uma queda do processo chamador DEPOIS do commit (ou uma
//     resposta perdida na rede) não perde o evento: ele já está no
//     Postgres, esperando o worker (ProcessPending) processá-lo, e uma
//     nova tentativa com a mesma chave/payload encontra o resultado já
//     gravado e o devolve sem rodar o efeito de novo.
package outbox

import (
	"context"

	"github.com/jackc/pgx/v5"
)

const createIdempotencyKeysTableSQL = `
CREATE TABLE IF NOT EXISTS _sc_idempotency_keys (
	key text PRIMARY KEY,
	payload_hash text NOT NULL,
	result_json jsonb,
	created_at timestamptz NOT NULL DEFAULT now()
)`

// _sc_outbox guarda eventos gravados na mesma transação que o efeito que os
// originou — o worker (ProcessPending) os processa depois, de forma
// assíncrona e com retries, nunca despachados diretamente de dentro da
// transação de escrita original.
const createOutboxTableSQL = `
CREATE TABLE IF NOT EXISTS _sc_outbox (
	id bigserial PRIMARY KEY,
	idempotency_key text NOT NULL,
	event_type text NOT NULL,
	payload_json jsonb NOT NULL,
	status text NOT NULL DEFAULT 'pending',
	attempts int NOT NULL DEFAULT 0,
	last_error text,
	created_at timestamptz NOT NULL DEFAULT now(),
	processed_at timestamptz
)`

const createOutboxStatusIndexSQL = `
CREATE INDEX IF NOT EXISTS idx_sc_outbox_status ON _sc_outbox (status, id)`

// EnsureSchema cria as tabelas de framework de idempotência/outbox,
// idempotente — chamar dentro de db.WithTenant, uma por tenant (mesmo
// padrão de internal/identity, internal/metadata).
func EnsureSchema(ctx context.Context, tx pgx.Tx) error {
	if _, err := tx.Exec(ctx, createIdempotencyKeysTableSQL); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, createOutboxTableSQL); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, createOutboxStatusIndexSQL); err != nil {
		return err
	}
	return nil
}
