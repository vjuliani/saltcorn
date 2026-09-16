package metadata

import "errors"

var (
	// ErrInvalidName é retornado quando um nome, depois de SQLSanitize, vira
	// string vazia — nunca prosseguir com um identificador vazio.
	ErrInvalidName = errors.New("metadata: nome inválido (vazio após sanitização)")
	// ErrNotAuthorized é retornado quando o ator não tem papel suficiente
	// para mutar o catálogo — só admin (identity.RoleAdmin) pode
	// criar/alterar/remover tabela ou campo.
	ErrNotAuthorized = errors.New("metadata: ator não tem papel suficiente para alterar o catálogo")
	// ErrTableNotFound é retornado quando uma operação referencia uma
	// tabela que não existe no catálogo do tenant atual.
	ErrTableNotFound = errors.New("metadata: tabela não encontrada no catálogo")
	// ErrFieldNotFound é retornado quando uma operação referencia um campo
	// que não existe na tabela.
	ErrFieldNotFound = errors.New("metadata: campo não encontrado na tabela")
	// ErrFieldTypeMismatch é retornado por AddField quando já existe um
	// campo com o mesmo nome, mas de tipo diferente — AddField é idempotente
	// só quando a definição é idêntica; uma mudança de tipo precisa de uma
	// operação explícita (fora do escopo desta tarefa), nunca silenciosa.
	ErrFieldTypeMismatch = errors.New("metadata: campo já existe com um tipo diferente")
	// ErrReferencedTableNotFound é retornado quando um FieldDef do tipo
	// FieldKey referencia uma tabela que não existe no catálogo.
	ErrReferencedTableNotFound = errors.New("metadata: tabela referenciada (FieldDef.References) não encontrada")
	// ErrMissingReference é retornado quando um FieldDef do tipo FieldKey
	// não informa References.
	ErrMissingReference = errors.New("metadata: campo do tipo key exige References")
)
