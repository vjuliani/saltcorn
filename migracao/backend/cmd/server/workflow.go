// Rotas de workflow (GO-048) — CRUD da definição persistida
// (internal/workflow.Workflow/StoredStep, novo nesta tarefa) e o
// endpoint de execução ponta a ponta que o botão "Executar" do editor
// visual usa. Mesmo padrão de transporte HTTP já estabelecido em
// views.go: outbox.Do para idempotência nas mutações, resolveActorRole
// para o papel do ator, um erro sentinela por vez classificado em
// writeWorkflowError.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"

	"github.com/jackc/pgx/v5"

	"github.com/vjuliani/saltcorn/migracao/backend/internal/expression"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/metadata"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/platform/database"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/platform/outbox"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/platform/shutdown"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/platform/tenancy"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/records"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/views"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/workflow"
)

// workflowRunMaxSteps limita quantos passos StartAndRun/RunToCompletion
// processam numa única chamada de POST .../run — proteção contra um
// grafo com ciclo infinito sem condição de parada (mesmo espírito do
// parâmetro homônimo de internal/workflow.RunToCompletion), nunca
// ajustável pelo corpo da requisição.
const workflowRunMaxSteps = 200

type stepResponse struct {
	ID            int            `json:"id"`
	Name          string         `json:"name"`
	ActionName    string         `json:"action_name"`
	Configuration map[string]any `json:"configuration"`
	OnlyIf        string         `json:"only_if"`
	NextStep      string         `json:"next_step"`
	ElseStep      string         `json:"else_step"`
	ErrorStep     string         `json:"error_step"`
	PositionX     float64        `json:"position_x"`
	PositionY     float64        `json:"position_y"`
	Version       string         `json:"_version"`
}

func stepToResponse(s workflow.StoredStep) stepResponse {
	return stepResponse{
		ID: s.ID, Name: s.Name, ActionName: s.ActionName, Configuration: s.Configuration,
		OnlyIf: s.OnlyIf, NextStep: s.NextStep, ElseStep: s.ElseStep, ErrorStep: s.ErrorStep,
		PositionX: s.PositionX, PositionY: s.PositionY, Version: s.Version,
	}
}

type workflowResponse struct {
	ID          int            `json:"id"`
	Name        string         `json:"name"`
	InitialStep string         `json:"initial_step"`
	Version     string         `json:"_version"`
	Steps       []stepResponse `json:"steps,omitempty"`
}

func workflowToResponse(wf workflow.Workflow) workflowResponse {
	return workflowResponse{ID: wf.ID, Name: wf.Name, InitialStep: wf.InitialStep, Version: wf.Version}
}

type createWorkflowRequest struct {
	Name string `json:"name"`
}

// createWorkflowHandler implementa POST /v1/tenants/{tenant}/workflows.
func createWorkflowHandler(tracker *shutdown.Tracker, db *database.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		end, err := tracker.Begin()
		if err != nil {
			http.Error(w, err.Error(), http.StatusServiceUnavailable)
			return
		}
		defer end()

		tenant, _ := tenancy.TenantFromContext(r.Context())
		if db == nil {
			writeAPIError(w, http.StatusBadGateway, "database_unavailable", "banco não configurado nesta instância")
			return
		}

		idempotencyKey := r.Header.Get("Idempotency-Key")
		if idempotencyKey == "" {
			writeAPIError(w, http.StatusBadRequest, "idempotency_key_required", "cabeçalho Idempotency-Key é obrigatório")
			return
		}
		body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
		if err != nil {
			writeAPIError(w, http.StatusBadRequest, "invalid_body", "não foi possível ler o corpo da requisição")
			return
		}
		var req createWorkflowRequest
		if err := json.Unmarshal(body, &req); err != nil {
			writeAPIError(w, http.StatusBadRequest, "invalid_json", "corpo da requisição não é um JSON válido")
			return
		}
		var payload map[string]any
		_ = json.Unmarshal(body, &payload)

		var created any
		err = db.WithTenant(r.Context(), tenant, func(ctx context.Context, tx pgx.Tx) error {
			role, ok := resolveActorRole(ctx, tx, r, w)
			if !ok {
				return errHandled
			}
			result, _, doErr := outbox.Do(ctx, tx, idempotencyKey, payload,
				func(ctx context.Context, tx pgx.Tx) (any, []outbox.Event, error) {
					wf, err := workflow.CreateWorkflow(ctx, tx, role, req.Name)
					if err != nil {
						return nil, nil, err
					}
					return workflowToResponse(wf), nil, nil
				})
			if doErr != nil {
				return doErr
			}
			created = result
			return nil
		})
		if err != nil {
			if errors.Is(err, errHandled) {
				return
			}
			if errors.Is(err, outbox.ErrKeyConflict) {
				writeAPIError(w, http.StatusConflict, "idempotency_key_conflict", "Idempotency-Key já foi usada com um payload diferente")
				return
			}
			writeWorkflowError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, created)
	}
}

