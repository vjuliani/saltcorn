// Testes puros de ClassifyView/classifyListColumns/classifyShowColumns/
// classifyEditColumns/classifyOrderField — não exigem Postgres (View e
// []metadata.Field são valores em memória), diferente de
// commands_test.go/render_test.go. Cobrem o subconjunto suportado
// documentado em render.go/show.go/edit.go.
//
// Fonte de verdade: `configuration.columns[]` — a lista flat que o
// builder legado grava (list.ts:1009 `get_state_fields`), confirmada por
// leitura de um pack real (guitars, GO-039) — NÃO `configuration.layout`
// (a árvore de arranjo visual, que usa tipos em minúsculas e nunca é
// interpretada por este runtime).
package views

import (
	"errors"
	"testing"

	"github.com/vjuliani/saltcorn/migracao/backend/internal/metadata"
)

func fieldsFixture() []metadata.Field {
	return []metadata.Field{
		{ID: 1, Name: "title", Type: metadata.FieldText},
		{ID: 2, Name: "pages", Type: metadata.FieldInteger},
	}
}

func fieldColumn(fieldName string) map[string]any {
	return map[string]any{"type": "Field", "field_name": fieldName}
}

func compatibleConfiguration() map[string]any {
	return map[string]any{
		"columns": []any{
			fieldColumn("title"),
			fieldColumn("pages"),
		},
	}
}

func TestClassifyListColumns_CompatibleWithFieldColumns(t *testing.T) {
	columns, err := classifyListColumns(compatibleConfiguration(), fieldsByNameMap(fieldsFixture()))
	if err != nil {
		t.Fatalf("classifyListColumns() erro inesperado: %v", err)
	}
	if len(columns) != 2 {
		t.Fatalf("len(columns) = %d, esperado 2", len(columns))
	}
	if columns[0] != (ListColumn{Kind: ListColumnField, FieldName: "title", HeaderLabel: "title"}) {
		t.Errorf("columns[0] = %+v, esperado title/title (rótulo default = nome do campo)", columns[0])
	}
	if columns[1] != (ListColumn{Kind: ListColumnField, FieldName: "pages", HeaderLabel: "pages"}) {
		t.Errorf("columns[1] = %+v, esperado pages/pages", columns[1])
	}
}

func TestClassifyListColumns_JoinField(t *testing.T) {
	fields := append(fieldsFixture(), metadata.Field{ID: 3, Name: "author", Type: metadata.FieldKey, ReferencesTable: 99})
	conf := map[string]any{
		"columns": []any{
			map[string]any{"type": "JoinField", "join_field": "author.name"},
		},
	}
	columns, err := classifyListColumns(conf, fieldsByNameMap(fields))
	if err != nil {
		t.Fatalf("classifyListColumns() erro inesperado: %v", err)
	}
	if len(columns) != 1 {
		t.Fatalf("len(columns) = %d, esperado 1", len(columns))
	}
	if columns[0] != (ListColumn{Kind: ListColumnJoinField, FieldName: "author__name", HeaderLabel: "author"}) {
		t.Errorf("columns[0] = %+v, esperado author__name/author", columns[0])
	}
}

func TestClassifyListColumns_JoinFieldOnNonKeyField_Rejected(t *testing.T) {
	conf := map[string]any{
		"columns": []any{
			map[string]any{"type": "JoinField", "join_field": "title.name"},
		},
	}
	_, err := classifyListColumns(conf, fieldsByNameMap(fieldsFixture()))
	var target *UnsupportedLayoutError
	if !errors.As(err, &target) {
		t.Fatalf("classifyListColumns() erro = %v, esperado *UnsupportedLayoutError (title não é key)", err)
	}
}

func TestClassifyListColumns_ActionDelete(t *testing.T) {
	conf := map[string]any{
		"columns": []any{
			fieldColumn("title"),
			map[string]any{"type": "Action", "action_name": "Delete", "minRole": float64(100)},
		},
	}
	columns, err := classifyListColumns(conf, fieldsByNameMap(fieldsFixture()))
	if err != nil {
		t.Fatalf("classifyListColumns() erro inesperado: %v", err)
	}
	if len(columns) != 2 {
		t.Fatalf("len(columns) = %d, esperado 2", len(columns))
	}
	if columns[1].Kind != ListColumnAction || columns[1].ActionName != "Delete" || columns[1].ActionMinRole != 100 {
		t.Errorf("columns[1] = %+v, esperado ação Delete com ActionMinRole=100", columns[1])
	}
}

func TestClassifyListColumns_UnsupportedAction_Rejected(t *testing.T) {
	conf := map[string]any{
		"columns": []any{
			map[string]any{"type": "Action", "action_name": "Duplicate"},
		},
	}
	_, err := classifyListColumns(conf, fieldsByNameMap(fieldsFixture()))
	var target *UnsupportedLayoutError
	if !errors.As(err, &target) {
		t.Fatalf("classifyListColumns() erro = %v, esperado *UnsupportedLayoutError (ação não suportada)", err)
	}
}

