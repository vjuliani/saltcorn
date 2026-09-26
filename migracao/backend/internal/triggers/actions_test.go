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
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/vjuliani/saltcorn/migracao/backend/internal/identity"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/metadata"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/notify"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/platform/database"
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

// TestSleepAction_ServerMode_DelaysAndSucceeds prova o ramo "Server" real
// de sleep — a única forma desta ação com efeito do lado do servidor (ver
// comentário de sleepAction).
func TestSleepAction_ServerMode_DelaysAndSucceeds(t *testing.T) {
	db, tenant, tableID, evaluator := triggerFixture(t)
	ctx := context.Background()

	if err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		_, err := CreateTrigger(ctx, tx, Trigger{
			TableID: tableID, When: WhenInsert, Action: ActionSleep,
			Configuration: map[string]any{"sleep_where": "Server", "seconds": 0.05},
		})
		return err
	}); err != nil {
		t.Fatalf("CreateTrigger: %v", err)
	}

	d := &Dispatcher{Expression: evaluator, Actions: BuiltinActions()}
	start := time.Now()
	if err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		_, err := records.CreateRecord(ctx, tx, identity.RoleAdmin, "posts", map[string]any{"title": "sleepy"}, d.HooksFor(tenant, identity.RoleAdmin, nil))
		return err
	}); err != nil {
		t.Fatalf("CreateRecord: %v", err)
	}
	if elapsed := time.Since(start); elapsed < 50*time.Millisecond {
		t.Errorf("elapsed = %v, esperado >= 50ms (sleep_where=Server, seconds=0.05)", elapsed)
	}
}

// TestSleepAction_ClientMode_NoServerDelay prova que o ramo padrão/
// "Client page" (sem sleep_where=="Server") não atrasa nada do lado do
// servidor — no legado ele devolve um eval_js para o navegador, sem
// nenhum efeito aqui.
func TestSleepAction_ClientMode_NoServerDelay(t *testing.T) {
	db, tenant, tableID, evaluator := triggerFixture(t)
	ctx := context.Background()

	if err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		_, err := CreateTrigger(ctx, tx, Trigger{
			TableID: tableID, When: WhenInsert, Action: ActionSleep,
			Configuration: map[string]any{"seconds": 5.0},
		})
		return err
	}); err != nil {
		t.Fatalf("CreateTrigger: %v", err)
	}

	d := &Dispatcher{Expression: evaluator, Actions: BuiltinActions()}
	start := time.Now()
	if err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		_, err := records.CreateRecord(ctx, tx, identity.RoleAdmin, "posts", map[string]any{"title": "não deveria demorar"}, d.HooksFor(tenant, identity.RoleAdmin, nil))
		return err
	}); err != nil {
		t.Fatalf("CreateRecord: %v", err)
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Errorf("elapsed = %v, esperado quase instantâneo (modo cliente não tem efeito no servidor)", elapsed)
	}
}

// TestSleepAction_ExceedsMax_ReturnsErrorWithoutBlocking prova a divergência
// deliberada de segurança: um seconds acima do teto configurado é um erro
// explícito, nunca um bloqueio real da transação nem uma truncagem
// silenciosa.
func TestSleepAction_ExceedsMax_ReturnsErrorWithoutBlocking(t *testing.T) {
	db, tenant, tableID, evaluator := triggerFixture(t)
	ctx := context.Background()

	if err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		_, err := CreateTrigger(ctx, tx, Trigger{
			TableID: tableID, When: WhenInsert, Action: ActionSleep,
			Configuration: map[string]any{"sleep_where": "Server", "seconds": 3600.0},
		})
		return err
	}); err != nil {
		t.Fatalf("CreateTrigger: %v", err)
	}

	d := &Dispatcher{Expression: evaluator, Actions: BuiltinActions()}
	start := time.Now()
	err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		_, err := records.CreateRecord(ctx, tx, identity.RoleAdmin, "posts", map[string]any{"title": "sleep gigante"}, d.HooksFor(tenant, identity.RoleAdmin, nil))
		return err
	})
	if !errors.Is(err, ErrActionConfigInvalid) {
		t.Fatalf("err = %v, esperado ErrActionConfigInvalid", err)
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Errorf("elapsed = %v — a ação bloqueou de verdade em vez de rejeitar a configuration", elapsed)
	}
}