// listWorkflowsHandler implementa GET /v1/tenants/{tenant}/workflows —
// a lista que o editor visual mostra antes de abrir um workflow
// específico.
func listWorkflowsHandler(tracker *shutdown.Tracker, db *database.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		end, err := tracker.Begin()
		if err != nil {
			http.Error(w, err.Error(), http.StatusServiceUnavailable)
			return
		}
		defer end()

		tenant, _ := tenancy.TenantFromContext(r.Context())
		if db == nil {
			writeAPIError(w, http.StatusBadGateway, "database_unavailable", "banco não configurado nesta instância")
			return
		}

		var resp []workflowResponse
		err = db.WithTenant(r.Context(), tenant, func(ctx context.Context, tx pgx.Tx) error {
			role, ok := resolveActorRole(ctx, tx, r, w)
			if !ok {
				return errHandled
			}
			list, err := workflow.ListWorkflows(ctx, tx, role)
			if err != nil {
				return err
			}
			resp = make([]workflowResponse, 0, len(list))
			for _, wf := range list {
				resp = append(resp, workflowToResponse(wf))
			}
			return nil
		})
		if err != nil {
			if errors.Is(err, errHandled) {
				return
			}
			writeWorkflowError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, resp)
	}
}

// getWorkflowHandler implementa GET /v1/tenants/{tenant}/workflows/{id}
// — devolve o workflow COM todos os seus passos (Steps preenchido), o
// que o editor visual precisa para desenhar o grafo inteiro de uma vez,
// sem uma segunda chamada.
func getWorkflowHandler(tracker *shutdown.Tracker, db *database.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		end, err := tracker.Begin()
		if err != nil {
			http.Error(w, err.Error(), http.StatusServiceUnavailable)
			return
		}
		defer end()

		tenant, _ := tenancy.TenantFromContext(r.Context())
		id, convErr := strconv.Atoi(r.PathValue("id"))
		if convErr != nil {
			writeAPIError(w, http.StatusBadRequest, "invalid_id", "id inválido")
			return
		}
		if db == nil {
			writeAPIError(w, http.StatusBadGateway, "database_unavailable", "banco não configurado nesta instância")
			return
		}

		var resp workflowResponse
		err = db.WithTenant(r.Context(), tenant, func(ctx context.Context, tx pgx.Tx) error {
			role, ok := resolveActorRole(ctx, tx, r, w)
			if !ok {
				return errHandled
			}
			wf, err := workflow.GetWorkflow(ctx, tx, role, id)
			if err != nil {
				return err
			}
			steps, err := workflow.ListSteps(ctx, tx, role, id)
			if err != nil {
				return err
			}
			resp = workflowToResponse(wf)
			resp.Steps = make([]stepResponse, 0, len(steps))
			for _, s := range steps {
				resp.Steps = append(resp.Steps, stepToResponse(s))
			}
			return nil
		})
		if err != nil {
			if errors.Is(err, errHandled) {
				return
			}
			writeWorkflowError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, resp)
	}
}

