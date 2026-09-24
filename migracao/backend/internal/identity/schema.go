package identity

import (
	"context"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/vjuliani/saltcorn/migracao/backend/internal/platform/database"
)

// Tabelas de framework para identidade — distintas do catálogo de tabelas
// dinâmicas definidas pelo usuário final (GO-011). Nomeadas como a produção
// Node (`_sc_users`, `_sc_api_tokens`, matriz GO-001 §2.1) para manter o
// mapeamento conceitual claro, mas o formato de armazenamento não precisa
// (e não deveria) copiar bit-a-bit a produção — ver token.go sobre guardar
// hash em vez de texto plano.
const createUsersTableSQL = `
CREATE TABLE IF NOT EXISTS _sc_users (
	id serial PRIMARY KEY,
	email text NOT NULL UNIQUE,
	password_hash text NOT NULL,
	role_id int NOT NULL DEFAULT 80,
	totp_secret text,
	totp_enabled boolean NOT NULL DEFAULT false,
	language text,
	created_at timestamptz NOT NULL DEFAULT now()
)`

const createAPITokensTableSQL = `
CREATE TABLE IF NOT EXISTS _sc_api_tokens (
	id serial PRIMARY KEY,
	user_id int NOT NULL REFERENCES _sc_users(id) ON DELETE CASCADE,
	token_hash text NOT NULL UNIQUE,
	created_at timestamptz NOT NULL DEFAULT now(),
	revoked_at timestamptz
)`

// _sc_impersonation_log é a trilha de auditoria de "become-user" (GO-044,
// ver impersonation.go) — uma garantia NOVA, o legado não registra nada
// disso. admin_user_id/target_user_id não usam ON DELETE CASCADE de
// propósito: um registro de auditoria nunca deveria desaparecer junto com o
// usuário que ele documenta.
const createImpersonationLogTableSQL = `
CREATE TABLE IF NOT EXISTS _sc_impersonation_log (
	id serial PRIMARY KEY,
	admin_user_id int NOT NULL REFERENCES _sc_users(id),
	target_user_id int NOT NULL REFERENCES _sc_users(id),
	started_at timestamptz NOT NULL DEFAULT now(),
	ended_at timestamptz
)`

// EnsureSchema cria as tabelas de identidade no tenant/schema atual da
// transação (idempotente — seguro para chamar em todo teste/boot). Não é o
// executor de migrations real do backend (GO-011); é o mínimo necessário
// para esta tarefa ter onde persistir usuários e tokens.
//
// Convenção pgx.Tx mantida por compatibilidade com todos os chamadores
// HTTP existentes (cmd/server/cmd/cli, exclusivamente Postgres até
// GO-041) — delega para EnsureSchemaTx via database.AsTx, mesmo padrão
// de internal/records/postgres.go.
func EnsureSchema(ctx context.Context, tx pgx.Tx) error {
	return EnsureSchemaTx(ctx, database.AsTx(tx))
}

// EnsureSchemaTx (GO-041) é a variante dialeto-neutra — chamada
// diretamente por internal/platform/sqlite (cmd/server/cmd/worker
// rodando contra um tenant em arquivo) e pelos testes de paridade.
// dialectRewrite (GO-041, mesmo padrão de internal/platform/outbox.
// EnsureSchemaTx) reescreve a MESMA string DDL Postgres para SQLite —
// uma única fonte de verdade textual, nunca duas DDLs mantidas em
// paralelo.
func EnsureSchemaTx(ctx context.Context, tx database.Tx) error {
	users, tokens, impersonation := createUsersTableSQL, createAPITokensTableSQL, createImpersonationLogTableSQL
	if tx.Dialect() == database.DialectSQLite {
		rewrite := strings.NewReplacer("serial PRIMARY KEY", "INTEGER PRIMARY KEY AUTOINCREMENT", "timestamptz", "timestamp", "now()", "CURRENT_TIMESTAMP")
		users, tokens, impersonation = rewrite.Replace(users), rewrite.Replace(tokens), rewrite.Replace(impersonation)
	}
	if err := tx.Exec(ctx, users); err != nil {
		return err
	}
	if err := tx.Exec(ctx, tokens); err != nil {
		return err
	}
	if err := tx.Exec(ctx, impersonation); err != nil {
		return err
	}
	return nil
}
