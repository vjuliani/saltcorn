// Corpus de disparo de triggers exigido pelo critério de aceite de
// GO-024: "ordem e rollback equivalem às fixtures; falhas/repetições não
// duplicam efeitos internos". Sobe Postgres real e o host real de GO-022
// (para os casos com OnlyIf).
package triggers

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/vjuliani/saltcorn/migracao/backend/internal/expression"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/identity"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/metadata"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/platform/cutover"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/platform/database"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/platform/outbox"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/records"
)

var errRejected = errors.New("ação rejeitou o registro de propósito")

func TestValidate_AbortsWrite(t *testing.T) {
	db, tenant, tableID, evaluator := triggerFixture(t)
	ctx := context.Background()

	if err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		_, err := CreateTrigger(ctx, tx, Trigger{TableID: tableID, When: WhenValidate, Action: "reject"})
		return err
	}); err != nil {
		t.Fatalf("CreateTrigger: %v", err)
	}

	d := &Dispatcher{Expression: evaluator, Actions: map[string]ActionFunc{
		"reject": func(ctx context.Context, tx pgx.Tx, table metadata.Table, row map[string]any, config map[string]any) error {
			return errRejected
		},
	}}

	err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		_, err := records.CreateRecord(ctx, tx, identity.RoleAdmin, "posts", map[string]any{"title": "nunca deveria existir"}, d.HooksFor(tenant, nil))
		return err
	})
	if !errors.Is(err, errRejected) {
		t.Fatalf("err = %v, esperado errRejected", err)
	}

	if err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		var count int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM posts`).Scan(&count); err != nil {
			return err
		}
		if count != 0 {
			t.Errorf("posts tem %d linha(s), esperado 0 — Validate deveria ter abortado a escrita inteira", count)
		}
		return nil
	}); err != nil {
		t.Fatalf("verificação: %v", err)
	}
}

func TestOnlyIf_GatesAction(t *testing.T) {
	db, tenant, tableID, evaluator := triggerFixture(t)
	ctx := context.Background()

	if err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		_, err := CreateTrigger(ctx, tx, Trigger{TableID: tableID, When: WhenInsert, Action: "mark", OnlyIf: "row.published === true"})
		return err
	}); err != nil {
		t.Fatalf("CreateTrigger: %v", err)
	}

	called := 0
	d := &Dispatcher{Expression: evaluator, Actions: map[string]ActionFunc{
		"mark": func(ctx context.Context, tx pgx.Tx, table metadata.Table, row map[string]any, config map[string]any) error {
			called++
			return nil
		},
	}}

	if err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		_, err := records.CreateRecord(ctx, tx, identity.RoleAdmin, "posts", map[string]any{"title": "rascunho", "published": false}, d.HooksFor(tenant, nil))
		return err
	}); err != nil {
		t.Fatalf("CreateRecord (published=false): %v", err)
	}
	if called != 0 {
		t.Fatalf("called = %d, esperado 0 — OnlyIf=false não deveria disparar a ação", called)
	}

	if err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		_, err := records.CreateRecord(ctx, tx, identity.RoleAdmin, "posts", map[string]any{"title": "publicado", "published": true}, d.HooksFor(tenant, nil))
		return err
	}); err != nil {
		t.Fatalf("CreateRecord (published=true): %v", err)
	}
	if called != 1 {
		t.Fatalf("called = %d, esperado 1 — OnlyIf=true deveria disparar a ação exatamente uma vez", called)
	}
}

func TestAfterInsert_RunsInSameTransaction(t *testing.T) {
	db, tenant, tableID, evaluator := triggerFixture(t)
	ctx := context.Background()

	if err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `CREATE TABLE post_log (id serial primary key, title text)`); err != nil {
			return err
		}
		_, err := CreateTrigger(ctx, tx, Trigger{TableID: tableID, When: WhenInsert, Action: "log"})
		return err
	}); err != nil {
		t.Fatalf("setup: %v", err)
	}

	d := &Dispatcher{Expression: evaluator, Actions: map[string]ActionFunc{
		"log": func(ctx context.Context, tx pgx.Tx, table metadata.Table, row map[string]any, config map[string]any) error {
			_, err := tx.Exec(ctx, `INSERT INTO post_log (title) VALUES ($1)`, row["title"])
			return err
		},
	}}

	if err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		if _, err := records.CreateRecord(ctx, tx, identity.RoleAdmin, "posts", map[string]any{"title": "olá"}, d.HooksFor(tenant, nil)); err != nil {
			return err
		}
		// Visível DENTRO da mesma transação, antes do commit — prova que o
		// hook AfterInsert roda na mesma tx da escrita, não numa tx separada.
		var count int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM post_log WHERE title = 'olá'`).Scan(&count); err != nil {
			return err
		}
		if count != 1 {
			t.Errorf("post_log tem %d linha(s) dentro da mesma tx, esperado 1", count)
		}
		return nil
	}); err != nil {
		t.Fatalf("CreateRecord+verificação: %v", err)
	}

	// E continua persistido depois do commit.
	if err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		var count int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM post_log WHERE title = 'olá'`).Scan(&count); err != nil {
			return err
		}
		if count != 1 {
			t.Errorf("post_log tem %d linha(s) após commit, esperado 1", count)
		}
		return nil
	}); err != nil {
		t.Fatalf("verificação pós-commit: %v", err)
	}
}

// TestAfterCommit_EnqueuesOutboxEvent_NeverRunsSynchronously prova a
// separação central do escopo desta tarefa: um trigger AfterCommit NUNCA
// invoca a ActionFunc dentro da transação da escrita — só enfileira um
// evento outbox (GO-014), durável, para um worker processar depois.
func TestAfterCommit_EnqueuesOutboxEvent_NeverRunsSynchronously(t *testing.T) {
	db, tenant, tableID, evaluator := triggerFixture(t)
	ctx := context.Background()

	if err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		_, err := CreateTrigger(ctx, tx, Trigger{TableID: tableID, When: WhenInsert, Action: "slow_effect", AfterCommit: true})
		return err
	}); err != nil {
		t.Fatalf("CreateTrigger: %v", err)
	}

	calledSynchronously := false
	d := &Dispatcher{Expression: evaluator, Actions: map[string]ActionFunc{
		"slow_effect": func(ctx context.Context, tx pgx.Tx, table metadata.Table, row map[string]any, config map[string]any) error {
			calledSynchronously = true
			return nil
		},
	}}

	var recordID any
	if err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		rec, err := records.CreateRecord(ctx, tx, identity.RoleAdmin, "posts", map[string]any{"title": "efeito adiado"}, d.HooksFor(tenant, nil))
		if err != nil {
			return err
		}
		recordID = rec["id"]
		return nil
	}); err != nil {
		t.Fatalf("CreateRecord: %v", err)
	}

	if calledSynchronously {
		t.Fatal("a ActionFunc foi chamada sincronamente para um trigger AfterCommit — deveria só ser enfileirada")
	}

	if err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		events, err := outbox.ListPending(ctx, tx, 10)
		if err != nil {
			return err
		}
		if len(events) != 1 {
			t.Fatalf("outbox.ListPending = %d eventos, esperado 1", len(events))
		}
		if events[0].Type != "trigger:slow_effect" {
			t.Errorf("evento.Type = %q, esperado \"trigger:slow_effect\"", events[0].Type)
		}
		return nil
	}); err != nil {
		t.Fatalf("verificação outbox: %v", err)
	}
	_ = recordID
}

// TestAfterCommit_RollbackNeverEnqueuesEvent replica a fixture do legado
// "does not run when the transaction rolls back" (actions.test.ts) — aqui
// como "nem sequer enfileira", já que a garantia é mais forte
// (durabilidade dentro da MESMA transação, não uma fila em memória).
func TestAfterCommit_RollbackNeverEnqueuesEvent(t *testing.T) {
	db, tenant, tableID, evaluator := triggerFixture(t)
	ctx := context.Background()

	if err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		_, err := CreateTrigger(ctx, tx, Trigger{TableID: tableID, When: WhenInsert, Action: "noop", AfterCommit: true})
		return err
	}); err != nil {
		t.Fatalf("CreateTrigger: %v", err)
	}

	d := &Dispatcher{Expression: evaluator, Actions: map[string]ActionFunc{
		"noop": func(ctx context.Context, tx pgx.Tx, table metadata.Table, row map[string]any, config map[string]any) error {
			return nil
		},
	}}

	errDeliberateRollback := errors.New("rollback deliberado do teste")
	err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		if _, err := records.CreateRecord(ctx, tx, identity.RoleAdmin, "posts", map[string]any{"title": "vai sumir"}, d.HooksFor(tenant, nil)); err != nil {
			return err
		}
		return errDeliberateRollback
	})
	if !errors.Is(err, errDeliberateRollback) {
		t.Fatalf("err = %v, esperado errDeliberateRollback", err)
	}

	if err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		var count int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM _sc_outbox`).Scan(&count); err != nil {
			return err
		}
		if count != 0 {
			t.Errorf("_sc_outbox tem %d linha(s), esperado 0 — rollback nunca deveria deixar um evento enfileirado", count)
		}
		var posts int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM posts`).Scan(&posts); err != nil {
			return err
		}
		if posts != 0 {
			t.Errorf("posts tem %d linha(s), esperado 0 — rollback deveria ter desfeito a escrita também", posts)
		}
		return nil
	}); err != nil {
		t.Fatalf("verificação: %v", err)
	}
}

// TestAfterCommit_RetryDoesNotDuplicateEvent prova "falhas/repetições não
// duplicam efeitos internos" diretamente: chamar o disparo do MESMO
// trigger para o MESMO registro duas vezes (simulando uma repetição de
// hook, ex.: um retry de nível superior que reexecuta AfterInsert)
// resulta em exatamente UM evento outbox, nunca dois.
func TestAfterCommit_RetryDoesNotDuplicateEvent(t *testing.T) {
	db, tenant, tableID, evaluator := triggerFixture(t)
	ctx := context.Background()

	var trig Trigger
	if err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		trig, err = CreateTrigger(ctx, tx, Trigger{TableID: tableID, When: WhenInsert, Action: "noop", AfterCommit: true})
		return err
	}); err != nil {
		t.Fatalf("CreateTrigger: %v", err)
	}

	d := &Dispatcher{Expression: evaluator, Actions: map[string]ActionFunc{
		"noop": func(ctx context.Context, tx pgx.Tx, table metadata.Table, row map[string]any, config map[string]any) error {
			return nil
		},
	}}

	if err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		tbl, err := metadata.GetTable(ctx, database.AsTx(tx), "posts")
		if err != nil {
			return err
		}
		record := map[string]any{"id": float64(999), "title": "retry"}
		if err := d.enqueueAfterCommit(ctx, tx, trig, *tbl, record); err != nil {
			return err
		}
		// Repetição deliberada da MESMA chamada (mesmo trigger, mesmo
		// registro) — simula um retry de nível superior.
		return d.enqueueAfterCommit(ctx, tx, trig, *tbl, record)
	}); err != nil {
		t.Fatalf("enqueueAfterCommit (x2): %v", err)
	}

	if err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		var count int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM _sc_outbox`).Scan(&count); err != nil {
			return err
		}
		if count != 1 {
			t.Errorf("_sc_outbox tem %d linha(s), esperado 1 — a repetição não deveria duplicar o evento", count)
		}
		return nil
	}); err != nil {
		t.Fatalf("verificação: %v", err)
	}
}