type updateWorkflowRequest struct {
	Version     string  `json:"_version"`
	Name        *string `json:"name"`
	InitialStep *string `json:"initial_step"`
}

// updateWorkflowHandler implementa PATCH /v1/tenants/{tenant}/workflows/{id}
// — renomear e/ou trocar o passo inicial (o editor visual chama isto
// quando o usuário marca um nó existente como o ponto de partida).
func updateWorkflowHandler(tracker *shutdown.Tracker, db *database.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		end, err := tracker.Begin()
		if err != nil {
			http.Error(w, err.Error(), http.StatusServiceUnavailable)
			return
		}
		defer end()

		tenant, _ := tenancy.TenantFromContext(r.Context())
		id, convErr := strconv.Atoi(r.PathValue("id"))
		if convErr != nil {
			writeAPIError(w, http.StatusBadRequest, "invalid_id", "id inválido")
			return
		}
		if db == nil {
			writeAPIError(w, http.StatusBadGateway, "database_unavailable", "banco não configurado nesta instância")
			return
		}
		idempotencyKey := r.Header.Get("Idempotency-Key")
		if idempotencyKey == "" {
			writeAPIError(w, http.StatusBadRequest, "idempotency_key_required", "cabeçalho Idempotency-Key é obrigatório")
			return
		}
		body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
		if err != nil {
			writeAPIError(w, http.StatusBadRequest, "invalid_body", "não foi possível ler o corpo da requisição")
			return
		}
		var req updateWorkflowRequest
		if err := json.Unmarshal(body, &req); err != nil {
			writeAPIError(w, http.StatusBadRequest, "invalid_json", "corpo da requisição não é um JSON válido")
			return
		}
		if req.Version == "" {
			writeAPIError(w, http.StatusBadRequest, "version_required", "_version é obrigatório")
			return
		}
		var payload map[string]any
		_ = json.Unmarshal(body, &payload)

		var updated any
		err = db.WithTenant(r.Context(), tenant, func(ctx context.Context, tx pgx.Tx) error {
			role, ok := resolveActorRole(ctx, tx, r, w)
			if !ok {
				return errHandled
			}
			update := workflow.WorkflowUpdate{Name: req.Name, InitialStep: req.InitialStep}
			result, _, doErr := outbox.Do(ctx, tx, idempotencyKey, payload,
				func(ctx context.Context, tx pgx.Tx) (any, []outbox.Event, error) {
					wf, err := workflow.UpdateWorkflow(ctx, tx, role, id, req.Version, update)
					if err != nil {
						return nil, nil, err
					}
					return workflowToResponse(wf), nil, nil
				})
			if doErr != nil {
				return doErr
			}
			updated = result
			return nil
		})
		if err != nil {
			if errors.Is(err, errHandled) {
				return
			}
			if errors.Is(err, outbox.ErrKeyConflict) {
				writeAPIError(w, http.StatusConflict, "idempotency_key_conflict", "Idempotency-Key já foi usada com um payload diferente")
				return
			}
			writeWorkflowError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, updated)
	}
}

