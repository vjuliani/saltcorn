package metadata

import "fmt"

// FieldType é o conjunto pequeno e explícito de tipos de campo suportados
// nesta tarefa — deliberadamente não o catálogo completo de tipos da
// produção Node (matriz GO-001 §2.2): tipos definidos por plugin de
// terceiro ficam de fora. FieldKey é a "relação" do escopo de GO-011 —
// uma referência real a outra tabela do catálogo, aplicada como FOREIGN
// KEY de verdade, não só um metadado solto.
type FieldType string

const (
	FieldText    FieldType = "text"
	FieldInteger FieldType = "integer"
	FieldBoolean FieldType = "boolean"
	FieldFloat   FieldType = "float"
	FieldDate    FieldType = "date"
	// FieldKey referencia outra tabela do catálogo (a "relação" do escopo
	// de GO-011) — FieldDef.References deve nomear uma tabela já existente.
	FieldKey FieldType = "key"
	// FieldFile (GO-051) referencia o catálogo FIXO internal/files
	// (`_sc_files`, GO-026) — não uma tabela dinâmica do tenant, por isso
	// não usa FieldDef.References (o alvo é sempre `_sc_files`, nunca
	// escolhido pelo chamador). Armazena o id do arquivo, mesma
	// representação física de FieldKey (integer). Exige que
	// internal/files.EnsureSchema já tenha rodado neste tenant — mesma
	// disciplina de dependência entre pacotes já aplicada a
	// internal/config/internal/workflow em tarefas anteriores.
	FieldFile FieldType = "file"
)

// ErrUnsupportedFieldType é retornado quando FieldDef.Type não é um dos
// valores declarados acima.
var ErrUnsupportedFieldType = fmt.Errorf("metadata: tipo de campo não suportado")

func (t FieldType) valid() bool {
	switch t {
	case FieldText, FieldInteger, FieldBoolean, FieldFloat, FieldDate, FieldKey, FieldFile:
		return true
	default:
		return false
	}
}

// pgType retorna o tipo de coluna Postgres correspondente. FieldKey usa
// integer porque a chave primária de _sc_tables/tabelas dinâmicas é
// sempre serial (integer) nesta fundação.
func (t FieldType) pgType() string {
	switch t {
	case FieldText:
		return "text"
	case FieldInteger:
		return "integer"
	case FieldBoolean:
		return "boolean"
	case FieldFloat:
		return "double precision"
	case FieldDate:
		return "timestamptz"
	case FieldKey, FieldFile:
		return "integer"
	default:
		return ""
	}
}
