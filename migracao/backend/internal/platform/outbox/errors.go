package outbox

import "errors"

var (
	// errKeyNotFound é interno — getKey o usa para sinalizar "sem linha
	// existente", nunca escapa de Do (que traduz para o caminho de
	// execução fresca, não um erro visível de fora).
	errKeyNotFound = errors.New("outbox: chave de idempotência não encontrada")

	// ErrKeyConflict é o "erro definido" do critério de aceite "mesma
	// chave com payload diferente é rejeitada": a MESMA chave de
	// idempotência já foi usada com um payload diferente do desta
	// chamada — nunca reexecuta fn nem devolve um resultado que não
	// corresponde ao payload pedido.
	ErrKeyConflict = errors.New("outbox: mesma chave de idempotência usada com payload diferente")
)
