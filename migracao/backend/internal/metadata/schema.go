package metadata

import (
	"context"

	"github.com/jackc/pgx/v5"
)

// Tabelas de framework do catálogo de metadados — distintas das tabelas
// dinâmicas que elas DESCREVEM (uma linha em _sc_tables corresponde a uma
// tabela física própria, criada por CreateTable). Vivem no schema do
// tenant (chamar EnsureSchema dentro de db.WithTenant), como
// internal/identity — cada tenant tem seu próprio catálogo.
const createTablesTableSQL = `
CREATE TABLE IF NOT EXISTS _sc_tables (
	id serial PRIMARY KEY,
	name text NOT NULL UNIQUE,
	min_role_read int NOT NULL DEFAULT 100,
	min_role_write int NOT NULL DEFAULT 1,
	created_at timestamptz NOT NULL DEFAULT now()
)`

const createFieldsTableSQL = `
CREATE TABLE IF NOT EXISTS _sc_fields (
	id serial PRIMARY KEY,
	table_id int NOT NULL REFERENCES _sc_tables(id) ON DELETE CASCADE,
	name text NOT NULL,
	type text NOT NULL,
	required boolean NOT NULL DEFAULT false,
	is_unique boolean NOT NULL DEFAULT false,
	references_table_id int REFERENCES _sc_tables(id),
	created_at timestamptz NOT NULL DEFAULT now(),
	UNIQUE (table_id, name)
)`

// _sc_metadata_version é um contador monotônico de versão do catálogo,
// incrementado atomicamente (na mesma transação) por toda mutação — a base
// de "invalidação de cache" do critério de aceite de GO-011: um cache
// (metadata.Cache) compara sua versão guardada contra este contador para
// saber se precisa recarregar. Uma única linha por tenant (schema), com um
// CHECK garantindo isso.
const createVersionTableSQL = `
CREATE TABLE IF NOT EXISTS _sc_metadata_version (
	id smallint PRIMARY KEY DEFAULT 1,
	version bigint NOT NULL DEFAULT 0,
	CHECK (id = 1)
)`

const seedVersionRowSQL = `
INSERT INTO _sc_metadata_version (id, version) VALUES (1, 0)
ON CONFLICT (id) DO NOTHING`

// EnsureSchema cria as tabelas de framework do catálogo, idempotente. Não é
// o executor de migrations da própria plataforma de hospedagem (fora de
// escopo); é o mínimo necessário para este pacote ter onde persistir
// tabelas/campos/versão.
func EnsureSchema(ctx context.Context, tx pgx.Tx) error {
	if _, err := tx.Exec(ctx, createTablesTableSQL); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, createFieldsTableSQL); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, createVersionTableSQL); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, seedVersionRowSQL); err != nil {
		return err
	}
	return nil
}
