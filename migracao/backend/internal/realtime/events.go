package realtime

import (
	"context"
	"encoding/json"
	"time"

	"github.com/jackc/pgx/v5"
)

// Audience decide quem recebe um Event — só os dois modos com um
// consumidor Go real hoje (ver nota de escopo em schema.go): o modo
// "público" do legado (visitante anônimo) fica de fora.
type Audience string

const (
	// AudienceBroadcast entrega a QUALQUER ator autenticado do tenant —
	// equivalente a `_${tenant}_dynamic_update_room` do legado (userIds
	// omitido/undefined).
	AudienceBroadcast Audience = "broadcast"
	// AudienceUsers entrega só aos UserIDs listados — equivalente a
	// `_${tenant}:${userId}_dynamic_update_room` do legado (userIds como
	// array).
	AudienceUsers Audience = "users"
)

func (a Audience) valid() bool {
	switch a {
	case AudienceBroadcast, AudienceUsers:
		return true
	default:
		return false
	}
}

// Event é uma entrada de _sc_realtime_events já publicada — ID é a chave
// de ordenação/retomada que ListSinceForActor usa (nunca CreatedAt, que
// não é estritamente monotônico sob relógios ajustados/paralelismo de
// commit).
type Event struct {
	ID        int64
	Audience  Audience
	UserIDs   []int
	Payload   map[string]any
	CreatedAt time.Time
}

// Publish grava um novo evento na MESMA transação tx do efeito de domínio
// que o originou (ex.: internal/notify.Create) — atomicidade "de graça"
// pela convenção já estabelecida em todo o backend (toda função de
// comando recebe tx, nunca abre a própria transação). Valida a
// combinação audience/userIDs antes de tocar o banco, para um erro Go
// claro em vez de propagar a mensagem crua de uma violação de CHECK.
func Publish(ctx context.Context, tx pgx.Tx, audience Audience, userIDs []int, payload map[string]any) (Event, error) {
	if !audience.valid() {
		return Event{}, ErrInvalidAudience
	}
	switch audience {
	case AudienceUsers:
		if len(userIDs) == 0 {
			return Event{}, ErrInvalidUserIDs
		}
	case AudienceBroadcast:
		if len(userIDs) != 0 {
			return Event{}, ErrInvalidUserIDs
		}
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return Event{}, err
	}

	var userIDsArg any
	if len(userIDs) > 0 {
		ids32 := make([]int32, len(userIDs))
		for i, id := range userIDs {
			ids32[i] = int32(id)
		}
		userIDsArg = ids32
	}

	ev := Event{Audience: audience, UserIDs: userIDs, Payload: payload}
	err = tx.QueryRow(ctx,
		`INSERT INTO _sc_realtime_events (audience, user_ids, payload) VALUES ($1, $2, $3) RETURNING id, created_at`,
		string(audience), userIDsArg, body,
	).Scan(&ev.ID, &ev.CreatedAt)
	if err != nil {
		return Event{}, err
	}
	return ev, nil
}

// ListSinceForActor lê, em ordem de publicação (id crescente — a mesma
// ordem de INSERT, garantida por bigserial), os eventos com id > afterID
// visíveis a actorID: todo AudienceBroadcast, mais AudienceUsers cuja
// lista contenha actorID. O filtro por audience/userIDs roda NO Postgres
// (WHERE), não em Go depois de ler tudo — um BFF nunca recebe sequer a
// existência de um evento endereçado a outro usuário, defesa em
// profundidade além do próprio isolamento de schema por tenant.
func ListSinceForActor(ctx context.Context, tx pgx.Tx, afterID int64, actorID int, limit int) ([]Event, error) {
	rows, err := tx.Query(ctx,
		`SELECT id, audience, user_ids, payload, created_at FROM _sc_realtime_events
		 WHERE id > $1 AND (audience = 'broadcast' OR $2 = ANY(user_ids))
		 ORDER BY id ASC LIMIT $3`,
		afterID, int32(actorID), limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Event
	for rows.Next() {
		var ev Event
		var audienceStr string
		var userIDs32 []int32
		var body []byte
		if err := rows.Scan(&ev.ID, &audienceStr, &userIDs32, &body, &ev.CreatedAt); err != nil {
			return nil, err
		}
		ev.Audience = Audience(audienceStr)
		if len(userIDs32) > 0 {
			ev.UserIDs = make([]int, len(userIDs32))
			for i, id := range userIDs32 {
				ev.UserIDs[i] = int(id)
			}
		}
		if err := json.Unmarshal(body, &ev.Payload); err != nil {
			return nil, err
		}
		out = append(out, ev)
	}
	return out, rows.Err()
}