// deleteWorkflowHandler implementa DELETE /v1/tenants/{tenant}/workflows/{id}.
func deleteWorkflowHandler(tracker *shutdown.Tracker, db *database.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		end, err := tracker.Begin()
		if err != nil {
			http.Error(w, err.Error(), http.StatusServiceUnavailable)
			return
		}
		defer end()

		tenant, _ := tenancy.TenantFromContext(r.Context())
		id, convErr := strconv.Atoi(r.PathValue("id"))
		if convErr != nil {
			writeAPIError(w, http.StatusBadRequest, "invalid_id", "id inválido")
			return
		}
		if db == nil {
			writeAPIError(w, http.StatusBadGateway, "database_unavailable", "banco não configurado nesta instância")
			return
		}

		err = db.WithTenant(r.Context(), tenant, func(ctx context.Context, tx pgx.Tx) error {
			role, ok := resolveActorRole(ctx, tx, r, w)
			if !ok {
				return errHandled
			}
			return workflow.DeleteWorkflow(ctx, tx, role, id)
		})
		if err != nil {
			if errors.Is(err, errHandled) {
				return
			}
			writeWorkflowError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

type createStepRequest struct {
	Name          string         `json:"name"`
	ActionName    string         `json:"action_name"`
	Configuration map[string]any `json:"configuration"`
	OnlyIf        string         `json:"only_if"`
	NextStep      string         `json:"next_step"`
	ElseStep      string         `json:"else_step"`
	ErrorStep     string         `json:"error_step"`
	PositionX     float64        `json:"position_x"`
	PositionY     float64        `json:"position_y"`
}

// createStepHandler implementa POST /v1/tenants/{tenant}/workflows/{id}/steps
// — o editor visual chama isto quando o usuário solta um nó novo no
// canvas.
func createStepHandler(tracker *shutdown.Tracker, db *database.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		end, err := tracker.Begin()
		if err != nil {
			http.Error(w, err.Error(), http.StatusServiceUnavailable)
			return
		}
		defer end()

		tenant, _ := tenancy.TenantFromContext(r.Context())
		workflowID, convErr := strconv.Atoi(r.PathValue("id"))
		if convErr != nil {
			writeAPIError(w, http.StatusBadRequest, "invalid_id", "id inválido")
			return
		}
		if db == nil {
			writeAPIError(w, http.StatusBadGateway, "database_unavailable", "banco não configurado nesta instância")
			return
		}
		idempotencyKey := r.Header.Get("Idempotency-Key")
		if idempotencyKey == "" {
			writeAPIError(w, http.StatusBadRequest, "idempotency_key_required", "cabeçalho Idempotency-Key é obrigatório")
			return
		}
		body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
		if err != nil {
			writeAPIError(w, http.StatusBadRequest, "invalid_body", "não foi possível ler o corpo da requisição")
			return
		}
		var req createStepRequest
		if err := json.Unmarshal(body, &req); err != nil {
			writeAPIError(w, http.StatusBadRequest, "invalid_json", "corpo da requisição não é um JSON válido")
			return
		}
		var payload map[string]any
		_ = json.Unmarshal(body, &payload)

		var created any
		err = db.WithTenant(r.Context(), tenant, func(ctx context.Context, tx pgx.Tx) error {
			role, ok := resolveActorRole(ctx, tx, r, w)
			if !ok {
				return errHandled
			}
			result, _, doErr := outbox.Do(ctx, tx, idempotencyKey, payload,
				func(ctx context.Context, tx pgx.Tx) (any, []outbox.Event, error) {
					s, err := workflow.CreateStep(ctx, tx, role, workflowID, workflow.StepInput{
						Name: req.Name, ActionName: req.ActionName, Configuration: req.Configuration,
						OnlyIf: req.OnlyIf, NextStep: req.NextStep, ElseStep: req.ElseStep, ErrorStep: req.ErrorStep,
						PositionX: req.PositionX, PositionY: req.PositionY,
					})
					if err != nil {
						return nil, nil, err
					}
					return stepToResponse(s), nil, nil
				})
			if doErr != nil {
				return doErr
			}
			created = result
			return nil
		})
		if err != nil {
			if errors.Is(err, errHandled) {
				return
			}
			if errors.Is(err, outbox.ErrKeyConflict) {
				writeAPIError(w, http.StatusConflict, "idempotency_key_conflict", "Idempotency-Key já foi usada com um payload diferente")
				return
			}
			writeWorkflowError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, created)
	}
}

type updateStepRequest struct {
	Version       string          `json:"_version"`
	ActionName    *string         `json:"action_name"`
	Configuration *map[string]any `json:"configuration"`
	OnlyIf        *string         `json:"only_if"`
	NextStep      *string         `json:"next_step"`
	ElseStep      *string         `json:"else_step"`
	ErrorStep     *string         `json:"error_step"`
	PositionX     *float64        `json:"position_x"`
	PositionY     *float64        `json:"position_y"`
}

// updateStepHandler implementa PATCH
// /v1/tenants/{tenant}/workflows/{id}/steps/{stepId} — reconfigurar a
// ação/condição de um passo OU só reposicioná-lo no canvas (o editor
// visual chama isto a cada drag-and-drop solto, com só
// position_x/position_y no corpo).
func updateStepHandler(tracker *shutdown.Tracker, db *database.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		end, err := tracker.Begin()
		if err != nil {
			http.Error(w, err.Error(), http.StatusServiceUnavailable)
			return
		}
		defer end()

		tenant, _ := tenancy.TenantFromContext(r.Context())
		stepID, convErr := strconv.Atoi(r.PathValue("stepId"))
		if convErr != nil {
			writeAPIError(w, http.StatusBadRequest, "invalid_id", "id inválido")
			return
		}
		if db == nil {
			writeAPIError(w, http.StatusBadGateway, "database_unavailable", "banco não configurado nesta instância")
			return
		}
		idempotencyKey := r.Header.Get("Idempotency-Key")
		if idempotencyKey == "" {
			writeAPIError(w, http.StatusBadRequest, "idempotency_key_required", "cabeçalho Idempotency-Key é obrigatório")
			return
		}
		body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
		if err != nil {
			writeAPIError(w, http.StatusBadRequest, "invalid_body", "não foi possível ler o corpo da requisição")
			return
		}
		var req updateStepRequest
		if err := json.Unmarshal(body, &req); err != nil {
			writeAPIError(w, http.StatusBadRequest, "invalid_json", "corpo da requisição não é um JSON válido")
			return
		}
		if req.Version == "" {
			writeAPIError(w, http.StatusBadRequest, "version_required", "_version é obrigatório")
			return
		}
		var payload map[string]any
		_ = json.Unmarshal(body, &payload)

		var updated any
		err = db.WithTenant(r.Context(), tenant, func(ctx context.Context, tx pgx.Tx) error {
			role, ok := resolveActorRole(ctx, tx, r, w)
			if !ok {
				return errHandled
			}
			patch := workflow.StepPatch{
				ActionName: req.ActionName, Configuration: req.Configuration, OnlyIf: req.OnlyIf,
				NextStep: req.NextStep, ElseStep: req.ElseStep, ErrorStep: req.ErrorStep,
				PositionX: req.PositionX, PositionY: req.PositionY,
			}
			result, _, doErr := outbox.Do(ctx, tx, idempotencyKey, payload,
				func(ctx context.Context, tx pgx.Tx) (any, []outbox.Event, error) {
					s, err := workflow.UpdateStep(ctx, tx, role, stepID, req.Version, patch)
					if err != nil {
						return nil, nil, err
					}
					return stepToResponse(s), nil, nil
				})
			if doErr != nil {
				return doErr
			}
			updated = result
			return nil
		})
		if err != nil {
			if errors.Is(err, errHandled) {
				return
			}
			if errors.Is(err, outbox.ErrKeyConflict) {
				writeAPIError(w, http.StatusConflict, "idempotency_key_conflict", "Idempotency-Key já foi usada com um payload diferente")
				return
			}
			writeWorkflowError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, updated)
	}
}

