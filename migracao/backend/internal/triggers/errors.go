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
	// ErrAfterCommitRequiresTable (GO-052) é devolvido por CreateTrigger
	// quando um trigger de evento nomeado (TableID == 0) tenta marcar
	// AfterCommit=true — esse mecanismo depende de record["id"] (ver
	// Dispatcher.enqueueAfterCommit), que não existe para um evento sem
	// registro associado.
	ErrAfterCommitRequiresTable = errors.New("triggers: after_commit exige um trigger ligado a tabela (table_id != 0)")
	// ErrTriggerNotFound (GO-054) é devolvido por GetTriggerByID quando o
	// id referenciado (ex.: configuration.trigger_id da ação loop_rows) não
	// existe neste tenant.
	ErrTriggerNotFound = errors.New("triggers: trigger não encontrado")
)
