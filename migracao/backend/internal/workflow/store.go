// Definição persistida de um workflow (GO-048) — CRUD de _sc_workflows/
// _sc_workflow_steps e a montagem de uma Definition executável a partir
// deles (Compile), mesmo padrão de internal/views/commands.go: um
// pacote de domínio com sua própria checagem de papel (requireAdmin),
// nunca delegada ao transporte HTTP.
//
// Decisão de escopo: toda operação (leitura E escrita) de workflow exige
// identity.RoleAdmin — ao contrário de View (que tem MinRole próprio,
// pois uma view publicada é conteúdo voltado ao usuário final), um
// workflow é um artefato de automação interno; não existe um conceito de
// "workflow publicado para um papel menor" no legado nem no motor Go, e
// inventar um aqui seria escopo além do que GO-048 pede.
package workflow

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/vjuliani/saltcorn/migracao/backend/internal/expression"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/identity"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/platform/database"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/platform/tenancy"
)

const sqlstateUniqueViolation = "23505"

func classifyPgError(err error) error {
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		return err
	}
	if pgErr.Code == sqlstateUniqueViolation {
		return fmt.Errorf("%w: %s", ErrDuplicateStepName, pgErr.ConstraintName)
	}
	return err
}

func requireAdmin(actorRole identity.RoleID) error {
	if !identity.CanWrite(actorRole, identity.RoleAdmin) {
		return ErrNotAuthorized
	}
	return nil
}

const workflowColumns = `id, name, initial_step, xmin::text AS "_version"`

func scanWorkflow(row pgx.Row) (Workflow, error) {
	var wf Workflow
	if err := row.Scan(&wf.ID, &wf.Name, &wf.InitialStep, &wf.Version); err != nil {
		return Workflow{}, err
	}
	return wf, nil
}

const stepColumns = `id, workflow_id, name, action_name, configuration, only_if, next_step, else_step, error_step, position_x, position_y, xmin::text AS "_version"`

func scanStep(row pgx.Row) (StoredStep, error) {
	var s StoredStep
	var configJSON []byte
	if err := row.Scan(&s.ID, &s.WorkflowID, &s.Name, &s.ActionName, &configJSON, &s.OnlyIf, &s.NextStep, &s.ElseStep, &s.ErrorStep, &s.PositionX, &s.PositionY, &s.Version); err != nil {
		return StoredStep{}, err
	}
	s.Configuration = map[string]any{}
	if len(configJSON) > 0 {
		if err := json.Unmarshal(configJSON, &s.Configuration); err != nil {
			return StoredStep{}, fmt.Errorf("workflow: decodificar configuration do passo %d: %w", s.ID, err)
		}
	}
	return s, nil
}

// CreateWorkflow grava um workflow novo, sem passo inicial (o editor
// visual define initial_step ao criar o primeiro passo, ver
// SetInitialStep).
func CreateWorkflow(ctx context.Context, tx pgx.Tx, actorRole identity.RoleID, name string) (Workflow, error) {
	if err := requireAdmin(actorRole); err != nil {
		return Workflow{}, err
	}
	row := tx.QueryRow(ctx, fmt.Sprintf(`INSERT INTO _sc_workflows (name) VALUES ($1) RETURNING %s`, workflowColumns), name)
	return scanWorkflow(row)
}

// ListWorkflows lista todos os workflows do tenant, em ordem de criação.
func ListWorkflows(ctx context.Context, tx pgx.Tx, actorRole identity.RoleID) ([]Workflow, error) {
	if err := requireAdmin(actorRole); err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, fmt.Sprintf(`SELECT %s FROM _sc_workflows ORDER BY id`, workflowColumns))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Workflow
	for rows.Next() {
		wf, err := scanWorkflow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, wf)
	}
	return out, rows.Err()
}

// GetWorkflow lê um workflow por id.
func GetWorkflow(ctx context.Context, tx pgx.Tx, actorRole identity.RoleID, id int) (Workflow, error) {
	if err := requireAdmin(actorRole); err != nil {
		return Workflow{}, err
	}
	return getWorkflowRow(ctx, tx, id)
}

func getWorkflowRow(ctx context.Context, tx pgx.Tx, id int) (Workflow, error) {
	row := tx.QueryRow(ctx, fmt.Sprintf(`SELECT %s FROM _sc_workflows WHERE id = $1`, workflowColumns), id)
	wf, err := scanWorkflow(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Workflow{}, ErrWorkflowNotFound
		}
		return Workflow{}, err
	}
	return wf, nil
}

