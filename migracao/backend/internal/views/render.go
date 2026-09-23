// Regras de execução de views (GO-020, estendido em GO-039) — separadas
// da persistência opaca de GO-019 (commands.go: CreateView/UpdateView
// guardam `template`/`configuration` sem interpretar nada). Este arquivo
// decide se uma view é RENDERIZÁVEL pelo runtime novo (Go + BFF + React)
// e, se for, monta o plano de dados para renderizá-la — reaproveitando o
// compilador de consultas de internal/records (GO-012/013/015), nunca
// duplicando SQL.
//
// Fonte de verdade usada nesta tarefa: `configuration.columns[]` — a
// lista FLAT e explicitamente tipada ("Field"/"JoinField"/"Action", ver
// list.ts:1009 `get_state_fields`, que lê exatamente esta lista, não
// `layout`) que o builder legado (reaproveitado desde GO-018) sempre
// grava ao lado de `layout` — confirmado por leitura de um pack real
// (`guitars`, GO-039, ver docs/migracao-go/execucoes/GO-039.md item 1 dos
// achados de preflight). `configuration.layout` é a árvore de arranjo
// visual (grid/posição/estilo) que o CraftJS/saltcorn-builder produz —
// usada pelo renderizador HTML do legado, mas NUNCA pela decisão de "que
// dado buscar" — por isso este runtime não a interpreta: o frontend novo
// desenha a partir do plano linear que este arquivo produz, não da árvore
// recursiva de layout.
package views

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/vjuliani/saltcorn/migracao/backend/internal/identity"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/metadata"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/platform/database"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/records"
)

// supportedTemplates são os únicos `template` que este runtime executa —
// "Feed" (GO-001 §2.3) fica fora, ver decisão de escopo GO-051.
var supportedTemplates = map[string]bool{"List": true, "Show": true, "Edit": true}

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

// ClassifyView decide se v é compatível com o runtime atual, despachando
// para o classificador do template correspondente. Usada pelo ciclo de
// publicação (commands.go: publicar exige compatibilidade) — só confirma
// COMPATIBILIDADE, nunca produz o plano de renderização (que exige uma
// transação real para resolver dados/registros — CompileListPlan/
// CompileShowPlan/CompileEditPlan, chamadas em separado, cada uma no
// arquivo do seu próprio template).
func ClassifyView(v View, fields []metadata.Field) error {
	fieldsByName := fieldsByNameMap(fields)
	switch v.Template {
	case "List":
		if _, err := classifyListColumns(v.Configuration, fieldsByName); err != nil {
			return err
		}
		_, err := classifyOrderField(v.Configuration, fieldsByName)
		return err
	case "Show":
		_, err := classifyShowColumns(v.Configuration, fieldsByName)
		return err
	case "Edit":
		_, err := classifyEditColumns(v.Configuration, fieldsByName)
		return err
	case "Feed":
		return classifyFeedConfig(v.Configuration)
	default:
		return &UnsupportedLayoutError{
			Reason: fmt.Sprintf("template %q não suportado neste runtime (só \"List\", \"Show\", \"Edit\", \"Feed\")", v.Template),
		}
	}
}

// classifyFeedConfig valida a FORMA de configuration de uma view Feed —
// só `show_view` presente (uma string não vazia). Diferente de
// classifyListColumns/classifyEditColumns, não valida que a view
// referenciada por `show_view` de fato existe nem que é do template
// "Show": esta função roda no momento de PUBLICAR (ClassifyView, sem
// acesso a transação/catálogo de views), só CompileFeedPlan — que roda
// com tx — pode resolver a referência cruzada. Mesma limitação, mesmo
// espírito, de uma coluna "Action" de Edit não confirmar aqui que a
// view alvo existe.
func classifyFeedConfig(configuration map[string]any) error {
	showView, _ := configuration["show_view"].(string)
	if showView == "" {
		return &UnsupportedLayoutError{Reason: "configuration.show_view ausente ou vazio"}
	}
	return nil
}

func fieldsByNameMap(fields []metadata.Field) map[string]metadata.Field {
	m := make(map[string]metadata.Field, len(fields))
	for _, f := range fields {
		m[f.Name] = f
	}
	return m
}

// ListColumnKind discrimina o que uma ListColumn representa — nunca uma
// string solta comparada em vários lugares.
type ListColumnKind string

const (
	ListColumnField     ListColumnKind = "field"
	ListColumnJoinField ListColumnKind = "join_field"
	ListColumnAction    ListColumnKind = "action"
)

// listSupportedActions é o catálogo fechado de ações de COLUNA (Kind ==
// ListColumnAction) suportadas nesta entrega — só "Delete", a única usada
// pelo pack piloto guitars (`guitar_list`/`list_processed`). Outras ações
// de coluna do legado (customizadas via trigger/plugin) ficam fora, sem
// uma linha própria de carve-out (nicho, GO-050).
var listSupportedActions = map[string]bool{"Delete": true}