func TestClassifyListColumns_UnsupportedColumnContentType(t *testing.T) {
	conf := map[string]any{
		"columns": []any{
			map[string]any{"type": "Aggregation", "field_name": "count"},
		},
	}
	_, err := classifyListColumns(conf, fieldsByNameMap(fieldsFixture()))
	var target *UnsupportedLayoutError
	if !errors.As(err, &target) {
		t.Fatalf("classifyListColumns() erro = %v, esperado *UnsupportedLayoutError", err)
	}
}

func TestClassifyListColumns_MissingColumns(t *testing.T) {
	_, err := classifyListColumns(map[string]any{}, fieldsByNameMap(fieldsFixture()))
	var target *UnsupportedLayoutError
	if !errors.As(err, &target) {
		t.Fatalf("classifyListColumns() erro = %v, esperado *UnsupportedLayoutError", err)
	}
}

func TestClassifyListColumns_EmptyColumns(t *testing.T) {
	_, err := classifyListColumns(map[string]any{"columns": []any{}}, fieldsByNameMap(fieldsFixture()))
	var target *UnsupportedLayoutError
	if !errors.As(err, &target) {
		t.Fatalf("classifyListColumns() erro = %v, esperado *UnsupportedLayoutError", err)
	}
}

func TestClassifyListColumns_UnknownFieldName(t *testing.T) {
	conf := map[string]any{"columns": []any{fieldColumn("nao_existe")}}
	_, err := classifyListColumns(conf, fieldsByNameMap(fieldsFixture()))
	var target *UnsupportedLayoutError
	if !errors.As(err, &target) {
		t.Fatalf("classifyListColumns() erro = %v, esperado *UnsupportedLayoutError", err)
	}
}

func TestClassifyOrderField_UnsupportedDefaultStateOption(t *testing.T) {
	_, err := classifyOrderField(map[string]any{"default_state": map[string]any{"_group_by": "pages"}}, fieldsByNameMap(fieldsFixture()))
	var target *UnsupportedLayoutError
	if !errors.As(err, &target) {
		t.Fatalf("classifyOrderField() erro = %v, esperado *UnsupportedLayoutError (chave default_state não suportada)", err)
	}
}

func TestClassifyOrderField_UnknownOrderField(t *testing.T) {
	_, err := classifyOrderField(map[string]any{"default_state": map[string]any{"_order_field": "nao_existe"}}, fieldsByNameMap(fieldsFixture()))
	var target *UnsupportedLayoutError
	if !errors.As(err, &target) {
		t.Fatalf("classifyOrderField() erro = %v, esperado *UnsupportedLayoutError (_order_field desconhecido)", err)
	}
}

func TestClassifyOrderField_SupportedDefaultStateOptionsAllowed(t *testing.T) {
	orderField, err := classifyOrderField(map[string]any{"default_state": map[string]any{"_order_field": "pages", "_descending": true}}, fieldsByNameMap(fieldsFixture()))
	if err != nil {
		t.Fatalf("classifyOrderField() erro inesperado com default_state suportado: %v", err)
	}
	if orderField != "pages" {
		t.Errorf("orderField = %q, esperado pages", orderField)
	}
}

func TestClassifyShowColumns_Compatible(t *testing.T) {
	columns, err := classifyShowColumns(compatibleConfiguration(), fieldsByNameMap(fieldsFixture()))
	if err != nil {
		t.Fatalf("classifyShowColumns() erro inesperado: %v", err)
	}
	if len(columns) != 2 || columns[0].FieldName != "title" || columns[1].FieldName != "pages" {
		t.Fatalf("columns = %+v, esperado [title pages]", columns)
	}
}

func TestClassifyShowColumns_RejectsNonField(t *testing.T) {
	conf := map[string]any{"columns": []any{map[string]any{"type": "Action", "action_name": "Delete"}}}
	_, err := classifyShowColumns(conf, fieldsByNameMap(fieldsFixture()))
	var target *UnsupportedLayoutError
	if !errors.As(err, &target) {
		t.Fatalf("classifyShowColumns() erro = %v, esperado *UnsupportedLayoutError (Action não suportado em Show)", err)
	}
}

func editFieldsFixture() []metadata.Field {
	return []metadata.Field{
		{ID: 1, Name: "name", Type: metadata.FieldText, Required: true},
	}
}

func TestClassifyEditColumns_CompatibleWithFieldAndAction(t *testing.T) {
	conf := map[string]any{
		"columns": []any{
			map[string]any{"type": "Field", "field_name": "name", "fieldview": "edit"},
			map[string]any{"type": "Action", "action_name": "Save"},
		},
	}
	fields, err := classifyEditColumns(conf, fieldsByNameMap(editFieldsFixture()))
	if err != nil {
		t.Fatalf("classifyEditColumns() erro inesperado: %v", err)
	}
	if len(fields) != 1 || fields[0] != "name" {
		t.Fatalf("fields = %v, esperado [name]", fields)
	}
}

