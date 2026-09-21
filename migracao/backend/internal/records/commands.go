package records

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	sqliteDriver "modernc.org/sqlite"

	"github.com/vjuliani/saltcorn/migracao/backend/internal/identity"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/metadata"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/platform/database"
)

// Códigos SQLSTATE do Postgres classificados por classifyPgError — nunca a
// mensagem crua do driver, que pode ecoar de volta um valor de linha (ex.:
// "Key (email)=(x@y.com) already exists").
const (
	sqlstateUniqueViolation     = "23505"
	sqlstateForeignKeyViolation = "23503"
	sqlstateNotNullViolation    = "23502"
)

// TxHooks executa callbacks na mesma transação do comando nos dois bancos.
// O chamador deve propagar erros para que WithTenant reverta toda a operação.
// Campos nil são no-op; Hooks em postgres.go preserva a API pgx existente.
type TxHooks struct {
	BeforeInsert func(ctx context.Context, tx database.Tx, table metadata.Table, values map[string]any) error
	AfterInsert  func(ctx context.Context, tx database.Tx, table metadata.Table, record map[string]any) error
	BeforeUpdate func(ctx context.Context, tx database.Tx, table metadata.Table, id int, values map[string]any) error
	AfterUpdate  func(ctx context.Context, tx database.Tx, table metadata.Table, record map[string]any) error
	BeforeDelete func(ctx context.Context, tx database.Tx, table metadata.Table, id int) error
	AfterDelete  func(ctx context.Context, tx database.Tx, table metadata.Table, id int) error
}

func (h *TxHooks) beforeInsert(ctx context.Context, tx database.Tx, table metadata.Table, values map[string]any) error {
	if h == nil || h.BeforeInsert == nil {
		return nil
	}
	return h.BeforeInsert(ctx, tx, table, values)
}

func (h *TxHooks) afterInsert(ctx context.Context, tx database.Tx, table metadata.Table, record map[string]any) error {
	if h == nil || h.AfterInsert == nil {
		return nil
	}
	return h.AfterInsert(ctx, tx, table, record)
}

func (h *TxHooks) beforeUpdate(ctx context.Context, tx database.Tx, table metadata.Table, id int, values map[string]any) error {
	if h == nil || h.BeforeUpdate == nil {
		return nil
	}
	return h.BeforeUpdate(ctx, tx, table, id, values)
}

func (h *TxHooks) afterUpdate(ctx context.Context, tx database.Tx, table metadata.Table, record map[string]any) error {
	if h == nil || h.AfterUpdate == nil {
		return nil
	}
	return h.AfterUpdate(ctx, tx, table, record)
}

func (h *TxHooks) beforeDelete(ctx context.Context, tx database.Tx, table metadata.Table, id int) error {
	if h == nil || h.BeforeDelete == nil {
		return nil
	}
	return h.BeforeDelete(ctx, tx, table, id)
}

func (h *TxHooks) afterDelete(ctx context.Context, tx database.Tx, table metadata.Table, id int) error {
	if h == nil || h.AfterDelete == nil {
		return nil
	}
	return h.AfterDelete(ctx, tx, table, id)
}

// resolveTableForWrite resolve tableName contra o catálogo e confere
// identity.CanWrite(actorRole, table.MinRoleWrite) — o ponto de entrada
// comum de CreateRecord/UpdateRecord/DeleteRecord.
func resolveTableForWrite(ctx context.Context, tx database.Tx, actorRole identity.RoleID, tableName string) (metadata.Table, map[string]metadata.Field, error) {
	table, err := metadata.GetTable(ctx, tx, tableName)
	if err != nil {
		if isTableNotFound(err) {
			return metadata.Table{}, nil, fmt.Errorf("%w: %q", ErrUnknownTable, tableName)
		}
		return metadata.Table{}, nil, err
	}
	if !identity.CanWrite(actorRole, table.MinRoleWrite) {
		return metadata.Table{}, nil, ErrNotAuthorized
	}
	fields, err := metadata.ListFields(ctx, tx, table.ID)
	if err != nil {
		return metadata.Table{}, nil, err
	}
	return *table, fieldMap(fields), nil
}

