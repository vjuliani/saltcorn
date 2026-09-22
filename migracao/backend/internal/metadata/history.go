// Versionamento de linha (GO-045, `models/table.ts` — flag `versioned`,
// `create_history_table`/`__history`) — só o DDL da tabela física de
// histórico vive aqui (catálogo/schema, mesmo espírito deste pacote); a
// LÓGICA de gravar/ler/restaurar um snapshot vive em internal/records
// (que já é o dono de todo o caminho de escrita de registro, GO-013) —
// internal/metadata nunca depende de internal/records (a dependência é
// sempre records → metadata, nunca o contrário).
package metadata

import (
	"context"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/vjuliani/saltcorn/migracao/backend/internal/platform/database"
)

// historyTableName é `<tableName>__history` — mesma convenção de nome do
// legado.
func historyTableName(tableName string) string {
	return tableName + "__history"
}

// createHistoryTable cria a tabela de histórico de tableName com uma
// coluna por campo já catalogado (fields) — chamada tanto por CreateTable
// (Versioned=true na criação) quanto por SetTableVersioned (habilitado
// depois de já existirem campos). PK composta (id, _history_version):
// cada linha é um snapshot COMPLETO do registro naquele instante, nunca
// um diff — mesma estratégia do legado
// (`_version integer, ..., PRIMARY KEY(pk, _version)`), com um nome de
// coluna deliberadamente DIFERENTE (`_history_version`) para nunca
// colidir com o `_version`/xmin de controle de concorrência otimista
// (GO-013) que toda tabela dinâmica já tem — são dois conceitos de
// versão distintos (um é "que geração de escrita concorrente", o outro é
// "qual snapshot histórico"). Sem NOT NULL/UNIQUE nas colunas de dados
// (ao contrário da tabela principal): um campo adicionado DEPOIS que a
// tabela já tinha histórico deixa as linhas antigas com essa coluna NULL,
// nunca inválidas.
func createHistoryTable(ctx context.Context, tx database.Tx, tableName string, fields []Field) error {
	quoted := pgx.Identifier{historyTableName(tableName)}.Sanitize()
	cols := []string{
		"id int NOT NULL",
		"_history_version int NOT NULL",
		fmt.Sprintf("_history_time %s", timestampDefaultDDL(tx.Dialect())),
		"_restore_of_version int",
	}
	for _, f := range fields {
		columnType := f.Type.pgType()
		if tx.Dialect() == database.DialectSQLite && f.Type == FieldDate {
			columnType = "timestamp"
		}
		cols = append(cols, fmt.Sprintf("%s %s", pgx.Identifier{f.Name}.Sanitize(), columnType))
	}
	ddl := fmt.Sprintf(`CREATE TABLE %s (%s, PRIMARY KEY (id, _history_version))`, quoted, strings.Join(cols, ", "))
	return tx.Exec(ctx, ddl)
}

// addHistoryColumn replica um ALTER TABLE ADD COLUMN de AddField na
// tabela de histórico, quando a tabela de dados é Versioned — sem essa
// réplica, o histórico ficaria estruturalmente desalinhado com a tabela
// atual (um campo novo nunca apareceria em nenhum snapshot, mesmo dos
// registros criados/atualizados DEPOIS do campo existir). Nunca NOT
// NULL/UNIQUE, mesmo raciocínio de createHistoryTable.
func addHistoryColumn(ctx context.Context, tx database.Tx, tableName string, f Field) error {
	quoted := pgx.Identifier{historyTableName(tableName)}.Sanitize()
	columnType := f.Type.pgType()
	if tx.Dialect() == database.DialectSQLite && f.Type == FieldDate {
		columnType = "timestamp"
	}
	ddl := fmt.Sprintf("ALTER TABLE %s ADD COLUMN %s %s", quoted, pgx.Identifier{f.Name}.Sanitize(), columnType)
	return tx.Exec(ctx, ddl)
}

// dropHistoryTable remove a tabela de histórico — chamada por DropTable
// (a tabela de dados inteira some, o histórico não deveria sobreviver
// órfão) e por SetTableVersioned(versioned=false) (mesmo comportamento
// do legado: desabilitar versionamento DESCARTA o histórico existente,
// não o preserva desligado — ver decisão de escopo em
// docs/migracao-go/execucoes/GO-045.md). Idempotente (IF EXISTS).
func dropHistoryTable(ctx context.Context, tx database.Tx, tableName string) error {
	quoted := pgx.Identifier{historyTableName(tableName)}.Sanitize()
	return tx.Exec(ctx, fmt.Sprintf("DROP TABLE IF EXISTS %s", quoted))
}
