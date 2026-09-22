// Versionamento de linha (GO-045, `models/table.ts` — flag `versioned`,
// `insert_history_row`/`get_history`/`restore_row_version`). Achado real
// de leitura do legado: `Table.deleteRows` NUNCA chama
// `insert_history_row` — só `updateRow`/`insertRow` gravam histórico; uma
// linha deletada simplesmente some da tabela principal, seu último
// snapshot de histórico continua sendo o do último insert/update. Este
// port segue o comportamento REAL (nunca o texto do aceite original da
// task, que presumia "grava histórico a cada update/delete" sem ter
// confirmado contra o código do legado) — DeleteRecordTx nunca chama
// insertHistoryRow.
package records

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/vjuliani/saltcorn/migracao/backend/internal/identity"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/metadata"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/platform/database"
)

// ErrTableNotVersioned é devolvido por GetHistoryTx/RestoreRowVersionTx
// quando a tabela não tem `versioned=true` — nunca um histórico vazio
// silencioso para uma tabela que nunca foi configurada para ter um.
var ErrTableNotVersioned = fmt.Errorf("records: tabela não é versionada (versioned=false)")

func historyTableName(tableName string) string {
	return tableName + "__history"
}

// insertHistoryRow grava um snapshot COMPLETO de record (o resultado já
// escrito de CreateRecordTx/UpdateRecordTx, incluindo id) na tabela de
// histórico — `_history_version` é sempre
// `COALESCE(MAX(_history_version), 0) + 1` calculado na MESMA instrução
// SQL (subquery, mesma técnica de `next_version_by_id` do legado),
// nunca uma leitura-depois-escreve separada que uma escrita concorrente
// poderia invalidar. Só grava colunas que existem em fieldsByName — nunca
// `_version` (controle de concorrência otimista, um conceito
// completamente diferente de `_history_version`, ver history.go de
// internal/metadata).
func insertHistoryRow(ctx context.Context, tx database.Tx, table metadata.Table, fieldsByName map[string]metadata.Field, record map[string]any, restoreOfVersion *int) error {
	id, ok := record["id"]
	if !ok {
		return fmt.Errorf("records: histórico requer \"id\" no registro escrito")
	}

	cols := []string{"id", "_restore_of_version"}
	args := []any{id, restoreOfVersion}
	for name := range fieldsByName {
		// fieldMap (compiler.go) sempre injeta uma entrada sintética "id"
		// (idField) para que joins/consultas possam tratar "id" como um
		// campo qualquer — mas "id" já está em cols/args explicitamente
		// acima, incluí-la de novo aqui duplicaria a coluna no INSERT
		// ("column \"id\" specified more than once", achado real pego
		// pelo teste desta task).
		if name == "id" {
			continue
		}
		val, ok := record[name]
		if !ok {
			continue
		}
		cols = append(cols, pgx.Identifier{name}.Sanitize())
		args = append(args, val)
	}
	placeholders := make([]string, len(args))
	for i := range args {
		placeholders[i] = fmt.Sprintf("$%d", i+1)
	}

	quotedHistory := pgx.Identifier{historyTableName(table.Name)}.Sanitize()
	sql := fmt.Sprintf(
		`INSERT INTO %s (%s, _history_version) VALUES (%s, COALESCE((SELECT MAX(_history_version) FROM %s WHERE id = $1), 0) + 1)`,
		quotedHistory, strings.Join(cols, ", "), strings.Join(placeholders, ", "), quotedHistory,
	)
	return tx.Exec(ctx, sql, args...)
}

// HistoryVersion é uma linha de `<table>__history` devolvida por
// GetHistoryTx — os metadados de versionamento SEPARADOS dos dados do
// snapshot (Record), para o chamador nunca confundir `_history_version`
// (posição na linha do tempo) com `_version`/xmin (controle de
// concorrência otimista da tabela principal, que o snapshot nem carrega).
type HistoryVersion struct {
	Version          int
	Time             any // time.Time — `any` para não importar time só pela assinatura
	RestoreOfVersion *int
	Record           map[string]any
}

