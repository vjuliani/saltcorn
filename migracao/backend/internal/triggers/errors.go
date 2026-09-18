package triggers

import "errors"

var (
	// ErrInvalidWhenTrigger é devolvido por CreateTrigger quando When não é
	// um dos 4 valores válidos (WhenValidate/Insert/Update/Delete).
	ErrInvalidWhenTrigger = errors.New("triggers: when_trigger inválido")
	// ErrUnknownAction é devolvido quando um Trigger referencia uma Action
	// não registrada em Dispatcher.Actions — nunca disparado silenciosamente
	// como no-op.
	ErrUnknownAction = errors.New("triggers: ação não registrada neste Dispatcher")
)
