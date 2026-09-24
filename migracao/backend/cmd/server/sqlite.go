// Adapter SQLite ligado a cmd/server (GO-041) — "modo desktop"
// (internal/platform/sqlite, GO-030), mutuamente exclusivo com o Postgres
// multi-tenant no MESMO processo (main.go escolhe UM backend por
// instância, nunca os dois). Os handlers abaixo são um conjunto NOVO e
// SEPARADO dos handlers Postgres já existentes (records.go/views.go/
// render.go/tables.go) — nenhum deles é modificado por esta task, risco
// zero de regressão no caminho Postgres já testado e em uso.
//
// Escopo desta entrega, deliberadamente NARROW (ver
// docs/migracao-go/execucoes/GO-041.md para a decisão completa): tabelas/
// campos, registros (CRUD, SEM disparo de trigger) e views (CRUD +
// renderização List/Show/Edit + submit) — o mínimo real para "identidade
// e views funcionam via HTTP contra um tenant SQLite". `internal/triggers`,
// `internal/scheduler`, `internal/notify` e `internal/files` continuam
// 100% pgx.Tx (nunca convertidos para database.Tx) — nenhum destes é
// alcançável por nenhuma rota nesta entrega; os handlers de registro
// abaixo passam hooks=nil explicitamente, nunca fingem que um trigger
// dispararia. Ver GO-055 (a task de continuação já registrada) para
// fechar essa lacuna.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"

	"github.com/vjuliani/saltcorn/migracao/backend/internal/identity"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/metadata"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/platform/database"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/platform/outbox"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/platform/shutdown"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/platform/sqlite"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/platform/tenancy"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/records"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/views"
)

// resolveActorUserTx/resolveActorRoleTx são os equivalentes database.Tx
// de resolveActorUser/resolveActorRole (records.go) — mesma lógica,
// mesmas respostas de erro, só o tipo de Tx muda.
func resolveActorUserTx(ctx context.Context, tx database.Tx, r *http.Request, w http.ResponseWriter) (*identity.User, bool) {
	actorSub, _ := tenancy.ActorFromContext(ctx)
	actorID, err := strconv.Atoi(actorSub)
	if err != nil {
		writeAPIError(w, http.StatusForbidden, "actor_invalid", "identidade delegada com sub inválido")
		return nil, false
	}
	user, err := identity.FindUserByIDTx(ctx, tx, actorID)
	if err != nil {
		if errors.Is(err, identity.ErrUserNotFound) {
			writeAPIError(w, http.StatusForbidden, "actor_not_found", "ator da identidade delegada não existe neste tenant")
			return nil, false
		}
		writeAPIError(w, http.StatusBadGateway, "actor_lookup_failed", "falha ao resolver o papel do ator")
		return nil, false
	}
	_ = r
	return user, true
}

func resolveActorRoleTx(ctx context.Context, tx database.Tx, r *http.Request, w http.ResponseWriter) (identity.RoleID, bool) {
	user, ok := resolveActorUserTx(ctx, tx, r, w)
	if !ok {
		return 0, false
	}
	return user.RoleID, true
}

// sqliteGetActorHandler é o equivalente SQLite de getActorHandler — sem
// internal/config (também 100% pgx.Tx, fora de escopo aqui): DefaultLocale
// é sempre o fallback fixo, nunca uma preferência de tenant configurada
// via HTTP (a mesma simplificação documentada, não uma tentativa
// disfarçada de resolver locale de verdade).
func sqliteGetActorHandler(tracker *shutdown.Tracker, db *sqlite.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		end, err := tracker.Begin()
		if err != nil {
			http.Error(w, err.Error(), http.StatusServiceUnavailable)
			return
		}
		defer end()

		tenant, _ := tenancy.TenantFromContext(r.Context())
		var resp actorResponse
		err = db.WithTenant(r.Context(), tenant, func(ctx context.Context, tx database.Tx) error {
			user, ok := resolveActorUserTx(ctx, tx, r, w)
			if !ok {
				return errHandled
			}
			resp = actorResponse{ID: user.ID, RoleID: int(user.RoleID), Language: user.Language, DefaultLocale: defaultLocaleDefault}
			return nil
		})
		if err != nil {
			if errors.Is(err, errHandled) {
				return
			}
			writeAPIError(w, http.StatusBadGateway, "database_error", "erro ao consultar o ator")
			return
		}
		writeJSON(w, http.StatusOK, resp)
	}
}

