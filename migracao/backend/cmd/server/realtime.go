// Rota HTTP de GO-028: GET .../realtime/events — o único ponto de contato
// entre o backend Go e a camada de tempo real, que vive inteiramente no
// BFF Node.js (ADR-0003/ADR-0007, ver docs/migracao-go/execucoes/GO-028.md).
// O BFF faz polling curto desta rota, uma vez por socket conectado (o
// `sub` da identidade delegada é o MESMO ator do socket, resolvido da
// sessão de navegador do BFF) — o filtro por audience/destinatário já
// roda inteiramente em internal/realtime.ListSinceForActor, então o corpo
// desta resposta é exatamente o que aquele ator deveria receber, sem o
// BFF precisar reinterpretar audience/rooms.
package main

import (
	"context"
	"errors"
	"net/http"
	"strconv"

	"github.com/jackc/pgx/v5"

	"github.com/vjuliani/saltcorn/migracao/backend/internal/platform/database"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/platform/shutdown"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/platform/tenancy"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/realtime"
)

const (
	realtimeEventsDefaultLimit = 50
	realtimeEventsMaxLimit     = 200
)

type realtimeEventDTO struct {
	ID       int64          `json:"id"`
	Audience string         `json:"audience"`
	Payload  map[string]any `json:"payload"`
}

// realtimeEventsHandler implementa GET /v1/tenants/{tenant}/realtime/events
// (extensão de GO-028 a internal-api.yaml). `after` é o cursor opaco de
// retomada (ver README de GO-028): 0 (ou ausente) lê desde o início da
// janela de retenção atual. `next_after` na resposta é sempre o maior id
// devolvido (ou o `after` recebido, se a página vier vazia) — o BFF só
// precisa guardar esse valor e reenviar como `after` na próxima
// requisição, nunca precisa entender o significado do id em si.
func realtimeEventsHandler(tracker *shutdown.Tracker, db *database.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		end, err := tracker.Begin()
		if err != nil {
			http.Error(w, err.Error(), http.StatusServiceUnavailable)
			return
		}
		defer end()

		tenant, _ := tenancy.TenantFromContext(r.Context())
		if db == nil {
			writeAPIError(w, http.StatusBadGateway, "database_unavailable", "banco não configurado nesta instância")
			return
		}

		var afterID int64
		if raw := r.URL.Query().Get("after"); raw != "" {
			if n, err := strconv.ParseInt(raw, 10, 64); err == nil && n >= 0 {
				afterID = n
			}
		}
		limit := realtimeEventsDefaultLimit
		if raw := r.URL.Query().Get("limit"); raw != "" {
			if n, err := strconv.Atoi(raw); err == nil && n > 0 {
				limit = n
			}
		}
		if limit > realtimeEventsMaxLimit {
			limit = realtimeEventsMaxLimit
		}

		type page struct {
			Items     []realtimeEventDTO `json:"items"`
			NextAfter int64              `json:"next_after"`
		}
		resp := page{NextAfter: afterID}

		err = db.WithTenant(r.Context(), tenant, func(ctx context.Context, tx pgx.Tx) error {
			_, ok := resolveActorRole(ctx, tx, r, w)
			if !ok {
				return errHandled
			}
			actorSub, _ := tenancy.ActorFromContext(ctx)
			actorID, err := strconv.Atoi(actorSub)
			if err != nil {
				writeAPIError(w, http.StatusForbidden, "actor_invalid", "identidade delegada com sub inválido")
				return errHandled
			}

			events, err := realtime.ListSinceForActor(ctx, tx, afterID, actorID, limit)
			if err != nil {
				return err
			}
			resp.Items = make([]realtimeEventDTO, len(events))
			for i, ev := range events {
				resp.Items[i] = realtimeEventDTO{ID: ev.ID, Audience: string(ev.Audience), Payload: ev.Payload}
				resp.NextAfter = ev.ID
			}
			return nil
		})
		if err != nil {
			if errors.Is(err, errHandled) {
				return
			}
			writeAPIError(w, http.StatusBadGateway, "database_error", "erro ao consultar eventos em tempo real")
			return
		}
		writeJSON(w, http.StatusOK, resp)
	}
}
