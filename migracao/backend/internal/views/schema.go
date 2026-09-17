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

	"github.com/jackc/pgx/v5"
)

// _sc_views é uma tabela de framework (como _sc_tables/_sc_fields de
// internal/metadata) — vive no schema do tenant, não é uma tabela dinâmica
// do catálogo que ela referencia.
const createViewsTableSQL = `
CREATE TABLE IF NOT EXISTS _sc_views (
	id serial PRIMARY KEY,
	name text NOT NULL UNIQUE,
	table_id int REFERENCES _sc_tables(id),
	template text NOT NULL,
	min_role int NOT NULL DEFAULT 1,
	configuration jsonb NOT NULL DEFAULT '{}',
	created_at timestamptz NOT NULL DEFAULT now()
)`

// EnsureSchema cria a tabela de framework deste pacote, idempotente.
// Assume que internal/metadata.EnsureSchema já rodou no mesmo tenant (a
// FOREIGN KEY para _sc_tables exige que ela já exista).
func EnsureSchema(ctx context.Context, tx pgx.Tx) error {
	_, err := tx.Exec(ctx, createViewsTableSQL)
	return err
}
