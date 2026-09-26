// Package config persiste configuração de aplicação/tenant, chave-valor
// (GO-027) — o subconjunto prioritário de models/config.ts do legado
// (~2140 linhas, ~40+ chaves TIPADAS: site_name, timezone, smtp_*,
// menu_items, etc.) necessário para o campo `config` de um Pack
// (`packages/saltcorn-types/base_types.ts`, `Pack.config?: Record<string,
// any>`) fazer round-trip sem perda.
//
// Deliberadamente NÃO um catálogo tipado — cada chave é uma string livre,
// cada valor é JSON opaco (`any`). Portar o catálogo COMPLETO de ~40+
// chaves tipadas do legado (com validação por tipo, valores default,
// alguns já parcialmente cobertos por outras tarefas — ex.: SMTP por
// internal/notify, GO-026) é trabalho maior que o necessário para esta
// tarefa: aqui, config é só "o que estiver dentro de um Pack precisa
// sobreviver ao round-trip", não um sistema de configuração completo.
// NÃO é o mesmo pacote que internal/platform/config — aquele é
// configuração do PROCESSO Go (variáveis de ambiente); este é
// configuração da APLICAÇÃO dentro de um tenant, persistida no catálogo.
package config

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/vjuliani/saltcorn/migracao/backend/internal/platform/database"
)

const createConfigTableSQL = `
CREATE TABLE IF NOT EXISTS _sc_config (
	key text PRIMARY KEY,
	value jsonb NOT NULL
)`

// EnsureSchemaTx cria o catálogo de configuração, idempotente — chamar
// dentro de db.WithTenant, uma vez por tenant. `jsonb` é reescrito para
// `text` no SQLite (GO-055, mesmo padrão de dialect-rewrite de
// internal/platform/outbox/GO-030) — SQLite não tem tipo jsonb nativo.
func EnsureSchemaTx(ctx context.Context, tx database.Tx) error {
	ddl := createConfigTableSQL
	if tx.Dialect() == database.DialectSQLite {
		ddl = strings.NewReplacer("jsonb", "text").Replace(ddl)
	}
	return tx.Exec(ctx, ddl)
}

// SetTx grava (ou substitui) o valor de key — idempotente, mesma chave
// grava por cima do valor anterior sem erro (reinstalação de um pack não
// deveria falhar só porque a chave já existe de uma instalação anterior).
func SetTx(ctx context.Context, tx database.Tx, key string, value any) error {
	valueJSON, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("config: codificar valor de %q: %w", key, err)
	}
	return tx.Exec(ctx,
		`INSERT INTO _sc_config (key, value) VALUES ($1, $2)
		 ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value`,
		key, valueJSON,
	)
}

// GetTx lê o valor de key — ok=false se a chave não existir (nunca um
// erro: ausência de configuração é um estado normal, não excepcional).
func GetTx(ctx context.Context, tx database.Tx, key string) (value any, ok bool, err error) {
	var valueJSON []byte
	err = tx.QueryRow(ctx, `SELECT value FROM _sc_config WHERE key = $1`, key).Scan(&valueJSON)
	if err != nil {
		if errors.Is(err, database.ErrNoRows) {
			return nil, false, nil
		}
		return nil, false, err
	}
	if err := json.Unmarshal(valueJSON, &value); err != nil {
		return nil, false, fmt.Errorf("config: decodificar valor de %q: %w", key, err)
	}
	return value, true, nil
}

// DeleteTx remove key — idempotente (remover uma chave inexistente não é
// erro).
func DeleteTx(ctx context.Context, tx database.Tx, key string) error {
	return tx.Exec(ctx, `DELETE FROM _sc_config WHERE key = $1`, key)
}

// ListAllTx lê todas as entradas de configuração do tenant, como um mapa
// — usado por internal/pack (GO-027) para exportar/importar o campo
// `Pack.Config` inteiro de uma vez.
func ListAllTx(ctx context.Context, tx database.Tx) (map[string]any, error) {
	rows, err := tx.Query(ctx, `SELECT key, value FROM _sc_config ORDER BY key`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := map[string]any{}
	for rows.Next() {
		var key string
		var valueJSON []byte
		if err := rows.Scan(&key, &valueJSON); err != nil {
			return nil, err
		}
		var value any
		if err := json.Unmarshal(valueJSON, &value); err != nil {
			return nil, fmt.Errorf("config: decodificar valor de %q: %w", key, err)
		}
		out[key] = value
	}
	return out, rows.Err()
}
