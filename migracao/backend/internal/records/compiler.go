package records

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/vjuliani/saltcorn/migracao/backend/internal/identity"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/metadata"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/platform/database"
)

var idField = metadata.Field{Name: "id", Type: metadata.FieldInteger}

// Compile resolve Query contra o catálogo de internal/metadata (tabela,
// campos, autorização) e monta o SQL parametrizado correspondente — a
// implementação central do critério de aceite de GO-012. Retorna
// ErrUnknownTable/ErrUnknownField para qualquer identificador não
// catalogado, e ErrNotAuthorized se o ator não tiver o papel mínimo de
// leitura da tabela — nos dois casos, nenhum SQL chega a ser montado com o
// identificador inválido.
func Compile(ctx context.Context, tx pgx.Tx, actorRole identity.RoleID, q Query) (string, []any, error) {
	table, err := metadata.GetTable(ctx, database.AsTx(tx), q.Table)
	if err != nil {
		if isTableNotFound(err) {
			return "", nil, fmt.Errorf("%w: %q", ErrUnknownTable, q.Table)
		}
		return "", nil, err
	}
	if !identity.CanRead(actorRole, table.MinRoleRead) {
		return "", nil, ErrNotAuthorized
	}

	fields, err := metadata.ListFields(ctx, database.AsTx(tx), table.ID)
	if err != nil {
		return "", nil, err
	}
	fieldsByName := fieldMap(fields)

	c := &compiler{fields: fieldsByName}

	// _version (xmin::text) acompanha toda leitura desde GO-013: é o token
	// de controle de concorrência otimista que CreateRecord/UpdateRecord/
	// DeleteRecord exigem para uma escrita subsequente saber se a linha
	// mudou entre a leitura e a escrita — nunca uma coluna de schema
	// própria (evita alterar o DDL já testado de GO-011), sempre a coluna
	// de sistema que o Postgres já mantém.
	cols := []string{`t."id"`, `t.xmin::text AS "_version"`}
	names := make([]string, 0, len(fields))
	for _, f := range fields {
		names = append(names, f.Name)
	}
	sort.Strings(names) // ordem determinística das colunas no SELECT
	for _, n := range names {
		cols = append(cols, "t."+pgx.Identifier{n}.Sanitize())
	}

	var joinSQL strings.Builder
	for i, j := range q.Joins {
		jf, ok := fieldsByName[j.Field]
		if !ok {
			return "", nil, fmt.Errorf("%w: %q", ErrUnknownField, j.Field)
		}
		if jf.Type != metadata.FieldKey {
			return "", nil, fmt.Errorf("%w: campo %q não é do tipo key", ErrInvalidJoin, j.Field)
		}
		refTable, err := metadata.GetTableByID(ctx, database.AsTx(tx), jf.ReferencesTable)
		if err != nil {
			return "", nil, err
		}
		// GO-015: um join não pode ser uma porta lateral para dados de uma
		// tabela que o ator não tem papel para ler diretamente — a mesma
		// checagem da tabela principal (linha acima) se aplica a toda tabela
		// referenciada, senão um ator com acesso só à tabela pública "books"
		// poderia trazer colunas de "salaries" (admin-only) via join.
		if !identity.CanRead(actorRole, refTable.MinRoleRead) {
			return "", nil, ErrNotAuthorized
		}
		refFields, err := metadata.ListFields(ctx, database.AsTx(tx), refTable.ID)
		if err != nil {
			return "", nil, err
		}
		refFieldsByName := fieldMap(refFields)

		alias := fmt.Sprintf("j%d", i)
		for _, sel := range j.Select {
			if _, ok := refFieldsByName[sel]; !ok {
				return "", nil, fmt.Errorf("%w: %q em %q", ErrUnknownField, sel, refTable.Name)
			}
			cols = append(cols, fmt.Sprintf("%s.%s AS %s",
				alias, pgx.Identifier{sel}.Sanitize(), pgx.Identifier{j.Field + "__" + sel}.Sanitize()))
		}
		fmt.Fprintf(&joinSQL, " LEFT JOIN %s %s ON t.%s = %s.id",
			pgx.Identifier{refTable.Name}.Sanitize(), alias,
			pgx.Identifier{j.Field}.Sanitize(), alias)
	}

	for _, agg := range q.Aggregations {
		aggSQL, err := compileAggregation(ctx, tx, actorRole, agg)
		if err != nil {
			return "", nil, err
		}
		cols = append(cols, aggSQL)
	}

	var sb strings.Builder
	sb.WriteString("SELECT ")
	sb.WriteString(strings.Join(cols, ", "))
	sb.WriteString(" FROM ")
	sb.WriteString(pgx.Identifier{table.Name}.Sanitize())
	sb.WriteString(" t")
	sb.WriteString(joinSQL.String())

	if q.Where != nil {
		whereSQL, err := c.compileWhere(q.Where)
		if err != nil {
			return "", nil, err
		}
		sb.WriteString(" WHERE ")
		sb.WriteString(whereSQL)
	}

	if len(q.OrderBy) > 0 {
		terms := make([]string, len(q.OrderBy))
		for i, ot := range q.OrderBy {
			if _, ok := fieldsByName[ot.Field]; !ok {
				return "", nil, fmt.Errorf("%w: %q", ErrUnknownField, ot.Field)
			}
			dir := "ASC"
			if ot.Desc {
				dir = "DESC"
			}
			terms[i] = fmt.Sprintf("t.%s %s", pgx.Identifier{ot.Field}.Sanitize(), dir)
		}
		sb.WriteString(" ORDER BY ")
		sb.WriteString(strings.Join(terms, ", "))
	}

	if q.Limit > 0 {
		sb.WriteString(" LIMIT " + c.push(q.Limit))
	}
	if q.Offset > 0 {
		sb.WriteString(" OFFSET " + c.push(q.Offset))
	}

	return sb.String(), c.args, nil
}

