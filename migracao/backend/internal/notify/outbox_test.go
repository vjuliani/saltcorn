// Corpus de enfileiramento/entrega exigido pelo critério de aceite de
// GO-026: "falha do provedor entra em retry e duplicatas externas têm
// política explícita" — contra Postgres real, reaproveitando
// internal/platform/outbox (GO-014) sem reinventar nada.
package notify

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/vjuliani/saltcorn/migracao/backend/internal/platform/outbox"
)

func TestEnqueueEmail_DuplicateKey_DoesNotDuplicateEvent(t *testing.T) {
	db, tenant := notifyFixture(t)
	ctx := context.Background()

	msg := EmailMessage{To: []string{"x@example.com"}, Subject: "assunto", Body: "corpo"}
	if err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		if err := EnqueueEmail(ctx, tx, "notif:1", msg); err != nil {
			return err
		}
		// Repetição deliberada da MESMA chamada — simula um retry de
		// nível superior (ex.: um trigger reexecutado).
		return EnqueueEmail(ctx, tx, "notif:1", msg)
	}); err != nil {
		t.Fatalf("EnqueueEmail (x2): %v", err)
	}

	if err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		var count int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM _sc_outbox WHERE event_type = $1`, EventTypeEmail).Scan(&count); err != nil {
			return err
		}
		if count != 1 {
			t.Errorf("_sc_outbox tem %d linha(s) de e-mail, esperado 1 — a repetição não deveria duplicar o evento", count)
		}
		return nil
	}); err != nil {
		t.Fatalf("verificação: %v", err)
	}
}

func TestEnqueueEmail_DuplicateKeyDifferentPayload_ReturnsConflict(t *testing.T) {
	db, tenant := notifyFixture(t)
	ctx := context.Background()

	err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		if err := EnqueueEmail(ctx, tx, "notif:2", EmailMessage{To: []string{"a@example.com"}, Subject: "a"}); err != nil {
			return err
		}
		return EnqueueEmail(ctx, tx, "notif:2", EmailMessage{To: []string{"b@example.com"}, Subject: "b"})
	})
	if !errors.Is(err, outbox.ErrKeyConflict) {
		t.Fatalf("err = %v, esperado outbox.ErrKeyConflict — mesma chave, conteúdo diferente", err)
	}
}

func TestHandler_DeliversEmailEvent_ViaProcessPending(t *testing.T) {
	db, tenant := notifyFixture(t)
	ctx := context.Background()

	smtpServer := &fakeSMTPServer{}
	host, port := startFakeSMTPServer(t, smtpServer)

	if err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		return EnqueueEmail(ctx, tx, "deliver:1", EmailMessage{To: []string{"dest@example.com"}, Subject: "entrega", Body: "conteudo"})
	}); err != nil {
		t.Fatalf("EnqueueEmail: %v", err)
	}

	handler := Handler(SMTPConfig{Host: host, Port: port, From: "remetente@example.com"}, nil, nil)
	var processed, failed int
	if err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		processed, failed, err = outbox.ProcessPending(ctx, tx, 10, 5, handler)
		return err
	}); err != nil {
		t.Fatalf("ProcessPending: %v", err)
	}
	if processed != 1 || failed != 0 {
		t.Fatalf("processed=%d failed=%d, esperado processed=1 failed=0", processed, failed)
	}

	_, rcptTo, body := smtpServer.snapshot()
	if len(rcptTo) != 1 {
		t.Fatalf("rcptTo = %v, esperado 1 destinatário", rcptTo)
	}
	if body == "" {
		t.Fatal("corpo do e-mail entregue está vazio")
	}
}

func TestHandler_DeliversWebhookEvent_ViaProcessPending(t *testing.T) {
	db, tenant := notifyFixture(t)
	ctx := context.Background()
	withFakePublicResolver(t)

	var receivedBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		buf := make([]byte, 1024)
		n, _ := r.Body.Read(buf)
		receivedBody = string(buf[:n])
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	if err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		return EnqueueWebhook(ctx, tx, "deliver:2", WebhookRequest{URL: srv.URL, Body: []byte(`{"evento":"teste"}`)})
	}); err != nil {
		t.Fatalf("EnqueueWebhook: %v", err)
	}

	handler := Handler(SMTPConfig{}, srv.Client(), nil)
	var processed, failed int
	if err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		processed, failed, err = outbox.ProcessPending(ctx, tx, 10, 5, handler)
		return err
	}); err != nil {
		t.Fatalf("ProcessPending: %v", err)
	}
	if processed != 1 || failed != 0 {
		t.Fatalf("processed=%d failed=%d, esperado processed=1 failed=0", processed, failed)
	}
	if receivedBody != `{"evento":"teste"}` {
		t.Fatalf("corpo recebido pelo webhook = %q, esperado %q", receivedBody, `{"evento":"teste"}`)
	}
}

// TestHandler_ProviderFailure_TriggersRetry prova "falha do provedor
// entra em retry": um handler que falha (aqui, SMTP inalcançável) faz
// outbox.ProcessPending marcar o evento para nova tentativa (status volta
// a 'pending', reaproveitando o mecanismo já testado desde GO-014) — não
// como concluído.
func TestHandler_ProviderFailure_TriggersRetry(t *testing.T) {
	db, tenant := notifyFixture(t)
	ctx := context.Background()

	if err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		return EnqueueEmail(ctx, tx, "deliver:3", EmailMessage{To: []string{"x@example.com"}, Subject: "vai falhar"})
	}); err != nil {
		t.Fatalf("EnqueueEmail: %v", err)
	}

	// Host que nunca resolve/conecta — simula uma falha real de provedor.
	handler := Handler(SMTPConfig{Host: "smtp.invalido.exemplo.nao-existe", Port: 25, From: "x@example.com"}, nil, nil)
	var processed, failed int
	if err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		processed, failed, err = outbox.ProcessPending(ctx, tx, 10, 5, handler)
		return err
	}); err != nil {
		t.Fatalf("ProcessPending: %v", err)
	}
	if processed != 0 || failed != 1 {
		t.Fatalf("processed=%d failed=%d, esperado processed=0 failed=1", processed, failed)
	}

	if err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		var status string
		var attempts int
		if err := tx.QueryRow(ctx, `SELECT status, attempts FROM _sc_outbox WHERE idempotency_key = $1`, "deliver:3").Scan(&status, &attempts); err != nil {
			return err
		}
		if status != "pending" {
			t.Errorf("status = %q, esperado \"pending\" (nova tentativa agendada, não descartado)", status)
		}
		if attempts != 1 {
			t.Errorf("attempts = %d, esperado 1", attempts)
		}
		return nil
	}); err != nil {
		t.Fatalf("verificação: %v", err)
	}
}

// TestHandler_UnknownEventType_UsesFallback prova que um tipo de evento
// que não seja e-mail/webhook nunca é descartado silenciosamente — cai
// no fallback fornecido pelo chamador (mesmo padrão já usado por
// cmd/worker.runOutboxJob desde GO-014 para eventos sem consumidor real).
func TestHandler_UnknownEventType_UsesFallback(t *testing.T) {
	db, tenant := notifyFixture(t)
	ctx := context.Background()

	if err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		_, _, err := outbox.Do(ctx, tx, "outro-tipo:1", map[string]any{"x": 1}, func(ctx context.Context, tx pgx.Tx) (any, []outbox.Event, error) {
			return nil, []outbox.Event{{Type: "outro.tipo", Payload: map[string]any{"x": 1}}}, nil
		})
		return err
	}); err != nil {
		t.Fatalf("outbox.Do: %v", err)
	}

	fallbackCalled := false
	handler := Handler(SMTPConfig{}, nil, func(ctx context.Context, tx pgx.Tx, ev outbox.OutboxEvent) error {
		fallbackCalled = true
		return nil
	})

	if err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		_, _, err := outbox.ProcessPending(ctx, tx, 10, 5, handler)
		return err
	}); err != nil {
		t.Fatalf("ProcessPending: %v", err)
	}
	if !fallbackCalled {
		t.Fatal("fallback não foi chamado para um tipo de evento desconhecido")
	}
}
