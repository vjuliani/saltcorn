// Regras de execução de views (GO-020) — separadas da persistência opaca
// de GO-019 (commands.go: CreateView/UpdateView guardam `template`/
// `configuration` sem interpretar nada). Este arquivo é o primeiro
// consumidor real desses dois campos: decide se uma view é RENDERIZÁVEL
// pelo runtime novo (Go + BFF + React) e, se for, monta o plano de dados
// para renderizá-la — reaproveitando o compilador de consultas de
// internal/records (GO-012/013/015), nunca duplicando SQL.
//
// Subconjunto suportado nesta tarefa (documentado em detalhe em
// docs/migracao-go/execucoes/GO-020.md): só o viewtemplate "List", com
// colunas do tipo `Field` direto (`configuration.layout.besides[].contents
// = {type: "Field", field_name: "..."}` — o MESMO shape que
// packages/saltcorn-data/base-plugin/viewtemplates/list.ts:1009-1055 já usa
// em produção, não um formato inventado) e um subconjunto fechado de
// `default_state` (`_order_field`, `_descending`). Qualquer outro
// viewtemplate, tipo de coluna (join/agregação/ação/link/view embutida) ou
// opção de `default_state` fora desse subconjunto é classificado como
// incompatível — nunca uma tentativa de renderizar parcialmente.
package views

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/vjuliani/saltcorn/migracao/backend/internal/identity"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/metadata"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/records"
)

// supportedTemplate é o único `template` que este runtime executa. Um
// nome diferente (ex.: "Show", "Edit", "Feed" — viewtemplates nativos do
// legado, matriz GO-001 §2.3) é incompatível por definição: nenhum deles
// tem uma regra de execução portada ainda.
const supportedTemplate = "List"

// supportedColumnContentType é o único tipo de conteúdo de coluna
// suportado — corresponde a `column.type === "Field"` do viewtemplate List
// legado (list.ts:1009,1114,1723). JoinField/ViewLink/Link/Action/
// Aggregation/Text/DropdownMenu (ver mesmo arquivo, `typeMap`) ficam fora.
const supportedColumnContentType = "Field"

// UnsupportedLayoutError é "a view existe e é legível, mas usa um recurso
// fora do subconjunto que este runtime executa" — sempre carrega o motivo
// específico (Reason), nunca uma mensagem genérica, porque tanto o
// bloqueio de publicação quanto a mensagem de UI precisam dizer O QUE
// exatamente não é suportado.
type UnsupportedLayoutError struct {
	Reason string
}

func (e *UnsupportedLayoutError) Error() string {
	return fmt.Sprintf("views: layout incompatível com o runtime atual: %s", e.Reason)
}

// ListColumn é uma coluna já resolvida contra o catálogo (metadata.Field
// existe de verdade) — o plano nunca referencia um campo que não existe.
type ListColumn struct {
	FieldName   string
	HeaderLabel string
}

// ListPlan é o DTO de renderização de uma view "List": todas as decisões
// (quais colunas, que ordenação, que consulta rodar) já foram tomadas
// aqui — o BFF/React só desenham o que está aqui dentro, não reinterpretam
// `configuration`.
type ListPlan struct {
	ViewID     int
	Table      string
	Columns    []ListColumn
	Rows       []map[string]any
	OrderBy    string
	Descending bool
}

// ClassifyView decide se v é renderizável por este runtime, dado o
// catálogo atual de campos de sua tabela. Retorna as colunas resolvidas
// quando compatível, ou um *UnsupportedLayoutError quando não.
func ClassifyView(v View, fields []metadata.Field) ([]ListColumn, error) {
	if v.Template != supportedTemplate {
		return nil, &UnsupportedLayoutError{
			Reason: fmt.Sprintf("template %q não suportado neste runtime (só %q)", v.Template, supportedTemplate),
		}
	}
	fieldsByName := make(map[string]metadata.Field, len(fields))
	for _, f := range fields {
		fieldsByName[f.Name] = f
	}
	columns, err := classifyListColumns(v.Configuration, fieldsByName)
	if err != nil {
		return nil, err
	}
	if _, err := classifyOrderField(v.Configuration, fieldsByName); err != nil {
		return nil, err
	}
	return columns, nil
}