func compileAggregation(ctx context.Context, tx pgx.Tx, actorRole identity.RoleID, agg Aggregation) (string, error) {
	childTable, err := metadata.GetTable(ctx, database.AsTx(tx), agg.ChildTable)
	if err != nil {
		if isTableNotFound(err) {
			return "", fmt.Errorf("%w: %q", ErrUnknownTable, agg.ChildTable)
		}
		return "", err
	}
	// GO-015: um agregado (incl. Count, que é a "contagem" do critério de
	// aceite) sobre uma tabela filha admin-only não pode vazar o total de
	// linhas nem qualquer soma/média/min/max dela para um ator sem papel de
	// leitura nessa tabela, mesmo que a tabela PAI seja de leitura pública.
	if !identity.CanRead(actorRole, childTable.MinRoleRead) {
		return "", ErrNotAuthorized
	}
	childFields, err := metadata.ListFields(ctx, database.AsTx(tx), childTable.ID)
	if err != nil {
		return "", err
	}
	childFieldsByName := fieldMap(childFields)

	fk, ok := childFieldsByName[agg.FKField]
	if !ok || fk.Type != metadata.FieldKey {
		return "", fmt.Errorf("%w: %q não é campo key em %q", ErrInvalidAggregation, agg.FKField, agg.ChildTable)
	}

	targetExpr := "*"
	if agg.Function != Count {
		if _, ok := childFieldsByName[agg.TargetField]; !ok {
			return "", fmt.Errorf("%w: %q", ErrUnknownField, agg.TargetField)
		}
		targetExpr = pgx.Identifier{agg.TargetField}.Sanitize()
	}

	quotedChild := pgx.Identifier{childTable.Name}.Sanitize()
	return fmt.Sprintf("(SELECT %s(%s) FROM %s WHERE %s.%s = t.id) AS %s",
		strings.ToUpper(string(agg.Function)), targetExpr,
		quotedChild, quotedChild, pgx.Identifier{agg.FKField}.Sanitize(),
		pgx.Identifier{agg.Alias}.Sanitize(),
	), nil
}

func fieldMap(fields []metadata.Field) map[string]metadata.Field {
	m := make(map[string]metadata.Field, len(fields)+1)
	m["id"] = idField
	for _, f := range fields {
		m[f.Name] = f
	}
	return m
}

func isTableNotFound(err error) bool {
	return errors.Is(err, metadata.ErrTableNotFound)
}

