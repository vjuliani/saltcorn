// Viewtemplate "Edit" (GO-039) — o formulário de escrita real do pack
// piloto guitars: lê `configuration.columns[]` (mesma fonte de verdade de
// render.go/show.go) para decidir quais campos são editáveis e qual ação
// de submissão existe, produz o plano de leitura (registro existente ou
// em branco, para criar), e executa a escrita (`form_action`, o
// mecanismo real por trás do botão "Salvar" do legado — `tryInsertRow`/
// `tryUpdateRow` em base-plugin/actions.ts) mais o redirecionamento
// pós-ação (`navigate`).
//
// GO-051 completa duas lacunas que GO-039 deixou deliberadamente de fora
// (ver docs/migracao-go/execucoes/GO-039.md, decisões de escopo 2-4):
// fieldview "upload" (agora que internal/metadata tem FieldFile, GO-051)
// e o nó de layout `type: "view"` (view aninhada, ex.: create_guitar
// embute edit_processed_embed via `.guitars.processed$guitar`) — a
// ÚNICA representação desse nó é `configuration.layout`, nunca
// `columns[]` (confirmado lendo o pack.json real do piloto guitars:
// create_guitar's columns só lista description/name/Save), por isso a
// resolução de view aninhada é a ÚNICA leitura de `layout` para fins de
// DADOS neste pacote — uma exceção deliberada à regra geral de GO-039
// ("layout é só a árvore de arranjo visual, nunca a fonte de dados"),
// justificada porque não existe outra representação possível para essa
// capacidade específica.
package views

import (
	"context"
	"fmt"
	"strings"

	"github.com/vjuliani/saltcorn/migracao/backend/internal/identity"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/metadata"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/platform/database"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/records"
)

// editSupportedActions é o catálogo fechado de ações de submissão
// suportadas — "Save" e "SubmitWithAjax" mapeiam para o MESMO mecanismo
// backend (só o estilo de requisição do FRONTEND difere entre as duas no
// legado; o contrato HTTP e a validação do lado Go são idênticos).
var editSupportedActions = map[string]bool{"Save": true, "SubmitWithAjax": true}

// unsupportedEditFieldviews são fieldviews cujo campo exige uma
// capacidade que este runtime ainda não tem. "upload" foi portada em
// GO-051 (metadata.FieldFile + internal/files) — mantido vazio, não
// removido, para o padrão de erro explícito continuar disponível se uma
// fieldview futura precisar dele.
var unsupportedEditFieldviews = map[string]string{}

// EditFieldOption é uma opção de um campo FieldKey (fieldview "select") —
// um registro candidato da tabela referenciada, com um rótulo legível.
// Simplificação deliberada: o rótulo é o primeiro campo de texto da
// tabela referenciada (ou o próprio id, se não houver nenhum) — o legado
// permite configurar um "summary field" por relação; esta entrega não
// reproduz essa configuração, só um heurístico honesto e documentado.
type EditFieldOption struct {
	ID    int
	Label string
}

// EditField é um campo editável já resolvido contra o catálogo.
type EditField struct {
	FieldName string
	Label     string
	FieldType metadata.FieldType
	Fieldview string
	Required  bool
	// FieldConfig é a `configuration` específica da fieldview (ex.:
	// `{"dateFormat": "Y-m-d H:i"}` de flatpickr) — repassada sem
	// interpretação, mesmo espírito de "Go decide O QUÊ, frontend decide
	// COMO".
	FieldConfig map[string]any
	// Value é o valor atual (registro existente) ou nil (criação nova).
	Value any
	// Options só é preenchido quando FieldType == metadata.FieldKey e
	// Fieldview == "select".
	Options []EditFieldOption
}

// EditPlan é o DTO de renderização/edição de uma view "Edit": os campos
// editáveis (com valor atual e opções, quando aplicável) e a ação de
// submissão disponível — o BFF/React só desenham o formulário a partir
// daqui, nunca reinterpretam `configuration`.
type EditPlan struct {
	ViewID     int
	Table      string
	RecordID   int // 0 = registro novo (criação)
	Version    string
	Fields     []EditField
	ActionName string // "Save" ou "SubmitWithAjax"
	// Nested (GO-051) são as views Edit embutidas via nó de layout
	// `type: "view"` — vazio quando RecordID == 0 (ver
	// resolveNestedEditPlans) ou quando a view não embute nenhuma outra.
	Nested []NestedEditPlan
}

