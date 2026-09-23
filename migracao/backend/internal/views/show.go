// Viewtemplate "Show" (GO-039) — lê `configuration.columns[]` (mesma
// fonte de verdade de render.go: a lista flat que o builder legado grava,
// não a árvore de `layout`) e exibe os valores de UM registro. Só
// colunas Field, sem ações nem join — o único formato usado pelo pack
// piloto guitars (`show_guitar`: dois campos `as_text`, nada mais).
package views

import (
	"context"
	"fmt"

	"github.com/vjuliani/saltcorn/migracao/backend/internal/identity"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/metadata"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/platform/database"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/records"
)

// ShowColumn é um campo já resolvido contra o catálogo, exibido para um
// registro específico.
type ShowColumn struct {
	FieldName   string
	HeaderLabel string
}

// ShowPlan é o DTO de renderização de uma view "Show": os valores já
// resolvidos de UM registro — o BFF/React só desenham o que está aqui,
// nunca reinterpretam `configuration`.
type ShowPlan struct {
	ViewID   int
	Table    string
	RecordID int
	Columns  []ShowColumn
	Values   map[string]any
}

func classifyShowColumns(configuration map[string]any, fieldsByName map[string]metadata.Field) ([]ShowColumn, error) {
	raw, ok := configuration["columns"].([]any)
	if !ok {
		return nil, &UnsupportedLayoutError{Reason: "configuration.columns ausente ou não é uma lista"}
	}
	if len(raw) == 0 {
		return nil, &UnsupportedLayoutError{Reason: "configuration.columns está vazio"}
	}

	columns := make([]ShowColumn, 0, len(raw))
	for i, item := range raw {
		col, ok := item.(map[string]any)
		if !ok {
			return nil, &UnsupportedLayoutError{Reason: fmt.Sprintf("coluna %d: não é um objeto", i)}
		}
		colType, _ := col["type"].(string)
		if colType != "Field" {
			return nil, &UnsupportedLayoutError{Reason: fmt.Sprintf("coluna %d: tipo de conteúdo %q não suportado em Show (só \"Field\")", i, colType)}
		}
		fieldName, _ := col["field_name"].(string)
		if fieldName == "" {
			return nil, &UnsupportedLayoutError{Reason: fmt.Sprintf("coluna %d: field_name ausente", i)}
		}
		if _, known := fieldsByName[fieldName]; !known {
			return nil, &UnsupportedLayoutError{Reason: fmt.Sprintf("coluna %d: campo %q não existe na tabela", i, fieldName)}
		}
		header, _ := col["header_label"].(string)
		if header == "" {
			header = fieldName
		}
		columns = append(columns, ShowColumn{FieldName: fieldName, HeaderLabel: header})
	}
	return columns, nil
}

// CompileShowPlan monta o ShowPlan de uma view Show compatível para o
// registro recordID — mesma dupla checagem de autorização de
// CompileListPlan (papel de leitura da VIEW, depois papel de leitura da
// TABELA, via records.Rows/Where).
func CompileShowPlanTx(ctx context.Context, tx database.Tx, actorRole identity.RoleID, viewID int, recordID int) (*ShowPlan, error) {
	v, err := GetViewTx(ctx, tx, actorRole, viewID)
	if err != nil {
		return nil, err
	}
	if v.Template != "Show" {
		return nil, &UnsupportedLayoutError{Reason: fmt.Sprintf("template %q não é \"Show\"", v.Template)}
	}
	table, err := metadata.GetTableByID(ctx, tx, v.TableID)
	if err != nil {
		return nil, err
	}
	fields, err := metadata.ListFields(ctx, tx, table.ID)
	if err != nil {
		return nil, err
	}
	columns, err := classifyShowColumns(v.Configuration, fieldsByNameMap(fields))
	if err != nil {
		return nil, err
	}

	rows, err := records.RowsTx(ctx, tx, actorRole, records.Query{
		Table: table.Name,
		Where: records.Eq{Field: "id", Value: recordID},
		Limit: 1,
	})
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, records.ErrRecordNotFound
	}

	return &ShowPlan{
		ViewID:   v.ID,
		Table:    table.Name,
		RecordID: recordID,
		Columns:  columns,
		Values:   rows[0],
	}, nil
}
