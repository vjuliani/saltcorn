// Package records implementa o compilador de consultas dinâmicas (GO-012)
// e, futuramente, os comandos de registro (GO-013) — "registros/consultas"
// é um único módulo em ADR-0001 (CQRS lógico: Commands e Queries convivem
// no mesmo módulo, não em pacotes separados). Esta tarefa cobre só o lado
// de consulta.
//
// Todo identificador (nome de tabela, campo, campo de join/agregação) é
// resolvido contra o catálogo de internal/metadata ANTES de qualquer SQL
// ser montado — uma consulta que referencia algo não catalogado é
// rejeitada (ErrUnknownTable/ErrUnknownField), nunca sanitizada-e-aceita.
// Todo valor é parametrizado ($1, $2, ...) via pgx, nunca interpolado em
// texto SQL — a garantia dupla por trás do critério de aceite "consultas
// inválidas não permitem injeção SQL".
package records

// Where é uma condição de filtro — a versão tipada em Go do DSL legado
// (`packages/db-common/internal.ts`, `mkWhere`/`whereClause`), portando só
// o subconjunto de operadores desta tarefa (ver comentário de pacote): o
// mapa flexível de chave-como-operador do JavaScript não tem equivalente
// direto e seguro em Go, então cada operador é seu próprio tipo.
type Where interface{ isWhere() }

// Eq representa igualdade (`campo = valor`). Value == nil compila para
// `campo IS NULL` (nunca `= NULL`, que nunca é verdadeiro em SQL).
type Eq struct {
	Field string
	Value any
}

// In representa `campo IN (...)`. Values vazio compila para `FALSE` —
// nunca gera `IN ()`, sintaticamente inválido e semanticamente ambíguo.
type In struct {
	Field  string
	Values []any
}

// NotIn representa `NOT (campo IN (...))`. Values vazio compila para
// `TRUE` — mesma convenção do legado (nada está "fora de uma lista
// vazia" é sempre verdadeiro).
type NotIn struct {
	Field  string
	Values []any
}

// Like representa correspondência de substring sem diferenciar
// maiúsculas/minúsculas (`ILIKE '%...%'`) — o operador `ilike` do legado no
// modo substring (o modo `fullMatch` do legado é só igualdade, já coberto
// por Eq).
type Like struct {
	Field     string
	Substring string
}

// Gt, Gte, Lt, Lte representam comparação simples.
type (
	Gt struct {
		Field string
		Value any
	}
	Gte struct {
		Field string
		Value any
	}
	Lt struct {
		Field string
		Value any
	}
	Lte struct {
		Field string
		Value any
	}
)

// Between representa um intervalo fechado (`campo >= Min AND campo <= Max`)
// — o par gt+lt com equal:true do legado, expresso como um único operador.
type Between struct {
	Field    string
	Min, Max any
}

// And combina condições com E — a forma explícita de "chaves compostas" do
// critério de aceite: um filtro por várias colunas ao mesmo tempo (o
// catálogo de GO-011 só tem chave primária simples, id serial, então não
// existe PK composta a portar — isto é o equivalente prático).
type And []Where

// Or combina condições com OU.
type Or []Where

// Not nega uma condição inteira.
type Not struct{ Cond Where }

func (Eq) isWhere()      {}
func (In) isWhere()      {}
func (NotIn) isWhere()   {}
func (Like) isWhere()    {}
func (Gt) isWhere()      {}
func (Gte) isWhere()     {}
func (Lt) isWhere()      {}
func (Lte) isWhere()     {}
func (Between) isWhere() {}
func (And) isWhere()     {}
func (Or) isWhere()      {}
func (Not) isWhere()     {}