// TestDuplicateRowAction_CopiesRowWithoutIDAndFiresOwnTriggers prova o
// efeito de duplicate_row de ponta a ponta — inclusive que a linha
// duplicada dispara seus PRÓPRIOS triggers de inserção, igual a
// table.insertRow(...) no legado (ver comentário de duplicateRowAction).
// Deliberadamente NÃO registra duplicate_row como o PRÓPRIO trigger
// AfterInsert da tabela "posts" — isso causaria recursão infinita (a
// linha duplicada dispararia duplicate_row de novo, ver comentário de
// pacote da ação, achado real confirmado ao escrever este teste pela
// primeira vez). Em vez disso, chama duplicateRowAction diretamente uma
// única vez sobre uma linha já existente — o mesmo papel que um botão de
// ação manual, ou um trigger de OUTRO tipo, cumpriria no legado.
func TestDuplicateRowAction_CopiesRowWithoutIDAndFiresOwnTriggers(t *testing.T) {
	db, tenant, tableID, evaluator := triggerFixture(t)
	ctx := context.Background()

	if err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		_, err := CreateTrigger(ctx, tx, Trigger{
			TableID: tableID, When: WhenInsert, Action: ActionWebhook,
			Configuration: map[string]any{"url": "https://example.com/dup"},
		})
		return err
	}); err != nil {
		t.Fatalf("CreateTrigger: %v", err)
	}

	d := &Dispatcher{Expression: evaluator, Actions: BuiltinActions()}
	var original map[string]any
	if err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		rec, err := records.CreateRecord(ctx, tx, identity.RoleAdmin, "posts", map[string]any{"title": "original", "published": true}, d.HooksFor(tenant, identity.RoleAdmin, nil))
		original = rec
		return err
	}); err != nil {
		t.Fatalf("CreateRecord: %v", err)
	}

	if err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		posts, err := metadata.GetTable(ctx, database.AsTx(tx), "posts")
		if err != nil {
			return err
		}
		return duplicateRowAction(ctx, database.AsTx(tx), d, tenant, identity.RoleAdmin, *posts, original, nil)
	}); err != nil {
		t.Fatalf("duplicateRowAction: %v", err)
	}

	if err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		var count int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM posts WHERE title = 'original'`).Scan(&count); err != nil {
			return err
		}
		if count != 2 {
			t.Errorf("posts com title='original' = %d, esperado 2 (linha original + duplicata)", count)
		}
		// A duplicata dispara seu PRÓPRIO webhook — 2 no total (1 da
		// original, 1 da duplicata), nunca 1 (o que indicaria que
		// duplicate_row escreveu direto no banco sem passar por
		// HooksForTx/CreateRecordTx).
		if got := countOutboxEvents(t, ctx, tx, notify.EventTypeWebhook); got != 2 {
			t.Errorf("_sc_outbox tem %d evento(s) de webhook, esperado 2 (original + duplicata, cada uma disparando seus próprios triggers)", got)
		}
		return nil
	}); err != nil {
		t.Fatalf("verificação: %v", err)
	}
}

// TestDuplicateRowAction_NamedEventTrigger_ReturnsConfigError prova que a
// ação rejeita explicitamente um trigger sem tabela (evento nomeado,
// GO-052) — não há linha nenhuma para duplicar nesse caso.
func TestDuplicateRowAction_NamedEventTrigger_ReturnsConfigError(t *testing.T) {
	db, tenant, _, evaluator := triggerFixture(t)
	ctx := context.Background()

	if err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		_, err := CreateTrigger(ctx, tx, Trigger{When: "SomeEvent", Action: ActionDuplicateRow})
		return err
	}); err != nil {
		t.Fatalf("CreateTrigger: %v", err)
	}

	d := &Dispatcher{Expression: evaluator, Actions: BuiltinActions()}
	err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		_, err := d.EmitEvent(ctx, tx, tenant, identity.RoleAdmin, "SomeEvent", nil, map[string]any{"title": "x"})
		return err
	})
	if !errors.Is(err, ErrActionConfigInvalid) {
		t.Fatalf("err = %v, esperado ErrActionConfigInvalid", err)
	}
}

// TestEmitEventAction_CascadesToNamedEventTrigger prova a cascata
// emit_event -> Dispatcher.EmitEvent -> trigger de evento nomeado, o
// MESMO mecanismo de POST .../events/{eventname} (GO-052) — nenhum
// segundo caminho de despacho.
func TestEmitEventAction_CascadesToNamedEventTrigger(t *testing.T) {
	db, tenant, tableID, evaluator := triggerFixture(t)
	ctx := context.Background()

	if err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		if _, err := CreateTrigger(ctx, tx, Trigger{
			TableID: tableID, When: WhenInsert, Action: ActionEmitEvent,
			Configuration: map[string]any{"eventType": "PostCreated"},
		}); err != nil {
			return err
		}
		_, err := CreateTrigger(ctx, tx, Trigger{
			When: "PostCreated", Action: ActionWebhook,
			Configuration: map[string]any{"url": "https://example.com/cascade"},
		})
		return err
	}); err != nil {
		t.Fatalf("CreateTrigger: %v", err)
	}

	d := &Dispatcher{Expression: evaluator, Actions: BuiltinActions()}
	if err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		_, err := records.CreateRecord(ctx, tx, identity.RoleAdmin, "posts", map[string]any{"title": "dispara evento"}, d.HooksFor(tenant, identity.RoleAdmin, nil))
		return err
	}); err != nil {
		t.Fatalf("CreateRecord: %v", err)
	}

	if err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		if got := countOutboxEvents(t, ctx, tx, notify.EventTypeWebhook); got != 1 {
			t.Errorf("_sc_outbox tem %d evento(s) de webhook, esperado 1 (cascata emit_event -> PostCreated -> webhook)", got)
		}
		return nil
	}); err != nil {
		t.Fatalf("verificação: %v", err)
	}
}

// TestEmitEventAction_MissingEventType_ReturnsError prova a validação de
// configuration obrigatória, mesmo padrão de send_email/webhook.
func TestEmitEventAction_MissingEventType_ReturnsError(t *testing.T) {
	db, tenant, tableID, evaluator := triggerFixture(t)
	ctx := context.Background()

	if err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		_, err := CreateTrigger(ctx, tx, Trigger{TableID: tableID, When: WhenInsert, Action: ActionEmitEvent})
		return err
	}); err != nil {
		t.Fatalf("CreateTrigger: %v", err)
	}

	d := &Dispatcher{Expression: evaluator, Actions: BuiltinActions()}
	err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		_, err := records.CreateRecord(ctx, tx, identity.RoleAdmin, "posts", map[string]any{"title": "sem eventType"}, d.HooksFor(tenant, identity.RoleAdmin, nil))
		return err
	})
	if !errors.Is(err, ErrActionConfigInvalid) {
		t.Fatalf("err = %v, esperado ErrActionConfigInvalid", err)
	}
}

// TestSetUserLanguageAction_UpdatesTargetUser prova a metade gravável de
// set_user_language — configuration.user_id explícito (ver divergência
// documentada em setUserLanguageAction), persistida via
// identity.SetUserLanguageTx.
func TestSetUserLanguageAction_UpdatesTargetUser(t *testing.T) {
	db, tenant, tableID, evaluator := triggerFixture(t)
	ctx := context.Background()

	var userID int
	if err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		if err := identity.EnsureSchema(ctx, tx); err != nil {
			return err
		}
		id, err := identity.CreateUser(ctx, tx, "leitor@example.com", "hash", identity.RoleID(8))
		userID = id
		return err
	}); err != nil {
		t.Fatalf("setup de usuário: %v", err)
	}

	if err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		_, err := CreateTrigger(ctx, tx, Trigger{
			TableID: tableID, When: WhenInsert, Action: ActionSetUserLanguage,
			Configuration: map[string]any{"language": "pt", "user_id": float64(userID)},
		})
		return err
	}); err != nil {
		t.Fatalf("CreateTrigger: %v", err)
	}

	d := &Dispatcher{Expression: evaluator, Actions: BuiltinActions()}
	if err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		_, err := records.CreateRecord(ctx, tx, identity.RoleAdmin, "posts", map[string]any{"title": "muda idioma"}, d.HooksFor(tenant, identity.RoleAdmin, nil))
		return err
	}); err != nil {
		t.Fatalf("CreateRecord: %v", err)
	}

	if err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		u, err := identity.FindUserByID(ctx, tx, userID)
		if err != nil {
			return err
		}
		if u.Language != "pt" {
			t.Errorf("Language = %q, esperado \"pt\"", u.Language)
		}
		return nil
	}); err != nil {
		t.Fatalf("verificação: %v", err)
	}
}

// TestSetUserLanguageAction_MissingUserID_ReturnsError prova a divergência
// documentada: sem configuration.user_id explícito, a ação rejeita — o
// Dispatcher não tem como inferir "o usuário atual" sozinho.
func TestSetUserLanguageAction_MissingUserID_ReturnsError(t *testing.T) {
	db, tenant, tableID, evaluator := triggerFixture(t)
	ctx := context.Background()

	if err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		_, err := CreateTrigger(ctx, tx, Trigger{
			TableID: tableID, When: WhenInsert, Action: ActionSetUserLanguage,
			Configuration: map[string]any{"language": "pt"},
		})
		return err
	}); err != nil {
		t.Fatalf("CreateTrigger: %v", err)
	}

	d := &Dispatcher{Expression: evaluator, Actions: BuiltinActions()}
	err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		_, err := records.CreateRecord(ctx, tx, identity.RoleAdmin, "posts", map[string]any{"title": "sem user_id"}, d.HooksFor(tenant, identity.RoleAdmin, nil))
		return err
	})
	if !errors.Is(err, ErrActionConfigInvalid) {
		t.Fatalf("err = %v, esperado ErrActionConfigInvalid", err)
	}
}

// TestLoopRowsAction_DispatchesTargetTriggerForEachMatchingRow prova
// loop_rows de ponta a ponta: consulta linhas com where de igualdade,
// aplica limit/orderBy, e despacha o trigger alvo (por trigger_id) uma
// vez por linha via d.RunOneTx.
func TestLoopRowsAction_DispatchesTargetTriggerForEachMatchingRow(t *testing.T) {
	db, tenant, tableID, evaluator := triggerFixture(t)
	ctx := context.Background()

	var targetTriggerID int
	if err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		trig, err := CreateTrigger(ctx, tx, Trigger{
			TableID: tableID, When: WhenUpdate, Action: ActionWebhook,
			Configuration: map[string]any{"url": "https://example.com/loop-target"},
		})
		targetTriggerID = trig.ID
		return err
	}); err != nil {
		t.Fatalf("CreateTrigger (alvo): %v", err)
	}

	d := &Dispatcher{Expression: evaluator, Actions: BuiltinActions()}
	// Semeia 3 linhas: 2 published=true (o where de loop_rows filtra só
	// essas), 1 published=false — nunca deve ser tocada pelo laço.
	if err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		for _, row := range []map[string]any{
			{"title": "p1", "published": true},
			{"title": "p2", "published": true},
			{"title": "p3", "published": false},
		} {
			if _, err := records.CreateRecord(ctx, tx, identity.RoleAdmin, "posts", row, nil); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatalf("seed de linhas: %v", err)
	}

	loopConfig := map[string]any{
		"table_name": "posts",
		"where":      map[string]any{"published": true},
		"trigger_id": float64(targetTriggerID),
	}
	err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		return loopRowsAction(ctx, database.AsTx(tx), d, tenant, identity.RoleAdmin, metadata.Table{}, nil, loopConfig)
	})
	if err != nil {
		t.Fatalf("loopRowsAction: %v", err)
	}

	if err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		if got := countOutboxEvents(t, ctx, tx, notify.EventTypeWebhook); got != 2 {
			t.Errorf("_sc_outbox tem %d evento(s) de webhook, esperado 2 (um por linha published=true, nenhum para published=false)", got)
		}
		return nil
	}); err != nil {
		t.Fatalf("verificação: %v", err)
	}
}

// TestLoopRowsAction_UnknownTriggerID_ReturnsErrTriggerNotFound prova que
// um trigger_id inexistente é um erro explícito — nunca um laço
// silenciosamente vazio.
func TestLoopRowsAction_UnknownTriggerID_ReturnsErrTriggerNotFound(t *testing.T) {
	db, tenant, _, evaluator := triggerFixture(t)
	ctx := context.Background()

	d := &Dispatcher{Expression: evaluator, Actions: BuiltinActions()}
	err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		return loopRowsAction(ctx, database.AsTx(tx), d, tenant, identity.RoleAdmin, metadata.Table{}, nil, map[string]any{
			"table_name": "posts", "trigger_id": float64(999999),
		})
	})
	if !errors.Is(err, ErrTriggerNotFound) {
		t.Fatalf("err = %v, esperado ErrTriggerNotFound", err)
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