// ListSteps lê todos os passos de um workflow, em ordem de criação —
// exatamente o que o editor visual precisa para desenhar o grafo
// completo de uma vez.
func ListSteps(ctx context.Context, tx pgx.Tx, actorRole identity.RoleID, workflowID int) ([]StoredStep, error) {
	if err := requireAdmin(actorRole); err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, fmt.Sprintf(`SELECT %s FROM _sc_workflow_steps WHERE workflow_id = $1 ORDER BY id`, stepColumns), workflowID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []StoredStep
	for rows.Next() {
		s, err := scanStep(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// WorkflowUpdate são os campos opcionais de UpdateWorkflow — nil deixa o
// campo como está.
type WorkflowUpdate struct {
	Name        *string
	InitialStep *string
}

// UpdateWorkflow renomeia e/ou muda o passo inicial, com controle de
// concorrência otimista (expectedVersion) — mesmo mecanismo de
// internal/views.UpdateView. Definir InitialStep para um nome que ainda
// não existe como passo é permitido aqui (a checagem de que o passo
// existe de fato só acontece em Compile, no momento de rodar — mesmo
// espírito de CreateTrigger não validar Action contra nenhum Dispatcher
// na criação).
func UpdateWorkflow(ctx context.Context, tx pgx.Tx, actorRole identity.RoleID, id int, expectedVersion string, update WorkflowUpdate) (Workflow, error) {
	if err := requireAdmin(actorRole); err != nil {
		return Workflow{}, err
	}
	current, err := getWorkflowRow(ctx, tx, id)
	if err != nil {
		return Workflow{}, err
	}
	name := current.Name
	if update.Name != nil {
		name = *update.Name
	}
	initialStep := current.InitialStep
	if update.InitialStep != nil {
		initialStep = *update.InitialStep
	}
	row := tx.QueryRow(ctx, fmt.Sprintf(`
		UPDATE _sc_workflows SET name = $1, initial_step = $2
		WHERE id = $3 AND xmin::text = $4
		RETURNING %s
	`, workflowColumns), name, initialStep, id, expectedVersion)
	wf, err := scanWorkflow(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Workflow{}, conflictOrWorkflowNotFound(ctx, tx, id)
		}
		return Workflow{}, err
	}
	return wf, nil
}

// DeleteWorkflow remove um workflow e seus passos (ON DELETE CASCADE) —
// nunca as execuções já feitas (_sc_workflow_runs/_sc_workflow_trace
// são histórico independente, sem FK para _sc_workflows: um workflow
// pode ser reeditado/removido sem apagar o rastro de quem já rodou).
func DeleteWorkflow(ctx context.Context, tx pgx.Tx, actorRole identity.RoleID, id int) error {
	if err := requireAdmin(actorRole); err != nil {
		return err
	}
	tag, err := tx.Exec(ctx, `DELETE FROM _sc_workflows WHERE id = $1`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrWorkflowNotFound
	}
	return nil
}

func conflictOrWorkflowNotFound(ctx context.Context, tx pgx.Tx, id int) error {
	var exists bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM _sc_workflows WHERE id = $1)`, id).Scan(&exists); err != nil {
		return err
	}
	if !exists {
		return ErrWorkflowNotFound
	}
	return ErrVersionConflict
}

// StepInput são os campos de um passo novo — Name/ActionName
// obrigatórios, os demais opcionais (zero-value: sempre roda, termina o
// workflow ao final, posição na origem do canvas).
type StepInput struct {
	Name          string
	ActionName    string
	Configuration map[string]any
	OnlyIf        string
	NextStep      string
	ElseStep      string
	ErrorStep     string
	PositionX     float64
	PositionY     float64
}

// CreateStep insere um passo novo em um workflow existente. Não valida
// se ActionName está registrada em nenhum catálogo (ver comentário do
// pacote) — essa checagem só faz sentido em Compile, no momento de rodar.
func CreateStep(ctx context.Context, tx pgx.Tx, actorRole identity.RoleID, workflowID int, in StepInput) (StoredStep, error) {
	if err := requireAdmin(actorRole); err != nil {
		return StoredStep{}, err
	}
	if _, err := getWorkflowRow(ctx, tx, workflowID); err != nil {
		return StoredStep{}, err
	}
	if in.Name == "" || in.ActionName == "" {
		return StoredStep{}, fmt.Errorf("%w: name e action_name são obrigatórios", ErrActionConfigInvalid)
	}
	config := in.Configuration
	if config == nil {
		config = map[string]any{}
	}
	configJSON, err := json.Marshal(config)
	if err != nil {
		return StoredStep{}, fmt.Errorf("workflow: codificar configuration: %w", err)
	}
	row := tx.QueryRow(ctx, fmt.Sprintf(`
		INSERT INTO _sc_workflow_steps (workflow_id, name, action_name, configuration, only_if, next_step, else_step, error_step, position_x, position_y)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
		RETURNING %s
	`, stepColumns), workflowID, in.Name, in.ActionName, configJSON, in.OnlyIf, in.NextStep, in.ElseStep, in.ErrorStep, in.PositionX, in.PositionY)
	s, err := scanStep(row)
	if err != nil {
		return StoredStep{}, classifyPgError(err)
	}
	return s, nil
}

// StepPatch são os campos opcionais de UpdateStep — nil deixa o campo
// como está. Ponteiros de string vazia ("") são válidos (ex.: limpar
// OnlyIf/NextStep de volta para "sempre roda"/"termina aqui").
type StepPatch struct {
	ActionName    *string
	Configuration *map[string]any
	OnlyIf        *string
	NextStep      *string
	ElseStep      *string
	ErrorStep     *string
	PositionX     *float64
	PositionY     *float64
}

// UpdateStep atualiza um passo existente, com controle de concorrência
// otimista — usado tanto para reconfigurar a ação quanto só para
// reposicionar no canvas (o editor visual chama isto a cada drag-and-drop
// solto, exatamente como o editor legado persiste workflow_position).
func UpdateStep(ctx context.Context, tx pgx.Tx, actorRole identity.RoleID, stepID int, expectedVersion string, patch StepPatch) (StoredStep, error) {
	if err := requireAdmin(actorRole); err != nil {
		return StoredStep{}, err
	}
	current, err := getStepRow(ctx, tx, stepID)
	if err != nil {
		return StoredStep{}, err
	}
	actionName := current.ActionName
	if patch.ActionName != nil {
		actionName = *patch.ActionName
	}
	configuration := current.Configuration
	if patch.Configuration != nil {
		configuration = *patch.Configuration
	}
	onlyIf := current.OnlyIf
	if patch.OnlyIf != nil {
		onlyIf = *patch.OnlyIf
	}
	nextStep := current.NextStep
	if patch.NextStep != nil {
		nextStep = *patch.NextStep
	}
	elseStep := current.ElseStep
	if patch.ElseStep != nil {
		elseStep = *patch.ElseStep
	}
	errorStep := current.ErrorStep
	if patch.ErrorStep != nil {
		errorStep = *patch.ErrorStep
	}
	positionX := current.PositionX
	if patch.PositionX != nil {
		positionX = *patch.PositionX
	}
	positionY := current.PositionY
	if patch.PositionY != nil {
		positionY = *patch.PositionY
	}
	configJSON, err := json.Marshal(configuration)
	if err != nil {
		return StoredStep{}, fmt.Errorf("workflow: codificar configuration: %w", err)
	}
	row := tx.QueryRow(ctx, fmt.Sprintf(`
		UPDATE _sc_workflow_steps
		SET action_name = $1, configuration = $2, only_if = $3, next_step = $4, else_step = $5, error_step = $6, position_x = $7, position_y = $8
		WHERE id = $9 AND xmin::text = $10
		RETURNING %s
	`, stepColumns), actionName, configJSON, onlyIf, nextStep, elseStep, errorStep, positionX, positionY, stepID, expectedVersion)
	s, err := scanStep(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return StoredStep{}, conflictOrStepNotFound(ctx, tx, stepID)
		}
		return StoredStep{}, classifyPgError(err)
	}
	return s, nil
}

// DeleteStep remove um passo — não reescreve automaticamente os
// next_step/else_step de OUTROS passos que apontavam para ele (o editor
// visual é responsável por atualizar essas arestas antes/depois de
// remover um nó; Compile propaga o erro de passo desconhecido só quando o
// workflow for de fato rodado, nunca silenciosamente).
func DeleteStep(ctx context.Context, tx pgx.Tx, actorRole identity.RoleID, stepID int) error {
	if err := requireAdmin(actorRole); err != nil {
		return err
	}
	tag, err := tx.Exec(ctx, `DELETE FROM _sc_workflow_steps WHERE id = $1`, stepID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrStepNotFound
	}
	return nil
}

func getStepRow(ctx context.Context, tx pgx.Tx, stepID int) (StoredStep, error) {
	row := tx.QueryRow(ctx, fmt.Sprintf(`SELECT %s FROM _sc_workflow_steps WHERE id = $1`, stepColumns), stepID)
	s, err := scanStep(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return StoredStep{}, ErrStepNotFound
		}
		return StoredStep{}, err
	}
	return s, nil
}

func conflictOrStepNotFound(ctx context.Context, tx pgx.Tx, stepID int) error {
	var exists bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM _sc_workflow_steps WHERE id = $1)`, stepID).Scan(&exists); err != nil {
		return err
	}
	if !exists {
		return ErrStepNotFound
	}
	return ErrVersionConflict
}

