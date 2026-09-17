// Rota de renderização de views (GO-020) — o primeiro consumidor real das
// regras de execução de internal/views/render.go. Não é uma página HTML:
// devolve o DTO (colunas resolvidas + linhas + paginação) que o BFF/React
// desenham, a mesma divisão de responsabilidade já usada pelo restante da
// pilha (Go decide o QUÊ, o frontend decide o COMO desenhar).
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
	"github.com/vjuliani/saltcorn/migracao/backend/internal/views"
)

type renderColumnResponse struct {
	FieldName   string `json:"field_name"`
	HeaderLabel string `json:"header_label"`
}

type renderListResponse struct {
	ViewID     int                    `json:"view_id"`
	Columns    []renderColumnResponse `json:"columns"`
	Rows       []map[string]any       `json:"rows"`
	OrderBy    string                 `json:"order_by"`
	Descending bool                   `json:"descending"`
	NextCursor *string                `json:"next_cursor"`
}

func listPlanToResponse(plan *views.ListPlan) renderListResponse {
	columns := make([]renderColumnResponse, 0, len(plan.Columns))
	for _, c := range plan.Columns {
		columns = append(columns, renderColumnResponse{FieldName: c.FieldName, HeaderLabel: c.HeaderLabel})
	}
	rows := plan.Rows
	if rows == nil {
		rows = []map[string]any{}
	}
	return renderListResponse{
		ViewID:     plan.ViewID,
		Columns:    columns,
		Rows:       rows,
		OrderBy:    plan.OrderBy,
		Descending: plan.Descending,
	}
}

// renderListHandler implementa GET /v1/tenants/{tenant}/views/{id}/render.
// Reaproveita a mesma convenção de paginação por cursor opaco (base64 de
// um offset) de listRecordsHandler (GO-017) — decisão de implementação,
// não parte de nenhum contrato novo.
func renderListHandler(tracker *shutdown.Tracker, db *database.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		end, err := tracker.Begin()
		if err != nil {
			http.Error(w, err.Error(), http.StatusServiceUnavailable)
			return
		}
		defer end()

		tenant, _ := tenancy.TenantFromContext(r.Context())
		id, convErr := strconv.Atoi(r.PathValue("id"))
		if convErr != nil {
			writeAPIError(w, http.StatusBadRequest, "invalid_id", "id inválido")
			return
		}
		if db == nil {
			writeAPIError(w, http.StatusBadGateway, "database_unavailable", "banco não configurado nesta instância")
			return
		}

		limit := recordsListDefaultLimit
		if raw := r.URL.Query().Get("limit"); raw != "" {
			if n, err := strconv.Atoi(raw); err == nil && n > 0 {
				limit = n
			}
		}
		if limit > recordsListMaxLimit {
			limit = recordsListMaxLimit
		}
		offset := decodeCursor(r.URL.Query().Get("cursor"))

		var resp renderListResponse
		err = db.WithTenant(r.Context(), tenant, func(ctx context.Context, tx pgx.Tx) error {
			role, ok := resolveActorRole(ctx, tx, r, w)
			if !ok {
				return errHandled
			}
			plan, hasMore, err := views.CompileListPlan(ctx, tx, role, id, limit, offset)
			if err != nil {
				return err
			}
			resp = listPlanToResponse(plan)
			if hasMore {
				next := encodeCursor(offset + limit)
				resp.NextCursor = &next
			}
			return nil
		})
		if err != nil {
			if errors.Is(err, errHandled) {
				return
			}
			writeViewsOrMetadataError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, resp)
	}
}