// deleteStepHandler implementa DELETE
// /v1/tenants/{tenant}/workflows/{id}/steps/{stepId}.
func deleteStepHandler(tracker *shutdown.Tracker, db *database.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		end, err := tracker.Begin()
		if err != nil {
			http.Error(w, err.Error(), http.StatusServiceUnavailable)
			return
		}
		defer end()

		tenant, _ := tenancy.TenantFromContext(r.Context())
		stepID, convErr := strconv.Atoi(r.PathValue("stepId"))
		if convErr != nil {
			writeAPIError(w, http.StatusBadRequest, "invalid_id", "id inválido")
			return
		}
		if db == nil {
			writeAPIError(w, http.StatusBadGateway, "database_unavailable", "banco não configurado nesta instância")
			return
		}

		err = db.WithTenant(r.Context(), tenant, func(ctx context.Context, tx pgx.Tx) error {
			role, ok := resolveActorRole(ctx, tx, r, w)
			if !ok {
				return errHandled
			}
			return workflow.DeleteStep(ctx, tx, role, stepID)
		})
		if err != nil {
			if errors.Is(err, errHandled) {
				return
			}
			writeWorkflowError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

type runWorkflowRequest struct {
	Context map[string]any `json:"context"`
}

type runResponse struct {
	ID          int            `json:"id"`
	Name        string         `json:"name"`
	Status      string         `json:"status"`
	CurrentStep string         `json:"current_step"`
	StepSeq     int            `json:"step_seq"`
	Context     map[string]any `json:"context"`
	Error       string         `json:"error,omitempty"`
}

func runToResponse(run workflow.Run) runResponse {
	return runResponse{
		ID: run.ID, Name: run.Name, Status: string(run.Status), CurrentStep: run.CurrentStep,
		StepSeq: run.StepSeq, Context: run.Context, Error: run.Error,
	}
}

// runWorkflowHandler implementa POST
// /v1/tenants/{tenant}/workflows/{id}/run — compila a definição
// persistida, inicia um run novo (a parte idempotente, protegida por
// Idempotency-Key: um retry de rede da MESMA chamada nunca inicia um
// SEGUNDO run) e roda até o fim (ou workflowRunMaxSteps), devolvendo o
// estado final. Esta é a prova ponta a ponta do critério de aceite de
// GO-048: o resultado do editor visual é o que este endpoint executa de
// verdade via internal/workflow, sem nenhuma camada extra de simulação.
//
// A definição é compilada DUAS vezes — uma dentro da transação
// idempotente que cria o run (para que Start veja exatamente a
// definição vigente no instante da criação), outra logo depois, FORA
// dela, para alimentar RunToCompletion (que abre suas PRÓPRIAS
// transações por passo, ver run.go — não pode reaproveitar uma
// transação já commitada). Compile é uma leitura pura e barata; compilar
// duas vezes é mais simples e mais correto do que tentar carregar a
// Definition (que contém closures Go, não serializável) através da
// fronteira do outbox.
func runWorkflowHandler(tracker *shutdown.Tracker, db *database.DB, expr *expression.Evaluator) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		end, err := tracker.Begin()
		if err != nil {
			http.Error(w, err.Error(), http.StatusServiceUnavailable)
			return
		}
		defer end()

		tenant, _ := tenancy.TenantFromContext(r.Context())
		id, convErr := strconv.Atoi(r.PathValue("id"))
		if convErr != nil {
			writeAPIError(w, http.StatusBadRequest, "invalid_id", "id inválido")
			return
		}
		if db == nil {
			writeAPIError(w, http.StatusBadGateway, "database_unavailable", "banco não configurado nesta instância")
			return
		}
		idempotencyKey := r.Header.Get("Idempotency-Key")
		if idempotencyKey == "" {
			writeAPIError(w, http.StatusBadRequest, "idempotency_key_required", "cabeçalho Idempotency-Key é obrigatório")
			return
		}
		body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
		if err != nil {
			writeAPIError(w, http.StatusBadRequest, "invalid_body", "não foi possível ler o corpo da requisição")
			return
		}
		var req runWorkflowRequest
		if len(body) > 0 {
			if err := json.Unmarshal(body, &req); err != nil {
				writeAPIError(w, http.StatusBadRequest, "invalid_json", "corpo da requisição não é um JSON válido")
				return
			}
		}
		var payload map[string]any
		_ = json.Unmarshal(body, &payload)

		var runID int
		err = db.WithTenant(r.Context(), tenant, func(ctx context.Context, tx pgx.Tx) error {
			role, ok := resolveActorRole(ctx, tx, r, w)
			if !ok {
				return errHandled
			}
			wf, err := workflow.GetWorkflow(ctx, tx, role, id)
			if err != nil {
				return err
			}
			if wf.InitialStep == "" {
				return workflow.ErrNoInitialStep
			}
			result, _, doErr := outbox.Do(ctx, tx, idempotencyKey, payload,
				func(ctx context.Context, tx pgx.Tx) (any, []outbox.Event, error) {
					def, err := workflow.Compile(ctx, tx, id, workflow.BuiltinActions())
					if err != nil {
						return nil, nil, err
					}
					rid, err := workflow.Start(ctx, tx, wf.Name, def, req.Context)
					if err != nil {
						return nil, nil, err
					}
					return float64(rid), nil, nil
				})
			if doErr != nil {
				return doErr
			}
			runID = int(result.(float64))
			return nil
		})
		if err != nil {
			if errors.Is(err, errHandled) {
				return
			}
			if errors.Is(err, outbox.ErrKeyConflict) {
				writeAPIError(w, http.StatusConflict, "idempotency_key_conflict", "Idempotency-Key já foi usada com um payload diferente")
				return
			}
			writeWorkflowError(w, err)
			return
		}

		var def workflow.Definition
		err = db.WithTenant(r.Context(), tenant, func(ctx context.Context, tx pgx.Tx) error {
			var err error
			def, err = workflow.Compile(ctx, tx, id, workflow.BuiltinActions())
			return err
		})
		if err != nil {
			writeWorkflowError(w, err)
			return
		}

		run, err := workflow.RunToCompletion(r.Context(), db, tenant, expr, def, runID, workflowRunMaxSteps)
		if err != nil {
			writeAPIError(w, http.StatusBadGateway, "run_incomplete", "a execução não terminou dentro do limite de passos")
			return
		}
		writeJSON(w, http.StatusOK, runToResponse(run))
	}
}