func TestClassifyEditColumns_SubmitWithAjaxAlsoAccepted(t *testing.T) {
	conf := map[string]any{
		"columns": []any{
			map[string]any{"type": "Field", "field_name": "name", "fieldview": "edit"},
			map[string]any{"type": "Action", "action_name": "SubmitWithAjax"},
		},
	}
	if _, err := classifyEditColumns(conf, fieldsByNameMap(editFieldsFixture())); err != nil {
		t.Fatalf("classifyEditColumns() erro inesperado: %v", err)
	}
}

// TestClassifyEditColumns_UploadFieldviewAccepted (GO-051): "upload" era
// bloqueada em GO-039 por exigir um FieldFile que internal/metadata ainda
// não tinha (ver git history desta função) — agora que FieldFile existe
// (GO-051), a fieldview passa a ser aceita como qualquer outra.
func TestClassifyEditColumns_UploadFieldviewAccepted(t *testing.T) {
	fields := []metadata.Field{{ID: 1, Name: "photo", Type: metadata.FieldFile}}
	conf := map[string]any{
		"columns": []any{
			map[string]any{"type": "Field", "field_name": "photo", "fieldview": "upload"},
			map[string]any{"type": "Action", "action_name": "Save"},
		},
	}
	fieldNames, err := classifyEditColumns(conf, fieldsByNameMap(fields))
	if err != nil {
		t.Fatalf("classifyEditColumns() erro inesperado: %v", err)
	}
	if len(fieldNames) != 1 || fieldNames[0] != "photo" {
		t.Fatalf("classifyEditColumns() campos = %v, esperado [\"photo\"]", fieldNames)
	}
}

func TestClassifyEditColumns_NoFieldsRejected(t *testing.T) {
	conf := map[string]any{"columns": []any{map[string]any{"type": "Action", "action_name": "Save"}}}
	_, err := classifyEditColumns(conf, fieldsByNameMap(editFieldsFixture()))
	var target *UnsupportedLayoutError
	if !errors.As(err, &target) {
		t.Fatalf("classifyEditColumns() erro = %v, esperado *UnsupportedLayoutError (sem campo editável)", err)
	}
}

func TestClassifyEditColumns_NoActionRejected(t *testing.T) {
	conf := map[string]any{"columns": []any{map[string]any{"type": "Field", "field_name": "name", "fieldview": "edit"}}}
	_, err := classifyEditColumns(conf, fieldsByNameMap(editFieldsFixture()))
	var target *UnsupportedLayoutError
	if !errors.As(err, &target) {
		t.Fatalf("classifyEditColumns() erro = %v, esperado *UnsupportedLayoutError (sem ação de submissão)", err)
	}
}

func TestClassifyView_UnsupportedTemplate(t *testing.T) {
	v := View{Template: "Page", Configuration: compatibleConfiguration()}
	err := ClassifyView(v, fieldsFixture())
	var target *UnsupportedLayoutError
	if !errors.As(err, &target) {
		t.Fatalf("ClassifyView() erro = %v, esperado *UnsupportedLayoutError (template nunca implementado neste runtime)", err)
	}
}

// TestClassifyView_FeedDispatch (GO-051): Feed passou de "não suportado"
// (GO-039) para dispatch real — mesma checagem estrutural de show_view
// que classifyFeedConfig faz (a resolução cruzada de show_view/
// view_to_create só acontece em CompileFeedPlan, que tem acesso a tx).
func TestClassifyView_FeedDispatch(t *testing.T) {
	v := View{Template: "Feed", Configuration: map[string]any{"show_view": "showguitar"}}
	if err := ClassifyView(v, fieldsFixture()); err != nil {
		t.Fatalf("ClassifyView() erro inesperado: %v", err)
	}
}

func TestClassifyView_FeedDispatch_MissingShowView(t *testing.T) {
	v := View{Template: "Feed", Configuration: map[string]any{}}
	err := ClassifyView(v, fieldsFixture())
	var target *UnsupportedLayoutError
	if !errors.As(err, &target) {
		t.Fatalf("ClassifyView() erro = %v, esperado *UnsupportedLayoutError (show_view ausente)", err)
	}
}

func TestClassifyView_ListDispatch(t *testing.T) {
	v := View{Template: "List", Configuration: compatibleConfiguration()}
	if err := ClassifyView(v, fieldsFixture()); err != nil {
		t.Fatalf("ClassifyView() erro inesperado: %v", err)
	}
}

func TestClassifyView_ShowDispatch(t *testing.T) {
	v := View{Template: "Show", Configuration: compatibleConfiguration()}
	if err := ClassifyView(v, fieldsFixture()); err != nil {
		t.Fatalf("ClassifyView() erro inesperado: %v", err)
	}
}

func TestClassifyView_EditDispatch(t *testing.T) {
	conf := map[string]any{
		"columns": []any{
			map[string]any{"type": "Field", "field_name": "name", "fieldview": "edit"},
			map[string]any{"type": "Action", "action_name": "Save"},
		},
	}
	v := View{Template: "Edit", Configuration: conf}
	if err := ClassifyView(v, editFieldsFixture()); err != nil {
		t.Fatalf("ClassifyView() erro inesperado: %v", err)
	}
}
