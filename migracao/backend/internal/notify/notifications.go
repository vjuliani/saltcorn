package notify

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/vjuliani/saltcorn/migracao/backend/internal/platform/database"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/realtime"
)

// Notification é uma entrada de _sc_notifications — equivalente reduzido
// do legado (models/notification.ts), sem os canais push nativo/in-app
// dinâmico (ver comentário de schema.go).
type Notification struct {
	ID     int
	UserID int
	Title  string
	Body   string
	Link   string
	Read   bool
}

// Create insere a notificação, publica o evento de tempo real
// correspondente (internal/realtime, GO-028) e, se notifyEmail for true e
// emailTo não vazio, enfileira o e-mail — tudo NA MESMA transação tx.
// Divergência deliberada e MAIS FORTE que o legado: Notification.create()
// insere a linha e só DEPOIS, fora de qualquer transação, aciona
// MailQueue.handleNotification (fila em memória do processo, agendada via
// setTimeout) e o emissor Socket.IO em memória (state.emitDynamicUpdate) —
// se o processo cair entre os passos, ou não houver socket conectado
// naquele instante, o e-mail e/ou o evento em tempo real nunca saem
// (achado de preflight/GO-028). Aqui, se tx não commitar, nada sobrevive;
// se commitar, o evento de tempo real fica persistido esperando o BFF
// buscar (realtime.ListSinceForActor), nunca dependente de um socket já
// estar conectado no momento exato da publicação.
//
// GO-055: permanece pgx.Tx-only, deliberadamente não convertida —
// internal/realtime (Publish) é, ele mesmo, inteiramente pgx.Tx-only e
// não fazia parte do escopo nomeado desta task; converter Create exigiria
// converter realtime também, uma dependência transitiva nova descoberta
// só no preflight desta task. Nenhuma rota hoje conecta esta função a um
// tenant SQLite (create/atualizar/apagar registro contra SQLite não
// dispara nenhuma notificação in-app), então a lacuna é honesta e sem
// impacto de produto observável nesta entrega — ver GO-055.md.
func Create(ctx context.Context, tx pgx.Tx, n Notification, notifyEmail bool, emailTo string) (Notification, error) {
	err := tx.QueryRow(ctx,
		`INSERT INTO _sc_notifications (user_id, title, body, link) VALUES ($1, $2, $3, $4) RETURNING id`,
		n.UserID, n.Title, n.Body, n.Link,
	).Scan(&n.ID)
	if err != nil {
		return Notification{}, err
	}

	_, err = realtime.Publish(ctx, tx, realtime.AudienceUsers, []int{n.UserID}, map[string]any{
		"type":  "notification",
		"id":    n.ID,
		"title": n.Title,
		"body":  n.Body,
		"link":  n.Link,
	})
	if err != nil {
		return Notification{}, err
	}

	if notifyEmail && emailTo != "" {
		key := fmt.Sprintf("notification:%d:email", n.ID)
		if err := EnqueueEmail(ctx, tx, key, EmailMessage{To: []string{emailTo}, Subject: n.Title, Body: n.Body}); err != nil {
			return Notification{}, err
		}
	}
	return n, nil
}

// MarkReadTx marca a notificação id como lida — idempotente (marcar uma
// já lida de novo não é erro).
func MarkReadTx(ctx context.Context, tx database.Tx, id int) error {
	return tx.Exec(ctx, `UPDATE _sc_notifications SET read = true WHERE id = $1`, id)
}

// ListForUserTx lê as notificações de userID, mais recentes primeiro.
func ListForUserTx(ctx context.Context, tx database.Tx, userID int, unreadOnly bool, limit int) ([]Notification, error) {
	sql := `SELECT id, user_id, title, COALESCE(body, ''), COALESCE(link, ''), read FROM _sc_notifications WHERE user_id = $1`
	args := []any{userID}
	if unreadOnly {
		sql += ` AND read = false`
	}
	sql += ` ORDER BY id DESC LIMIT $2`
	args = append(args, limit)

	rows, err := tx.Query(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Notification
	for rows.Next() {
		var n Notification
		if err := rows.Scan(&n.ID, &n.UserID, &n.Title, &n.Body, &n.Link, &n.Read); err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}