// validateFieldValues resolve e valida cada entrada de values contra
// fieldsByName — nunca aceita um nome não catalogado nem um valor de tipo
// incompatível, a mesma disciplina de resolução de GO-012 aplicada aos
// comandos de escrita. Coage cada valor para o tipo Go que validateValue
// exige ANTES de validar — sem isso, todo valor decodificado de um corpo
// JSON HTTP (números sempre chegam como float64, nunca int; datas sempre
// como string) falharia ErrTypeMismatch para qualquer campo que não fosse
// texto/boolean. Achado real de preflight de GO-039: nenhum teste HTTP
// anterior exercitava um campo FieldInteger/FieldKey/FieldDate via
// records.CreateRecord/UpdateRecord — só campos de texto (ex.:
// TestCreateRecordHandler_Success usa só "label"). Muta values in-place
// (o mesmo mapa que CreateRecordTx/UpdateRecordTx usam para montar
// INSERT/UPDATE logo em seguida), nunca retorna uma cópia.
func validateFieldValues(fieldsByName map[string]metadata.Field, values map[string]any) error {
	for name, val := range values {
		if name == "id" || name == "_version" {
			return fmt.Errorf("%w: %q não pode ser definido diretamente", ErrUnknownField, name)
		}
		f, ok := fieldsByName[name]
		if !ok {
			return fmt.Errorf("%w: %q", ErrUnknownField, name)
		}
		val = coerceJSONValue(f, val)
		values[name] = val
		if err := validateValue(f, val); err != nil {
			return err
		}
	}
	return nil
}

// coerceJSONValue converte um valor decodificado de JSON (encoding/json:
// todo número vira float64, toda data vira string RFC3339) para o tipo Go
// nativo que validateValue exige — nunca aceita um float64 com parte
// fracionária para um campo inteiro/key (permanece float64, e
// validateValue rejeita com ErrTypeMismatch como já fazia), nem uma
// string que não é RFC3339 válido para um campo de data (mesma coisa).
// Valores que já chegam no tipo nativo (chamador Go direto, não HTTP)
// passam inalterados.
func coerceJSONValue(field metadata.Field, value any) any {
	switch field.Type {
	case metadata.FieldInteger, metadata.FieldKey:
		if f, ok := value.(float64); ok && f == math.Trunc(f) {
			return int64(f)
		}
	case metadata.FieldDate:
		if s, ok := value.(string); ok {
			if t, err := time.Parse(time.RFC3339, s); err == nil {
				return t
			}
		}
	}
	return value
}

// CreateRecordTx insere um novo registro em tableName, validando cada campo
// de values contra o catálogo (nome conhecido, tipo compatível) e
// confirmando que todo campo obrigatório (Field.Required) foi informado.
// Roda dentro da transação tx já aberta (WithTenant do chamador) — se
// qualquer etapa falhar, inclusive um hook, a transação inteira desfaz.
func CreateRecordTx(ctx context.Context, tx database.Tx, actorRole identity.RoleID, tableName string, values map[string]any, hooks *TxHooks) (map[string]any, error) {
	table, fieldsByName, err := resolveTableForWrite(ctx, tx, actorRole, tableName)
	if err != nil {
		return nil, err
	}
	if err := validateFieldValues(fieldsByName, values); err != nil {
		return nil, err
	}
	for name, f := range fieldsByName {
		if f.Required {
			if _, ok := values[name]; !ok {
				return nil, fmt.Errorf("%w: %q", ErrRequiredField, name)
			}
		}
	}

	if err := hooks.beforeInsert(ctx, tx, table, values); err != nil {
		return nil, err
	}

	cols := make([]string, 0, len(values))
	placeholders := make([]string, 0, len(values))
	args := make([]any, 0, len(values))
	for name, val := range values {
		cols = append(cols, pgx.Identifier{name}.Sanitize())
		args = append(args, val)
		placeholders = append(placeholders, fmt.Sprintf("$%d", len(args)))
	}

	quotedTable := pgx.Identifier{table.Name}.Sanitize()
	returning := returningColumns(fieldsByName, tx.Dialect())
	var sql string
	if len(cols) == 0 {
		sql = fmt.Sprintf(`INSERT INTO %s DEFAULT VALUES RETURNING %s`, quotedTable, returning)
	} else {
		sql = fmt.Sprintf(`INSERT INTO %s (%s) VALUES (%s) RETURNING %s`,
			quotedTable, strings.Join(cols, ", "), strings.Join(placeholders, ", "), returning)
	}

	rows, err := tx.Query(ctx, sql, args...)
	if err != nil {
		return nil, classifyPgError(err)
	}
	record, err := scanOne(rows)
	if err != nil {
		return nil, classifyPgError(err)
	}

	if err := hooks.afterInsert(ctx, tx, table, record); err != nil {
		return nil, err
	}
	return record, nil
}