func classifyListColumns(configuration map[string]any, fieldsByName map[string]metadata.Field) ([]ListColumn, error) {
	layout, ok := configuration["layout"].(map[string]any)
	if !ok {
		return nil, &UnsupportedLayoutError{Reason: "configuration.layout ausente ou não é um objeto"}
	}
	besides, ok := layout["besides"].([]any)
	if !ok {
		return nil, &UnsupportedLayoutError{Reason: "layout.besides ausente ou não é uma lista — só layouts de lista de colunas são suportados"}
	}
	if len(besides) == 0 {
		return nil, &UnsupportedLayoutError{Reason: "layout.besides está vazio"}
	}

	columns := make([]ListColumn, 0, len(besides))
	for i, raw := range besides {
		item, ok := raw.(map[string]any)
		if !ok {
			return nil, &UnsupportedLayoutError{Reason: fmt.Sprintf("coluna %d: não é um objeto", i)}
		}
		contents, ok := item["contents"].(map[string]any)
		if !ok {
			return nil, &UnsupportedLayoutError{Reason: fmt.Sprintf("coluna %d: contents ausente ou não é um objeto", i)}
		}
		colType, _ := contents["type"].(string)
		if colType != supportedColumnContentType {
			return nil, &UnsupportedLayoutError{Reason: fmt.Sprintf("coluna %d: tipo de conteúdo %q não suportado (só %q)", i, colType, supportedColumnContentType)}
		}
		fieldName, _ := contents["field_name"].(string)
		if fieldName == "" {
			return nil, &UnsupportedLayoutError{Reason: fmt.Sprintf("coluna %d: field_name ausente", i)}
		}
		if _, known := fieldsByName[fieldName]; !known {
			return nil, &UnsupportedLayoutError{Reason: fmt.Sprintf("coluna %d: campo %q não existe na tabela", i, fieldName)}
		}
		header, _ := item["header_label"].(string)
		if header == "" {
			header = fieldName
		}
		columns = append(columns, ListColumn{FieldName: fieldName, HeaderLabel: header})
	}
	return columns, nil
}

// classifyOrderField valida (sem aplicar) o subconjunto suportado de
// `default_state` — só `_order_field`/`_descending`, mesmas chaves que
// list.ts usa em produção (linhas 656-990 do configuration_workflow,
// achado do preflight desta tarefa). Qualquer OUTRA chave presente em
// `default_state` é incompatível: são dezenas de opções de exibição
// (agrupamento, filtros de cabeçalho, transposição, cor de linha por
// fórmula etc.) que este runtime não interpreta — aceitar a view e
// ignorá-las silenciosamente seria uma renderização incompleta disfarçada
// de compatível, exatamente o que o critério de aceite proíbe.
func classifyOrderField(configuration map[string]any, fieldsByName map[string]metadata.Field) (string, error) {
	defaultState, ok := configuration["default_state"].(map[string]any)
	if !ok || defaultState == nil {
		return "id", nil
	}
	for key := range defaultState {
		if key != "_order_field" && key != "_descending" {
			return "", &UnsupportedLayoutError{Reason: fmt.Sprintf("default_state usa a opção %q, fora do subconjunto suportado (_order_field, _descending)", key)}
		}
	}
	orderField, _ := defaultState["_order_field"].(string)
	if orderField == "" {
		return "id", nil
	}
	if orderField != "id" {
		if _, known := fieldsByName[orderField]; !known {
			return "", &UnsupportedLayoutError{Reason: fmt.Sprintf("default_state._order_field %q não existe na tabela", orderField)}
		}
	}
	return orderField, nil
}

func descendingFromState(configuration map[string]any) bool {
	defaultState, ok := configuration["default_state"].(map[string]any)
	if !ok {
		return false
	}
	desc, _ := defaultState["_descending"].(bool)
	return desc
}

// CompileListPlan monta o ListPlan de uma view List compatível: resolve a
// view (GetView, checando identity.CanRead(actorRole, view.MinRole) —
// GO-019), classifica o layout contra o catálogo atual, e executa a
// consulta via internal/records.Rows (GO-012/013), que por sua vez checa
// identity.CanRead(actorRole, table.MinRoleRead) — GO-015. As duas
// checagens são independentes: publicar uma view não contorna o papel
// mínimo de leitura da própria tabela por baixo.
//
// limit/offset seguem a mesma convenção de cmd/server/records.go
// (listRecordsHandler): o chamador pede limit+1 linhas e usa o
// "hasMore" retornado para decidir se há próxima página, sem uma consulta
// de contagem separada.
func CompileListPlan(ctx context.Context, tx pgx.Tx, actorRole identity.RoleID, viewID int, limit, offset int) (*ListPlan, bool, error) {
	v, err := GetView(ctx, tx, actorRole, viewID)
	if err != nil {
		return nil, false, err
	}
	table, err := metadata.GetTableByID(ctx, tx, v.TableID)
	if err != nil {
		return nil, false, err
	}
	fields, err := metadata.ListFields(ctx, tx, table.ID)
	if err != nil {
		return nil, false, err
	}
	columns, err := ClassifyView(v, fields)
	if err != nil {
		return nil, false, err
	}
	fieldsByName := make(map[string]metadata.Field, len(fields))
	for _, f := range fields {
		fieldsByName[f.Name] = f
	}
	orderField, err := classifyOrderField(v.Configuration, fieldsByName)
	if err != nil {
		return nil, false, err
	}
	descending := descendingFromState(v.Configuration)

	rows, err := records.Rows(ctx, tx, actorRole, records.Query{
		Table:   table.Name,
		OrderBy: []records.OrderTerm{{Field: orderField, Desc: descending}},
		Limit:   limit + 1,
		Offset:  offset,
	})
	if err != nil {
		return nil, false, err
	}
	hasMore := len(rows) > limit
	if hasMore {
		rows = rows[:limit]
	}

	return &ListPlan{
		ViewID:     v.ID,
		Table:      table.Name,
		Columns:    columns,
		Rows:       rows,
		OrderBy:    orderField,
		Descending: descending,
	}, hasMore, nil
}
