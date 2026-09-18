package notify

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
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

// Create insere a notificação e, se notifyEmail for true e emailTo não
// vazio, enfileira o e-mail correspondente — NA MESMA transação tx.
// Divergência deliberada e MAIS FORTE que o legado: Notification.create()
// insere a linha e só DEPOIS, fora de qualquer transação, aciona
// MailQueue.handleNotification (fila em memória do processo, agendada via
// setTimeout) — se o processo cair entre os dois passos, ou com uma
// notificação "pending" agendada, o e-mail nunca sai (achado de
// preflight). Aqui, se tx não commitar, nem a notificação nem o e-mail
// enfileirado sobrevivem — os dois efeitos são atômicos entre si.
func Create(ctx context.Context, tx pgx.Tx, n Notification, notifyEmail bool, emailTo string) (Notification, error) {
	err := tx.QueryRow(ctx,
		`INSERT INTO _sc_notifications (user_id, title, body, link) VALUES ($1, $2, $3, $4) RETURNING id`,
		n.UserID, n.Title, n.Body, n.Link,
	).Scan(&n.ID)
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

// MarkRead marca a notificação id como lida — idempotente (marcar uma já
// lida de novo não é erro).
func MarkRead(ctx context.Context, tx pgx.Tx, id int) error {
	_, err := tx.Exec(ctx, `UPDATE _sc_notifications SET read = true WHERE id = $1`, id)
	return err
}

// ListForUser lê as notificações de userID, mais recentes primeiro.
func ListForUser(ctx context.Context, tx pgx.Tx, userID int, unreadOnly bool, limit int) ([]Notification, error) {
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
