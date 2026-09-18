package outbox

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/vjuliani/saltcorn/migracao/backend/internal/platform/database"
)

// OutboxEvent é um evento pendente lido de _sc_outbox para processamento.
type OutboxEvent struct {
	ID       int64
	Key      string
	Type     string
	Payload  map[string]any
	Attempts int
}

// TxHandler processa um OutboxEvent — roda dentro de uma savepoint própria
// (ver ProcessPending): se retornar erro, só o efeito do handler é
// desfeito, o lote inteiro não é abortado.
type TxHandler func(ctx context.Context, tx database.Tx, event OutboxEvent) error

// ListPendingTx lê até limit eventos com status='pending', mais antigos
// primeiro, travando as linhas escolhidas com `FOR UPDATE SKIP LOCKED` —
// o mecanismo que garante que dois workers concorrentes (duas transações
// diferentes chamando ListPending ao mesmo tempo) nunca peguem o mesmo
// evento: cada um pula silenciosamente as linhas que o outro já travou,
// em vez de bloquear esperando ou processar em duplicado.
func ListPendingTx(ctx context.Context, tx database.Tx, limit int) ([]OutboxEvent, error) {
	query := `
		SELECT id, idempotency_key, event_type, payload_json, attempts
		FROM _sc_outbox
		WHERE status = 'pending'
		ORDER BY id
		LIMIT $1
	`
	if tx.Dialect() == database.DialectPostgres {
		query += " FOR UPDATE SKIP LOCKED"
	}
	rows, err := tx.Query(ctx, query, limit)
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

// ListFailedTx lê até limit eventos com status='failed' — "falhas
// inspecionáveis" do critério de aceite: um operador (ou um teste) consulta
// isto para ver o que esgotou as tentativas, com o último erro registrado.
func ListFailedTx(ctx context.Context, tx database.Tx, limit int) ([]OutboxEvent, error) {
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

func markDone(ctx context.Context, tx database.Tx, id int64) error {
	err := tx.Exec(ctx, "UPDATE _sc_outbox SET status = 'done', processed_at = CURRENT_TIMESTAMP WHERE id = $1", id)
	return err
}

// markFailed incrementa attempts e decide o próximo status: de volta para
// 'pending' (nova tentativa mais tarde) se ainda não atingiu maxAttempts,
// ou 'failed' (terminal, inspecionável) se atingiu — nunca fica
// eternamente "pending" para um evento que já provou não conseguir
// processar.
func markFailed(ctx context.Context, tx database.Tx, id int64, errMsg string, maxAttempts int) error {
	err := tx.Exec(ctx, `
		UPDATE _sc_outbox
		SET attempts = attempts + 1,
			last_error = $2,
			status = CASE WHEN attempts + 1 >= $3 THEN 'failed' ELSE 'pending' END
		WHERE id = $1
	`, id, errMsg, maxAttempts)
	return err
}

// ProcessPendingTx lê até limit eventos pendentes (ListPending) e roda
// handler para cada um, DENTRO da transação tx já aberta pelo chamador —
// mas cada evento roda em sua própria savepoint SQL: se handler falhar para
// um evento, só o efeito DELE é desfeito (ROLLBACK TO SAVEPOINT) e ele é
// marcado para nova tentativa ou como falha terminal — os demais eventos
// do lote continuam sendo processados normalmente. maxAttempts limita
// quantas vezes um evento é tentado antes de virar 'failed' (retries com
// corte, não infinitos).
func ProcessPendingTx(ctx context.Context, tx database.Tx, limit, maxAttempts int, handler TxHandler) (processed, failed int, err error) {
	events, err := ListPendingTx(ctx, tx, limit)
	if err != nil {
		return 0, 0, err
	}

	for _, ev := range events {
		attemptErr := runInSavepoint(ctx, tx, func(sp database.Tx) error {
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

// runInSavepoint isola os efeitos de uma tentativa sem abrir conexão nova.
// O sucesso permanece pendente do commit da transação externa.
func runInSavepoint(ctx context.Context, tx database.Tx, fn func(sp database.Tx) error) error {
	// Nome privado; uma tentativa termina antes de começar a próxima.
	if err := tx.Exec(ctx, "SAVEPOINT sc_outbox_attempt"); err != nil {
		return err
	}
	if err := fn(tx); err != nil {
		if rollbackErr := tx.Exec(ctx, "ROLLBACK TO SAVEPOINT sc_outbox_attempt"); rollbackErr != nil {
			return fmt.Errorf("outbox: rollback: %w", rollbackErr)
		}
		if releaseErr := tx.Exec(ctx, "RELEASE SAVEPOINT sc_outbox_attempt"); releaseErr != nil {
			return releaseErr
		}
		return err
	}
	return tx.Exec(ctx, "RELEASE SAVEPOINT sc_outbox_attempt")
}