func sqliteCreateTableHandler(tracker *shutdown.Tracker, db *sqlite.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		end, err := tracker.Begin()
		if err != nil {
			http.Error(w, err.Error(), http.StatusServiceUnavailable)
			return
		}
		defer end()

		tenant, _ := tenancy.TenantFromContext(r.Context())
		body, err := io.ReadAll(io.LimitReader(r.Body, 1<<16))
		if err != nil {
			writeAPIError(w, http.StatusBadRequest, "invalid_body", "não foi possível ler o corpo da requisição")
			return
		}
		var req createTableRequest
		if err := json.Unmarshal(body, &req); err != nil {
			writeAPIError(w, http.StatusBadRequest, "invalid_json", "corpo da requisição não é um JSON válido")
			return
		}
		if req.Name == "" {
			writeAPIError(w, http.StatusBadRequest, "invalid_input", "name é obrigatório")
			return
		}

		var resp tableResponse
		err = db.WithTenant(r.Context(), tenant, func(ctx context.Context, tx database.Tx) error {
			role, ok := resolveActorRoleTx(ctx, tx, r, w)
			if !ok {
				return errHandled
			}
			opts := metadata.TableOptions{}
			if req.MinRoleRead != nil {
				opts.MinRoleRead = identity.RoleID(*req.MinRoleRead)
			}
			if req.MinRoleWrite != nil {
				opts.MinRoleWrite = identity.RoleID(*req.MinRoleWrite)
			}
			if req.Versioned != nil {
				opts.Versioned = *req.Versioned
			}
			table, err := metadata.CreateTable(ctx, tx, role, req.Name, opts)
			if err != nil {
				return err
			}
			resp = tableToResponse(*table)
			return nil
		})
		if err != nil {
			if errors.Is(err, errHandled) {
				return
			}
			writeMetadataError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, resp)
	}
}

func sqliteAddFieldHandler(tracker *shutdown.Tracker, db *sqlite.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		end, err := tracker.Begin()
		if err != nil {
			http.Error(w, err.Error(), http.StatusServiceUnavailable)
			return
		}
		defer end()

		tenant, _ := tenancy.TenantFromContext(r.Context())
		tableName := r.PathValue("table")
		body, err := io.ReadAll(io.LimitReader(r.Body, 1<<16))
		if err != nil {
			writeAPIError(w, http.StatusBadRequest, "invalid_body", "não foi possível ler o corpo da requisição")
			return
		}
		var req addFieldRequest
		if err := json.Unmarshal(body, &req); err != nil {
			writeAPIError(w, http.StatusBadRequest, "invalid_json", "corpo da requisição não é um JSON válido")
			return
		}

		var resp fieldResponse
		err = db.WithTenant(r.Context(), tenant, func(ctx context.Context, tx database.Tx) error {
			role, ok := resolveActorRoleTx(ctx, tx, r, w)
			if !ok {
				return errHandled
			}
			table, err := metadata.GetTable(ctx, tx, tableName)
			if err != nil {
				return err
			}
			field, err := metadata.AddField(ctx, tx, role, table.ID, metadata.FieldDef{
				Name: req.Name, Type: metadata.FieldType(req.Type),
				Required: req.Required, Unique: req.Unique, References: req.References,
			})
			if err != nil {
				return err
			}
			resp = fieldToResponse(*field)
			return nil
		})
		if err != nil {
			if errors.Is(err, errHandled) {
				return
			}
			writeMetadataError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, resp)
	}
}

func sqliteListRecordsHandler(tracker *shutdown.Tracker, db *sqlite.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		end, err := tracker.Begin()
		if err != nil {
			http.Error(w, err.Error(), http.StatusServiceUnavailable)
			return
		}
		defer end()

		tenant, _ := tenancy.TenantFromContext(r.Context())
		table := r.PathValue("table")

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

		type page struct {
			Items      []map[string]any `json:"items"`
			NextCursor *string          `json:"next_cursor"`
		}
		var resp page

		err = db.WithTenant(r.Context(), tenant, func(ctx context.Context, tx database.Tx) error {
			role, ok := resolveActorRoleTx(ctx, tx, r, w)
			if !ok {
				return errHandled
			}
			rows, err := records.RowsTx(ctx, tx, role, records.Query{
				Table:   table,
				OrderBy: []records.OrderTerm{{Field: "id"}},
				Limit:   limit + 1,
				Offset:  offset,
			})
			if err != nil {
				return err
			}
			hasMore := len(rows) > limit
			if hasMore {
				rows = rows[:limit]
			}
			resp.Items = rows
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
			writeRecordsError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, resp)
	}
}

