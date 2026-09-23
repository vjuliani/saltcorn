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

	// ErrWorkflowNotFound é devolvido quando o id de workflow não existe
	// (GO-048).
	ErrWorkflowNotFound = errors.New("workflow: workflow não encontrado")
	// ErrStepNotFound é devolvido quando o id de passo não existe, ou não
	// pertence ao workflow informado.
	ErrStepNotFound = errors.New("workflow: passo não encontrado")
	// ErrNotAuthorized é devolvido quando o ator não tem o papel mínimo
	// exigido — só admin cria/edita/roda um workflow (mesma regra de
	// internal/views: definição de automação não é editável por um papel
	// menor).
	ErrNotAuthorized = errors.New("workflow: ator não tem papel suficiente para esta operação")
	// ErrVersionConflict é o erro de concorrência otimista de
	// UpdateWorkflow/UpdateStep — mesma semântica de internal/views.
	ErrVersionConflict = errors.New("workflow: conflito de concorrência — o recurso foi modificado por outra transação")
	// ErrDuplicateStepName classifica uma violação da restrição UNIQUE
	// (workflow_id, name) de _sc_workflow_steps — nunca a mensagem crua do
	// driver.
	ErrDuplicateStepName = errors.New("workflow: já existe um passo com este nome neste workflow")
	// ErrUnknownAction é devolvido quando action_name de um passo não está
	// registrada no catálogo passado a Compile (ver actions.go).
	ErrUnknownAction = errors.New("workflow: ação desconhecida")
	// ErrActionConfigInvalid é devolvido quando a configuration de um
	// passo não tem os campos que a ação exige.
	ErrActionConfigInvalid = errors.New("workflow: configuração de ação inválida")
)
