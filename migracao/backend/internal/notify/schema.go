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
	"strings"

	"github.com/vjuliani/saltcorn/migracao/backend/internal/platform/database"
)

// _sc_notifications é o equivalente reduzido de _sc_notifications do
// legado — sem o canal push nativo (Web Push/FCM/APNS): exige credenciais
// externas e bibliotecas pesadas sem exercício no piloto deste checkout,
// fora de escopo desta tarefa (ver README/execução para a lista completa
// de decisões de escopo). A atualização dinâmica in-app, que esta tarefa
// (GO-026) tinha deixado explicitamente de fora, é coberta por GO-028
// (internal/realtime) — Create publica um evento em tempo real na MESMA
// transação da notificação, ver notifications.go.
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

// EnsureSchemaTx cria o catálogo de notificações, idempotente — chamar
// dentro de db.WithTenant, uma vez por tenant (mesmo padrão de
// internal/files, internal/triggers). Não cria nenhuma tabela de outbox
// própria — reaproveita _sc_outbox de internal/platform/outbox
// (EnsureSchemaTx desse pacote deve ter rodado antes, mesma dependência
// já existente entre internal/triggers e outbox desde GO-024).
// Dialect-rewrite (GO-055) igual ao já usado por internal/platform/outbox
// desde GO-030.
func EnsureSchemaTx(ctx context.Context, tx database.Tx) error {
	ddl := createNotificationsTableSQL
	if tx.Dialect() == database.DialectSQLite {
		ddl = strings.NewReplacer(
			"serial PRIMARY KEY", "INTEGER PRIMARY KEY AUTOINCREMENT",
			"timestamptz", "timestamp",
			"now()", "CURRENT_TIMESTAMP",
		).Replace(ddl)
	}
	if err := tx.Exec(ctx, ddl); err != nil {
		return err
	}
	return tx.Exec(ctx, createNotificationsUserIndexSQL)
}