// ListColumn é uma coluna já resolvida contra o catálogo — o plano nunca
// referencia um campo que não existe (Kind Field/JoinField) nem uma ação
// fora do catálogo suportado (Kind Action).
type ListColumn struct {
	Kind ListColumnKind
	// FieldName: para Kind==Field, o nome do campo; para Kind==JoinField,
	// a chave "<campo_local>__<campo_remoto>" — a MESMA convenção que
	// internal/records.Query.Joins usa para expor a coluna trazida
	// (records/query.go: `"<Field>__<coluna>"`), assim o plano nunca
	// inventa uma chave própria para um valor que o compilador de
	// consultas já calculou.
	FieldName   string
	HeaderLabel string
	// ActionName/ActionMinRole: só para Kind==Action.
	ActionName    string
	ActionMinRole identity.RoleID
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

func classifyListColumns(configuration map[string]any, fieldsByName map[string]metadata.Field) ([]ListColumn, error) {
	raw, ok := configuration["columns"].([]any)
	if !ok {
		return nil, &UnsupportedLayoutError{Reason: "configuration.columns ausente ou não é uma lista"}
	}
	if len(raw) == 0 {
		return nil, &UnsupportedLayoutError{Reason: "configuration.columns está vazio"}
	}

	columns := make([]ListColumn, 0, len(raw))
	for i, item := range raw {
		col, ok := item.(map[string]any)
		if !ok {
			return nil, &UnsupportedLayoutError{Reason: fmt.Sprintf("coluna %d: não é um objeto", i)}
		}
		colType, _ := col["type"].(string)
		header, _ := col["header_label"].(string)

		switch colType {
		case "Field":
			fieldName, _ := col["field_name"].(string)
			if fieldName == "" {
				return nil, &UnsupportedLayoutError{Reason: fmt.Sprintf("coluna %d: field_name ausente", i)}
			}
			if _, known := fieldsByName[fieldName]; !known {
				return nil, &UnsupportedLayoutError{Reason: fmt.Sprintf("coluna %d: campo %q não existe na tabela", i, fieldName)}
			}
			if header == "" {
				header = fieldName
			}
			columns = append(columns, ListColumn{Kind: ListColumnField, FieldName: fieldName, HeaderLabel: header})

		case "JoinField":
			joinField, _ := col["join_field"].(string)
			local, remote, cutOK := strings.Cut(joinField, ".")
			if !cutOK || local == "" || remote == "" {
				return nil, &UnsupportedLayoutError{Reason: fmt.Sprintf("coluna %d: join_field %q não está no formato \"campo.campo_remoto\"", i, joinField)}
			}
			localField, known := fieldsByName[local]
			if !known {
				return nil, &UnsupportedLayoutError{Reason: fmt.Sprintf("coluna %d: campo %q não existe na tabela", i, local)}
			}
			if localField.Type != metadata.FieldKey {
				return nil, &UnsupportedLayoutError{Reason: fmt.Sprintf("coluna %d: campo %q não é do tipo key — join_field exige uma referência real", i, local)}
			}
			// O nome do campo REMOTO só é confirmado na primeira
			// renderização de verdade (CompileListPlan chama
			// internal/records, que resolve a tabela referenciada e
			// rejeita um campo remoto desconhecido) — ClassifyView não
			// tem acesso ao catálogo de OUTRA tabela sem uma transação
			// real. Validação parcial, documentada: publicar aceita
			// sempre o campo LOCAL, mas um campo remoto errado só
			// aparece ao efetivamente listar.
			if header == "" {
				header = local
			}
			columns = append(columns, ListColumn{Kind: ListColumnJoinField, FieldName: local + "__" + remote, HeaderLabel: header})

		case "Action":
			actionName, _ := col["action_name"].(string)
			if !listSupportedActions[actionName] {
				return nil, &UnsupportedLayoutError{Reason: fmt.Sprintf("coluna %d: ação de coluna %q não suportada (só %s)", i, actionName, sortedKeys(listSupportedActions))}
			}
			columns = append(columns, ListColumn{
				Kind:          ListColumnAction,
				ActionName:    actionName,
				ActionMinRole: minRoleFromColumn(col),
			})

		default:
			return nil, &UnsupportedLayoutError{Reason: fmt.Sprintf("coluna %d: tipo de conteúdo %q não suportado (só \"Field\", \"JoinField\", \"Action\")", i, colType)}
		}
	}
	return columns, nil
}

// minRoleFromColumn lê `minRole` de um nó de coluna (ex.: a ação "Delete"
// de guitar_list tem `"minRole": 100`) — um número JSON decodifica sempre
// como float64 em Go (encoding/json), nunca int diretamente. Ausente ou
// de outro tipo vira identity.RoleAdmin, o padrão seguro (mesma convenção
// de ViewOptions.MinRole em views/types.go).
func minRoleFromColumn(col map[string]any) identity.RoleID {
	if raw, ok := col["minRole"].(float64); ok {
		return identity.RoleID(int(raw))
	}
	return identity.RoleAdmin
}

func sortedKeys(m map[string]bool) string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return strings.Join(keys, ", ")
}

