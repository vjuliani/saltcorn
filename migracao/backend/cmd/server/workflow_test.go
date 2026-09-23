// Testes deste arquivo exigem Postgres real — pulam (t.Skip) se
// SALTCORN_GO_TEST_DATABASE_URL não estiver definida. Cobrem GO-048: o
// ciclo do editor visual via HTTP real (criar workflow, criar passos,
// marcar passo inicial, rodar de ponta a ponta) — reaproveita os
// helpers de records_test.go/views_test.go (testDB, sanitizeForSchema,
// mintServiceIdentity, testServiceIdentitySecret, buildEditorHandler),
// mesmo pacote.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/vjuliani/saltcorn/migracao/backend/internal/expression"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/identity"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/metadata"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/platform/cutover"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/platform/database"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/platform/outbox"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/platform/shutdown"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/platform/tenancy"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/workflow"
)

type workflowHTTPFixture struct {
	tenant   tenancy.Tenant
	adminID  int
	publicID int
	guard    *cutover.Guard
	tracker  *shutdown.Tracker
}

// newWorkflowHTTPFixture monta um tenant isolado com identidade/
// metadados/outbox/workflow aplicados, uma tabela "widgets" com 3 linhas
// (para o passo count_rows ter o que contar), um usuário admin e um
// público, e ownership de workflowsCapability registrado para OwnerGo.
func newWorkflowHTTPFixture(t *testing.T, db *database.DB) workflowHTTPFixture {
	t.Helper()
	ctx := context.Background()
	tenant := tenancy.Tenant(fmt.Sprintf("workflow_http_%s", sanitizeForSchema(t.Name())))

	if err := db.WithTenant(ctx, "public", func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, fmt.Sprintf(`CREATE SCHEMA IF NOT EXISTS %s`, pgx.Identifier{string(tenant)}.Sanitize()))
		return err
	}); err != nil {
		t.Fatalf("criar schema de teste: %v", err)
	}
	t.Cleanup(func() {
		_ = db.WithTenant(context.Background(), "public", func(ctx context.Context, tx pgx.Tx) error {
			_, err := tx.Exec(ctx, fmt.Sprintf(`DROP SCHEMA IF EXISTS %s CASCADE`, pgx.Identifier{string(tenant)}.Sanitize()))
			return err
		})
		_ = db.WithTenant(context.Background(), "public", func(ctx context.Context, tx pgx.Tx) error {
			_, err := tx.Exec(ctx, "DELETE FROM _sc_capability_ownership WHERE tenant = $1 AND capability = $2", string(tenant), workflowsCapability)
			return err
		})
	})

	var adminID, publicID int
	if err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		if err := identity.EnsureSchema(ctx, tx); err != nil {
			return err
		}
		if err := metadata.EnsureSchema(ctx, database.AsTx(tx)); err != nil {
			return err
		}
		if err := outbox.EnsureSchema(ctx, tx); err != nil {
			return err
		}
		if err := workflow.EnsureSchema(ctx, tx); err != nil {
			return err
		}
		hash, err := identity.HashPassword("hunter2")
		if err != nil {
			return err
		}
		adminID, err = identity.CreateUser(ctx, tx, "admin@example.com", hash, identity.RoleAdmin)
		if err != nil {
			return err
		}
		publicID, err = identity.CreateUser(ctx, tx, "public@example.com", hash, identity.RolePublic)
		if err != nil {
			return err
		}
		if _, err := metadata.CreateTable(ctx, database.AsTx(tx), identity.RoleAdmin, "widgets", metadata.TableOptions{}); err != nil {
			return err
		}
		for i := 0; i < 3; i++ {
			if _, err := tx.Exec(ctx, `INSERT INTO widgets DEFAULT VALUES`); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatalf("setup do fixture: %v", err)
	}

	if err := db.WithTenant(ctx, "public", func(ctx context.Context, tx pgx.Tx) error {
		return cutover.EnsureSchema(ctx, tx)
	}); err != nil {
		t.Fatalf("cutover.EnsureSchema: %v", err)
	}
	guard := cutover.NewGuard()
	if err := cutover.SwitchOwner(ctx, db, guard, tenant, workflowsCapability, cutover.OwnerGo, 2*time.Second); err != nil {
		t.Fatalf("cutover.SwitchOwner (workflows): %v", err)
	}

	return workflowHTTPFixture{tenant: tenant, adminID: adminID, publicID: publicID, guard: guard, tracker: shutdown.NewTracker()}
}

