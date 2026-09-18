package metadata

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/vjuliani/saltcorn/migracao/backend/internal/platform/database"
)

// Tabelas de framework do catálogo de metadados — distintas das tabelas
// dinâmicas que elas DESCREVEM (uma linha em _sc_tables corresponde a uma
// tabela física própria, criada por CreateTable). Vivem no schema do
// tenant (chamar EnsureSchema dentro de db.WithTenant), como
// internal/identity — cada tenant tem seu próprio catálogo.
//
// idColumnDDL/timestampDefaultDDL (GO-030) são os ÚNICOS dois pontos de
// divergência de sintaxe DDL entre Postgres e SQLite neste arquivo — tudo
// mais (tipos `text`/`int`/`boolean`, `UNIQUE`, `REFERENCES ... ON DELETE
// CASCADE`, `CHECK`, `ON CONFLICT ... DO NOTHING`) é sintaxe compartilhada
// pelos dois SGBDs. `serial`/`smallint`/`bigint` do Postgres funcionariam
// como NOME DE TIPO em SQLite também (SQLite só usa o nome para inferir
// afinidade, nunca rejeita um nome desconhecido) — mas `serial` como
// coluna de ID especificamente NÃO ganha o comportamento de
// autoincremento do SQLite, que exige o literal `INTEGER PRIMARY KEY`;
// por isso só esse ponto precisa de um branch real.
func idColumnDDL(d database.Dialect) string {
	if d == database.DialectSQLite {
		return "id INTEGER PRIMARY KEY AUTOINCREMENT"
	}
	return "id serial PRIMARY KEY"
}

func timestampDefaultDDL(d database.Dialect) string {
	if d == database.DialectSQLite {
		return "timestamp NOT NULL DEFAULT CURRENT_TIMESTAMP"
	}
	return "timestamptz NOT NULL DEFAULT now()"
}

func createTablesTableSQL(d database.Dialect) string {
	return fmt.Sprintf(`
CREATE TABLE IF NOT EXISTS _sc_tables (
	%s,
	name text NOT NULL UNIQUE,
	min_role_read int NOT NULL DEFAULT 100,
	min_role_write int NOT NULL DEFAULT 1,
	created_at %s
)`, idColumnDDL(d), timestampDefaultDDL(d))
}

func createFieldsTableSQL(d database.Dialect) string {
	return fmt.Sprintf(`
CREATE TABLE IF NOT EXISTS _sc_fields (
	%s,
	table_id int NOT NULL REFERENCES _sc_tables(id) ON DELETE CASCADE,
	name text NOT NULL,
	type text NOT NULL,
	required boolean NOT NULL DEFAULT false,
	is_unique boolean NOT NULL DEFAULT false,
	references_table_id int REFERENCES _sc_tables(id),
	created_at %s,
	UNIQUE (table_id, name)
)`, idColumnDDL(d), timestampDefaultDDL(d))
}

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
// tabelas/campos/versão. tx.Dialect() (GO-030) decide a DDL exata — quem
// chama nunca precisa saber qual SGBD está por trás.
func EnsureSchema(ctx context.Context, tx database.Tx) error {
	d := tx.Dialect()
	if err := tx.Exec(ctx, createTablesTableSQL(d)); err != nil {
		return err
	}
	if err := tx.Exec(ctx, createFieldsTableSQL(d)); err != nil {
		return err
	}
	if err := tx.Exec(ctx, createVersionTableSQL); err != nil {
		return err
	}
	if err := tx.Exec(ctx, seedVersionRowSQL); err != nil {
		return err
	}
	if d == database.DialectSQLite {
		return ensureSQLiteRecordVersions(ctx, tx)
	}
	return nil
}

// Atualiza arquivos criados pelo primeiro checkpoint de GO-030 sem apagar
// registros locais. O DDL e a verificação rodam sob BEGIN IMMEDIATE.
func ensureSQLiteRecordVersions(ctx context.Context, tx database.Tx) error {
	tables, err := ListTables(ctx, tx)
	if err != nil {
		return err
	}
	for _, table := range tables {
		fields, err := ListFields(ctx, tx, table.ID)
		if err != nil {
			return err
		}
		for _, field := range fields {
			if field.Name == "_version" {
				return fmt.Errorf("metadata: campo reservado _version em %s exige migração explícita", table.Name)
			}
		}
		quoted := pgx.Identifier{table.Name}.Sanitize()
		rows, err := tx.Query(ctx, "PRAGMA table_info("+quoted+")")
		if err != nil {
			return err
		}
		found := false
		for rows.Next() {
			var cid, required, pk int
			var name, columnType string
			var defaultValue any
			if err := rows.Scan(&cid, &name, &columnType, &required, &defaultValue, &pk); err != nil {
				rows.Close()
				return err
			}
			if name == "_version" {
				found = true
			}
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		if !found {
			if err := tx.Exec(ctx, "ALTER TABLE "+quoted+` ADD COLUMN "_version" INTEGER NOT NULL DEFAULT 1`); err != nil {
				return err
			}
		}
	}
	return nil
}
