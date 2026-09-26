// Testes do mecanismo de EVENTO NOMEADO (GO-052) — Dispatcher.EmitEvent,
// TriggersForEvent e a validação de CreateTrigger para um trigger
// table_id==0 (sem tabela). Mesma fixture de dispatch_test.go/
// runjscode_test.go (Postgres real + host real de GO-022).
package triggers

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/vjuliani/saltcorn/migracao/backend/internal/identity"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/metadata"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/platform/database"
)

func TestCreateTrigger_NamedEvent_TableIDZero_Succeeds(t *testing.T) {
	db, tenant, _, _ := triggerFixture(t)
	ctx := context.Background()

	err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		_, err := CreateTrigger(ctx, tx, Trigger{When: "ReceiveMobileShareData", Action: "noop"})
		return err
	})
	if err != nil {
		t.Fatalf("CreateTrigger (evento nomeado) erro inesperado: %v", err)
	}
}

func TestCreateTrigger_NamedEvent_ReservedWhen_Rejected(t *testing.T) {
	db, tenant, _, _ := triggerFixture(t)
	ctx := context.Background()

	// TableID==0 com um dos 4 nomes reservados (Insert/Update/Delete/
	// Validate) nunca seria encontrado por TriggersFor (exige table_id
	// real) nem por TriggersForEvent (exclui exatamente esses 4) — um
	// trigger morto, rejeitado na criação em vez de silenciosamente
	// inalcançável.
	err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		_, err := CreateTrigger(ctx, tx, Trigger{When: WhenInsert, Action: "noop"})
		return err
	})
	if !errors.Is(err, ErrInvalidWhenTrigger) {
		t.Fatalf("err = %v, esperado ErrInvalidWhenTrigger", err)
	}
}

func TestCreateTrigger_TableBound_NonReservedWhen_Rejected(t *testing.T) {
	db, tenant, tableID, _ := triggerFixture(t)
	ctx := context.Background()

	// O inverso do teste acima: TableID != 0 com um nome de evento livre
	// nunca seria encontrado por TriggersFor (quer um dos 4 reservados).
	err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		_, err := CreateTrigger(ctx, tx, Trigger{TableID: tableID, When: "ReceiveMobileShareData", Action: "noop"})
		return err
	})
	if !errors.Is(err, ErrInvalidWhenTrigger) {
		t.Fatalf("err = %v, esperado ErrInvalidWhenTrigger", err)
	}
}

func TestCreateTrigger_NamedEvent_AfterCommit_Rejected(t *testing.T) {
	db, tenant, _, _ := triggerFixture(t)
	ctx := context.Background()

	err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		_, err := CreateTrigger(ctx, tx, Trigger{When: "ReceiveMobileShareData", Action: "noop", AfterCommit: true})
		return err
	})
	if !errors.Is(err, ErrAfterCommitRequiresTable) {
		t.Fatalf("err = %v, esperado ErrAfterCommitRequiresTable", err)
	}
}

func TestDispatcherEmitEvent_DispatchesOnlyMatchingNamedTriggers(t *testing.T) {
	db, tenant, tableID, evaluator := triggerFixture(t)
	ctx := context.Background()

	var fired []string
	d := &Dispatcher{Expression: evaluator, Actions: map[string]ActionFuncTx{
		"mark": func(ctx context.Context, tx database.Tx, table metadata.Table, row map[string]any, config map[string]any) error {
			name, _ := config["name"].(string)
			fired = append(fired, name)
			return nil
		},
	}}

	if err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		if _, err := CreateTrigger(ctx, tx, Trigger{When: "MyEvent", Action: "mark", Configuration: map[string]any{"name": "certo"}}); err != nil {
			return err
		}
		// Nunca disparado: nome de evento diferente.
		if _, err := CreateTrigger(ctx, tx, Trigger{When: "OutroEvento", Action: "mark", Configuration: map[string]any{"name": "errado"}}); err != nil {
			return err
		}
		// Nunca disparado: trigger de TABELA (Insert), mesmo com o mesmo
		// Dispatcher — EmitEvent só consulta TriggersForEvent.
		_, err := CreateTrigger(ctx, tx, Trigger{TableID: tableID, When: WhenInsert, Action: "mark", Configuration: map[string]any{"name": "tabela"}})
		return err
	}); err != nil {
		t.Fatalf("CreateTrigger: %v", err)
	}

	err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		firedCount, err := d.EmitEvent(ctx, tx, tenant, identity.RoleAdmin, "MyEvent", nil, map[string]any{})
		if err != nil {
			return err
		}
		if firedCount != 1 {
			t.Errorf("firedCount = %d, esperado 1", firedCount)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("EmitEvent: %v", err)
	}
	if len(fired) != 1 || fired[0] != "certo" {
		t.Fatalf("fired = %v, esperado só [\"certo\"]", fired)
	}
}

func TestDispatcherEmitEvent_NoMatchingTrigger_FiredZero_NoError(t *testing.T) {
	db, tenant, _, evaluator := triggerFixture(t)
	ctx := context.Background()

	d := &Dispatcher{Expression: evaluator, Actions: map[string]ActionFuncTx{}}
	err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		firedCount, err := d.EmitEvent(ctx, tx, tenant, identity.RoleAdmin, "NuncaRegistrado", nil, map[string]any{})
		if err != nil {
			return err
		}
		if firedCount != 0 {
			t.Errorf("firedCount = %d, esperado 0", firedCount)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("EmitEvent: %v", err)
	}
}

