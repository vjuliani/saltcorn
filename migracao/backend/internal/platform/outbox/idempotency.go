package outbox

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/vjuliani/saltcorn/migracao/backend/internal/platform/database"
)

// Event é um evento de domínio a gravar na tabela outbox na mesma
// transação da escrita que o originou — nunca despachado diretamente; o
// worker (ProcessPending) o processa depois.
type Event struct {
	Type    string
	Payload map[string]any
}

// Do executa fn exatamente uma vez por (key, payload): chamadas
// subsequentes com a MESMA key e o MESMO payload retornam o resultado já
// gravado sem rodar fn de novo (replayed=true) — "redelivery não repete
// efeito interno". A MESMA key com um payload DIFERENTE falha com
// ErrKeyConflict, sem rodar fn.
//
// fn roda dentro da MESMA transação tx que grava a chave de idempotência e
// os eventos que ela retornar — se fn falhar, ou se o chamador de Do
// decidir abortar tx por qualquer motivo depois, nada disso persiste
// (nem o efeito de fn, nem a chave, nem os eventos): não há necessidade de
// um estado "failed" durável para a chave, porque uma tentativa que não
// commitou nunca "confirmou" nada — uma nova chamada com a mesma
// key/payload encontra o catálogo limpo e tenta de novo, cumprindo "crash
// antes do commit não perde evento confirmado" pela própria atomicidade do
// Postgres.
//
// Um pg_advisory_xact_lock escopado a (tenant atual, key) serializa
// tentativas concorrentes com a MESMA chave — sem isso, duas chamadas
// concorrentes poderiam ambas ver "não existe" e rodar fn duas vezes,
// exatamente o que a idempotência existe para impedir.
func DoTx(ctx context.Context, tx database.Tx, key string, payload any, fn func(ctx context.Context, tx database.Tx) (result any, events []Event, err error)) (result any, replayed bool, err error) {
	if err := lockKey(ctx, tx, key); err != nil {
		return nil, false, err
	}

	payloadHash, err := hashPayload(payload)
	if err != nil {
		return nil, false, fmt.Errorf("outbox: calcular hash do payload: %w", err)
	}

	existingHash, existingResult, err := getKey(ctx, tx, key)
	if err == nil {
		if existingHash != payloadHash {
			return nil, false, fmt.Errorf("%w: %q", ErrKeyConflict, key)
		}
		var decoded any
		if len(existingResult) > 0 {
			if err := json.Unmarshal(existingResult, &decoded); err != nil {
				return nil, false, fmt.Errorf("outbox: decodificar resultado gravado: %w", err)
			}
		}
		return decoded, true, nil
	} else if !errors.Is(err, errKeyNotFound) {
		return nil, false, err
	}

	result, events, fnErr := fn(ctx, tx)
	if fnErr != nil {
		// Nada é gravado aqui de propósito — ver comentário da função.
		return nil, false, fnErr
	}

	resultJSON, err := json.Marshal(result)
	if err != nil {
		return nil, false, fmt.Errorf("outbox: codificar resultado: %w", err)
	}
	if err := insertKey(ctx, tx, key, payloadHash, resultJSON); err != nil {
		return nil, false, err
	}
	for _, ev := range events {
		if err := insertOutboxEvent(ctx, tx, key, ev); err != nil {
			return nil, false, err
		}
	}
	return result, false, nil
}

func lockKey(ctx context.Context, tx database.Tx, key string) error {
	if tx.Dialect() == database.DialectSQLite {
		return nil
	} // BEGIN IMMEDIATE já reserva o escritor.
	err := tx.Exec(ctx, "SELECT pg_advisory_xact_lock(hashtext(current_schema() || ':idempotency:' || $1)::bigint)", key)
	if err != nil {
		return fmt.Errorf("outbox: adquirir lock de chave de idempotência: %w", err)
	}
	return nil
}

func getKey(ctx context.Context, tx database.Tx, key string) (payloadHash string, resultJSON []byte, err error) {
	err = tx.QueryRow(ctx, "SELECT payload_hash, result_json FROM _sc_idempotency_keys WHERE key = $1", key).Scan(&payloadHash, &resultJSON)
	if err != nil {
		if errors.Is(err, database.ErrNoRows) {
			return "", nil, errKeyNotFound
		}
		return "", nil, err
	}
	return payloadHash, resultJSON, nil
}

func insertKey(ctx context.Context, tx database.Tx, key, payloadHash string, resultJSON []byte) error {
	err := tx.Exec(ctx,
		"INSERT INTO _sc_idempotency_keys (key, payload_hash, result_json) VALUES ($1, $2, $3)",
		key, payloadHash, string(resultJSON),
	)
	return err
}

func insertOutboxEvent(ctx context.Context, tx database.Tx, key string, ev Event) error {
	payloadJSON, err := json.Marshal(ev.Payload)
	if err != nil {
		return fmt.Errorf("outbox: codificar payload do evento: %w", err)
	}
	err = tx.Exec(ctx,
		"INSERT INTO _sc_outbox (idempotency_key, event_type, payload_json) VALUES ($1, $2, $3)",
		key, ev.Type, string(payloadJSON),
	)
	return err
}

// hashPayload usa encoding/json (que ordena chaves de map alfabeticamente
// de forma determinística) seguido de SHA-256 — dois payloads
// semanticamente iguais (mesmas chaves/valores, ordem de inserção
// diferente num map) produzem o mesmo hash.
func hashPayload(payload any) (string, error) {
	b, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:]), nil
}
