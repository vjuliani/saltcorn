package outbox

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// OutboxEvent é um evento pendente lido de _sc_outbox para processamento.
type OutboxEvent struct {
	ID       int64
	Key      string
	Type     string
	Payload  map[string]any
	Attempts int
}

// Handler processa um OutboxEvent — roda dentro de uma savepoint própria
// (ver ProcessPending): se retornar erro, só o efeito do handler é
// desfeito, o lote inteiro não é abortado.
type Handler func(ctx context.Context, tx pgx.Tx, event OutboxEvent) error

// ListPending lê até limit eventos com status='pending', mais antigos
// primeiro, travando as linhas escolhidas com `FOR UPDATE SKIP LOCKED` —
// o mecanismo que garante que dois workers concorrentes (duas transações
// diferentes chamando ListPending ao mesmo tempo) nunca peguem o mesmo
// evento: cada um pula silenciosamente as linhas que o outro já travou,
// em vez de bloquear esperando ou processar em duplicado.
func ListPending(ctx context.Context, tx pgx.Tx, limit int) ([]OutboxEvent, error) {
	rows, err := tx.Query(ctx, `
		SELECT id, idempotency_key, event_type, payload_json, attempts
		FROM _sc_outbox
		WHERE status = 'pending'
		ORDER BY id
		LIMIT $1
		FOR UPDATE SKIP LOCKED
	`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var events []OutboxEvent
	for rows.Next() {
		var ev OutboxEvent
		var payloadJSON []byte
		if err := rows.Scan(&ev.ID, &ev.Key, &ev.Type, &payloadJSON, &ev.Attempts); err != nil {
			return nil, err
		}
		if len(payloadJSON) > 0 {
			if err := json.Unmarshal(payloadJSON, &ev.Payload); err != nil {
				return nil, fmt.Errorf("outbox: decodificar payload do evento %d: %w", ev.ID, err)
			}
		}
		events = append(events, ev)
	}
	return events, rows.Err()
}

// ListFailed lê até limit eventos com status='failed' — "falhas
// inspecionáveis" do critério de aceite: um operador (ou um teste) consulta
// isto para ver o que esgotou as tentativas, com o último erro registrado.
func ListFailed(ctx context.Context, tx pgx.Tx, limit int) ([]OutboxEvent, error) {
	rows, err := tx.Query(ctx, `
		SELECT id, idempotency_key, event_type, payload_json, attempts
		FROM _sc_outbox
		WHERE status = 'failed'
		ORDER BY id
		LIMIT $1
	`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var events []OutboxEvent
	for rows.Next() {
		var ev OutboxEvent
		var payloadJSON []byte
		if err := rows.Scan(&ev.ID, &ev.Key, &ev.Type, &payloadJSON, &ev.Attempts); err != nil {
			return nil, err
		}
		if len(payloadJSON) > 0 {
			if err := json.Unmarshal(payloadJSON, &ev.Payload); err != nil {
				return nil, fmt.Errorf("outbox: decodificar payload do evento %d: %w", ev.ID, err)
			}
		}
		events = append(events, ev)
	}
	return events, rows.Err()
}

func markDone(ctx context.Context, tx pgx.Tx, id int64) error {
	_, err := tx.Exec(ctx, "UPDATE _sc_outbox SET status = 'done', processed_at = now() WHERE id = $1", id)
	return err
}

// markFailed incrementa attempts e decide o próximo status: de volta para
// 'pending' (nova tentativa mais tarde) se ainda não atingiu maxAttempts,
// ou 'failed' (terminal, inspecionável) se atingiu — nunca fica
// eternamente "pending" para um evento que já provou não conseguir
// processar.
func markFailed(ctx context.Context, tx pgx.Tx, id int64, errMsg string, maxAttempts int) error {
	_, err := tx.Exec(ctx, `
		UPDATE _sc_outbox
		SET attempts = attempts + 1,
			last_error = $2,
			status = CASE WHEN attempts + 1 >= $3 THEN 'failed' ELSE 'pending' END
		WHERE id = $1
	`, id, errMsg, maxAttempts)
	return err
}

// ProcessPending lê até limit eventos pendentes (ListPending) e roda
// handler para cada um, DENTRO da transação tx já aberta pelo chamador —
// mas cada evento roda em sua PRÓPRIA savepoint (uma "transação aninhada"
// do pgx, tx.Begin() sobre uma pgx.Tx já aberta): se handler falhar para
// um evento, só o efeito DELE é desfeito (ROLLBACK TO SAVEPOINT) e ele é
// marcado para nova tentativa ou como falha terminal — os demais eventos
// do lote continuam sendo processados normalmente. maxAttempts limita
// quantas vezes um evento é tentado antes de virar 'failed' (retries com
// corte, não infinitos).
func ProcessPending(ctx context.Context, tx pgx.Tx, limit, maxAttempts int, handler Handler) (processed, failed int, err error) {
	events, err := ListPending(ctx, tx, limit)
	if err != nil {
		return 0, 0, err
	}

	for _, ev := range events {
		attemptErr := runInSavepoint(ctx, tx, func(sp pgx.Tx) error {
			return handler(ctx, sp, ev)
		})
		if attemptErr != nil {
			if err := markFailed(ctx, tx, ev.ID, attemptErr.Error(), maxAttempts); err != nil {
				return processed, failed, err
			}
			failed++
			continue
		}
		if err := markDone(ctx, tx, ev.ID); err != nil {
			return processed, failed, err
		}
		processed++
	}
	return processed, failed, nil
}

// runInSavepoint executa fn dentro de uma savepoint de tx (tx.Begin() numa
// pgx.Tx já aberta é uma transação aninhada simulada via SAVEPOINT, não
// uma conexão nova) — se fn falhar, a savepoint é desfeita sem afetar tx;
// se suceder, a savepoint é liberada (RELEASE SAVEPOINT, via Commit) e o
// efeito de fn permanece dentro de tx, pendente do commit externo.
func runInSavepoint(ctx context.Context, tx pgx.Tx, fn func(sp pgx.Tx) error) error {
	sp, err := tx.Begin(ctx)
	if err != nil {
		return fmt.Errorf("outbox: abrir savepoint: %w", err)
	}
	if err := fn(sp); err != nil {
		_ = sp.Rollback(ctx)
		return err
	}
	return sp.Commit(ctx)
}
