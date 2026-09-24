// Package views persiste o documento produzido pelo editor Craft.js
// (GO-018, `craftToSaltcorn`) contra uma tabela dinâmica do catálogo — o
// ciclo "criar, salvar, reabrir, publicar" de GO-019. Deliberadamente NÃO
// inclui nenhuma lógica de RENDERIZAÇÃO de página para usuário final: isso
// é GO-020 ("Portar runtime de views e páginas"), que ainda não existe.
// `Template` aqui é só um identificador de intenção (ex.: "List", "Show"),
// armazenado, nunca interpretado.
package views

import (
	"context"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/vjuliani/saltcorn/migracao/backend/internal/platform/database"
)

// _sc_views é uma tabela de framework (como _sc_tables/_sc_fields de
// internal/metadata) — vive no schema do tenant, não é uma tabela dinâmica
// do catálogo que ela referencia.
//
// "_version" integer (GO-041): controle de concorrência otimista explícito
// para SQLite, que não tem `xmin` (Postgres continua usando `xmin`, nunca
// esta coluna — ver records.VersionExpr, reaproveitado por commands.go).
// default 1, incrementado a cada UPDATE só no dialeto SQLite — mesma
// convenção já usada por internal/metadata.AddTable para tabelas dinâmicas.
const createViewsTableSQL = `
CREATE TABLE IF NOT EXISTS _sc_views (
	id serial PRIMARY KEY,
	name text NOT NULL UNIQUE,
	table_id int REFERENCES _sc_tables(id),
	template text NOT NULL,
	min_role int NOT NULL DEFAULT 1,
	configuration jsonb NOT NULL DEFAULT '{}',
	"_version" integer NOT NULL DEFAULT 1,
	created_at timestamptz NOT NULL DEFAULT now()
)`

// EnsureSchema cria a tabela de framework deste pacote, idempotente.
// Assume que internal/metadata.EnsureSchema já rodou no mesmo tenant (a
// FOREIGN KEY para _sc_tables exige que ela já exista).
//
// Convenção pgx.Tx mantida por compatibilidade com todos os chamadores
// existentes (Postgres-only até GO-041).
func EnsureSchema(ctx context.Context, tx pgx.Tx) error {
	return EnsureSchemaTx(ctx, database.AsTx(tx))
}

// EnsureSchemaTx (GO-041) é a variante dialeto-neutra — chamada
// diretamente por internal/platform/sqlite e pelos testes de paridade.
func EnsureSchemaTx(ctx context.Context, tx database.Tx) error {
	sql := createViewsTableSQL
	if tx.Dialect() == database.DialectSQLite {
		rewrite := strings.NewReplacer("serial PRIMARY KEY", "INTEGER PRIMARY KEY AUTOINCREMENT", "timestamptz", "timestamp", "now()", "CURRENT_TIMESTAMP")
		sql = rewrite.Replace(sql)
	}
	return tx.Exec(ctx, sql)
}