// sqliteCreateRecordHandler NUNCA passa hooks (nil) — internal/triggers
// continua pgx.Tx-only (fora de escopo desta task, ver cabeçalho do
// arquivo); um trigger real do tenant SQLite simplesmente não dispara
// aqui, documentado, nunca um comportamento fingido.
func sqliteCreateRecordHandler(tracker *shutdown.Tracker, db *sqlite.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		end, err := tracker.Begin()
		if err != nil {
			http.Error(w, err.Error(), http.StatusServiceUnavailable)
			return
		}
		defer end()

		tenant, _ := tenancy.TenantFromContext(r.Context())
		table := r.PathValue("table")
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
		var input map[string]any
		if err := json.Unmarshal(body, &input); err != nil {
			writeAPIError(w, http.StatusBadRequest, "invalid_json", "corpo da requisição não é um JSON válido")
			return
		}

		var created any
		err = db.WithTenant(r.Context(), tenant, func(ctx context.Context, tx database.Tx) error {
			role, ok := resolveActorRoleTx(ctx, tx, r, w)
			if !ok {
				return errHandled
			}
			result, _, doErr := outbox.DoTx(ctx, tx, idempotencyKey, input,
				func(ctx context.Context, tx database.Tx) (any, []outbox.Event, error) {
					rec, err := records.CreateRecordTx(ctx, tx, role, table, input, nil)
					if err != nil {
						return nil, nil, err
					}
					return rec, nil, nil
				})
			if doErr != nil {
				return doErr
			}
			created = result
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
			writeRecordsError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, created)
	}
}

