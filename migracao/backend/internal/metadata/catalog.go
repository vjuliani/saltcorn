package metadata

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/vjuliani/saltcorn/migracao/backend/internal/identity"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/platform/database"
)

// lockCatalog serializa toda mutação de catálogo do tenant atual (schema
// já escopado por WithTenant/WithTenantAndActor via search_path) — o
// "executor único de migrations" de ADR-0001, aplicado por tenant, não
// globalmente. pg_advisory_xact_lock é escopado à transação: libera sozinho
// no commit ou rollback, nunca precisa de unlock explícito, e nunca fica
// pendurado se o processo cair no meio.
//
// A chave inclui current_schema() para que tenants diferentes (schemas
// diferentes) nunca se bloqueiem mutuamente — só operações concorrentes no
// MESMO tenant serializam entre si.
//
// SQLite serializa escritores desde BEGIN IMMEDIATE, inclusive entre
// handles/processos diferentes que compartilham o mesmo arquivo.
func lockCatalog(ctx context.Context, tx database.Tx) error {
	if tx.Dialect() == database.DialectSQLite {
		return nil
	}
	if err := tx.Exec(ctx, "SELECT pg_advisory_xact_lock(hashtext(current_schema() || ':metadata')::bigint)"); err != nil {
		return fmt.Errorf("metadata: adquirir lock de catálogo: %w", err)
	}
	return nil
}

func requireAdmin(actorRole identity.RoleID) error {
	if !identity.CanWrite(actorRole, identity.RoleAdmin) {
		return ErrNotAuthorized
	}
	return nil
}

// GetTable busca uma tabela do catálogo pelo nome (já sanitizado — o mesmo
// nome usado na criação). Retorna ErrTableNotFound se não existir.
func GetTable(ctx context.Context, tx database.Tx, name string) (*Table, error) {
	t := &Table{}
	var minRead, minWrite int
	err := tx.QueryRow(ctx,
		"SELECT id, name, min_role_read, min_role_write FROM _sc_tables WHERE name = $1",
		name,
	).Scan(&t.ID, &t.Name, &minRead, &minWrite)
	if err != nil {
		if errors.Is(err, database.ErrNoRows) {
			return nil, ErrTableNotFound
		}
		return nil, err
	}
	t.MinRoleRead = identity.RoleID(minRead)
	t.MinRoleWrite = identity.RoleID(minWrite)
	return t, nil
}

