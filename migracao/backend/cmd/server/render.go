// Rota de renderização de views (GO-020, estendida em GO-039) — o
// consumidor HTTP de internal/views/{render,show,edit}.go. Não é uma
// página HTML: devolve o DTO (dados já resolvidos) que o BFF/React
// desenham, a mesma divisão de responsabilidade já usada pelo restante da
// pilha (Go decide o QUÊ, o frontend decide o COMO desenhar). Uma única
// rota (GET .../views/{id}/render) atende os três templates suportados
// (List/Show/Edit) — o handler lê a view uma vez para saber o Template e
// despacha para o Compile* correspondente; ?record= é obrigatório para
// Show, opcional para Edit (ausente = registro novo), ignorado por List.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"

	"github.com/jackc/pgx/v5"

	"github.com/vjuliani/saltcorn/migracao/backend/internal/platform/database"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/platform/outbox"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/platform/shutdown"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/platform/tenancy"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/views"
)

type renderColumnResponse struct {
	Kind        string `json:"kind"`
	FieldName   string `json:"field_name,omitempty"`
	HeaderLabel string `json:"header_label,omitempty"`
	ActionName  string `json:"action_name,omitempty"`
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
		columns = append(columns, renderColumnResponse{
			Kind: string(c.Kind), FieldName: c.FieldName, HeaderLabel: c.HeaderLabel, ActionName: c.ActionName,
		})
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

type renderShowResponse struct {
	ViewID   int                    `json:"view_id"`
	Table    string                 `json:"table"`
	RecordID int                    `json:"record_id"`
	Columns  []renderColumnResponse `json:"columns"`
	Values   map[string]any         `json:"values"`
}

func showPlanToResponse(plan *views.ShowPlan) renderShowResponse {
	columns := make([]renderColumnResponse, 0, len(plan.Columns))
	for _, c := range plan.Columns {
		columns = append(columns, renderColumnResponse{Kind: "field", FieldName: c.FieldName, HeaderLabel: c.HeaderLabel})
	}
	values := plan.Values
	if values == nil {
		values = map[string]any{}
	}
	return renderShowResponse{ViewID: plan.ViewID, Table: plan.Table, RecordID: plan.RecordID, Columns: columns, Values: values}
}

type editFieldOptionResponse struct {
	ID    int    `json:"id"`
	Label string `json:"label"`
}

type editFieldResponse struct {
	FieldName string                    `json:"field_name"`
	Label     string                    `json:"label"`
	FieldType string                    `json:"field_type"`
	Fieldview string                    `json:"fieldview"`
	Required  bool                      `json:"required"`
	Config    map[string]any            `json:"config,omitempty"`
	Value     any                       `json:"value"`
	Options   []editFieldOptionResponse `json:"options,omitempty"`
}

type renderNestedEditResponse struct {
	ViewID     int                  `json:"view_id"`
	ViewName   string               `json:"view_name"`
	ChildTable string               `json:"child_table"`
	FKField    string               `json:"fk_field"`
	ParentID   int                  `json:"parent_id"`
	Rows       []renderEditResponse `json:"rows"`
}

type renderEditResponse struct {
	ViewID     int                 `json:"view_id"`
	Table      string              `json:"table"`
	RecordID   int                 `json:"record_id"`
	Version    string              `json:"_version,omitempty"`
	Fields     []editFieldResponse `json:"fields"`
	ActionName string              `json:"action_name"`
	// Nested (GO-051) são as views Edit embutidas via nó de layout
	// `type: "view"` — ausente/vazio quando esta view não embute nenhuma
	// outra, ou quando RecordID == 0 (ver views.resolveNestedEditPlans).
	Nested []renderNestedEditResponse `json:"nested,omitempty"`
}

func editPlanToResponse(plan *views.EditPlan) renderEditResponse {
	fields := make([]editFieldResponse, 0, len(plan.Fields))
	for _, f := range plan.Fields {
		options := make([]editFieldOptionResponse, 0, len(f.Options))
		for _, o := range f.Options {
			options = append(options, editFieldOptionResponse{ID: o.ID, Label: o.Label})
		}
		fields = append(fields, editFieldResponse{
			FieldName: f.FieldName, Label: f.Label, FieldType: string(f.FieldType), Fieldview: f.Fieldview,
			Required: f.Required, Config: f.FieldConfig, Value: f.Value, Options: options,
		})
	}
	nested := make([]renderNestedEditResponse, 0, len(plan.Nested))
	for _, n := range plan.Nested {
		rows := make([]renderEditResponse, 0, len(n.Rows))
		for _, row := range n.Rows {
			rowCopy := row
			rows = append(rows, editPlanToResponse(&rowCopy))
		}
		nested = append(nested, renderNestedEditResponse{
			ViewID: n.ViewID, ViewName: n.ViewName, ChildTable: n.ChildTable, FKField: n.FKField, ParentID: n.ParentID, Rows: rows,
		})
	}
	return renderEditResponse{
		ViewID: plan.ViewID, Table: plan.Table, RecordID: plan.RecordID, Version: plan.Version,
		Fields: fields, ActionName: plan.ActionName, Nested: nested,
	}
}

type renderFeedCardResponse struct {
	RecordID int                `json:"record_id"`
	Show     renderShowResponse `json:"show"`
}

type renderFeedResponse struct {
	ViewID           int                      `json:"view_id"`
	Table            string                   `json:"table"`
	Cards            []renderFeedCardResponse `json:"cards"`
	ViewToCreateID   int                      `json:"view_to_create_id,omitempty"`
	ViewToCreateName string                   `json:"view_to_create_name,omitempty"`
	NextCursor       *string                  `json:"next_cursor"`
}

func feedPlanToResponse(plan *views.FeedPlan) renderFeedResponse {
	cards := make([]renderFeedCardResponse, 0, len(plan.Cards))
	for _, c := range plan.Cards {
		showCopy := c.Show
		cards = append(cards, renderFeedCardResponse{RecordID: c.RecordID, Show: showPlanToResponse(&showCopy)})
	}
	return renderFeedResponse{
		ViewID: plan.ViewID, Table: plan.Table, Cards: cards,
		ViewToCreateID: plan.ViewToCreateID, ViewToCreateName: plan.ViewToCreateName,
	}
}

// renderViewHandler implementa GET /v1/tenants/{tenant}/views/{id}/render
// para os três templates suportados. Reaproveita a mesma convenção de
// paginação por cursor opaco (base64 de um offset) de listRecordsHandler
// (GO-017) para List — decisão de implementação, não parte de nenhum
// contrato novo.
func renderViewHandler(tracker *shutdown.Tracker, db *database.DB) http.HandlerFunc {
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

		var resp any
		var nextCursorFor *int // só List usa paginação por cursor
		err = db.WithTenant(r.Context(), tenant, func(ctx context.Context, tx pgx.Tx) error {
			role, ok := resolveActorRole(ctx, tx, r, w)
			if !ok {
				return errHandled
			}
			// Uma leitura extra e barata (uma linha por id) para saber o
			// Template antes de despachar — cada Compile* também chama
			// views.GetView por conta própria (a mesma checagem de
			// autorização de leitura precisa valer isoladamente para cada
			// chamada pública dessas funções, não só quando vêm daqui).
			v, err := views.GetView(ctx, tx, role, id)
			if err != nil {
				return err
			}
			switch v.Template {
			case "List":
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
				plan, hasMore, err := views.CompileListPlan(ctx, tx, role, id, limit, offset)
				if err != nil {
					return err
				}
				listResp := listPlanToResponse(plan)
				if hasMore {
					next := offset + limit
					nextCursorFor = &next
				}
				resp = listResp
			case "Show":
				recordID, convErr := strconv.Atoi(r.URL.Query().Get("record"))
				if convErr != nil {
					writeAPIError(w, http.StatusBadRequest, "record_required", "parâmetro ?record= é obrigatório e precisa ser um id válido para o template Show")
					return errHandled
				}
				plan, err := views.CompileShowPlan(ctx, tx, role, id, recordID)
				if err != nil {
					return err
				}
				resp = showPlanToResponse(plan)
			case "Edit":
				recordID := 0
				if raw := r.URL.Query().Get("record"); raw != "" {
					recordID, convErr = strconv.Atoi(raw)
					if convErr != nil {
						writeAPIError(w, http.StatusBadRequest, "invalid_record", "parâmetro ?record= precisa ser um id válido")
						return errHandled
					}
				}
				plan, err := views.CompileEditPlan(ctx, tx, role, id, recordID)
				if err != nil {
					return err
				}
				resp = editPlanToResponse(plan)
			case "Feed":
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
				plan, hasMore, err := views.CompileFeedPlan(ctx, tx, role, id, limit, offset)
				if err != nil {
					return err
				}
				feedResp := feedPlanToResponse(plan)
				if hasMore {
					next := offset + limit
					nextCursorFor = &next
				}
				resp = feedResp
			default:
				return &views.UnsupportedLayoutError{Reason: "template não suportado neste runtime"}
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
		if listResp, ok := resp.(renderListResponse); ok && nextCursorFor != nil {
			next := encodeCursor(*nextCursorFor)
			listResp.NextCursor = &next
			resp = listResp
		}
		if feedResp, ok := resp.(renderFeedResponse); ok && nextCursorFor != nil {
			next := encodeCursor(*nextCursorFor)
			feedResp.NextCursor = &next
			resp = feedResp
		}
		writeJSON(w, http.StatusOK, resp)
	}
}

type submitViewRequest struct {
	RecordID int            `json:"record_id"`
	Version  string         `json:"_version"`
	Values   map[string]any `json:"values"`
}

type navigateResponse struct {
	Type     string `json:"type"`
	ViewName string `json:"view_name,omitempty"`
}

type submitViewResponse struct {
	Record   map[string]any   `json:"record"`
	Navigate navigateResponse `json:"navigate"`
}

// submitViewHandler implementa POST /v1/tenants/{tenant}/views/{id}/submit
// — o `form_action` real por trás do botão "Salvar"/"SubmitWithAjax" de
// uma view Edit (GO-039): cria (record_id ausente/0) ou atualiza
// (record_id presente, exigindo _version) o registro, e devolve a decisão
// de navegação (`navigate`) calculada a partir de destination_type.
// Idempotência via outbox.Do — mesma convenção de createViewHandler:
// criar não é idempotente por definição própria (ao contrário de
// createTable/addField), e mesmo update/delete se beneficiam do dedup
// para nunca duplicar um efeito colateral futuro.
func submitViewHandler(tracker *shutdown.Tracker, db *database.DB) http.HandlerFunc {
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
		var req submitViewRequest
		if err := json.Unmarshal(body, &req); err != nil {
			writeAPIError(w, http.StatusBadRequest, "invalid_json", "corpo da requisição não é um JSON válido")
			return
		}
		if req.RecordID != 0 && req.Version == "" {
			writeAPIError(w, http.StatusBadRequest, "version_required", "_version é obrigatório ao atualizar um registro existente")
			return
		}

		var payload map[string]any
		_ = json.Unmarshal(body, &payload)

		var result any
		err = db.WithTenant(r.Context(), tenant, func(ctx context.Context, tx pgx.Tx) error {
			role, ok := resolveActorRole(ctx, tx, r, w)
			if !ok {
				return errHandled
			}
			doResult, _, doErr := outbox.Do(ctx, tx, idempotencyKey, payload,
				func(ctx context.Context, tx pgx.Tx) (any, []outbox.Event, error) {
					submitted, err := views.SubmitEditView(ctx, tx, role, id, req.RecordID, req.Version, req.Values)
					if err != nil {
						return nil, nil, err
					}
					return submitViewResponse{
						Record: submitted.Record,
						Navigate: navigateResponse{
							Type: submitted.Navigate.Type, ViewName: submitted.Navigate.ViewName,
						},
					}, nil, nil
				})
			if doErr != nil {
				return doErr
			}
			result = doResult
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
			writeViewsOrMetadataError(w, err)
			return
		}
		status := http.StatusOK
		if req.RecordID == 0 {
			status = http.StatusCreated
		}
		writeJSON(w, status, result)
	}
}

// deleteViewRowHandler implementa
// DELETE /v1/tenants/{tenant}/views/{id}/rows/{recordId} — a ação de
// coluna "Delete" de uma view List (GO-039). expectedVersion vem de
// `?version=`, seguindo a convenção REST de que DELETE não carrega corpo
// nesta API (diferente de submitViewHandler/updateViewHandler, que
// escrevem `_version` no corpo — aqui não há nenhum outro campo a
// transportar).
func deleteViewRowHandler(tracker *shutdown.Tracker, db *database.DB) http.HandlerFunc {
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
		recordID, convErr := strconv.Atoi(r.PathValue("recordId"))
		if convErr != nil {
			writeAPIError(w, http.StatusBadRequest, "invalid_record_id", "id de registro inválido")
			return
		}
		if db == nil {
			writeAPIError(w, http.StatusBadGateway, "database_unavailable", "banco não configurado nesta instância")
			return
		}
		expectedVersion := r.URL.Query().Get("version")
		if expectedVersion == "" {
			writeAPIError(w, http.StatusBadRequest, "version_required", "parâmetro ?version= é obrigatório")
			return
		}

		err = db.WithTenant(r.Context(), tenant, func(ctx context.Context, tx pgx.Tx) error {
			role, ok := resolveActorRole(ctx, tx, r, w)
			if !ok {
				return errHandled
			}
			return views.DeleteListRow(ctx, tx, role, id, recordID, expectedVersion)
		})
		if err != nil {
			if errors.Is(err, errHandled) {
				return
			}
			writeViewsOrMetadataError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}