func TestUnknownAction_ReturnsExplicitError(t *testing.T) {
	db, tenant, tableID, evaluator := triggerFixture(t)
	ctx := context.Background()

	if err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		_, err := CreateTrigger(ctx, tx, Trigger{TableID: tableID, When: WhenInsert, Action: "nao_existe"})
		return err
	}); err != nil {
		t.Fatalf("CreateTrigger: %v", err)
	}

	d := &Dispatcher{Expression: evaluator, Actions: map[string]ActionFunc{}}

	err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		_, err := records.CreateRecord(ctx, tx, identity.RoleAdmin, "posts", map[string]any{"title": "x"}, d.HooksFor(tenant, nil))
		return err
	})
	if !errors.Is(err, ErrUnknownAction) {
		t.Fatalf("err = %v, esperado ErrUnknownAction", err)
	}
}

// TestOnlyIf_LegacyOwner_FailsClosed prova que uma condição que não pode
// ser avaliada (capacidade de expressão ainda não é Go para o tenant)
// nunca decide silenciosamente nada — a escrita inteira falha de forma
// explícita, em vez de disparar ou deixar de disparar sem aviso.
func TestOnlyIf_LegacyOwner_FailsClosed(t *testing.T) {
	db, tenant, tableID, evaluator := triggerFixture(t)
	ctx := context.Background()

	if err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		_, err := CreateTrigger(ctx, tx, Trigger{TableID: tableID, When: WhenInsert, Action: "noop", OnlyIf: "true"})
		return err
	}); err != nil {
		t.Fatalf("CreateTrigger: %v", err)
	}

	// Um Evaluator com o MESMO Client mas uma Guard NUNCA trocada para
	// OwnerGo — a mesma capacidade plugins.expr, mas para uma guard "fria"
	// — reproduz o estado real de um tenant onde a expressão nunca foi
	// cortada para Go.
	coldEvaluator := &expression.Evaluator{Client: evaluator.Client, Guard: cutover.NewGuard()}

	d := &Dispatcher{
		Expression: coldEvaluator,
		Actions: map[string]ActionFunc{"noop": func(ctx context.Context, tx pgx.Tx, table metadata.Table, row map[string]any, config map[string]any) error {
			return nil
		}},
	}

	err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		_, err := records.CreateRecord(ctx, tx, identity.RoleAdmin, "posts", map[string]any{"title": "x"}, d.HooksFor(tenant, nil))
		return err
	})
	if err == nil {
		t.Fatal("esperado erro — only_if não deveria ser avaliável com uma capacidade de expressão nunca trocada para Go")
	}
}
