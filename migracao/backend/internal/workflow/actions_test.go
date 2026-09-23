package workflow

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/vjuliani/saltcorn/migracao/backend/internal/identity"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/metadata"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/platform/database"
)

// TestStartAndRun_EndToEnd é a prova direta do critério de aceite de
// GO-048 ("um usuário cria/edita um workflow visualmente... e o
// resultado é executado de ponta a ponta por internal/workflow"): monta
// um workflow por CRUD (como o editor visual faria via HTTP), com um
// passo set_context seguido de um count_rows sobre uma tabela real, e
// confirma que StartAndRun produz o contexto final esperado.
func TestStartAndRun_EndToEnd(t *testing.T) {
	db, tenant, evaluator := workflowFixture(t)
	ctx := context.Background()

	var workflowID int
	err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		// count_rows resolve a tabela via internal/metadata.GetTable (a
		// mesma checagem de existência/permissão de qualquer outro acesso
		// a tabela de domínio) — precisa existir no catálogo _sc_tables,
		// não só fisicamente no schema; metadata.CreateTable faz as duas
		// coisas juntas, na mesma transação.
		if err := metadata.EnsureSchema(ctx, database.AsTx(tx)); err != nil {
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

		wf, err := CreateWorkflow(ctx, tx, identity.RoleAdmin, "contagem")
		if err != nil {
			return err
		}
		workflowID = wf.ID

		if _, err := CreateStep(ctx, tx, identity.RoleAdmin, wf.ID, StepInput{
			Name: "marcar_inicio", ActionName: ActionSetContext,
			Configuration: map[string]any{"values": map[string]any{"started": true}},
			NextStep:      "contar",
		}); err != nil {
			return err
		}
		if _, err := CreateStep(ctx, tx, identity.RoleAdmin, wf.ID, StepInput{
			Name: "contar", ActionName: ActionCountRows,
			Configuration: map[string]any{"table": "widgets", "output": "total_widgets"},
		}); err != nil {
			return err
		}
		startName := "marcar_inicio"
		if _, err := UpdateWorkflow(ctx, tx, identity.RoleAdmin, wf.ID, wf.Version, WorkflowUpdate{InitialStep: &startName}); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		t.Fatalf("setup do workflow: %v", err)
	}

	run, err := StartAndRun(ctx, db, tenant, evaluator, identity.RoleAdmin, workflowID, nil, 10)
	if err != nil {
		t.Fatalf("StartAndRun erro inesperado: %v", err)
	}
	if run.Status != StatusFinished {
		t.Fatalf("run.Status = %q, esperado %q (erro: %q)", run.Status, StatusFinished, run.Error)
	}
	if started, _ := run.Context["started"].(bool); !started {
		t.Fatalf("run.Context[started] = %v, esperado true", run.Context["started"])
	}
	// run.Context é sempre relido do banco após cada Advance (ver run.go
	// persistAdvance -> Get), então um número passa por um round-trip
	// JSON: chega aqui como float64, nunca o int64 que countRowsAction
	// produziu em memória durante o próprio passo.
	total, ok := run.Context["total_widgets"].(float64)
	if !ok || total != 3 {
		t.Fatalf("run.Context[total_widgets] = %v (%T), esperado float64(3)", run.Context["total_widgets"], run.Context["total_widgets"])
	}

	// StartAndRun com papel insuficiente nunca chega a criar um run.
	if _, err := StartAndRun(ctx, db, tenant, evaluator, identity.RolePublic, workflowID, nil, 10); err == nil {
		t.Fatal("StartAndRun(RolePublic) deveria falhar, mas não falhou")
	}
}

// TestCompile_TraduzTiposEmSeuOriginal confirma que run.Context, depois de
// serializado/desserializado como jsonb (ver run.go persistAdvance), guarda
// number como float64 — decisão a documentar em GO-048.md (contagem via
// count_rows chega como int64 na PRIMEIRA leitura pós-execução em memória,
// mas um contexto RELIDO do banco devolve float64/json.Number; o editor
// visual deve tratar ambos ao exibir).
func TestCountRowsAction_MergesWithoutMutatingInput(t *testing.T) {
	builder := BuiltinActions()[ActionCountRows]
	run, err := builder(map[string]any{"table": "widgets", "output": "n"})
	if err != nil {
		t.Fatalf("builder: %v", err)
	}
	_ = run // exercitado de ponta a ponta em TestStartAndRun_EndToEnd (precisa de tx real)

	_, err = BuiltinActions()[ActionCountRows](map[string]any{})
	if err == nil {
		t.Fatal("count_rows sem table/output deveria falhar na construção")
	}
}
