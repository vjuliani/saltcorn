// Rota de EVENTO NOMEADO (GO-052) — POST .../events/{eventname}, o
// mecanismo Go por trás de `Trigger.emitEvent`/`POST /api/emit-event` do
// legado (models/trigger.ts + routes/api.ts). Mesmo padrão de transporte
// já estabelecido em workflow.go: Idempotency-Key obrigatório envolvendo
// toda a operação via outbox.Do, resolveActorUser para o papel/identidade
// do ator, um erro sentinela por vez.
//
// Autorização por nome de evento, porta o modelo do legado (routes/
// api.ts:798-830) com uma simplificação real e documentada: o legado
// distingue usuário autenticado (config `mobile_emit_allowed_events`,
// exceto "ReceiveMobileShareData" sempre permitido) de usuário PÚBLICO
// não autenticado (config separada `mobile_emit_public_events`) — este
// runtime não tem um caminho de chamador verdadeiramente anônimo: toda
// requisição que chega até aqui já passou por tenancy.Middleware, que
// exige uma identidade delegada verificada (resolveActorUser sempre
// resolve um identity.User real ou já devolveu 403 antes de chegar
// aqui). `mobile_emit_public_events` fica FORA de escopo — não há
// chamador para exercitá-lo nesta arquitetura (o BFF resolve a sessão do
// navegador ANTES de delegar ao Go; um usuário de navegador não logado
// nunca gera uma identidade delegada Go).
package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/jackc/pgx/v5"

	"github.com/vjuliani/saltcorn/migracao/backend/internal/config"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/platform/database"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/platform/outbox"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/platform/shutdown"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/platform/tenancy"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/triggers"
)

// mobileEmitAllowedEventsConfigKey espelha `mobile_emit_allowed_events`
// do legado (models/config.ts) — um array JSON de nomes de evento
// permitidos para um ator autenticado além de "ReceiveMobileShareData"
// (sempre permitido, ver receiveMobileShareDataEventName abaixo).
const mobileEmitAllowedEventsConfigKey = "mobile_emit_allowed_events"

// receiveMobileShareDataEventName é o único nome de evento
// especial-cased pelo legado — sempre permitido para qualquer ator
// autenticado, independente de configuration (routes/api.ts: `eventname
// !== "ReceiveMobileShareData"`).
const receiveMobileShareDataEventName = "ReceiveMobileShareData"

type emitEventRequest struct {
	Payload map[string]any `json:"payload"`
}

type emitEventResponse struct {
	Fired int `json:"fired"`
}

// eventNameAllowed decide se actorRole pode emitir eventName — lê
// mobile_emit_allowed_events só quando necessário (eventName já sendo
// receiveMobileShareDataEventName nunca toca o catálogo de config,
// mesmo espírito de custo zero de shouldFire com only_if vazio).
func eventNameAllowed(ctx context.Context, tx pgx.Tx, eventName string) (bool, error) {
	if eventName == receiveMobileShareDataEventName {
		return true, nil
	}
	value, ok, err := config.Get(ctx, tx, mobileEmitAllowedEventsConfigKey)
	if err != nil {
		return false, err
	}
	if !ok {
		return false, nil
	}
	list, ok := value.([]any)
	if !ok {
		return false, nil
	}
	for _, item := range list {
		if name, ok := item.(string); ok && name == eventName {
			return true, nil
		}
	}
	return false, nil
}

// emitEventHandler implementa POST .../events/{eventname}. O corpo é
// `{"payload": {...}}` — deliberadamente SEM o `channel` do legado (um
// filtro secundário/pub-sub reaproveitado também para cron/hora-do-dia;
// nenhuma view real neste port precisa dele, e receive_share_trigger do
// pack piloto guitars usa `channel: null` — inventar suporte sem um
// segundo caso real para validar contra seria especulativo).
func emitEventHandler(tracker *shutdown.Tracker, db *database.DB, dispatcher *triggers.Dispatcher) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		end, err := tracker.Begin()
		if err != nil {
			http.Error(w, err.Error(), http.StatusServiceUnavailable)
			return
		}
		defer end()

		tenant, _ := tenancy.TenantFromContext(r.Context())
		eventName := r.PathValue("eventname")
		if eventName == "" {
			writeAPIError(w, http.StatusBadRequest, "invalid_event_name", "nome de evento inválido")
			return
		}
		if db == nil {
			writeAPIError(w, http.StatusBadGateway, "database_unavailable", "banco não configurado nesta instância")
			return
		}
		idempotencyKey := r.Header.Get("Idempotency-Key")
		if idempotencyKey == "" {
			writeAPIError(w, http.StatusBadRequest, "idempotency_key_required", "cabeçalho Idempotency-Key é obrigatório")
			return
		}
		body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
		if err != nil {
			writeAPIError(w, http.StatusBadRequest, "invalid_body", "não foi possível ler o corpo da requisição")
			return
		}
		var req emitEventRequest
		if len(body) > 0 {
			if err := json.Unmarshal(body, &req); err != nil {
				writeAPIError(w, http.StatusBadRequest, "invalid_json", "corpo da requisição não é um JSON válido")
				return
			}
		}
		payload := req.Payload
		if payload == nil {
			payload = map[string]any{}
		}

		var fired int
		err = db.WithTenant(r.Context(), tenant, func(ctx context.Context, tx pgx.Tx) error {
			role, ok := resolveActorRole(ctx, tx, r, w)
			if !ok {
				return errHandled
			}
			allowed, err := eventNameAllowed(ctx, tx, eventName)
			if err != nil {
				return err
			}
			if !allowed {
				writeAPIError(w, http.StatusForbidden, "event_not_allowed", "este ator não tem permissão para emitir este evento")
				return errHandled
			}
			result, _, doErr := outbox.Do(ctx, tx, idempotencyKey, payload,
				func(ctx context.Context, tx pgx.Tx) (any, []outbox.Event, error) {
					n, err := dispatcher.EmitEvent(ctx, tx, tenant, role, eventName, actorUserContext(ctx, role), payload)
					if err != nil {
						return nil, nil, err
					}
					return float64(n), nil, nil
				})
			if doErr != nil {
				return doErr
			}
			fired = int(result.(float64))
			return nil
		})
		if err != nil {
			if errors.Is(err, errHandled) {
				return
			}
			if errors.Is(err, outbox.ErrKeyConflict) {
				writeAPIError(w, http.StatusConflict, "idempotency_key_conflict", "Idempotency-Key já foi usada com um payload diferente")
				return
			}
			writeAPIError(w, http.StatusBadGateway, "event_dispatch_failed", err.Error())
			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(emitEventResponse{Fired: fired})
	}
}