// writeWorkflowError classifica os erros sentinela de internal/workflow
// (e de internal/metadata, resolvido por count_rows, e de
// internal/views/internal/records por continuidade com
// writeViewsOrMetadataError) — mesma disciplina de não vazar a mensagem
// crua de um erro Go ao cliente HTTP.
func writeWorkflowError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, workflow.ErrNotAuthorized), errors.Is(err, views.ErrNotAuthorized), errors.Is(err, metadata.ErrNotAuthorized), errors.Is(err, records.ErrNotAuthorized):
		writeAPIError(w, http.StatusForbidden, "not_authorized", "ator não tem papel suficiente para esta operação")
	case errors.Is(err, workflow.ErrWorkflowNotFound), errors.Is(err, workflow.ErrStepNotFound), errors.Is(err, metadata.ErrTableNotFound):
		writeAPIError(w, http.StatusNotFound, "not_found", "recurso não encontrado")
	case errors.Is(err, workflow.ErrVersionConflict):
		writeAPIError(w, http.StatusConflict, "version_conflict", "o recurso foi modificado por outra transação — releia e tente novamente")
	case errors.Is(err, workflow.ErrDuplicateStepName):
		writeAPIError(w, http.StatusConflict, "duplicate_value", "já existe um passo com este nome neste workflow")
	case errors.Is(err, workflow.ErrActionConfigInvalid):
		writeAPIError(w, http.StatusBadRequest, "invalid_input", err.Error())
	case errors.Is(err, workflow.ErrUnknownAction), errors.Is(err, workflow.ErrNoInitialStep), errors.Is(err, workflow.ErrUnknownStep):
		writeAPIError(w, http.StatusUnprocessableEntity, "workflow_unrunnable", err.Error())
	default:
		writeAPIError(w, http.StatusBadGateway, "database_error", "erro ao processar a operação")
	}
}