// TestWorkflowHTTP_CreateStepsRunEndToEnd é a prova direta, via HTTP
// real, do critério de aceite de GO-048: cria um workflow, dois passos
// (set_context -> count_rows) ligados por next_step, marca o passo
// inicial e roda até o fim — confirmando que o contexto final reflete os
// dois efeitos, exatamente como o editor visual React faria através do
// BFF.
func TestWorkflowHTTP_CreateStepsRunEndToEnd(t *testing.T) {
	db := testDB(t)
	fx := newWorkflowHTTPFixture(t, db)
	verifier, err := tenancy.NewVerifier([]byte(testServiceIdentitySecret))
	if err != nil {
		t.Fatalf("NewVerifier: %v", err)
	}
	adminToken := mintServiceIdentity(t, testServiceIdentitySecret, strconv.Itoa(fx.adminID), fx.tenant, time.Minute)
	publicToken := mintServiceIdentity(t, testServiceIdentitySecret, strconv.Itoa(fx.publicID), fx.tenant, time.Minute)

	evaluator := &expression.Evaluator{}
	createWfH := buildEditorHandler(t, verifier, fx.guard, workflowsCapability, createWorkflowHandler(fx.tracker, db))
	createStepH := buildEditorHandler(t, verifier, fx.guard, workflowsCapability, createStepHandler(fx.tracker, db))
	updateWfH := buildEditorHandler(t, verifier, fx.guard, workflowsCapability, updateWorkflowHandler(fx.tracker, db))
	getWfH := buildEditorHandler(t, verifier, fx.guard, workflowsCapability, getWorkflowHandler(fx.tracker, db))
	runWfH := buildEditorHandler(t, verifier, fx.guard, workflowsCapability, runWorkflowHandler(fx.tracker, db, evaluator))

	// 1. Criar workflow — público não pode.
	createBody := `{"name":"contagem"}`
	publicReq := httptest.NewRequest(http.MethodPost, "/v1/tenants/"+string(fx.tenant)+"/workflows", bytes.NewBufferString(createBody))
	publicReq.SetPathValue("tenant", string(fx.tenant))
	publicReq.Header.Set("Authorization", "Bearer "+publicToken)
	publicReq.Header.Set("Idempotency-Key", "create-wf-public")
	publicRec := httptest.NewRecorder()
	createWfH.ServeHTTP(publicRec, publicReq)
	if publicRec.Code != http.StatusForbidden {
		t.Fatalf("criar workflow (público): status = %d, corpo = %s, esperado 403", publicRec.Code, publicRec.Body.String())
	}

	createReq := httptest.NewRequest(http.MethodPost, "/v1/tenants/"+string(fx.tenant)+"/workflows", bytes.NewBufferString(createBody))
	createReq.SetPathValue("tenant", string(fx.tenant))
	createReq.Header.Set("Authorization", "Bearer "+adminToken)
	createReq.Header.Set("Idempotency-Key", "create-wf-1")
	createRec := httptest.NewRecorder()
	createWfH.ServeHTTP(createRec, createReq)
	if createRec.Code != http.StatusCreated {
		t.Fatalf("criar workflow: status = %d, corpo = %s", createRec.Code, createRec.Body.String())
	}
	var wf workflowResponse
	if err := json.Unmarshal(createRec.Body.Bytes(), &wf); err != nil {
		t.Fatalf("decodificar workflow: %v", err)
	}
	wfPath := "/v1/tenants/" + string(fx.tenant) + "/workflows/" + strconv.Itoa(wf.ID)

	// 2. Criar passo "marcar_inicio" (set_context, next=contar).
	step1Body := `{"name":"marcar_inicio","action_name":"set_context","configuration":{"values":{"started":true}},"next_step":"contar"}`
	step1Req := httptest.NewRequest(http.MethodPost, wfPath+"/steps", bytes.NewBufferString(step1Body))
	step1Req.SetPathValue("tenant", string(fx.tenant))
	step1Req.SetPathValue("id", strconv.Itoa(wf.ID))
	step1Req.Header.Set("Authorization", "Bearer "+adminToken)
	step1Req.Header.Set("Idempotency-Key", "create-step-1")
	step1Rec := httptest.NewRecorder()
	createStepH.ServeHTTP(step1Rec, step1Req)
	if step1Rec.Code != http.StatusCreated {
		t.Fatalf("criar passo 1: status = %d, corpo = %s", step1Rec.Code, step1Rec.Body.String())
	}

	// 3. Criar passo "contar" (count_rows sobre widgets).
	step2Body := `{"name":"contar","action_name":"count_rows","configuration":{"table":"widgets","output":"total_widgets"}}`
	step2Req := httptest.NewRequest(http.MethodPost, wfPath+"/steps", bytes.NewBufferString(step2Body))
	step2Req.SetPathValue("tenant", string(fx.tenant))
	step2Req.SetPathValue("id", strconv.Itoa(wf.ID))
	step2Req.Header.Set("Authorization", "Bearer "+adminToken)
	step2Req.Header.Set("Idempotency-Key", "create-step-2")
	step2Rec := httptest.NewRecorder()
	createStepH.ServeHTTP(step2Rec, step2Req)
	if step2Rec.Code != http.StatusCreated {
		t.Fatalf("criar passo 2: status = %d, corpo = %s", step2Rec.Code, step2Rec.Body.String())
	}

	// 4. Marcar passo inicial.
	updateBody := `{"_version":"` + wf.Version + `","initial_step":"marcar_inicio"}`
	updateReq := httptest.NewRequest(http.MethodPatch, wfPath, bytes.NewBufferString(updateBody))
	updateReq.SetPathValue("tenant", string(fx.tenant))
	updateReq.SetPathValue("id", strconv.Itoa(wf.ID))
	updateReq.Header.Set("Authorization", "Bearer "+adminToken)
	updateReq.Header.Set("Idempotency-Key", "update-wf-1")
	updateRec := httptest.NewRecorder()
	updateWfH.ServeHTTP(updateRec, updateReq)
	if updateRec.Code != http.StatusOK {
		t.Fatalf("marcar passo inicial: status = %d, corpo = %s", updateRec.Code, updateRec.Body.String())
	}

	// 5. GET workflow devolve os dois passos.
	getReq := httptest.NewRequest(http.MethodGet, wfPath, nil)
	getReq.SetPathValue("tenant", string(fx.tenant))
	getReq.SetPathValue("id", strconv.Itoa(wf.ID))
	getReq.Header.Set("Authorization", "Bearer "+adminToken)
	getRec := httptest.NewRecorder()
	getWfH.ServeHTTP(getRec, getReq)
	if getRec.Code != http.StatusOK {
		t.Fatalf("GET workflow: status = %d, corpo = %s", getRec.Code, getRec.Body.String())
	}
	var got workflowResponse
	if err := json.Unmarshal(getRec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decodificar workflow: %v", err)
	}
	if len(got.Steps) != 2 {
		t.Fatalf("GET workflow devolveu %d passos, esperado 2", len(got.Steps))
	}
	if got.InitialStep != "marcar_inicio" {
		t.Fatalf("GET workflow InitialStep = %q, esperado \"marcar_inicio\"", got.InitialStep)
	}

	// 6. Rodar — a prova ponta a ponta.
	runReq := httptest.NewRequest(http.MethodPost, wfPath+"/run", bytes.NewBufferString(`{}`))
	runReq.SetPathValue("tenant", string(fx.tenant))
	runReq.SetPathValue("id", strconv.Itoa(wf.ID))
	runReq.Header.Set("Authorization", "Bearer "+adminToken)
	runReq.Header.Set("Idempotency-Key", "run-1")
	runRec := httptest.NewRecorder()
	runWfH.ServeHTTP(runRec, runReq)
	if runRec.Code != http.StatusOK {
		t.Fatalf("rodar workflow: status = %d, corpo = %s", runRec.Code, runRec.Body.String())
	}
	var run runResponse
	if err := json.Unmarshal(runRec.Body.Bytes(), &run); err != nil {
		t.Fatalf("decodificar run: %v", err)
	}
	if run.Status != string(workflow.StatusFinished) {
		t.Fatalf("run.Status = %q, esperado %q (erro: %q)", run.Status, workflow.StatusFinished, run.Error)
	}
	if started, _ := run.Context["started"].(bool); !started {
		t.Fatalf("run.Context[started] = %v, esperado true", run.Context["started"])
	}
	if total, _ := run.Context["total_widgets"].(float64); total != 3 {
		t.Fatalf("run.Context[total_widgets] = %v, esperado 3", run.Context["total_widgets"])
	}

	// 7. Repetir a MESMA Idempotency-Key de /run não cria um segundo run
	// — mesmo runID de volta, nunca um novo (a garantia central do
	// design de runWorkflowHandler, ver comentário do handler).
	runAgainReq := httptest.NewRequest(http.MethodPost, wfPath+"/run", bytes.NewBufferString(`{}`))
	runAgainReq.SetPathValue("tenant", string(fx.tenant))
	runAgainReq.SetPathValue("id", strconv.Itoa(wf.ID))
	runAgainReq.Header.Set("Authorization", "Bearer "+adminToken)
	runAgainReq.Header.Set("Idempotency-Key", "run-1")
	runAgainRec := httptest.NewRecorder()
	runWfH.ServeHTTP(runAgainRec, runAgainReq)
	if runAgainRec.Code != http.StatusOK {
		t.Fatalf("rodar workflow (retry): status = %d, corpo = %s", runAgainRec.Code, runAgainRec.Body.String())
	}
	var runAgain runResponse
	if err := json.Unmarshal(runAgainRec.Body.Bytes(), &runAgain); err != nil {
		t.Fatalf("decodificar run (retry): %v", err)
	}
	if runAgain.ID != run.ID {
		t.Fatalf("retry de /run com a mesma Idempotency-Key criou um run NOVO: %d != %d", runAgain.ID, run.ID)
	}
}

