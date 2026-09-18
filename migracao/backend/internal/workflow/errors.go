package workflow

import "errors"

var (
	// ErrNoInitialStep é devolvido por Start quando Definition.Initial é
	// vazio — nunca cria um run sem saber por onde começar.
	ErrNoInitialStep = errors.New("workflow: definição sem passo inicial")
	// ErrUnknownStep é devolvido por Advance quando o current_step
	// persistido do run não existe em Definition.Steps — nunca finge que o
	// run terminou nem o ignora silenciosamente.
	ErrUnknownStep = errors.New("workflow: passo desconhecido nesta definição")
	// ErrRunNotFound é devolvido quando runID não existe no catálogo.
	ErrRunNotFound = errors.New("workflow: execução não encontrada")
)