// NavigateDecision é o resultado do mecanismo `navigate` do legado —
// para onde ir depois de uma submissão bem-sucedida. Type é sempre um de
// "reload" (padrão — recarrega a própria view, destination_type ausente),
// "referer" (`destination_type: "Back to referer"`) ou "view"
// (`destination_type: "View"`, com ViewName de `view_when_done`).
type NavigateDecision struct {
	Type     string
	ViewName string
}

func resolveNavigate(configuration map[string]any) NavigateDecision {
	destType, _ := configuration["destination_type"].(string)
	switch destType {
	case "Back to referer":
		return NavigateDecision{Type: "referer"}
	case "View":
		viewName, _ := configuration["view_when_done"].(string)
		return NavigateDecision{Type: "view", ViewName: viewName}
	default:
		return NavigateDecision{Type: "reload"}
	}
}

// EditSubmitResult é o resultado de SubmitEditView.
type EditSubmitResult struct {
	Record   map[string]any
	Navigate NavigateDecision
}

// classifyEditColumns valida `configuration.columns[]` para o template
// Edit — só "Field" (rejeitando fieldviews não suportadas) e "Action" (só
// do catálogo editSupportedActions) — e exige pelo menos um campo e
// exatamente uma ação de submissão (sem isso, o formulário não tem como
// salvar). Retorna os nomes dos campos editáveis, na ordem declarada.
func classifyEditColumns(configuration map[string]any, fieldsByName map[string]metadata.Field) ([]string, error) {
	raw, ok := configuration["columns"].([]any)
	if !ok {
		return nil, &UnsupportedLayoutError{Reason: "configuration.columns ausente ou não é uma lista"}
	}
	if len(raw) == 0 {
		return nil, &UnsupportedLayoutError{Reason: "configuration.columns está vazio"}
	}

	var fieldNames []string
	actionCount := 0
	for i, item := range raw {
		col, ok := item.(map[string]any)
		if !ok {
			return nil, &UnsupportedLayoutError{Reason: fmt.Sprintf("coluna %d: não é um objeto", i)}
		}
		colType, _ := col["type"].(string)
		switch colType {
		case "Field":
			fieldName, _ := col["field_name"].(string)
			if fieldName == "" {
				return nil, &UnsupportedLayoutError{Reason: fmt.Sprintf("coluna %d: field_name ausente", i)}
			}
			if _, known := fieldsByName[fieldName]; !known {
				return nil, &UnsupportedLayoutError{Reason: fmt.Sprintf("coluna %d: campo %q não existe na tabela", i, fieldName)}
			}
			fieldview, _ := col["fieldview"].(string)
			if reason, unsupported := unsupportedEditFieldviews[fieldview]; unsupported {
				return nil, &UnsupportedLayoutError{Reason: fmt.Sprintf("coluna %d: fieldview %q %s", i, fieldview, reason)}
			}
			fieldNames = append(fieldNames, fieldName)
		case "Action":
			actionName, _ := col["action_name"].(string)
			if !editSupportedActions[actionName] {
				return nil, &UnsupportedLayoutError{Reason: fmt.Sprintf("coluna %d: ação %q não suportada em Edit (só %s)", i, actionName, sortedKeys(editSupportedActions))}
			}
			actionCount++
		default:
			return nil, &UnsupportedLayoutError{Reason: fmt.Sprintf("coluna %d: tipo de conteúdo %q não suportado em Edit (só \"Field\", \"Action\")", i, colType)}
		}
	}
	if len(fieldNames) == 0 {
		return nil, &UnsupportedLayoutError{Reason: "nenhum campo editável (\"Field\") em configuration.columns"}
	}
	if actionCount == 0 {
		return nil, &UnsupportedLayoutError{Reason: "nenhuma ação de submissão (\"Save\"/\"SubmitWithAjax\") em configuration.columns"}
	}
	if actionCount > 1 {
		return nil, &UnsupportedLayoutError{Reason: "mais de uma ação de submissão em configuration.columns — não suportado nesta entrega"}
	}
	return fieldNames, nil
}