// Compile monta a Definition executável de um workflow persistido — a
// ponte que faltava entre _sc_workflow_steps (dado) e Definition (código,
// ver comentário de definition.go). Resolve cada StoredStep.ActionName no
// catálogo `actions` (normalmente BuiltinActions()); um action_name não
// registrado é ErrUnknownAction, nunca um passo silenciosamente
// ignorado.
func Compile(ctx context.Context, tx pgx.Tx, workflowID int, actions map[string]ActionBuilder) (Definition, error) {
	wf, err := getWorkflowRow(ctx, tx, workflowID)
	if err != nil {
		return Definition{}, err
	}
	rows, err := tx.Query(ctx, fmt.Sprintf(`SELECT %s FROM _sc_workflow_steps WHERE workflow_id = $1 ORDER BY id`, stepColumns), workflowID)
	if err != nil {
		return Definition{}, err
	}
	defer rows.Close()

	def := Definition{Initial: wf.InitialStep, Steps: map[string]Step{}}
	for rows.Next() {
		s, err := scanStep(rows)
		if err != nil {
			return Definition{}, err
		}
		builder, ok := actions[s.ActionName]
		if !ok {
			return Definition{}, fmt.Errorf("%w: %q (passo %q)", ErrUnknownAction, s.ActionName, s.Name)
		}
		run, err := builder(s.Configuration)
		if err != nil {
			return Definition{}, fmt.Errorf("workflow: passo %q: %w", s.Name, err)
		}
		def.Steps[s.Name] = Step{Name: s.Name, Run: run, OnlyIf: s.OnlyIf, Next: s.NextStep, Else: s.ElseStep, ErrorStep: s.ErrorStep}
	}
	if err := rows.Err(); err != nil {
		return Definition{}, err
	}
	return def, nil
}

