package records

import "errors"

var (
	// ErrUnknownTable é retornado quando Query.Table não existe no catálogo
	// do tenant atual — nunca compilamos SQL para uma tabela não catalogada.
	ErrUnknownTable = errors.New("records: tabela não encontrada no catálogo")
	// ErrUnknownField é retornado quando um nome de campo (em Where, join,
	// agregação ou ordenação) não existe na tabela resolvida.
	ErrUnknownField = errors.New("records: campo não encontrado no catálogo")
	// ErrNotAuthorized é retornado quando o ator não tem papel suficiente
	// (identity.CanRead contra Table.MinRoleRead) para ler a tabela.
	ErrNotAuthorized = errors.New("records: ator não tem papel suficiente para ler esta tabela")
	// ErrInvalidJoin é retornado quando Join.Field não é um campo do tipo
	// metadata.FieldKey na tabela consultada.
	ErrInvalidJoin = errors.New("records: join inválido")
	// ErrInvalidAggregation é retornado quando Aggregation não referencia
	// uma tabela filha com um campo key de volta para a tabela consultada.
	ErrInvalidAggregation = errors.New("records: agregação inválida")
	// ErrTypeMismatch é retornado quando um valor de filtro não corresponde
	// ao tipo Go esperado para o tipo de campo declarado no catálogo.
	ErrTypeMismatch = errors.New("records: valor não corresponde ao tipo do campo")
)
