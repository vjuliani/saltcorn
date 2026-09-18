package notify

import (
	"context"
	"net/http"

	"github.com/jackc/pgx/v5"

	"github.com/vjuliani/saltcorn/migracao/backend/internal/platform/outbox"
)

// Tipos de evento gravados em _sc_outbox (GO-014) — despachados por
// Handler, consumidos por outbox.ProcessPending (cmd/worker, mesmo job
// de processamento de outbox já existente desde GO-014).
const (
	EventTypeEmail   = "notify.email"
	EventTypeWebhook = "notify.webhook"
)

// EnqueueEmail grava um evento de e-mail na MESMA transação tx, via
// outbox.Do — key é a chave de idempotência: a MESMA key com o MESMO
// conteúdo nunca enfileira duas vezes (outbox.Do já garante isso,
// reaproveitado, não reinventado); a MESMA key com conteúdo DIFERENTE
// falha com outbox.ErrKeyConflict — "duplicatas externas têm política
// explícita" (critério de aceite) é este contrato.
func EnqueueEmail(ctx context.Context, tx pgx.Tx, key string, msg EmailMessage) error {
	payload := map[string]any{"to": msg.To, "subject": msg.Subject, "body": msg.Body}
	_, _, err := outbox.Do(ctx, tx, key, payload, func(ctx context.Context, tx pgx.Tx) (any, []outbox.Event, error) {
		return nil, []outbox.Event{{Type: EventTypeEmail, Payload: payload}}, nil
	})
	return err
}

// EnqueueWebhook grava um evento de webhook na MESMA transação tx — mesmo
// contrato de idempotência de EnqueueEmail.
func EnqueueWebhook(ctx context.Context, tx pgx.Tx, key string, req WebhookRequest) error {
	headers := make(map[string]any, len(req.Headers))
	for k, v := range req.Headers {
		headers[k] = v
	}
	payload := map[string]any{"method": req.Method, "url": req.URL, "headers": headers, "body": string(req.Body)}
	_, _, err := outbox.Do(ctx, tx, key, payload, func(ctx context.Context, tx pgx.Tx) (any, []outbox.Event, error) {
		return nil, []outbox.Event{{Type: EventTypeWebhook, Payload: payload}}, nil
	})
	return err
}

// Handler despacha um outbox.OutboxEvent para o remetente correto
// (e-mail ou webhook) — usado com outbox.ProcessPending (GO-014, já
// testado: retry com corte de tentativas, savepoint por evento). Um
// tipo de evento que não seja "notify.email"/"notify.webhook" é
// delegado a fallback (nunca descartado silenciosamente) — nil fallback
// não faz nada, mesmo comportamento de "sem consumidor real" já
// documentado para outros tipos desde GO-014/024/025.
func Handler(smtpCfg SMTPConfig, httpClient *http.Client, fallback outbox.Handler) outbox.Handler {
	return func(ctx context.Context, tx pgx.Tx, ev outbox.OutboxEvent) error {
		switch ev.Type {
		case EventTypeEmail:
			return handleEmailEvent(ctx, smtpCfg, ev)
		case EventTypeWebhook:
			return handleWebhookEvent(ctx, httpClient, ev)
		default:
			if fallback != nil {
				return fallback(ctx, tx, ev)
			}
			return nil
		}
	}
}

func handleEmailEvent(ctx context.Context, cfg SMTPConfig, ev outbox.OutboxEvent) error {
	toRaw, _ := ev.Payload["to"].([]any)
	to := make([]string, 0, len(toRaw))
	for _, v := range toRaw {
		if s, ok := v.(string); ok {
			to = append(to, s)
		}
	}
	subject, _ := ev.Payload["subject"].(string)
	body, _ := ev.Payload["body"].(string)
	return SendEmail(ctx, cfg, EmailMessage{To: to, Subject: subject, Body: body})
}

func handleWebhookEvent(ctx context.Context, client *http.Client, ev outbox.OutboxEvent) error {
	method, _ := ev.Payload["method"].(string)
	urlStr, _ := ev.Payload["url"].(string)
	bodyStr, _ := ev.Payload["body"].(string)
	headers := map[string]string{}
	if h, ok := ev.Payload["headers"].(map[string]any); ok {
		for k, v := range h {
			if s, ok := v.(string); ok {
				headers[k] = s
			}
		}
	}
	return SendWebhook(ctx, client, WebhookRequest{Method: method, URL: urlStr, Body: []byte(bodyStr), Headers: headers})
}