// StartAndRun compila, inicia e roda um workflow persistido até o fim
// (ou maxSteps) numa única chamada — a operação que o botão "Executar" do
// editor visual (e o critério de aceite de GO-048, "o resultado é
// executado de ponta a ponta") precisa. Compile roda dentro da MESMA
// transação que Start (garante que a definição vista por Start é a
// mesma que existia no banco no instante de criar o run); RunToCompletion
// já gerencia suas próprias transações por passo (ver run.go).
func StartAndRun(ctx context.Context, db *database.DB, tenant tenancy.Tenant, expr *expression.Evaluator, actorRole identity.RoleID, workflowID int, wfContext map[string]any, maxSteps int) (Run, error) {
	if err := requireAdmin(actorRole); err != nil {
		return Run{}, err
	}
	if wfContext == nil {
		wfContext = map[string]any{}
	}
	var def Definition
	var runID int
	err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		wf, err := getWorkflowRow(ctx, tx, workflowID)
		if err != nil {
			return err
		}
		if wf.InitialStep == "" {
			return ErrNoInitialStep
		}
		def, err = Compile(ctx, tx, workflowID, BuiltinActions())
		if err != nil {
			return err
		}
		runID, err = Start(ctx, tx, wf.Name, def, wfContext)
		return err
	})
	if err != nil {
		return Run{}, err
	}
	return RunToCompletion(ctx, db, tenant, expr, def, runID, maxSteps)
}
