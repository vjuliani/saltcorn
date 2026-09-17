package views

import "errors"

var (
	// ErrViewNotFound é retornado quando o id de view não existe.
	ErrViewNotFound = errors.New("views: view não encontrada")
	// ErrDuplicateName classifica uma violação de unicidade em _sc_views.name
	// (SQLSTATE 23505) — nunca a mensagem crua do driver.
	ErrDuplicateName = errors.New("views: já existe uma view com este nome")
	// ErrNotAuthorized é retornado quando o ator não tem o papel mínimo
	// exigido — identity.CanRead(actorRole, view.MinRole) para leitura,
	// identity.CanWrite(actorRole, identity.RoleAdmin) para escrita (só
	// admin cria/edita/publica views, mesma regra de
	// internal/metadata — o catálogo/estrutura de uma aplicação não é
	// editável por um papel menor).
	ErrNotAuthorized = errors.New("views: ator não tem papel suficiente para esta operação")
	// ErrVersionConflict é o "erro definido" do critério de aceite de
	// GO-019 para edição concorrente: a view foi modificada por outra
	// transação entre a leitura (que forneceu expectedVersion) e esta
	// escrita — nunca uma sobrescrita silenciosa.
	ErrVersionConflict = errors.New("views: conflito de concorrência — a view foi modificada por outra transação")
)
