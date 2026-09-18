package files

import "errors"

var (
	// ErrNotFound é devolvido quando o arquivo (catálogo ou bytes físicos)
	// não existe.
	ErrNotFound = errors.New("files: arquivo não encontrado")
	// ErrNotAuthorized é devolvido por Download quando o ator não tem
	// papel suficiente NEM é o dono do arquivo — nunca confundido com
	// ErrNotFound internamente (um chamador HTTP, se algum dia existir,
	// pode optar por mapear os dois para 404, mesma disciplina de "nunca
	// revelar existência" do legado — mas essa é uma decisão da camada de
	// transporte, não deste pacote).
	ErrNotAuthorized = errors.New("files: ator não tem acesso a este arquivo")
)
