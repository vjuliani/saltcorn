// Package notify implementa envio de e-mail e webhook (GO-026) — as
// ações de I/O externo que GO-024 (internal/triggers) deixou
// explicitamente fora de escopo. Reaproveita o padrão outbox (GO-014)
// para "falha do provedor entra em retry e duplicatas externas têm
// política explícita" (critério de aceite): enfileirar é feito via
// outbox.Do (idempotência por chave, na MESMA transação do chamador);
// entregar de fato é feito por um outbox.Handler consumido por
// outbox.ProcessPending (retry com corte de tentativas, savepoint por
// evento — mecanismo já testado desde GO-014, reaproveitado aqui, não
// reinventado).
//
// Divergência deliberada do legado: a ação `send_email`
// (base-plugin/actions.ts) faz um único `await sendMail(...)` direto,
// sem fila nem retry — uma falha de SMTP propaga como erro de trigger,
// sem nova tentativa. `Notification.create()` (models/notification.ts)
// usa uma fila separada (MailQueue, models/internal/mail_queue.ts) que
// agenda reenvio via `setTimeout` EM MEMÓRIA do processo — se o processo
// cai com uma notificação "pending" agendada, o e-mail correspondente
// nunca sai (achado de preflight, não corrigido no legado). Aqui os DOIS
// casos (ação de trigger e notificação) usam o MESMO mecanismo durável
// (outbox): uma queda do processo depois do commit nunca perde o evento.
package notify

import (
	"context"

	"github.com/jackc/pgx/v5"
)

// _sc_notifications é o equivalente reduzido de _sc_notifications do
// legado — sem os canais push nativo (Web Push/FCM/APNS) nem
// atualização dinâmica in-app: exigem credenciais externas (VAPID/FCM/
// APNS) e bibliotecas pesadas sem exercício no piloto deste checkout,
// fora de escopo desta tarefa (ver README/execução para a lista
// completa de decisões de escopo).
const createNotificationsTableSQL = `
CREATE TABLE IF NOT EXISTS _sc_notifications (
	id serial PRIMARY KEY,
	user_id integer NOT NULL,
	title text NOT NULL,
	body text,
	link text,
	read boolean NOT NULL DEFAULT false,
	created_at timestamptz NOT NULL DEFAULT now()
)`

const createNotificationsUserIndexSQL = `
CREATE INDEX IF NOT EXISTS idx_sc_notifications_user ON _sc_notifications (user_id, read, id)`

// EnsureSchema cria o catálogo de notificações, idempotente — chamar
// dentro de db.WithTenant, uma vez por tenant (mesmo padrão de
// internal/files, internal/triggers). Não cria nenhuma tabela de outbox
// própria — reaproveita _sc_outbox de internal/platform/outbox
// (EnsureSchema desse pacote deve ter rodado antes, mesma dependência já
// existente entre internal/triggers e outbox desde GO-024).
func EnsureSchema(ctx context.Context, tx pgx.Tx) error {
	if _, err := tx.Exec(ctx, createNotificationsTableSQL); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, createNotificationsUserIndexSQL)
	return err
}
