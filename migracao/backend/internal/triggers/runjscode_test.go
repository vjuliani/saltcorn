// Testes de integração de run_js_code (GO-040) — mesma fixture de
// dispatch_test.go (Postgres real + host real de GO-022, plugins.expr já
// pertencendo ao Go via SwitchOwner).
package triggers

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/vjuliani/saltcorn/migracao/backend/internal/identity"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/records"
)

func TestRunJSCode_ExecutesWithRowContext_SuccessAndFailure(t *testing.T) {
	db, tenant, tableID, evaluator := triggerFixture(t)
	ctx := context.Background()

	if err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		// O host (GO-022) só aceita uma EXPRESSÃO — o código é embrulhado
		// em `(async () => { return (${code}); })()` — nunca um statement
		// como `if`. Mesmo limite que `only_if` já tem; `run_js_code`
		// herda o mesmo mecanismo, não uma segunda fronteira de execução.
		_, err := CreateTrigger(ctx, tx, Trigger{
			TableID: tableID, When: WhenValidate, Action: ActionRunJSCode,
			Configuration: map[string]any{"code": "row.title === 'permitido' || (function(){ throw new Error('titulo rejeitado pelo trigger') })()"},
		})
		return err
	}); err != nil {
		t.Fatalf("CreateTrigger: %v", err)
	}

	d := &Dispatcher{Expression: evaluator, RunJSCode: NewRunJSCode(evaluator)}

	// Título que o código aceita: escrita passa.
	if err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		_, err := records.CreateRecord(ctx, tx, identity.RoleAdmin, "posts", map[string]any{"title": "permitido"}, d.HooksFor(tenant, nil))
		return err
	}); err != nil {
		t.Fatalf("CreateRecord (título permitido) erro inesperado: %v", err)
	}

	// Título que o código rejeita: a escrita inteira aborta — prova que o
	// erro do host de verdade propaga e desfaz a transação (mesmo
	// contrato de TestValidate_AbortsWrite, mas via run_js_code real).
	err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		_, err := records.CreateRecord(ctx, tx, identity.RoleAdmin, "posts", map[string]any{"title": "rejeitado"}, d.HooksFor(tenant, nil))
		return err
	})
	if err == nil {
		t.Fatal("CreateRecord (título rejeitado) = nil, esperado erro do run_js_code")
	}

	if err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		var count int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM posts WHERE title = 'rejeitado'`).Scan(&count); err != nil {
			return err
		}
		if count != 0 {
			t.Errorf("posts com title='rejeitado' = %d, esperado 0 — Validate deveria ter abortado", count)
		}
		return nil
	}); err != nil {
		t.Fatalf("verificação: %v", err)
	}
}

func TestRunJSCode_MissingCodeConfig_Fails(t *testing.T) {
	db, tenant, tableID, evaluator := triggerFixture(t)
	ctx := context.Background()

	if err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		_, err := CreateTrigger(ctx, tx, Trigger{TableID: tableID, When: WhenInsert, Action: ActionRunJSCode, Configuration: map[string]any{}})
		return err
	}); err != nil {
		t.Fatalf("CreateTrigger: %v", err)
	}

	d := &Dispatcher{Expression: evaluator, RunJSCode: NewRunJSCode(evaluator)}
	err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		_, err := records.CreateRecord(ctx, tx, identity.RoleAdmin, "posts", map[string]any{"title": "x"}, d.HooksFor(tenant, nil))
		return err
	})
	if err == nil {
		t.Fatal("CreateRecord com run_js_code sem \"code\" = nil, esperado erro")
	}
}

func TestRunJSCode_UnsupportedDomainSingleton_FailsExplicitly(t *testing.T) {
	db, tenant, tableID, evaluator := triggerFixture(t)
	ctx := context.Background()

	// Mesmo limite já documentado em GO-022/023: uma referência a um
	// singleton de domínio (Table) sem canal de callback falha
	// explicitamente — nunca undefined silencioso, nunca uma escrita real
	// via Table (GO-052, fora de escopo aqui).
	if err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		_, err := CreateTrigger(ctx, tx, Trigger{
			TableID: tableID, When: WhenInsert, Action: ActionRunJSCode,
			Configuration: map[string]any{"code": "Table.findOne({name: 'posts'})"},
		})
		return err
	}); err != nil {
		t.Fatalf("CreateTrigger: %v", err)
	}

	d := &Dispatcher{Expression: evaluator, RunJSCode: NewRunJSCode(evaluator)}
	err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		_, err := records.CreateRecord(ctx, tx, identity.RoleAdmin, "posts", map[string]any{"title": "x"}, d.HooksFor(tenant, nil))
		return err
	})
	if err == nil {
		t.Fatal("CreateRecord com run_js_code referenciando Table = nil, esperado erro (ErrUnsupportedReference)")
	}
}

func TestRunJSCode_UnregisteredOnDispatcher_ReturnsErrUnknownAction(t *testing.T) {
	db, tenant, tableID, evaluator := triggerFixture(t)
	ctx := context.Background()

	if err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		_, err := CreateTrigger(ctx, tx, Trigger{TableID: tableID, When: WhenInsert, Action: ActionRunJSCode, Configuration: map[string]any{"code": "true"}})
		return err
	}); err != nil {
		t.Fatalf("CreateTrigger: %v", err)
	}

	// Dispatcher SEM RunJSCode configurado — mesmo comportamento de uma
	// ação nativa desconhecida (ErrUnknownAction), nunca um no-op
	// silencioso.
	d := &Dispatcher{Expression: evaluator, Actions: map[string]ActionFunc{}}
	err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		_, err := records.CreateRecord(ctx, tx, identity.RoleAdmin, "posts", map[string]any{"title": "x"}, d.HooksFor(tenant, nil))
		return err
	})
	if !errors.Is(err, ErrUnknownAction) {
		t.Fatalf("err = %v, esperado ErrUnknownAction", err)
	}
}
