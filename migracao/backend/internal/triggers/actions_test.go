// Corpus de GO-029: catálogo de ações nativas nomeadas (actions.go)
// realmente disparadas via Dispatcher + records.CreateRecord — não só
// chamadas diretas às funções, para provar que o catálogo funciona
// integrado ao mecanismo de GO-024.
package triggers

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/vjuliani/saltcorn/migracao/backend/internal/identity"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/notify"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/records"
)

func countOutboxEvents(t *testing.T, ctx context.Context, tx pgx.Tx, eventType string) int {
	t.Helper()
	var count int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM _sc_outbox WHERE event_type = $1`, eventType).Scan(&count); err != nil {
		t.Fatalf("contar _sc_outbox: %v", err)
	}
	return count
}

func TestSendEmailAction_EnqueuesEmailWithInterpolatedFields(t *testing.T) {
	db, tenant, tableID, evaluator := triggerFixture(t)
	ctx := context.Background()

	if err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		_, err := CreateTrigger(ctx, tx, Trigger{
			TableID: tableID, When: WhenInsert, Action: ActionSendEmail,
			Configuration: map[string]any{"to": "autor@example.com", "subject": "Novo post: {{title}}", "body": "O post {{title}} foi criado"},
		})
		return err
	}); err != nil {
		t.Fatalf("CreateTrigger: %v", err)
	}

	d := &Dispatcher{Expression: evaluator, Actions: BuiltinActions()}
	if err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		_, err := records.CreateRecord(ctx, tx, identity.RoleAdmin, "posts", map[string]any{"title": "Olá mundo"}, d.HooksFor(tenant, identity.RoleAdmin, nil))
		return err
	}); err != nil {
		t.Fatalf("CreateRecord: %v", err)
	}

	if err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		if got := countOutboxEvents(t, ctx, tx, notify.EventTypeEmail); got != 1 {
			t.Errorf("_sc_outbox tem %d evento(s) de e-mail, esperado 1", got)
		}
		var payload []byte
		if err := tx.QueryRow(ctx, `SELECT payload_json FROM _sc_outbox WHERE event_type = $1`, notify.EventTypeEmail).Scan(&payload); err != nil {
			return err
		}
		body := string(payload)
		if !strings.Contains(body, "Novo post: Olá mundo") || !strings.Contains(body, "O post Olá mundo foi criado") {
			t.Errorf("payload do e-mail não contém os campos interpolados: %s", body)
		}
		return nil
	}); err != nil {
		t.Fatalf("verificação: %v", err)
	}
}

func TestWebhookAction_EnqueuesPostWithRowAsBody(t *testing.T) {
	db, tenant, tableID, evaluator := triggerFixture(t)
	ctx := context.Background()

	if err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		_, err := CreateTrigger(ctx, tx, Trigger{
			TableID: tableID, When: WhenInsert, Action: ActionWebhook,
			Configuration: map[string]any{"url": "https://example.com/hook"},
		})
		return err
	}); err != nil {
		t.Fatalf("CreateTrigger: %v", err)
	}

	d := &Dispatcher{Expression: evaluator, Actions: BuiltinActions()}
	if err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		_, err := records.CreateRecord(ctx, tx, identity.RoleAdmin, "posts", map[string]any{"title": "Webhook post"}, d.HooksFor(tenant, identity.RoleAdmin, nil))
		return err
	}); err != nil {
		t.Fatalf("CreateRecord: %v", err)
	}

	if err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		if got := countOutboxEvents(t, ctx, tx, notify.EventTypeWebhook); got != 1 {
			t.Errorf("_sc_outbox tem %d evento(s) de webhook, esperado 1", got)
		}
		var payload []byte
		if err := tx.QueryRow(ctx, `SELECT payload_json FROM _sc_outbox WHERE event_type = $1`, notify.EventTypeWebhook).Scan(&payload); err != nil {
			return err
		}
		body := string(payload)
		if !strings.Contains(body, "https://example.com/hook") || !strings.Contains(body, "Webhook post") {
			t.Errorf("payload do webhook não contém url/linha esperados: %s", body)
		}
		return nil
	}); err != nil {
		t.Fatalf("verificação: %v", err)
	}
}

func TestSendEmailAction_MissingTo_ReturnsErrorWithoutEnqueuing(t *testing.T) {
	db, tenant, tableID, evaluator := triggerFixture(t)
	ctx := context.Background()

	if err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		_, err := CreateTrigger(ctx, tx, Trigger{TableID: tableID, When: WhenInsert, Action: ActionSendEmail})
		return err
	}); err != nil {
		t.Fatalf("CreateTrigger: %v", err)
	}

	d := &Dispatcher{Expression: evaluator, Actions: BuiltinActions()}
	err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		_, err := records.CreateRecord(ctx, tx, identity.RoleAdmin, "posts", map[string]any{"title": "sem destinatário"}, d.HooksFor(tenant, identity.RoleAdmin, nil))
		return err
	})
	if !errors.Is(err, ErrActionConfigInvalid) {
		t.Fatalf("err = %v, esperado ErrActionConfigInvalid", err)
	}
}

func TestWebhookAction_MissingURL_ReturnsErrorWithoutEnqueuing(t *testing.T) {
	db, tenant, tableID, evaluator := triggerFixture(t)
	ctx := context.Background()

	if err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		_, err := CreateTrigger(ctx, tx, Trigger{TableID: tableID, When: WhenInsert, Action: ActionWebhook})
		return err
	}); err != nil {
		t.Fatalf("CreateTrigger: %v", err)
	}

	d := &Dispatcher{Expression: evaluator, Actions: BuiltinActions()}
	err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		_, err := records.CreateRecord(ctx, tx, identity.RoleAdmin, "posts", map[string]any{"title": "sem url"}, d.HooksFor(tenant, identity.RoleAdmin, nil))
		return err
	})
	if !errors.Is(err, ErrActionConfigInvalid) {
		t.Fatalf("err = %v, esperado ErrActionConfigInvalid", err)
	}
}

// TestSendEmailAction_TwoTriggersSameRowDifferentConfig_BothEnqueue prova
// que a chave de idempotência (actionIdempotencyKey) distingue triggers
// DIFERENTES sobre a MESMA linha — sem o hash da configuration na chave,
// o segundo `send_email` colidiria com o primeiro em outbox.Do e seria
// silenciosamente tratado como duplicata (mesmo `to`/`row["id"]`
// coincidindo por acaso não seria o caso aqui, mas o nome da ação e o id
// da linha sozinhos SERIAM iguais para os dois triggers).
func TestSendEmailAction_TwoTriggersSameRowDifferentConfig_BothEnqueue(t *testing.T) {
	db, tenant, tableID, evaluator := triggerFixture(t)
	ctx := context.Background()

	if err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		if _, err := CreateTrigger(ctx, tx, Trigger{
			TableID: tableID, When: WhenInsert, Action: ActionSendEmail,
			Configuration: map[string]any{"to": "admin@example.com", "subject": "Para admin"},
		}); err != nil {
			return err
		}
		_, err := CreateTrigger(ctx, tx, Trigger{
			TableID: tableID, When: WhenInsert, Action: ActionSendEmail,
			Configuration: map[string]any{"to": "dono@example.com", "subject": "Para dono"},
		})
		return err
	}); err != nil {
		t.Fatalf("CreateTrigger: %v", err)
	}

	d := &Dispatcher{Expression: evaluator, Actions: BuiltinActions()}
	if err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		_, err := records.CreateRecord(ctx, tx, identity.RoleAdmin, "posts", map[string]any{"title": "dois destinatários"}, d.HooksFor(tenant, identity.RoleAdmin, nil))
		return err
	}); err != nil {
		t.Fatalf("CreateRecord: %v", err)
	}

	if err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		if got := countOutboxEvents(t, ctx, tx, notify.EventTypeEmail); got != 2 {
			t.Errorf("_sc_outbox tem %d evento(s) de e-mail, esperado 2 (um por trigger, configs diferentes)", got)
		}
		return nil
	}); err != nil {
		t.Fatalf("verificação: %v", err)
	}
}

func TestInterpolate(t *testing.T) {
	row := map[string]any{"title": "Título", "count": 3}
	cases := []struct {
		template string
		want     string
	}{
		{"", ""},
		{"sem placeholder", "sem placeholder"},
		{"{{title}}", "Título"},
		{"{{title}} tem {{count}} itens", "Título tem 3 itens"},
		{"{{ausente}}", ""},
	}
	for _, c := range cases {
		if got := interpolate(c.template, row); got != c.want {
			t.Errorf("interpolate(%q) = %q, esperado %q", c.template, got, c.want)
		}
	}
}
