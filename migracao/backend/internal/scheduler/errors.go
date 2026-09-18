package scheduler

import "errors"

// ErrUnknownAction é devolvido quando um ScheduledTrigger referencia uma
// Action não registrada em Dispatcher.Actions — nunca disparado como
// no-op silencioso.
var ErrUnknownAction = errors.New("scheduler: ação não registrada neste Dispatcher")
