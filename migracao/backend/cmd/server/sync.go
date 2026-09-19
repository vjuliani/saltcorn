package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"

	"github.com/jackc/pgx/v5"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/metadata"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/platform/database"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/platform/outbox"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/platform/shutdown"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/platform/tenancy"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/records"
	syncengine "github.com/vjuliani/saltcorn/migracao/backend/internal/sync"
)

func syncExchangeHandler(tracker *shutdown.Tracker, db *database.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		end, err := tracker.Begin()
		if err != nil {
			writeAPIError(w, 503, "shutting_down", "serviço encerrando")
			return
		}
		defer end()
		if db == nil {
			writeAPIError(w, 503, "database_unavailable", "banco indisponível")
			return
		}
		var req syncengine.Request
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
		decoder.UseNumber()
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&req); err != nil {
			var sizeError *http.MaxBytesError
			if errors.As(err, &sizeError) {
				writeAPIError(w, 413, "payload_too_large", "requisição excede o limite")
			} else {
				writeAPIError(w, 400, "invalid_sync", "requisição inválida")
			}
			return
		}
		var trailing any
		if err := decoder.Decode(&trailing); err != io.EOF {
			writeAPIError(w, 400, "invalid_sync", "requisição inválida")
			return
		}
		tenant, _ := tenancy.TenantFromContext(r.Context())
		actor, _ := tenancy.ActorFromContext(r.Context())
		scope := syncengine.Scope{Tenant: string(tenant), Actor: actor, Table: r.PathValue("table")}
		var response *syncengine.Response
		err = db.WithTenant(r.Context(), tenant, func(ctx context.Context, tx pgx.Tx) error {
			role, ok := resolveActorRole(ctx, tx, r, w)
			if !ok {
				return errHandled
			}
			// RLS must use the current authenticated actor/role, resolved in this
			// transaction. Neither value is taken from the request body.
			if _, err := tx.Exec(ctx, "SELECT set_config('app.current_user_id', $1, true), set_config('app.current_user_role', $2, true)", actor, strconv.Itoa(int(role))); err != nil {
				return err
			}
			var err error
			response, err = syncengine.Exchange(ctx, database.AsTx(tx), role, scope, req)
			return err
		})
		if err != nil {
			switch {
			case errors.Is(err, errHandled):
				return
			case errors.Is(err, syncengine.ErrScope), errors.Is(err, records.ErrNotAuthorized):
				writeAPIError(w, 403, "sync_forbidden", "sincronização não autorizada")
			case errors.Is(err, syncengine.ErrVersion):
				writeAPIError(w, 400, "sync_version", "versão de protocolo não suportada")
			case errors.Is(err, syncengine.ErrInvalid):
				writeAPIError(w, 400, "invalid_sync", "requisição inválida")
			case errors.Is(err, syncengine.ErrSchema):
				writeAPIError(w, 409, "schema_changed", "atualize o schema local antes de enviar alterações")
			case errors.Is(err, outbox.ErrKeyConflict):
				writeAPIError(w, 409, "mutation_reused", "identificador reutilizado com conteúdo diferente")
			case errors.Is(err, syncengine.ErrTooLarge):
				writeAPIError(w, 413, "snapshot_too_large", "tabela excede o limite de sincronização offline")
			case errors.Is(err, metadata.ErrTableNotFound):
				writeAPIError(w, 404, "table_not_found", "tabela indisponível")
			default:
				writeAPIError(w, 502, "sync_failed", "falha ao sincronizar")
			}
			return
		}
		writeJSON(w, 200, response)
	}
}