// compiler acumula os argumentos posicionais ($1, $2, ...) enquanto
// percorre uma árvore Where — um por Query.Compile, nunca reutilizado
// entre consultas.
type compiler struct {
	fields map[string]metadata.Field
	args   []any
}

func (c *compiler) push(v any) string {
	c.args = append(c.args, v)
	return fmt.Sprintf("$%d", len(c.args))
}

func (c *compiler) column(name string) (metadata.Field, string, error) {
	f, ok := c.fields[name]
	if !ok {
		return metadata.Field{}, "", fmt.Errorf("%w: %q", ErrUnknownField, name)
	}
	return f, "t." + pgx.Identifier{name}.Sanitize(), nil
}

func (c *compiler) compileWhere(w Where) (string, error) {
	switch v := w.(type) {
	case Eq:
		field, col, err := c.column(v.Field)
		if err != nil {
			return "", err
		}
		if v.Value == nil {
			return col + " IS NULL", nil
		}
		if err := validateValue(field, v.Value); err != nil {
			return "", err
		}
		return col + " = " + c.push(v.Value), nil

	case In:
		field, col, err := c.column(v.Field)
		if err != nil {
			return "", err
		}
		if len(v.Values) == 0 {
			return "FALSE", nil
		}
		placeholders := make([]string, len(v.Values))
		for i, val := range v.Values {
			if err := validateValue(field, val); err != nil {
				return "", err
			}
			placeholders[i] = c.push(val)
		}
		return col + " IN (" + strings.Join(placeholders, ", ") + ")", nil

	case NotIn:
		field, col, err := c.column(v.Field)
		if err != nil {
			return "", err
		}
		if len(v.Values) == 0 {
			return "TRUE", nil
		}
		placeholders := make([]string, len(v.Values))
		for i, val := range v.Values {
			if err := validateValue(field, val); err != nil {
				return "", err
			}
			placeholders[i] = c.push(val)
		}
		return "NOT (" + col + " IN (" + strings.Join(placeholders, ", ") + "))", nil

	case Like:
		field, col, err := c.column(v.Field)
		if err != nil {
			return "", err
		}
		if field.Type != metadata.FieldText {
			return "", fmt.Errorf("%w: Like só é válido em campo text, %q é %s", ErrTypeMismatch, v.Field, field.Type)
		}
		return col + " ILIKE '%' || " + c.push(v.Substring) + " || '%'", nil

	case Gt:
		return c.compareOp(v.Field, ">", v.Value)
	case Gte:
		return c.compareOp(v.Field, ">=", v.Value)
	case Lt:
		return c.compareOp(v.Field, "<", v.Value)
	case Lte:
		return c.compareOp(v.Field, "<=", v.Value)

	case Between:
		field, col, err := c.column(v.Field)
		if err != nil {
			return "", err
		}
		if err := validateValue(field, v.Min); err != nil {
			return "", err
		}
		if err := validateValue(field, v.Max); err != nil {
			return "", err
		}
		return fmt.Sprintf("%s >= %s AND %s <= %s", col, c.push(v.Min), col, c.push(v.Max)), nil

	case And:
		if len(v) == 0 {
			return "TRUE", nil
		}
		parts := make([]string, len(v))
		for i, cond := range v {
			s, err := c.compileWhere(cond)
			if err != nil {
				return "", err
			}
			parts[i] = "(" + s + ")"
		}
		return strings.Join(parts, " AND "), nil

	case Or:
		if len(v) == 0 {
			return "FALSE", nil
		}
		parts := make([]string, len(v))
		for i, cond := range v {
			s, err := c.compileWhere(cond)
			if err != nil {
				return "", err
			}
			parts[i] = "(" + s + ")"
		}
		return strings.Join(parts, " OR "), nil

	case Not:
		s, err := c.compileWhere(v.Cond)
		if err != nil {
			return "", err
		}
		return "NOT (" + s + ")", nil

	default:
		return "", fmt.Errorf("records: tipo Where não suportado: %T", w)
	}
}

func (c *compiler) compareOp(fieldName, op string, value any) (string, error) {
	field, col, err := c.column(fieldName)
	if err != nil {
		return "", err
	}
	if err := validateValue(field, value); err != nil {
		return "", err
	}
	return col + " " + op + " " + c.push(value), nil
}