// actionNameFromColumns devolve o nome da única ação de submissão já
// validada por classifyEditColumns — chamado depois, nunca recalcula a
// validação.
func actionNameFromColumns(raw []any) string {
	for _, item := range raw {
		col, ok := item.(map[string]any)
		if !ok {
			continue
		}
		if t, _ := col["type"].(string); t == "Action" {
			name, _ := col["action_name"].(string)
			return name
		}
	}
	return ""
}

// labelFieldFor escolhe o campo de rótulo de uma tabela referenciada para
// EditFieldOption — ver comentário do tipo.
func labelFieldFor(fields []metadata.Field) string {
	for _, f := range fields {
		if f.Type == metadata.FieldText {
			return f.Name
		}
	}
	return ""
}

// idAsInt normaliza o valor da coluna "id" devolvido por
// internal/records.Rows — o driver Postgres (pgx) devolve int32 para
// `integer`/serial, mas este helper aceita int/int64 também (defensivo,
// nunca assume um único tipo concreto).
func idAsInt(v any) int {
	switch n := v.(type) {
	case int32:
		return int(n)
	case int64:
		return int(n)
	case int:
		return n
	default:
		return 0
	}
}

// editFieldOptionsLimit é o teto de opções carregadas por campo select —
// suficiente para o pack piloto guitars, documentado como limite de
// escala desta entrega (sem paginação/busca no dropdown).
const editFieldOptionsLimit = 200

func loadEditFieldOptions(ctx context.Context, tx database.Tx, actorRole identity.RoleID, refTable metadata.Table) ([]EditFieldOption, error) {
	refFields, err := metadata.ListFields(ctx, tx, refTable.ID)
	if err != nil {
		return nil, err
	}
	labelField := labelFieldFor(refFields)
	rows, err := records.RowsTx(ctx, tx, actorRole, records.Query{
		Table:   refTable.Name,
		OrderBy: []records.OrderTerm{{Field: "id"}},
		Limit:   editFieldOptionsLimit,
	})
	if err != nil {
		return nil, err
	}
	options := make([]EditFieldOption, 0, len(rows))
	for _, row := range rows {
		id := idAsInt(row["id"])
		label := fmt.Sprint(id)
		if labelField != "" {
			if v, ok := row[labelField]; ok && v != nil {
				label = fmt.Sprint(v)
			}
		}
		options = append(options, EditFieldOption{ID: id, Label: label})
	}
	return options, nil
}

// buildEditFields monta os EditField de uma view Edit já classificada,
// para um registro já lido (existing == nil para criação) — extraído de
// CompileEditPlan para ser reaproveitado também na construção do
// EditPlan de cada linha FILHA de uma view aninhada (mesma lógica,
// tabela/view diferentes).
func buildEditFields(ctx context.Context, tx database.Tx, actorRole identity.RoleID, raw []any, fieldsByName map[string]metadata.Field, existing map[string]any) ([]EditField, error) {
	editFields := make([]EditField, 0, len(raw))
	for _, item := range raw {
		col, ok := item.(map[string]any)
		if !ok {
			continue
		}
		if t, _ := col["type"].(string); t != "Field" {
			continue
		}
		fieldName, _ := col["field_name"].(string)
		f := fieldsByName[fieldName]
		fieldview, _ := col["fieldview"].(string)
		fieldConfig, _ := col["configuration"].(map[string]any)

		ef := EditField{
			FieldName:   fieldName,
			Label:       fieldName,
			FieldType:   f.Type,
			Fieldview:   fieldview,
			Required:    f.Required,
			FieldConfig: fieldConfig,
		}
		if existing != nil {
			ef.Value = existing[fieldName]
		}
		if f.Type == metadata.FieldKey && fieldview == "select" {
			refTable, err := metadata.GetTableByID(ctx, tx, f.ReferencesTable)
			if err != nil {
				return nil, err
			}
			options, err := loadEditFieldOptions(ctx, tx, actorRole, *refTable)
			if err != nil {
				return nil, err
			}
			ef.Options = options
		}
		editFields = append(editFields, ef)
	}
	return editFields, nil
}