func sqliteUpdateRecordHandler(tracker *shutdown.Tracker, db *sqlite.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		end, err := tracker.Begin()
		if err != nil {
			http.Error(w, err.Error(), http.StatusServiceUnavailable)
			return
		}
		defer end()

		tenant, _ := tenancy.TenantFromContext(r.Context())
		table := r.PathValue("table")
		id, convErr := strconv.Atoi(r.PathValue("id"))
		if convErr != nil {
			writeAPIError(w, http.StatusBadRequest, "invalid_id", "id inválido")
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
		var input map[string]any
		if err := json.Unmarshal(body, &input); err != nil {
			writeAPIError(w, http.StatusBadRequest, "invalid_json", "corpo da requisição não é um JSON válido")
			return
		}
		expectedVersion, _ := input["_version"].(string)
		if expectedVersion == "" {
			writeAPIError(w, http.StatusBadRequest, "version_required", "_version é obrigatório")
			return
		}
		values := make(map[string]any, len(input))
		for k, v := range input {
			if k == "_version" {
				continue
			}
			values[k] = v
		}

		var updated any
		err = db.WithTenant(r.Context(), tenant, func(ctx context.Context, tx database.Tx) error {
			role, ok := resolveActorRoleTx(ctx, tx, r, w)
			if !ok {
				return errHandled
			}
			result, _, doErr := outbox.DoTx(ctx, tx, idempotencyKey, input,
				func(ctx context.Context, tx database.Tx) (any, []outbox.Event, error) {
					rec, err := records.UpdateRecordTx(ctx, tx, role, table, id, expectedVersion, values, nil)
					if err != nil {
						return nil, nil, err
					}
					return rec, nil, nil
				})
			if doErr != nil {
				return doErr
			}
			updated = result
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
			writeRecordsError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, updated)
	}
}

func sqliteDeleteRecordHandler(tracker *shutdown.Tracker, db *sqlite.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		end, err := tracker.Begin()
		if err != nil {
			http.Error(w, err.Error(), http.StatusServiceUnavailable)
			return
		}
		defer end()

		tenant, _ := tenancy.TenantFromContext(r.Context())
		table := r.PathValue("table")
		id, convErr := strconv.Atoi(r.PathValue("id"))
		if convErr != nil {
			writeAPIError(w, http.StatusBadRequest, "invalid_id", "id inválido")
			return
		}
		expectedVersion := r.URL.Query().Get("version")
		if expectedVersion == "" {
			writeAPIError(w, http.StatusBadRequest, "version_required", "parâmetro ?version= é obrigatório")
			return
		}

		err = db.WithTenant(r.Context(), tenant, func(ctx context.Context, tx database.Tx) error {
			role, ok := resolveActorRoleTx(ctx, tx, r, w)
			if !ok {
				return errHandled
			}
			return records.DeleteRecordTx(ctx, tx, role, table, id, expectedVersion, nil)
		})
		if err != nil {
			if errors.Is(err, errHandled) {
				return
			}
			writeRecordsError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

func sqliteCreateViewHandler(tracker *shutdown.Tracker, db *sqlite.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		end, err := tracker.Begin()
		if err != nil {
			http.Error(w, err.Error(), http.StatusServiceUnavailable)
			return
		}
		defer end()

		tenant, _ := tenancy.TenantFromContext(r.Context())
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
		var req createViewRequest
		if err := json.Unmarshal(body, &req); err != nil {
			writeAPIError(w, http.StatusBadRequest, "invalid_json", "corpo da requisição não é um JSON válido")
			return
		}
		var payload map[string]any
		_ = json.Unmarshal(body, &payload)

		var created any
		err = db.WithTenant(r.Context(), tenant, func(ctx context.Context, tx database.Tx) error {
			role, ok := resolveActorRoleTx(ctx, tx, r, w)
			if !ok {
				return errHandled
			}
			table, err := metadata.GetTable(ctx, tx, req.TableName)
			if err != nil {
				return err
			}
			opts := views.ViewOptions{}
			if req.MinRole != nil {
				opts.MinRole = identity.RoleID(*req.MinRole)
			}
			result, _, doErr := outbox.DoTx(ctx, tx, idempotencyKey, payload,
				func(ctx context.Context, tx database.Tx) (any, []outbox.Event, error) {
					v, err := views.CreateViewTx(ctx, tx, role, req.Name, table.ID, req.Template, req.Configuration, opts)
					if err != nil {
						return nil, nil, err
					}
					return viewToResponse(v), nil, nil
				})
			if doErr != nil {
				return doErr
			}
			created = result
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
		writeJSON(w, http.StatusCreated, created)
	}
}

func sqliteListViewsHandler(tracker *shutdown.Tracker, db *sqlite.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		end, err := tracker.Begin()
		if err != nil {
			http.Error(w, err.Error(), http.StatusServiceUnavailable)
			return
		}
		defer end()

		tenant, _ := tenancy.TenantFromContext(r.Context())
		tableName := r.URL.Query().Get("table")

		var resp []viewResponse
		err = db.WithTenant(r.Context(), tenant, func(ctx context.Context, tx database.Tx) error {
			role, ok := resolveActorRoleTx(ctx, tx, r, w)
			if !ok {
				return errHandled
			}
			tableID := 0
			if tableName != "" {
				table, err := metadata.GetTable(ctx, tx, tableName)
				if err != nil {
					return err
				}
				tableID = table.ID
			}
			list, err := views.ListViewsTx(ctx, tx, role, tableID)
			if err != nil {
				return err
			}
			resp = make([]viewResponse, 0, len(list))
			for _, v := range list {
				resp = append(resp, viewToResponse(v))
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

func sqliteRenderViewHandler(tracker *shutdown.Tracker, db *sqlite.DB) http.HandlerFunc {
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

		var resp any
		var nextCursorFor *int
		err = db.WithTenant(r.Context(), tenant, func(ctx context.Context, tx database.Tx) error {
			role, ok := resolveActorRoleTx(ctx, tx, r, w)
			if !ok {
				return errHandled
			}
			v, err := views.GetViewTx(ctx, tx, role, id)
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
				plan, hasMore, err := views.CompileListPlanTx(ctx, tx, role, id, limit, offset)
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
				plan, err := views.CompileShowPlanTx(ctx, tx, role, id, recordID)
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
				plan, err := views.CompileEditPlanTx(ctx, tx, role, id, recordID)
				if err != nil {
					return err
				}
				resp = editPlanToResponse(plan)
			default:
				return &views.UnsupportedLayoutError{Reason: "template " + v.Template + " não suportado neste caminho SQLite (GO-041, escopo narrow — só List/Show/Edit)"}
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
		if nextCursorFor != nil {
			if listResp, ok := resp.(renderListResponse); ok {
				next := encodeCursor(*nextCursorFor)
				listResp.NextCursor = &next
				resp = listResp
			}
		}
		writeJSON(w, http.StatusOK, resp)
	}
}

func sqliteSubmitViewHandler(tracker *shutdown.Tracker, db *sqlite.DB) http.HandlerFunc {
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
		err = db.WithTenant(r.Context(), tenant, func(ctx context.Context, tx database.Tx) error {
			role, ok := resolveActorRoleTx(ctx, tx, r, w)
			if !ok {
				return errHandled
			}
			doResult, _, doErr := outbox.DoTx(ctx, tx, idempotencyKey, payload,
				func(ctx context.Context, tx database.Tx) (any, []outbox.Event, error) {
					submitted, err := views.SubmitEditViewTx(ctx, tx, role, id, req.RecordID, req.Version, req.Values)
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