// UpdateRecordTx atualiza o registro id em tableName com values, exigindo
// expectedVersion (o `_version`/xmin de uma leitura anterior) para
// controle de concorrência otimista: se a linha foi modificada por outra
// transação desde a leitura, retorna ErrVersionConflict — o "erro
// definido" do critério de aceite de GO-013, nunca uma sobrescrita
// silenciosa. Se id não existir, retorna ErrRecordNotFound.
func UpdateRecordTx(ctx context.Context, tx database.Tx, actorRole identity.RoleID, tableName string, id int, expectedVersion string, values map[string]any, hooks *TxHooks) (map[string]any, error) {
	table, fieldsByName, err := resolveTableForWrite(ctx, tx, actorRole, tableName)
	if err != nil {
		return nil, err
	}
	if len(values) == 0 {
		return nil, ErrNoFields
	}
	if err := validateFieldValues(fieldsByName, values); err != nil {
		return nil, err
	}

	if err := hooks.beforeUpdate(ctx, tx, table, id, values); err != nil {
		return nil, err
	}

	setClauses := make([]string, 0, len(values))
	args := make([]any, 0, len(values)+2)
	for name, val := range values {
		args = append(args, val)
		setClauses = append(setClauses, fmt.Sprintf("%s = $%d", pgx.Identifier{name}.Sanitize(), len(args)))
	}
	args = append(args, id)
	idPos := len(args)
	args = append(args, expectedVersion)
	versionPos := len(args)

	quotedTable := pgx.Identifier{table.Name}.Sanitize()
	version := versionExpr(tx.Dialect(), "")
	if tx.Dialect() == database.DialectSQLite {
		setClauses = append(setClauses, `"_version" = "_version" + 1`)
	}
	sql := fmt.Sprintf(`UPDATE %s SET %s WHERE id = $%d AND %s = $%d RETURNING %s`,
		quotedTable, strings.Join(setClauses, ", "), idPos, version, versionPos, returningColumns(fieldsByName, tx.Dialect()))

	rows, err := tx.Query(ctx, sql, args...)
	if err != nil {
		return nil, classifyPgError(err)
	}
	record, err := scanOne(rows)
	if err != nil {
		if errors.Is(err, database.ErrNoRows) {
			return nil, conflictOrNotFound(ctx, tx, table.Name, id)
		}
		return nil, classifyPgError(err)
	}

	if err := hooks.afterUpdate(ctx, tx, table, record); err != nil {
		return nil, err
	}
	return record, nil
}