// NestedEditPlan (GO-051) é uma view Edit embutida dentro de OUTRA view
// Edit via um nó de layout `type: "view"` com `relation` (ex.:
// create_guitar embute edit_processed_embed via
// `.guitars.processed$guitar`) — uma linha por registro FILHO já
// existente (relação 1:N filtrada pelo id do registro PAI). ViewID/
// FKField/ParentID são expostos para o frontend montar a submissão de
// uma linha filha NOVA: `POST .../views/{ViewID}/submit` com
// `values[FKField] = ParentID` mais os campos próprios da view filha —
// o mesmo endpoint `submitView` que qualquer view Edit standalone já
// usa, nenhuma rota nova para "criar uma linha filha".
type NestedEditPlan struct {
	ViewID     int
	ViewName   string
	ChildTable string
	FKField    string
	ParentID   int
	Rows       []EditPlan
}

// parseRelation decodifica a sintaxe de relação do legado
// `.{tabelaPai}.{tabelaFilha}${campoFK}` (ex.: `.guitars.processed$guitar`
// — "para cada linha de guitars, as linhas de processed cujo campo
// guitar aponta para ela"). Formato confirmado lendo o pack.json real do
// piloto guitars (GO-051, preflight) — nenhuma outra variante de sintaxe
// de relação (ex.: many-to-many) aparece nesse pack, então só esta é
// suportada; qualquer outra devolve ok=false, tratado como
// UnsupportedLayoutError pelo chamador.
func parseRelation(relation string) (parentTable, childTable, fkField string, ok bool) {
	if !strings.HasPrefix(relation, ".") {
		return "", "", "", false
	}
	rest := strings.TrimPrefix(relation, ".")
	parts := strings.SplitN(rest, ".", 2)
	if len(parts) != 2 {
		return "", "", "", false
	}
	parentTable = parts[0]
	dollarIdx := strings.Index(parts[1], "$")
	if dollarIdx < 0 {
		return "", "", "", false
	}
	childTable = parts[1][:dollarIdx]
	fkField = parts[1][dollarIdx+1:]
	if parentTable == "" || childTable == "" || fkField == "" {
		return "", "", "", false
	}
	return parentTable, childTable, fkField, true
}

// findViewNodes percorre `configuration.layout` recursivamente
// procurando nós `{"type": "view", "view": ..., "relation": ...}` — a
// ÚNICA representação de uma view aninhada (nunca aparece em
// `columns[]`, ver comentário do pacote). Percorre o formato genérico
// que `encoding/json` produz (map[string]any / []any), sem assumir uma
// posição fixa na árvore (o nó pode estar em `above`, `besides` de
// qualquer profundidade) — mais simples e mais robusto do que replicar a
// forma exata do layout do builder.
func findViewNodes(node any) []map[string]any {
	var out []map[string]any
	switch v := node.(type) {
	case map[string]any:
		if t, _ := v["type"].(string); t == "view" {
			out = append(out, v)
		}
		for _, val := range v {
			out = append(out, findViewNodes(val)...)
		}
	case []any:
		for _, item := range v {
			out = append(out, findViewNodes(item)...)
		}
	}
	return out
}

// maxNestedViewDepth limita a recursão de views aninhadas a UM nível —
// uma view aninhada não pode, por sua vez, embutir outra view (orçamento
// de recursão que o preflight de GO-039 já identificou como necessário
// antes de implementar a capacidade; nenhuma view do pack piloto guitars
// aninha mais de um nível, então este limite não corta nenhum uso real
// conhecido). Excedê-lo é um erro explícito, nunca uma recursão
// silenciosa.
const maxNestedViewDepth = 1