// GetHistoryTx lista os snapshots de id em tableName, do mais recente
// para o mais antigo — mesma autorização de leitura de qualquer consulta
// (identity.CanRead contra MinRoleRead, GO-015), nunca a de escrita:
// consultar histórico é uma operação de LEITURA. ErrTableNotVersioned se
// a tabela nunca foi marcada `versioned=true` (nunca uma lista vazia
// ambígua entre "nenhuma versão ainda" e "tabela nem tem histórico").
func GetHistoryTx(ctx context.Context, tx database.Tx, actorRole identity.RoleID, tableName string, id int) ([]HistoryVersion, error) {
	table, err := metadata.GetTable(ctx, tx, tableName)
	if err != nil {
		if isTableNotFound(err) {
			return nil, fmt.Errorf("%w: %q", ErrUnknownTable, tableName)
		}
		return nil, err
	}
	if !identity.CanRead(actorRole, table.MinRoleRead) {
		return nil, ErrNotAuthorized
	}
	if !table.Versioned {
		return nil, ErrTableNotVersioned
	}
	fields, err := metadata.ListFields(ctx, tx, table.ID)
	if err != nil {
		return nil, err
	}
	fieldsByName := fieldMap(fields)

	dataCols := make([]string, 0, len(fieldsByName)+1)
	dataCols = append(dataCols, "id")
	for name := range fieldsByName {
		if name == "id" { // fieldMap injeta "id" sintético — já incluído acima.
			continue
		}
		dataCols = append(dataCols, pgx.Identifier{name}.Sanitize())
	}

	quotedHistory := pgx.Identifier{historyTableName(table.Name)}.Sanitize()
	sql := fmt.Sprintf(
		`SELECT _history_version, _history_time, _restore_of_version, %s FROM %s WHERE id = $1 ORDER BY _history_version DESC`,
		strings.Join(dataCols, ", "), quotedHistory,
	)
	rows, err := tx.Query(ctx, sql, id)
	if err != nil {
		return nil, classifyPgError(err)
	}
	maps, err := database.CollectMaps(rows)
	if err != nil {
		return nil, classifyPgError(err)
	}

	versions := make([]HistoryVersion, 0, len(maps))
	for _, m := range maps {
		hv := HistoryVersion{Record: map[string]any{}}
		for k, v := range m {
			switch k {
			case "_history_version":
				hv.Version = toInt(v)
			case "_history_time":
				hv.Time = v
			case "_restore_of_version":
				if v != nil {
					n := toInt(v)
					hv.RestoreOfVersion = &n
				}
			default:
				hv.Record[k] = v
			}
		}
		versions = append(versions, hv)
	}
	return versions, nil
}

// RestoreRowVersionTx restaura o registro id em tableName para o estado
// gravado em historyVersion — lê o snapshot do histórico, descarta
// qualquer coluna que não pertence MAIS ao catálogo atual (um campo pode
// ter sido removido depois daquele snapshot — restaurar nunca reintroduz
// uma coluna morta), e delega a updateRecordTx (o MESMO caminho de
// escrita de qualquer UPDATE real — validação, controle de concorrência
// otimista, hooks de trigger, e agora também um NOVO snapshot de
// histórico, marcado com `_restore_of_version` apontando para a versão
// restaurada, mesmo contrato do legado `restore_row_version`). Restaurar
// nunca apaga histórico nem sobrescreve um snapshot existente — é sempre
// aditivo.
func RestoreRowVersionTx(ctx context.Context, tx database.Tx, actorRole identity.RoleID, tableName string, id int, historyVersion int, hooks *TxHooks) (map[string]any, error) {
	table, fieldsByName, err := resolveTableForWrite(ctx, tx, actorRole, tableName)
	if err != nil {
		return nil, err
	}
	if !table.Versioned {
		return nil, ErrTableNotVersioned
	}

	dataCols := make([]string, 0, len(fieldsByName))
	for name := range fieldsByName {
		if name == "id" { // fieldMap injeta "id" sintético — não pertence a values do UpdateRecordTx.
			continue
		}
		dataCols = append(dataCols, pgx.Identifier{name}.Sanitize())
	}
	quotedHistory := pgx.Identifier{historyTableName(table.Name)}.Sanitize()
	sql := fmt.Sprintf(`SELECT %s FROM %s WHERE id = $1 AND _history_version = $2`, strings.Join(dataCols, ", "), quotedHistory)
	rows, err := tx.Query(ctx, sql, id, historyVersion)
	if err != nil {
		return nil, classifyPgError(err)
	}
	snapshot, err := scanOne(rows)
	if err != nil {
		if errors.Is(err, database.ErrNoRows) {
			return nil, fmt.Errorf("%w: id=%d _history_version=%d", ErrRecordNotFound, id, historyVersion)
		}
		return nil, classifyPgError(err)
	}

	currentVersion, err := currentRowVersion(ctx, tx, table.Name, id)
	if err != nil {
		return nil, err
	}

	restoreOf := historyVersion
	return updateRecordTx(ctx, tx, actorRole, tableName, id, currentVersion, snapshot, hooks, &restoreOf)
}

// currentRowVersion lê o `_version`/xmin ATUAL do registro — RestoreRowVersionTx
// precisa disso para chamar updateRecordTx com o controle de concorrência
// otimista de qualquer UPDATE real (a restauração de um snapshot não é
// isenta da MESMA proteção contra escrita concorrente perdida).
func currentRowVersion(ctx context.Context, tx database.Tx, tableName string, id int) (string, error) {
	quotedTable := pgx.Identifier{tableName}.Sanitize()
	version := versionExpr(tx.Dialect(), "")
	sql := fmt.Sprintf(`SELECT %s FROM %s WHERE id = $1`, version, quotedTable)
	var v string
	err := tx.QueryRow(ctx, sql, id).Scan(&v)
	if err != nil {
		if errors.Is(err, database.ErrNoRows) {
			return "", fmt.Errorf("%w: id=%d", ErrRecordNotFound, id)
		}
		return "", classifyPgError(err)
	}
	return v, nil
}

func toInt(v any) int {
	switch n := v.(type) {
	case int:
		return n
	case int32:
		return int(n)
	case int64:
		return int(n)
	default:
		return 0
	}
}
