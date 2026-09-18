// Package realtime é o mecanismo de transporte Go→BFF para comunicação em
// tempo real (GO-028) — o Go nunca fala Socket.IO nem gerencia conexão de
// navegador (ADR-0003/ADR-0007: sessão/protocolo de borda é sempre BFF
// Node.js). O que este pacote oferece é só a parte que É domínio: um
// catálogo ordenado e durável de eventos a entregar, publicado na MESMA
// transação que o efeito de domínio que os originou (Publish chamado de
// dentro de internal/notify.Create, por ex.) e consumido pelo BFF via
// polling autenticado (cmd/server, rota .../realtime/events) — o BFF então
// reemite cada evento pela rota Socket.IO real da(s) sala(s) certa(s).
//
// Divergência deliberada do legado (`state.emitDynamicUpdate`,
// packages/saltcorn-data/db/state.ts:1719 — achado do levantamento de
// GO-028): lá, um evento existe só como uma chamada de função em memória
// contra um emissor Socket.IO já ativo — se não houver socket conectado
// naquele instante (ou se o processo cair entre publicar e entregar), o
// evento simplesmente não existe mais, sem nenhum registro. Aqui, todo
// evento é uma linha persistida ANTES de qualquer tentativa de entrega —
// um BFF que reconecta ou um socket que estava temporariamente
// desconectado sempre pode retomar do último id que recebeu (ver
// ListSinceForActor), nunca perde eventos publicados enquanto
// desconectado.
//
// Isolamento por tenant: ao contrário do legado (rooms nomeadas por
// tenant resolvido do header Host, sem nenhuma barreira estrutural — ver
// levantamento de GO-028, achado #8), este catálogo vive no schema
// Postgres do próprio tenant (mesma convenção de todo o resto do backend,
// internal/platform/database.WithTenant) — isolamento estrutural do
// banco, não apenas uma string de nome bem formada.
package realtime

import (
	"context"

	"github.com/jackc/pgx/v5"
)

// _sc_realtime_events é o catálogo ordenado (id crescente = ordem de
// publicação = ordem de entrega, ver ListSinceForActor) de eventos
// pendentes de entrega. audience/user_ids espelham os dois modos do
// legado realmente usados por um consumidor Go (`dynamic_update` com
// array de userIds, ou sem — "broadcast" para todo autenticado): o modo
// "público" (visível a visitante anônimo, `_${tenant}_public_dynamic_update_room`
// no legado) fica deliberadamente fora de escopo desta tarefa — nenhum
// consumidor Go hoje precisa dele, e endereçar handshake não autenticado
// é uma decisão de segurança própria que não deveria ser incidental a
// esta entrega (ver README/execução).
const createEventsTableSQL = `
CREATE TABLE IF NOT EXISTS _sc_realtime_events (
	id bigserial PRIMARY KEY,
	audience text NOT NULL CHECK (audience IN ('broadcast', 'users')),
	user_ids integer[],
	payload jsonb NOT NULL,
	created_at timestamptz NOT NULL DEFAULT now(),
	CONSTRAINT sc_realtime_events_user_ids_matches_audience CHECK (
		(audience = 'users' AND user_ids IS NOT NULL AND array_length(user_ids, 1) > 0)
		OR (audience = 'broadcast' AND user_ids IS NULL)
	)
)`

// EnsureSchema cria o catálogo, idempotente — mesmo padrão de
// internal/notify, internal/triggers: chamar dentro de db.WithTenant, uma
// vez por tenant.
func EnsureSchema(ctx context.Context, tx pgx.Tx) error {
	_, err := tx.Exec(ctx, createEventsTableSQL)
	return err
}