// resolveNestedEditPlans resolve cada nó `type: "view"` encontrado em
// `configuration.layout` para um NestedEditPlan — só quando parentID != 0
// (um registro-pai que ainda não existe não tem linhas filhas possíveis;
// documentado como limitação, não uma falha: o formulário embutido
// aparece a partir do primeiro salvamento do pai, nunca antes).
func resolveNestedEditPlans(ctx context.Context, tx database.Tx, actorRole identity.RoleID, parentTableName string, parentID int, layout map[string]any, depth int) ([]NestedEditPlan, error) {
	if parentID == 0 {
		return nil, nil
	}
	nodes := findViewNodes(layout)
	if len(nodes) == 0 {
		return nil, nil
	}
	if depth >= maxNestedViewDepth {
		return nil, &UnsupportedLayoutError{Reason: "view aninhada dentro de outra view aninhada não é suportado (limite de recursão)"}
	}

	var out []NestedEditPlan
	for _, node := range nodes {
		viewName, _ := node["view"].(string)
		relation, _ := node["relation"].(string)
		if viewName == "" || relation == "" {
			return nil, &UnsupportedLayoutError{Reason: "nó de view aninhada sem \"view\" ou \"relation\""}
		}
		relParent, childTableName, fkField, ok := parseRelation(relation)
		if !ok {
			return nil, &UnsupportedLayoutError{Reason: fmt.Sprintf("relation %q não reconhecida (formato esperado: .tabelaPai.tabelaFilha$campoFK)", relation)}
		}
		if relParent != parentTableName {
			return nil, &UnsupportedLayoutError{Reason: fmt.Sprintf("relation %q não corresponde à tabela desta view (%q)", relation, parentTableName)}
		}

		childView, err := GetViewByNameTx(ctx, tx, actorRole, viewName)
		if err != nil {
			return nil, err
		}
		if childView.Template != "Edit" {
			return nil, &UnsupportedLayoutError{Reason: fmt.Sprintf("view aninhada %q usa template %q — só \"Edit\" é suportado nesta entrega", viewName, childView.Template)}
		}
		childTable, err := metadata.GetTableByID(ctx, tx, childView.TableID)
		if err != nil {
			return nil, err
		}
		if childTable.Name != childTableName {
			return nil, &UnsupportedLayoutError{Reason: fmt.Sprintf("relation %q aponta para a tabela %q, mas a view %q é da tabela %q", relation, childTableName, viewName, childTable.Name)}
		}
		childFields, err := metadata.ListFields(ctx, tx, childTable.ID)
		if err != nil {
			return nil, err
		}
		childFieldsByName := fieldsByNameMap(childFields)
		if _, ok := childFieldsByName[fkField]; !ok {
			return nil, &UnsupportedLayoutError{Reason: fmt.Sprintf("relation %q: campo de chave estrangeira %q não existe na tabela %q", relation, fkField, childTableName)}
		}
		if _, err := classifyEditColumns(childView.Configuration, childFieldsByName); err != nil {
			return nil, err
		}
		childRaw, _ := childView.Configuration["columns"].([]any)

		childRows, err := records.RowsTx(ctx, tx, actorRole, records.Query{
			Table:   childTable.Name,
			Where:   records.Eq{Field: fkField, Value: parentID},
			OrderBy: []records.OrderTerm{{Field: "id"}},
		})
		if err != nil {
			return nil, err
		}

		rowPlans := make([]EditPlan, 0, len(childRows))
		for _, row := range childRows {
			version := ""
			if ver, ok := row["_version"].(string); ok {
				version = ver
			}
			editFields, err := buildEditFields(ctx, tx, actorRole, childRaw, childFieldsByName, row)
			if err != nil {
				return nil, err
			}
			rowPlans = append(rowPlans, EditPlan{
				ViewID:     childView.ID,
				Table:      childTable.Name,
				RecordID:   idAsInt(row["id"]),
				Version:    version,
				Fields:     editFields,
				ActionName: actionNameFromColumns(childRaw),
			})
		}

		out = append(out, NestedEditPlan{
			ViewID:     childView.ID,
			ViewName:   viewName,
			ChildTable: childTable.Name,
			FKField:    fkField,
			ParentID:   parentID,
			Rows:       rowPlans,
		})
	}
	return out, nil
}