// classifyOrderField valida (sem aplicar) o subconjunto suportado de
// `default_state` — só `_order_field`/`_descending`, mesmas chaves que
// list.ts usa em produção. Qualquer OUTRA chave presente em
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

// joinsFromColumns agrupa as colunas JoinField por campo local —
// internal/records.Query.Joins espera um Join por campo local, com
// Select agregando todos os campos remotos pedidos (guitars não usa dois
// JoinField do MESMO campo local, mas agrupar é o comportamento correto
// de qualquer forma, evitando dois LEFT JOIN redundantes na mesma tabela).
func joinsFromColumns(columns []ListColumn) []records.Join {
	var order []string
	selects := map[string][]string{}
	for _, c := range columns {
		if c.Kind != ListColumnJoinField {
			continue
		}
		local, remote, _ := strings.Cut(c.FieldName, "__")
		if _, seen := selects[local]; !seen {
			order = append(order, local)
		}
		selects[local] = append(selects[local], remote)
	}
	if len(order) == 0 {
		return nil
	}
	joins := make([]records.Join, 0, len(order))
	for _, local := range order {
		joins = append(joins, records.Join{Field: local, Select: selects[local]})
	}
	return joins
}

// CompileListPlan monta o ListPlan de uma view List compatível: resolve a
// view (GetView, checando identity.CanRead(actorRole, view.MinRole) —
// GO-019), classifica as colunas contra o catálogo atual, e executa a
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
	if v.Template != "List" {
		return nil, false, &UnsupportedLayoutError{Reason: fmt.Sprintf("template %q não é \"List\"", v.Template)}
	}
	table, err := metadata.GetTableByID(ctx, database.AsTx(tx), v.TableID)
	if err != nil {
		return nil, false, err
	}
	fields, err := metadata.ListFields(ctx, database.AsTx(tx), table.ID)
	if err != nil {
		return nil, false, err
	}
	fieldsByName := fieldsByNameMap(fields)
	columns, err := classifyListColumns(v.Configuration, fieldsByName)
	if err != nil {
		return nil, false, err
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
		Joins:   joinsFromColumns(columns),
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

// DeleteListRow executa a ação de coluna "Delete" de uma view List — só
// aceita id de view cujo layout de fato declara uma coluna Action
// "Delete" (nunca um atalho para deletar de qualquer view List, mesmo uma
// sem esse botão configurado), confere o ActionMinRole do PRÓPRIO nó de
// coluna (granularidade adicional do legado, além do MinRoleWrite da
// tabela que internal/records.DeleteRecordTx já confere por baixo), e
// delega a exclusão de verdade a records.DeleteRecordTx — mesmo controle
// de concorrência otimista (expectedVersion) de qualquer outra escrita.
func DeleteListRow(ctx context.Context, tx pgx.Tx, actorRole identity.RoleID, viewID int, recordID int, expectedVersion string) error {
	v, err := GetView(ctx, tx, actorRole, viewID)
	if err != nil {
		return err
	}
	if v.Template != "List" {
		return &UnsupportedLayoutError{Reason: fmt.Sprintf("template %q não é \"List\"", v.Template)}
	}
	table, err := metadata.GetTableByID(ctx, database.AsTx(tx), v.TableID)
	if err != nil {
		return err
	}
	fields, err := metadata.ListFields(ctx, database.AsTx(tx), table.ID)
	if err != nil {
		return err
	}
	columns, err := classifyListColumns(v.Configuration, fieldsByNameMap(fields))
	if err != nil {
		return err
	}
	var deleteCol *ListColumn
	for i := range columns {
		if columns[i].Kind == ListColumnAction && columns[i].ActionName == "Delete" {
			deleteCol = &columns[i]
			break
		}
	}
	if deleteCol == nil {
		return &UnsupportedLayoutError{Reason: "esta view não declara uma ação de coluna \"Delete\""}
	}
	if !identity.CanWrite(actorRole, deleteCol.ActionMinRole) {
		return records.ErrNotAuthorized
	}
	return records.DeleteRecord(ctx, tx, actorRole, table.Name, recordID, expectedVersion, nil)
}
