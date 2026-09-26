// Wrappers pgx.Tx (GO-055) — mesmo padrão de internal/records/postgres.go
// (GO-030): preservam o nome/assinatura ORIGINAL para todo chamador
// Postgres existente, delegando para as versões Tx via database.AsTx.
package notify

import (
	"context"
	"net/http"

	"github.com/jackc/pgx/v5"

	"github.com/vjuliani/saltcorn/migracao/backend/internal/platform/database"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/platform/outbox"
)

func EnsureSchema(ctx context.Context, tx pgx.Tx) error {
	return EnsureSchemaTx(ctx, database.AsTx(tx))
}

func EnqueueEmail(ctx context.Context, tx pgx.Tx, key string, msg EmailMessage) error {
	return EnqueueEmailTx(ctx, database.AsTx(tx), key, msg)
}

func EnqueueWebhook(ctx context.Context, tx pgx.Tx, key string, req WebhookRequest) error {
	return EnqueueWebhookTx(ctx, database.AsTx(tx), key, req)
}

func MarkRead(ctx context.Context, tx pgx.Tx, id int) error {
	return MarkReadTx(ctx, database.AsTx(tx), id)
}

func ListForUser(ctx context.Context, tx pgx.Tx, userID int, unreadOnly bool, limit int) ([]Notification, error) {
	return ListForUserTx(ctx, database.AsTx(tx), userID, unreadOnly, limit)
}

// Handler é o equivalente pgx.Tx de HandlerTx, para cmd/worker/Postgres —
// não delega a HandlerTx porque fallback aqui é pgx.Tx-shaped (não
// database.Tx); handleEmailEvent/handleWebhookEvent não dependem de tx,
// então a duplicação é só a troca do switch e do tipo de fallback.
func Handler(smtpCfg SMTPConfig, httpClient *http.Client, fallback outbox.Handler) outbox.Handler {
	return func(ctx context.Context, tx pgx.Tx, ev outbox.OutboxEvent) error {
		switch ev.Type {
		case EventTypeEmail:
			return handleEmailEvent(ctx, smtpCfg, ev)
		case EventTypeWebhook:
			return handleWebhookEvent(ctx, httpClient, ev)
		default:
			if fallback != nil {
				return fallback(ctx, tx, ev)
			}
			return nil
		}
	}
}
