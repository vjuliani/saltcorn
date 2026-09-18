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
	// ErrActionConfigInvalid (GO-029) é devolvido por uma ActionFunc do
	// catálogo nativo (actions.go) quando Trigger.Configuration não tem o
	// parâmetro obrigatório daquela ação — nunca um envio silencioso com
	// destinatário/URL vazio.
	ErrActionConfigInvalid = errors.New("triggers: configuration inválida para esta ação")
)