// CompileEditPlan monta o EditPlan de uma view Edit compatível.
// recordID == 0 monta o plano de CRIAÇÃO (todos os campos em branco);
// recordID != 0 lê o registro existente (records.Rows, mesma dupla
// checagem de autorização de CompileListPlan/CompileShowPlan) e preenche
// Value/Version a partir dele, e resolve qualquer view aninhada (GO-051).
func CompileEditPlanTx(ctx context.Context, tx database.Tx, actorRole identity.RoleID, viewID int, recordID int) (*EditPlan, error) {
	v, err := GetViewTx(ctx, tx, actorRole, viewID)
	if err != nil {
		return nil, err
	}
	if v.Template != "Edit" {
		return nil, &UnsupportedLayoutError{Reason: fmt.Sprintf("template %q não é \"Edit\"", v.Template)}
	}
	table, err := metadata.GetTableByID(ctx, tx, v.TableID)
	if err != nil {
		return nil, err
	}
	fields, err := metadata.ListFields(ctx, tx, table.ID)
	if err != nil {
		return nil, err
	}
	fieldsByName := fieldsByNameMap(fields)
	if _, err := classifyEditColumns(v.Configuration, fieldsByName); err != nil {
		return nil, err
	}
	raw, _ := v.Configuration["columns"].([]any)

	var existing map[string]any
	version := ""
	if recordID != 0 {
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
		existing = rows[0]
		if ver, ok := existing["_version"].(string); ok {
			version = ver
		}
	}

	editFields, err := buildEditFields(ctx, tx, actorRole, raw, fieldsByName, existing)
	if err != nil {
		return nil, err
	}

	var nested []NestedEditPlan
	if layout, ok := v.Configuration["layout"].(map[string]any); ok {
		nested, err = resolveNestedEditPlans(ctx, tx, actorRole, table.Name, recordID, layout, 0)
		if err != nil {
			return nil, err
		}
	}

	return &EditPlan{
		ViewID:     v.ID,
		Table:      table.Name,
		RecordID:   recordID,
		Version:    version,
		Fields:     editFields,
		ActionName: actionNameFromColumns(raw),
		Nested:     nested,
	}, nil
}

// SubmitEditView executa o `form_action` de uma view Edit — o mecanismo
// real por trás do botão "Salvar"/"SubmitWithAjax": valida que TODO campo
// submetido faz parte desta view (nunca aceita um campo extra que o
// formulário não expõe — defesa contra um cliente adulterado enviando
// campos fora do que a view realmente mostra), então delega a escrita a
// records.CreateRecord (recordID == 0) ou records.UpdateRecord
// (recordID != 0, com o mesmo controle de concorrência otimista de
// qualquer outra escrita). Devolve o registro resultante e a decisão de
// navegação (`navigate`) calculada a partir de `destination_type`.
func SubmitEditViewTx(ctx context.Context, tx database.Tx, actorRole identity.RoleID, viewID int, recordID int, expectedVersion string, values map[string]any) (*EditSubmitResult, error) {
	v, err := GetViewTx(ctx, tx, actorRole, viewID)
	if err != nil {
		return nil, err
	}
	if v.Template != "Edit" {
		return nil, &UnsupportedLayoutError{Reason: fmt.Sprintf("template %q não é \"Edit\"", v.Template)}
	}
	table, err := metadata.GetTableByID(ctx, tx, v.TableID)
	if err != nil {
		return nil, err
	}
	fields, err := metadata.ListFields(ctx, tx, table.ID)
	if err != nil {
		return nil, err
	}
	fieldNames, err := classifyEditColumns(v.Configuration, fieldsByNameMap(fields))
	if err != nil {
		return nil, err
	}
	allowed := make(map[string]bool, len(fieldNames))
	for _, name := range fieldNames {
		allowed[name] = true
	}
	for name := range values {
		if !allowed[name] {
			return nil, &UnsupportedLayoutError{Reason: fmt.Sprintf("campo %q não faz parte desta view", name)}
		}
	}

	var record map[string]any
	if recordID == 0 {
		record, err = records.CreateRecordTx(ctx, tx, actorRole, table.Name, values, nil)
	} else {
		record, err = records.UpdateRecordTx(ctx, tx, actorRole, table.Name, recordID, expectedVersion, values, nil)
	}
	if err != nil {
		return nil, err
	}

	return &EditSubmitResult{Record: record, Navigate: resolveNavigate(v.Configuration)}, nil
}
