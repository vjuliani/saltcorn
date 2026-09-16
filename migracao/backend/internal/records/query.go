package records

// Query descreve uma consulta dinâmica completa — o que GO-012 compila
// para SQL parametrizado contra o catálogo de internal/metadata.
type Query struct {
	// Table é o nome (já catalogado por GO-011) da tabela a consultar.
	Table string
	// Where é a condição de filtro; nil significa "sem filtro".
	Where Where
	// OrderBy ordena o resultado; vazio mantém a ordem natural do Postgres
	// (não garantida sem ORDER BY explícito — documentado, não escondido).
	OrderBy []OrderTerm
	// Limit/Offset paginam o resultado; 0 significa "sem limite"/"sem
	// deslocamento".
	Limit, Offset int
	// Joins traz colunas de tabelas referenciadas por campos do tipo
	// metadata.FieldKey — um nível, ver comentário de pacote.
	Joins []Join
	// Aggregations calcula valores escalares por linha a partir de tabelas
	// filhas — ver comentário de pacote.
	Aggregations []Aggregation
}

// OrderTerm é um termo de ordenação.
type OrderTerm struct {
	Field string
	Desc  bool
}

// Join traz colunas de uma tabela referenciada por um campo do tipo
// metadata.FieldKey da tabela consultada — um LEFT JOIN de um nível, com as
// colunas de Select expostas no resultado sob a chave `"<Field>__<coluna>"`.
type Join struct {
	// Field é o nome do campo (tipo metadata.FieldKey) na tabela consultada.
	Field string
	// Select lista as colunas da tabela referenciada a trazer.
	Select []string
}

// AggFunc é a função de agregação suportada.
type AggFunc string

const (
	Count AggFunc = "count"
	Sum   AggFunc = "sum"
	Avg   AggFunc = "avg"
	Min   AggFunc = "min"
	Max   AggFunc = "max"
)

// Aggregation calcula um valor escalar por linha a partir de uma tabela
// filha que referencia a tabela consultada via um campo metadata.FieldKey
// (ex.: "quantos livros este autor tem" — Count sobre a tabela "books" via
// o campo "author"). O resultado aparece na linha sob a chave Alias.
type Aggregation struct {
	Alias      string
	ChildTable string
	// FKField é o campo do tipo metadata.FieldKey na tabela filha que
	// aponta de volta para a tabela consultada.
	FKField  string
	Function AggFunc
	// TargetField é o campo a agregar na tabela filha — ignorado (pode
	// ficar vazio) quando Function == Count.
	TargetField string
}
