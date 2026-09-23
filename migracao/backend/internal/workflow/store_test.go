package workflow

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/vjuliani/saltcorn/migracao/backend/internal/identity"
)

func TestCreateWorkflow_RequiresAdmin(t *testing.T) {
	db, tenant, _ := workflowFixture(t)
	ctx := context.Background()
	err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		_, err := CreateWorkflow(ctx, tx, identity.RolePublic, "onboarding")
		if !errors.Is(err, ErrNotAuthorized) {
			t.Fatalf("CreateWorkflow(RolePublic) erro = %v, esperado ErrNotAuthorized", err)
		}
		wf, err := CreateWorkflow(ctx, tx, identity.RoleAdmin, "onboarding")
		if err != nil {
			t.Fatalf("CreateWorkflow(RoleAdmin) erro inesperado: %v", err)
		}
		if wf.ID == 0 || wf.Name != "onboarding" || wf.InitialStep != "" {
			t.Fatalf("CreateWorkflow devolveu %+v inesperado", wf)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("WithTenant erro inesperado: %v", err)
	}
}

// TestWorkflowStepCRUD_And_UpdateWorkflowInitialStep cobre o ciclo
// completo de CRUD numa sequência de transações separadas — como uma
// sequência real de requisições HTTP faria, uma transação por operação
// (nunca uma única transação de ponta a ponta). Isso importa de verdade
// aqui: um erro de violação de unicidade real do Postgres (SQLSTATE
// 23505, ver TestCreateStep_DuplicateName_IsIsolatedTransaction abaixo)
// deixa a transação inteira "aborted" para qualquer comando seguinte —
// então o teste do nome duplicado precisa da SUA PRÓPRIA transação,
// descartável, nunca reaproveitada para as asserções seguintes.
func TestWorkflowStepCRUD_And_UpdateWorkflowInitialStep(t *testing.T) {
	db, tenant, _ := workflowFixture(t)
	ctx := context.Background()

	var wf Workflow
	var step StoredStep
	err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		wf, err = CreateWorkflow(ctx, tx, identity.RoleAdmin, "wf")
		if err != nil {
			return err
		}
		step, err = CreateStep(ctx, tx, identity.RoleAdmin, wf.ID, StepInput{
			Name: "greet", ActionName: ActionSetContext,
			Configuration: map[string]any{"values": map[string]any{"greeting": "oi"}},
		})
		return err
	})
	if err != nil {
		t.Fatalf("setup (CreateWorkflow/CreateStep): %v", err)
	}
	if step.Name != "greet" || step.ActionName != ActionSetContext {
		t.Fatalf("CreateStep devolveu %+v inesperado", step)
	}

	// Nome duplicado no mesmo workflow deve falhar por unicidade — numa
	// transação própria e descartável (ver comentário da função).
	err = db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		_, err := CreateStep(ctx, tx, identity.RoleAdmin, wf.ID, StepInput{Name: "greet", ActionName: ActionSetContext})
		return err
	})
	if !errors.Is(err, ErrDuplicateStepName) {
		t.Fatalf("CreateStep duplicado erro = %v, esperado ErrDuplicateStepName", err)
	}

	err = db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		wf, err = UpdateWorkflow(ctx, tx, identity.RoleAdmin, wf.ID, wf.Version, WorkflowUpdate{InitialStep: &step.Name})
		return err
	})
	if err != nil {
		t.Fatalf("UpdateWorkflow: %v", err)
	}
	if wf.InitialStep != "greet" {
		t.Fatalf("UpdateWorkflow InitialStep = %q, esperado \"greet\"", wf.InitialStep)
	}

	// _version obsoleto deve ser rejeitado (mesma disciplina de views.UpdateView).
	err = db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		_, err := UpdateWorkflow(ctx, tx, identity.RoleAdmin, wf.ID, "999999", WorkflowUpdate{})
		return err
	})
	if !errors.Is(err, ErrVersionConflict) {
		t.Fatalf("UpdateWorkflow com versão obsoleta erro = %v, esperado ErrVersionConflict", err)
	}

	// _version obsoleto também deve ser rejeitado em UpdateStep — mesmo
	// mecanismo de UpdateWorkflow acima, checado independentemente aqui
	// porque cada função tem sua PRÓPRIA cláusula WHERE ... AND xmin::text
	// = $N; um bug numa não implica o mesmo bug na outra.
	err = db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		newOnlyIf := "true"
		_, err := UpdateStep(ctx, tx, identity.RoleAdmin, step.ID, "999999", StepPatch{OnlyIf: &newOnlyIf})
		return err
	})
	if !errors.Is(err, ErrVersionConflict) {
		t.Fatalf("UpdateStep com versão obsoleta erro = %v, esperado ErrVersionConflict", err)
	}

	err = db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		newOnlyIf := "true"
		var err error
		step, err = UpdateStep(ctx, tx, identity.RoleAdmin, step.ID, step.Version, StepPatch{OnlyIf: &newOnlyIf})
		return err
	})
	if err != nil {
		t.Fatalf("UpdateStep: %v", err)
	}
	if step.OnlyIf != "true" {
		t.Fatalf("UpdateStep OnlyIf = %q, esperado \"true\"", step.OnlyIf)
	}

	err = db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		steps, err := ListSteps(ctx, tx, identity.RoleAdmin, wf.ID)
		if err != nil {
			return err
		}
		if len(steps) != 1 {
			t.Fatalf("ListSteps devolveu %d passos, esperado 1", len(steps))
		}
		return nil
	})
	if err != nil {
		t.Fatalf("ListSteps: %v", err)
	}

	err = db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		return DeleteStep(ctx, tx, identity.RoleAdmin, step.ID)
	})
	if err != nil {
		t.Fatalf("DeleteStep: %v", err)
	}
	err = db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		return DeleteStep(ctx, tx, identity.RoleAdmin, step.ID)
	})
	if !errors.Is(err, ErrStepNotFound) {
		t.Fatalf("DeleteStep repetido erro = %v, esperado ErrStepNotFound", err)
	}

	err = db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		return DeleteWorkflow(ctx, tx, identity.RoleAdmin, wf.ID)
	})
	if err != nil {
		t.Fatalf("DeleteWorkflow: %v", err)
	}
	err = db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		_, err := GetWorkflow(ctx, tx, identity.RoleAdmin, wf.ID)
		return err
	})
	if !errors.Is(err, ErrWorkflowNotFound) {
		t.Fatalf("GetWorkflow após DeleteWorkflow erro = %v, esperado ErrWorkflowNotFound", err)
	}
}

func TestCompile_UnknownAction(t *testing.T) {
	db, tenant, _ := workflowFixture(t)
	ctx := context.Background()
	err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		wf, err := CreateWorkflow(ctx, tx, identity.RoleAdmin, "wf")
		if err != nil {
			t.Fatalf("CreateWorkflow: %v", err)
		}
		if _, err := CreateStep(ctx, tx, identity.RoleAdmin, wf.ID, StepInput{Name: "a", ActionName: "acao_inexistente"}); err != nil {
			t.Fatalf("CreateStep: %v", err)
		}
		if _, err := UpdateWorkflow(ctx, tx, identity.RoleAdmin, wf.ID, wf.Version, WorkflowUpdate{InitialStep: strPtr("a")}); err != nil {
			t.Fatalf("UpdateWorkflow: %v", err)
		}
		_, err = Compile(ctx, tx, wf.ID, BuiltinActions())
		if !errors.Is(err, ErrUnknownAction) {
			t.Fatalf("Compile com ação desconhecida erro = %v, esperado ErrUnknownAction", err)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("WithTenant erro inesperado: %v", err)
	}
}

func strPtr(s string) *string { return &s }
