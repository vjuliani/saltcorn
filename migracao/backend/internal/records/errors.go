package records

import "errors"

var (
	// ErrUnknownTable é retornado quando Query.Table não existe no catálogo
	// do tenant atual — nunca compilamos SQL para uma tabela não catalogada.
	ErrUnknownTable = errors.New("records: tabela não encontrada no catálogo")
	// ErrUnknownField é retornado quando um nome de campo (em Where, join,
	// agregação ou ordenação) não existe na tabela resolvida.
	ErrUnknownField = errors.New("records: campo não encontrado no catálogo")
	// ErrNotAuthorized é retornado quando o ator não tem papel suficiente —
	// identity.CanRead contra Table.MinRoleRead para consultas (GO-012),
	// identity.CanWrite contra Table.MinRoleWrite para comandos (GO-013).
	ErrNotAuthorized = errors.New("records: ator não tem papel suficiente para esta operação")
	// ErrInvalidJoin é retornado quando Join.Field não é um campo do tipo
	// metadata.FieldKey na tabela consultada.
	ErrInvalidJoin = errors.New("records: join inválido")
	// ErrInvalidAggregation é retornado quando Aggregation não referencia
	// uma tabela filha com um campo key de volta para a tabela consultada.
	ErrInvalidAggregation = errors.New("records: agregação inválida")
	// ErrTypeMismatch é retornado quando um valor de filtro/campo não
	// corresponde ao tipo Go esperado para o tipo de campo do catálogo.
	ErrTypeMismatch = errors.New("records: valor não corresponde ao tipo do campo")

	// Erros de GO-013 (comandos de registro):

	// ErrRequiredField é retornado por CreateRecord quando um campo
	// obrigatório do catálogo (Field.Required) não é informado — ou pela
	// classificação de uma violação NOT NULL do driver, como rede de
	// segurança.
	ErrRequiredField = errors.New("records: campo obrigatório ausente")
	// ErrNoFields é retornado por UpdateRecord quando values está vazio —
	// uma atualização sem nenhum campo é rejeitada explicitamente, não um
	// no-op silencioso.
	ErrNoFields = errors.New("records: nenhum campo informado para atualizar")
	// ErrDuplicateValue classifica uma violação de unicidade do Postgres
	// (SQLSTATE 23505) — nunca a mensagem crua do driver, que pode ecoar o
	// valor duplicado.
	ErrDuplicateValue = errors.New("records: valor duplicado viola unicidade")
	// ErrInvalidReference classifica uma violação de chave estrangeira do
	// Postgres (SQLSTATE 23503) — um campo key apontando para uma linha
	// que não existe na tabela referenciada.
	ErrInvalidReference = errors.New("records: referência inválida (chave estrangeira)")
	// ErrRecordNotFound é retornado por UpdateRecord/DeleteRecord quando o
	// id informado não existe na tabela.
	ErrRecordNotFound = errors.New("records: registro não encontrado")
	// ErrVersionConflict é o "erro definido" do critério de aceite de
	// GO-013 para conflito concorrente: o registro foi modificado por
	// outra transação entre a leitura (que forneceu expectedVersion) e
	// esta escrita.
	ErrVersionConflict = errors.New("records: conflito de concorrência — o registro foi modificado por outra transação")
)
