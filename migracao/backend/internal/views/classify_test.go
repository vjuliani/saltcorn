// Testes puros de ClassifyView/classifyListColumns/classifyOrderField —
// não exigem Postgres (View e []metadata.Field são valores em memória),
// diferente de commands_test.go/render_test.go. Cobrem o subconjunto
// suportado documentado em render.go e em
// docs/migracao-go/execucoes/GO-020.md.
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

func fieldColumn(fieldName, headerLabel string) map[string]any {
	item := map[string]any{
		"contents": map[string]any{
			"type":       "Field",
			"field_name": fieldName,
		},
	}
	if headerLabel != "" {
		item["header_label"] = headerLabel
	}
	return item
}

func compatibleConfiguration() map[string]any {
	return map[string]any{
		"layout": map[string]any{
			"besides": []any{
				fieldColumn("title", "Título"),
				fieldColumn("pages", ""),
			},
		},
	}
}

func TestClassifyView_CompatibleListWithFieldColumns(t *testing.T) {
	v := View{Template: "List", Configuration: compatibleConfiguration()}
	columns, err := ClassifyView(v, fieldsFixture())
	if err != nil {
		t.Fatalf("ClassifyView() erro inesperado: %v", err)
	}
	if len(columns) != 2 {
		t.Fatalf("len(columns) = %d, esperado 2", len(columns))
	}
	if columns[0] != (ListColumn{FieldName: "title", HeaderLabel: "Título"}) {
		t.Errorf("columns[0] = %+v, esperado title/Título", columns[0])
	}
	if columns[1] != (ListColumn{FieldName: "pages", HeaderLabel: "pages"}) {
		t.Errorf("columns[1] = %+v, esperado pages/pages (rótulo default = nome do campo)", columns[1])
	}
}

func TestClassifyView_UnsupportedTemplate(t *testing.T) {
	v := View{Template: "Show", Configuration: compatibleConfiguration()}
	_, err := ClassifyView(v, fieldsFixture())
	var target *UnsupportedLayoutError
	if !errors.As(err, &target) {
		t.Fatalf("ClassifyView() erro = %v, esperado *UnsupportedLayoutError", err)
	}
}

func TestClassifyView_MissingLayout(t *testing.T) {
	v := View{Template: "List", Configuration: map[string]any{}}
	_, err := ClassifyView(v, fieldsFixture())
	var target *UnsupportedLayoutError
	if !errors.As(err, &target) {
		t.Fatalf("ClassifyView() erro = %v, esperado *UnsupportedLayoutError", err)
	}
}

func TestClassifyView_EmptyBesides(t *testing.T) {
	v := View{Template: "List", Configuration: map[string]any{
		"layout": map[string]any{"besides": []any{}},
	}}
	_, err := ClassifyView(v, fieldsFixture())
	var target *UnsupportedLayoutError
	if !errors.As(err, &target) {
		t.Fatalf("ClassifyView() erro = %v, esperado *UnsupportedLayoutError", err)
	}
}

func TestClassifyView_UnsupportedColumnContentType(t *testing.T) {
	v := View{Template: "List", Configuration: map[string]any{
		"layout": map[string]any{
			"besides": []any{
				map[string]any{"contents": map[string]any{"type": "JoinField", "field_name": "author.name"}},
			},
		},
	}}
	_, err := ClassifyView(v, fieldsFixture())
	var target *UnsupportedLayoutError
	if !errors.As(err, &target) {
		t.Fatalf("ClassifyView() erro = %v, esperado *UnsupportedLayoutError", err)
	}
}

func TestClassifyView_UnknownFieldName(t *testing.T) {
	v := View{Template: "List", Configuration: map[string]any{
		"layout": map[string]any{
			"besides": []any{fieldColumn("nao_existe", "")},
		},
	}}
	_, err := ClassifyView(v, fieldsFixture())
	var target *UnsupportedLayoutError
	if !errors.As(err, &target) {
		t.Fatalf("ClassifyView() erro = %v, esperado *UnsupportedLayoutError", err)
	}
}

func TestClassifyView_UnsupportedDefaultStateOption(t *testing.T) {
	conf := compatibleConfiguration()
	conf["default_state"] = map[string]any{"_group_by": "pages"}
	v := View{Template: "List", Configuration: conf}
	_, err := ClassifyView(v, fieldsFixture())
	var target *UnsupportedLayoutError
	if !errors.As(err, &target) {
		t.Fatalf("ClassifyView() erro = %v, esperado *UnsupportedLayoutError (chave default_state não suportada)", err)
	}
}

func TestClassifyView_UnknownOrderField(t *testing.T) {
	conf := compatibleConfiguration()
	conf["default_state"] = map[string]any{"_order_field": "nao_existe"}
	v := View{Template: "List", Configuration: conf}
	_, err := ClassifyView(v, fieldsFixture())
	var target *UnsupportedLayoutError
	if !errors.As(err, &target) {
		t.Fatalf("ClassifyView() erro = %v, esperado *UnsupportedLayoutError (_order_field desconhecido)", err)
	}
}

func TestClassifyView_SupportedDefaultStateOptionsAllowed(t *testing.T) {
	conf := compatibleConfiguration()
	conf["default_state"] = map[string]any{"_order_field": "pages", "_descending": true}
	v := View{Template: "List", Configuration: conf}
	if _, err := ClassifyView(v, fieldsFixture()); err != nil {
		t.Fatalf("ClassifyView() erro inesperado com default_state suportado: %v", err)
	}
}