func TestDispatcherEmitEvent_OnlyIfFalse_NeverRuns(t *testing.T) {
	db, tenant, _, evaluator := triggerFixture(t)
	ctx := context.Background()

	ran := false
	d := &Dispatcher{Expression: evaluator, Actions: map[string]ActionFuncTx{
		"mark": func(ctx context.Context, tx database.Tx, table metadata.Table, row map[string]any, config map[string]any) error {
			ran = true
			return nil
		},
	}}

	if err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		_, err := CreateTrigger(ctx, tx, Trigger{When: "MyEvent", Action: "mark", OnlyIf: "false"})
		return err
	}); err != nil {
		t.Fatalf("CreateTrigger: %v", err)
	}

	err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		firedCount, err := d.EmitEvent(ctx, tx, tenant, identity.RoleAdmin, "MyEvent", nil, map[string]any{})
		if err != nil {
			return err
		}
		if firedCount != 0 {
			t.Errorf("firedCount = %d, esperado 0 (only_if=false)", firedCount)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("EmitEvent: %v", err)
	}
	if ran {
		t.Fatal("a ação rodou apesar de only_if=false")
	}
}

// TestDispatcherEmitEvent_ReceiveMobileShareData_WritesRealRow é a prova
// ponta a ponta do critério de aceite de GO-052: o trigger REAL do pack
// piloto guitars (receive_share_trigger), disparado por EmitEvent, grava
// uma linha real em "photos" via Table.findOne(...).insertRow(...) — o
// MESMO código JS do pack.json extraído no preflight, só com "photos"
// como tabela já provisionada pelo fixture (o pack real usa o mesmo
// nome).
func TestDispatcherEmitEvent_ReceiveMobileShareData_WritesRealRow(t *testing.T) {
	db, tenant, _, evaluator := triggerFixture(t)
	ctx := context.Background()

	if err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		photos, err := metadata.CreateTable(ctx, database.AsTx(tx), identity.RoleAdmin, "photos", metadata.TableOptions{})
		if err != nil {
			return err
		}
		if _, err := metadata.AddField(ctx, database.AsTx(tx), identity.RoleAdmin, photos.ID, metadata.FieldDef{Name: "photo", Type: metadata.FieldText, Required: true}); err != nil {
			return err
		}
		_, err = CreateTrigger(ctx, tx, Trigger{
			When: "ReceiveMobileShareData", Action: ActionRunJSCode,
			Configuration: map[string]any{
				"code": "(async () => { " +
					"if (Array.isArray(row.files) && row.files.length) { " +
					"const photos = Table.findOne({ name: 'photos' }); " +
					"for (const file of row.files) { await photos.insertRow({ photo: file.location }); } " +
					"} return true; })()",
			},
		})
		return err
	}); err != nil {
		t.Fatalf("setup (tabela photos + trigger): %v", err)
	}

	d := &Dispatcher{Expression: evaluator, RunJSCode: NewRunJSCode(evaluator)}
	payload := map[string]any{"files": []any{
		map[string]any{"location": "/tmp/a.png"},
		map[string]any{"location": "/tmp/b.png"},
	}}
	err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		firedCount, err := d.EmitEvent(ctx, tx, tenant, identity.RoleAdmin, "ReceiveMobileShareData", nil, payload)
		if err != nil {
			return err
		}
		if firedCount != 1 {
			t.Errorf("firedCount = %d, esperado 1", firedCount)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("EmitEvent(ReceiveMobileShareData): %v", err)
	}

	if err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		var count int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM photos WHERE photo IN ('/tmp/a.png', '/tmp/b.png')`).Scan(&count); err != nil {
			return err
		}
		if count != 2 {
			t.Errorf("linhas gravadas em photos = %d, esperado 2 (uma por arquivo em row.files)", count)
		}
		return nil
	}); err != nil {
		t.Fatalf("verificação: %v", err)
	}
}

// TestDispatcherEmitEvent_WriteUsesOriginatingActorRole_NeverElevated —
// actorRole propaga até o callback db.write sem elevação: um ator cujo
// papel não pode escrever na tabela alvo (MinRoleWrite mais restritivo
// que o ator) recebe o MESMO erro de autorização que uma escrita HTTP
// direta receberia, nunca um bypass via run_js_code.
func TestDispatcherEmitEvent_WriteUsesOriginatingActorRole_NeverElevated(t *testing.T) {
	db, tenant, _, evaluator := triggerFixture(t)
	ctx := context.Background()

	if err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		photos, err := metadata.CreateTable(ctx, database.AsTx(tx), identity.RoleAdmin, "photos", metadata.TableOptions{MinRoleWrite: identity.RoleAdmin})
		if err != nil {
			return err
		}
		if _, err := metadata.AddField(ctx, database.AsTx(tx), identity.RoleAdmin, photos.ID, metadata.FieldDef{Name: "photo", Type: metadata.FieldText, Required: true}); err != nil {
			return err
		}
		_, err = CreateTrigger(ctx, tx, Trigger{
			When: "ReceiveMobileShareData", Action: ActionRunJSCode,
			Configuration: map[string]any{"code": "Table.findOne({name: 'photos'}).insertRow({photo: 'via-trigger.png'})"},
		})
		return err
	}); err != nil {
		t.Fatalf("setup: %v", err)
	}

	d := &Dispatcher{Expression: evaluator, RunJSCode: NewRunJSCode(evaluator)}
	err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		// RolePublic nunca escreve numa tabela com MinRoleWrite=RoleAdmin —
		// o mesmo bloqueio de records.CreateRecordTx que qualquer escrita
		// HTTP direta teria.
		_, err := d.EmitEvent(ctx, tx, tenant, identity.RolePublic, "ReceiveMobileShareData", nil, map[string]any{})
		return err
	})
	if err == nil {
		t.Fatal("EmitEvent com actorRole=RolePublic contra tabela MinRoleWrite=RoleAdmin = nil, esperado erro de autorização")
	}
}