// DeleteRecordTx remove o registro id em tableName, com o mesmo controle de
// concorrência otimista de UpdateRecord.
func DeleteRecordTx(ctx context.Context, tx database.Tx, actorRole identity.RoleID, tableName string, id int, expectedVersion string, hooks *TxHooks) error {
	table, _, err := resolveTableForWrite(ctx, tx, actorRole, tableName)
	if err != nil {
		return err
	}

	if err := hooks.beforeDelete(ctx, tx, table, id); err != nil {
		return err
	}

	quotedTable := pgx.Identifier{table.Name}.Sanitize()
	version := versionExpr(tx.Dialect(), "")
	var deleted int
	err = tx.QueryRow(ctx, fmt.Sprintf(`DELETE FROM %s WHERE id = $1 AND %s = $2 RETURNING id`, quotedTable, version), id, expectedVersion).Scan(&deleted)
	if errors.Is(err, database.ErrNoRows) {
		return conflictOrNotFound(ctx, tx, table.Name, id)
	}
	if err != nil {
		return classifyPgError(err)
	}

	return hooks.afterDelete(ctx, tx, table, id)
}

// conflictOrNotFound decide, depois de uma escrita condicional (WHERE id=
// ... AND xmin::text=...) afetar zero linhas, se o motivo foi "o registro
// não existe" (ErrRecordNotFound) ou "o registro existe, mas mudou desde a
// leitura" (ErrVersionConflict) — as duas causas produzem o mesmo "zero
// linhas afetadas", então precisam de uma segunda consulta para se
// distinguir uma da outra.
func conflictOrNotFound(ctx context.Context, tx database.Tx, tableName string, id int) error {
	var exists bool
	err := tx.QueryRow(ctx,
		fmt.Sprintf(`SELECT EXISTS(SELECT 1 FROM %s WHERE id = $1)`, pgx.Identifier{tableName}.Sanitize()),
		id,
	).Scan(&exists)
	if err != nil {
		return err
	}
	if !exists {
		return ErrRecordNotFound
	}
	return ErrVersionConflict
}

func scanOne(rows database.Rows) (map[string]any, error) {
	records, err := database.CollectMaps(rows)
	if err != nil {
		return nil, err
	}
	if len(records) == 0 {
		return nil, database.ErrNoRows
	}
	return records[0], nil
}

// classifyPgError traduz um erro do driver Postgres para um dos erros
// classificados deste pacote — nunca deixa a mensagem crua do driver
// (que pode ecoar um valor de linha, ex.: violação de unicidade
// mostrando o valor duplicado) escapar como está.
func classifyPgError(err error) error {
	var sqliteErr *sqliteDriver.Error
	if errors.As(err, &sqliteErr) {
		switch sqliteErr.Code() {
		case 2067, 1555:
			return ErrDuplicateValue
		case 787:
			return ErrInvalidReference
		case 1299:
			return ErrRequiredField
		}
	}
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		return err
	}
	switch pgErr.Code {
	case sqlstateUniqueViolation:
		return fmt.Errorf("%w: %s", ErrDuplicateValue, pgErr.ConstraintName)
	case sqlstateForeignKeyViolation:
		return fmt.Errorf("%w: %s", ErrInvalidReference, pgErr.ConstraintName)
	case sqlstateNotNullViolation:
		return fmt.Errorf("%w: %s", ErrRequiredField, pgErr.ColumnName)
	default:
		return err
	}
}

// SQLite mantém uma versão explícita; PostgreSQL preserva xmin.
func versionExpr(d database.Dialect, prefix string) string {
	if d == database.DialectSQLite {
		return `CAST(` + prefix + `"_version" AS TEXT)`
	}
	return prefix + "xmin::text"
}

// Explicit columns make the SQL (and pgx prepared-statement cache key) change
// when metadata changes. RETURNING * retains an obsolete result shape after DDL.
func returningColumns(fields map[string]metadata.Field, dialect database.Dialect) string {
	names := make([]string, 0, len(fields))
	for name := range fields {
		names = append(names, name)
	}
	sort.Strings(names)
	columns := make([]string, 0, len(names)+1)
	for _, name := range names {
		columns = append(columns, pgx.Identifier{name}.Sanitize())
	}
	columns = append(columns, versionExpr(dialect, "")+` AS "_version"`)
	return strings.Join(columns, ", ")
}