// TestWorkflowHTTP_RunWithoutInitialStep_Is422 confirma que rodar um
// workflow sem passo inicial devolve um erro explícito e classificado
// (422), nunca uma exceção Go crua nem uma execução silenciosamente
// vazia.
func TestWorkflowHTTP_RunWithoutInitialStep_Is422(t *testing.T) {
	db := testDB(t)
	fx := newWorkflowHTTPFixture(t, db)
	verifier, err := tenancy.NewVerifier([]byte(testServiceIdentitySecret))
	if err != nil {
		t.Fatalf("NewVerifier: %v", err)
	}
	adminToken := mintServiceIdentity(t, testServiceIdentitySecret, strconv.Itoa(fx.adminID), fx.tenant, time.Minute)

	evaluator := &expression.Evaluator{}
	createWfH := buildEditorHandler(t, verifier, fx.guard, workflowsCapability, createWorkflowHandler(fx.tracker, db))
	runWfH := buildEditorHandler(t, verifier, fx.guard, workflowsCapability, runWorkflowHandler(fx.tracker, db, evaluator))

	createReq := httptest.NewRequest(http.MethodPost, "/v1/tenants/"+string(fx.tenant)+"/workflows", bytes.NewBufferString(`{"name":"vazio"}`))
	createReq.SetPathValue("tenant", string(fx.tenant))
	createReq.Header.Set("Authorization", "Bearer "+adminToken)
	createReq.Header.Set("Idempotency-Key", "create-wf-vazio")
	createRec := httptest.NewRecorder()
	createWfH.ServeHTTP(createRec, createReq)
	if createRec.Code != http.StatusCreated {
		t.Fatalf("criar workflow: status = %d, corpo = %s", createRec.Code, createRec.Body.String())
	}
	var wf workflowResponse
	if err := json.Unmarshal(createRec.Body.Bytes(), &wf); err != nil {
		t.Fatalf("decodificar workflow: %v", err)
	}

	runReq := httptest.NewRequest(http.MethodPost, "/v1/tenants/"+string(fx.tenant)+"/workflows/"+strconv.Itoa(wf.ID)+"/run", bytes.NewBufferString(`{}`))
	runReq.SetPathValue("tenant", string(fx.tenant))
	runReq.SetPathValue("id", strconv.Itoa(wf.ID))
	runReq.Header.Set("Authorization", "Bearer "+adminToken)
	runReq.Header.Set("Idempotency-Key", "run-vazio")
	runRec := httptest.NewRecorder()
	runWfH.ServeHTTP(runRec, runReq)
	if runRec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("rodar workflow sem passo inicial: status = %d, corpo = %s, esperado 422", runRec.Code, runRec.Body.String())
	}
}