// ListTables retorna todas as tabelas do catálogo do tenant, na ordem de
// criação — usado por internal/pack (GO-027) para exportar a aplicação
// inteira; nenhum outro chamador precisava disto até aqui (GetTable, por
// nome, bastava).
func ListTables(ctx context.Context, tx database.Tx) ([]Table, error) {
	rows, err := tx.Query(ctx, "SELECT id, name, min_role_read, min_role_write FROM _sc_tables ORDER BY id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var tables []Table
	for rows.Next() {
		var t Table
		var minRead, minWrite int
		if err := rows.Scan(&t.ID, &t.Name, &minRead, &minWrite); err != nil {
			return nil, err
		}
		t.MinRoleRead = identity.RoleID(minRead)
		t.MinRoleWrite = identity.RoleID(minWrite)
		tables = append(tables, t)
	}
	return tables, rows.Err()
}

// ListFields retorna os campos de uma tabela, na ordem de criação.
func ListFields(ctx context.Context, tx database.Tx, tableID int) ([]Field, error) {
	rows, err := tx.Query(ctx,
		"SELECT id, table_id, name, type, required, is_unique, references_table_id FROM _sc_fields WHERE table_id = $1 ORDER BY id",
		tableID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var fields []Field
	for rows.Next() {
		var f Field
		var refTable sql.NullInt64
		var fieldType string
		if err := rows.Scan(&f.ID, &f.TableID, &f.Name, &fieldType, &f.Required, &f.Unique, &refTable); err != nil {
			return nil, err
		}
		f.Type = FieldType(fieldType)
		if refTable.Valid {
			f.ReferencesTable = int(refTable.Int64)
		}
		fields = append(fields, f)
	}
	return fields, rows.Err()
}

func getFieldByName(ctx context.Context, tx database.Tx, tableID int, name string) (*Field, error) {
	f := &Field{}
	var refTable sql.NullInt64
	var fieldType string
	err := tx.QueryRow(ctx,
		"SELECT id, table_id, name, type, required, is_unique, references_table_id FROM _sc_fields WHERE table_id = $1 AND name = $2",
		tableID, name,
	).Scan(&f.ID, &f.TableID, &f.Name, &fieldType, &f.Required, &f.Unique, &refTable)
	if err != nil {
		if errors.Is(err, database.ErrNoRows) {
			return nil, ErrFieldNotFound
		}
		return nil, err
	}
	f.Type = FieldType(fieldType)
	if refTable.Valid {
		f.ReferencesTable = int(refTable.Int64)
	}
	return f, nil
}

// CreateTable cria uma tabela dinâmica: registra no catálogo (_sc_tables) e
// executa `CREATE TABLE` na mesma transação, e incrementa a versão do
// catálogo — se qualquer passo falhar, a transação inteira desfaz (critério
// de aceite "falha intermediária é recuperável"). Idempotente: se já existe
// uma tabela com o mesmo nome (depois de SQLSanitize), retorna a tabela
// EXISTENTE sem erro e sem incrementar a versão — nenhuma mudança real
// aconteceu.
//
// name passa por SQLSanitize antes de qualquer uso — nunca é interpolado
// cru em SQL (critério de aceite "nomes maliciosos são cobertos"); o
// identificador final ainda é citado via pgx.Identifier.Sanitize() na DDL,
// defesa em profundidade.
func CreateTable(ctx context.Context, tx database.Tx, actorRole identity.RoleID, name string, opts TableOptions) (*Table, error) {
	if err := requireAdmin(actorRole); err != nil {
		return nil, err
	}
	sanitized := SQLSanitize(name)
	if sanitized == "" {
		return nil, ErrInvalidName
	}

	if err := lockCatalog(ctx, tx); err != nil {
		return nil, err
	}

	if existing, err := GetTable(ctx, tx, sanitized); err == nil {
		return existing, nil
	} else if !errors.Is(err, ErrTableNotFound) {
		return nil, err
	}

	minRead := opts.MinRoleRead
	if minRead == 0 {
		minRead = identity.RolePublic
	}
	minWrite := opts.MinRoleWrite
	if minWrite == 0 {
		minWrite = identity.RoleAdmin
	}

	t := &Table{Name: sanitized, MinRoleRead: minRead, MinRoleWrite: minWrite}
	err := tx.QueryRow(ctx,
		"INSERT INTO _sc_tables (name, min_role_read, min_role_write) VALUES ($1, $2, $3) RETURNING id",
		sanitized, int(minRead), int(minWrite),
	).Scan(&t.ID)
	if err != nil {
		return nil, fmt.Errorf("metadata: inserir tabela no catálogo: %w", err)
	}

	columns := idColumnDDL(tx.Dialect())
	if tx.Dialect() == database.DialectSQLite {
		columns += `, "_version" INTEGER NOT NULL DEFAULT 1`
	}
	createDDL := fmt.Sprintf("CREATE TABLE %s (%s)", pgx.Identifier{sanitized}.Sanitize(), columns)
	if err := tx.Exec(ctx, createDDL); err != nil {
		return nil, fmt.Errorf("metadata: criar tabela física %q: %w", sanitized, err)
	}

	if _, err := bumpVersion(ctx, tx); err != nil {
		return nil, fmt.Errorf("metadata: incrementar versão do catálogo: %w", err)
	}
	return t, nil
}

// AddField adiciona um campo a uma tabela existente: registra no catálogo
// (_sc_fields) e executa `ALTER TABLE ... ADD COLUMN` na mesma transação, e
// incrementa a versão do catálogo. Idempotente quando a definição é
// idêntica a um campo já existente (mesmo nome e tipo) — não falha nem
// duplica, e não incrementa a versão (nada mudou de fato). Uma definição
// com o mesmo nome mas tipo diferente retorna ErrFieldTypeMismatch, nunca
// altera o tipo silenciosamente.
func AddField(ctx context.Context, tx database.Tx, actorRole identity.RoleID, tableID int, def FieldDef) (*Field, error) {
	if err := requireAdmin(actorRole); err != nil {
		return nil, err
	}
	if !def.Type.valid() {
		return nil, ErrUnsupportedFieldType
	}
	sanitized := SQLSanitize(def.Name)
	if sanitized == "" || sanitized == "id" || sanitized == "_version" {
		return nil, ErrInvalidName
	}

	if err := lockCatalog(ctx, tx); err != nil {
		return nil, err
	}

	tableName, err := tableNameByID(ctx, tx, tableID)
	if err != nil {
		return nil, err
	}

	if existing, err := getFieldByName(ctx, tx, tableID, sanitized); err == nil {
		if existing.Type != def.Type {
			return nil, ErrFieldTypeMismatch
		}
		return existing, nil
	} else if !errors.Is(err, ErrFieldNotFound) {
		return nil, err
	}

	var refTableID sql.NullInt64
	var refTableName string
	if def.Type == FieldKey {
		if def.References == "" {
			return nil, ErrMissingReference
		}
		refSanitized := SQLSanitize(def.References)
		refTable, err := GetTable(ctx, tx, refSanitized)
		if err != nil {
			if errors.Is(err, ErrTableNotFound) {
				return nil, ErrReferencedTableNotFound
			}
			return nil, err
		}
		refTableID = sql.NullInt64{Int64: int64(refTable.ID), Valid: true}
		refTableName = refTable.Name
	}

	f := &Field{TableID: tableID, Name: sanitized, Type: def.Type, Required: def.Required, Unique: def.Unique}
	err = tx.QueryRow(ctx,
		"INSERT INTO _sc_fields (table_id, name, type, required, is_unique, references_table_id) VALUES ($1, $2, $3, $4, $5, $6) RETURNING id",
		tableID, sanitized, string(def.Type), def.Required, def.Unique, refTableID,
	).Scan(&f.ID)
	if err != nil {
		return nil, fmt.Errorf("metadata: inserir campo no catálogo: %w", err)
	}
	if refTableID.Valid {
		f.ReferencesTable = int(refTableID.Int64)
	}

	var ddl string
	columnType := def.Type.pgType()
	if tx.Dialect() == database.DialectSQLite && def.Type == FieldDate {
		columnType = "timestamp"
	}
	quotedTable := pgx.Identifier{tableName}.Sanitize()
	quotedField := pgx.Identifier{sanitized}.Sanitize()
	switch {
	case def.Type == FieldKey:
		ddl = fmt.Sprintf("ALTER TABLE %s ADD COLUMN %s %s REFERENCES %s(id)",
			quotedTable, quotedField, columnType, pgx.Identifier{refTableName}.Sanitize())
	default:
		ddl = fmt.Sprintf("ALTER TABLE %s ADD COLUMN %s %s", quotedTable, quotedField, columnType)
	}
	if def.Required {
		ddl += " NOT NULL"
	}
	if def.Unique && tx.Dialect() != database.DialectSQLite {
		ddl += " UNIQUE"
	}
	if err := tx.Exec(ctx, ddl); err != nil {
		return nil, fmt.Errorf("metadata: adicionar coluna física %q: %w", sanitized, err)
	}

	if def.Unique && tx.Dialect() == database.DialectSQLite {
		index := pgx.Identifier{fmt.Sprintf("_sc_unique_%d_%d", tableID, f.ID)}.Sanitize()
		if err := tx.Exec(ctx, fmt.Sprintf("CREATE UNIQUE INDEX %s ON %s (%s)", index, quotedTable, quotedField)); err != nil {
			return nil, err
		}
	}
	if _, err := bumpVersion(ctx, tx); err != nil {
		return nil, err
	}
	return f, nil
}

// DropField remove um campo: apaga do catálogo e executa
// `ALTER TABLE ... DROP COLUMN` na mesma transação. Idempotente: remover um
// campo que não existe é um no-op bem-sucedido (mesma convenção de
// internal/identity.RevokeAPIToken), não incrementa a versão.
func DropField(ctx context.Context, tx database.Tx, actorRole identity.RoleID, tableID int, fieldName string) error {
	if err := requireAdmin(actorRole); err != nil {
		return err
	}
	sanitized := SQLSanitize(fieldName)

	if err := lockCatalog(ctx, tx); err != nil {
		return err
	}

	field, err := getFieldByName(ctx, tx, tableID, sanitized)
	if err != nil {
		if errors.Is(err, ErrFieldNotFound) {
			return nil
		}
		return err
	}

	tableName, err := tableNameByID(ctx, tx, tableID)
	if err != nil {
		return err
	}

	if err := tx.Exec(ctx, "DELETE FROM _sc_fields WHERE id = $1", field.ID); err != nil {
		return fmt.Errorf("metadata: remover campo do catálogo: %w", err)
	}

	// SQLite (GO-030) suporta `ALTER TABLE ... DROP COLUMN` desde a versão
	// 3.35, mas SEM a cláusula `IF EXISTS` (só Postgres tem essa variante) —
	// inofensivo aqui: já confirmamos acima, via getFieldByName, que o
	// campo existe no catálogo; `IF EXISTS` no Postgres é só uma defesa
	// extra contra drift catálogo/físico, não uma condição que este
	// caminho normal dependa de fato.
	if tx.Dialect() == database.DialectSQLite && field.Unique {
		index := pgx.Identifier{fmt.Sprintf("_sc_unique_%d_%d", tableID, field.ID)}.Sanitize()
		if err := tx.Exec(ctx, "DROP INDEX IF EXISTS "+index); err != nil {
			return err
		}
	}
	dropColumnDDL := "ALTER TABLE %s DROP COLUMN IF EXISTS %s"
	if tx.Dialect() == database.DialectSQLite {
		dropColumnDDL = "ALTER TABLE %s DROP COLUMN %s"
	}
	ddl := fmt.Sprintf(dropColumnDDL,
		pgx.Identifier{tableName}.Sanitize(), pgx.Identifier{sanitized}.Sanitize())
	if err := tx.Exec(ctx, ddl); err != nil {
		return fmt.Errorf("metadata: remover coluna física %q: %w", sanitized, err)
	}

	if _, err := bumpVersion(ctx, tx); err != nil {
		return fmt.Errorf("metadata: incrementar versão do catálogo: %w", err)
	}
	return nil
}

// DropTable remove uma tabela: apaga do catálogo (campos em cascata, ver
// schema.go) e executa `DROP TABLE` na mesma transação. Idempotente: remover
// uma tabela que não existe é um no-op bem-sucedido.
func DropTable(ctx context.Context, tx database.Tx, actorRole identity.RoleID, tableID int) error {
	if err := requireAdmin(actorRole); err != nil {
		return err
	}

	if err := lockCatalog(ctx, tx); err != nil {
		return err
	}

	tableName, err := tableNameByID(ctx, tx, tableID)
	if err != nil {
		if errors.Is(err, ErrTableNotFound) {
			return nil
		}
		return err
	}

	if err := tx.Exec(ctx, "DELETE FROM _sc_tables WHERE id = $1", tableID); err != nil {
		return fmt.Errorf("metadata: remover tabela do catálogo: %w", err)
	}

	ddl := fmt.Sprintf("DROP TABLE IF EXISTS %s", pgx.Identifier{tableName}.Sanitize())
	if err := tx.Exec(ctx, ddl); err != nil {
		return fmt.Errorf("metadata: remover tabela física %q: %w", tableName, err)
	}

	if _, err := bumpVersion(ctx, tx); err != nil {
		return fmt.Errorf("metadata: incrementar versão do catálogo: %w", err)
	}
	return nil
}

func tableNameByID(ctx context.Context, tx database.Tx, tableID int) (string, error) {
	var name string
	err := tx.QueryRow(ctx, "SELECT name FROM _sc_tables WHERE id = $1", tableID).Scan(&name)
	if err != nil {
		if errors.Is(err, database.ErrNoRows) {
			return "", ErrTableNotFound
		}
		return "", err
	}
	return name, nil
}

// GetTableByID busca uma tabela do catálogo pelo ID — o par de GetTable
// (que busca por nome). Usado por GO-012 (compilador de consultas) para
// resolver a tabela referenciada por um campo do tipo FieldKey
// (Field.ReferencesTable é um ID, não um nome) ao montar um join.
func GetTableByID(ctx context.Context, tx database.Tx, id int) (*Table, error) {
	t := &Table{}
	var minRead, minWrite int
	err := tx.QueryRow(ctx,
		"SELECT id, name, min_role_read, min_role_write FROM _sc_tables WHERE id = $1",
		id,
	).Scan(&t.ID, &t.Name, &minRead, &minWrite)
	if err != nil {
		if errors.Is(err, database.ErrNoRows) {
			return nil, ErrTableNotFound
		}
		return nil, err
	}
	t.MinRoleRead = identity.RoleID(minRead)
	t.MinRoleWrite = identity.RoleID(minWrite)
	return t, nil
}
