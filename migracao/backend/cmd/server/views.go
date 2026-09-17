// Rotas de views (GO-019) — o ciclo do editor: criar, salvar (editar),
// reabrir, publicar. internal/views (novo nesta tarefa) já tem toda a
// lógica de domínio; aqui só o transporte HTTP, seguindo exatamente o
// padrão já estabelecido em records.go (GO-017): outbox.Do para
// idempotência nas mutações, resolveActorRole para o papel do ator.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"

	"github.com/jackc/pgx/v5"

	"github.com/vjuliani/saltcorn/migracao/backend/internal/identity"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/metadata"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/platform/database"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/platform/outbox"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/platform/shutdown"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/platform/tenancy"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/views"
)

type viewResponse struct {
	ID            int            `json:"id"`
	Name          string         `json:"name"`
	TableID       int            `json:"table_id"`
	Template      string         `json:"template"`
	MinRole       int            `json:"min_role"`
	Configuration map[string]any `json:"configuration"`
	Version       string         `json:"_version"`
}

func viewToResponse(v views.View) viewResponse {
	return viewResponse{
		ID: v.ID, Name: v.Name, TableID: v.TableID, Template: v.Template,
		MinRole: int(v.MinRole), Configuration: v.Configuration, Version: v.Version,
	}
}

type createViewRequest struct {
	Name          string         `json:"name"`
	TableName     string         `json:"table"`
	Template      string         `json:"template"`
	Configuration map[string]any `json:"configuration"`
	MinRole       *int           `json:"min_role"`
}

// createViewHandler implementa POST /v1/tenants/{tenant}/views.
// Idempotência via outbox.Do: uma view não é idempotente por definição
// própria (ao contrário de CreateTable/AddField) — um retry com a mesma
// Idempotency-Key e o mesmo corpo retorna a view já criada, em vez de
// falhar com nome duplicado (o que aconteceria sem isso, uma experiência
// pior para um retry legítimo de rede).
func createViewHandler(tracker *shutdown.Tracker, db *database.DB) http.HandlerFunc {
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
		err = db.WithTenant(r.Context(), tenant, func(ctx context.Context, tx pgx.Tx) error {
			role, ok := resolveActorRole(ctx, tx, r, w)
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
			result, _, doErr := outbox.Do(ctx, tx, idempotencyKey, payload,
				func(ctx context.Context, tx pgx.Tx) (any, []outbox.Event, error) {
					v, err := views.CreateView(ctx, tx, role, req.Name, table.ID, req.Template, req.Configuration, opts)
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

// getViewHandler implementa GET /v1/tenants/{tenant}/views/{id} — "reabre"
// do critério de aceite: o mesmo papel do ator é resolvido de novo, do
// zero, a cada chamada (nunca cacheado), então uma view despublicada
// depois de aberta uma vez já nega a segunda leitura, sem nenhum
// resquício da primeira (mesmo espírito de GO-015).
func getViewHandler(tracker *shutdown.Tracker, db *database.DB) http.HandlerFunc {
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

		var resp viewResponse
		err = db.WithTenant(r.Context(), tenant, func(ctx context.Context, tx pgx.Tx) error {
			role, ok := resolveActorRole(ctx, tx, r, w)
			if !ok {
				return errHandled
			}
			v, err := views.GetView(ctx, tx, role, id)
			if err != nil {
				return err
			}
			resp = viewToResponse(v)
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

type updateViewRequest struct {
	Version       string          `json:"_version"`
	Configuration *map[string]any `json:"configuration"`
	Template      *string         `json:"template"`
	MinRole       *int            `json:"min_role"`
}

// updateViewHandler implementa PATCH /v1/tenants/{tenant}/views/{id} — o
// caminho de "salvar" (com uma configuration nova) e de "publicar" (com um
// min_role menor), a mesma operação para os dois: são só campos
// diferentes do mesmo PATCH. Idempotência via outbox.Do (a mesma
// Idempotency-Key + mesmo corpo retorna o resultado já salvo, sem exigir
// que o _version ainda bata numa segunda tentativa de rede do MESMO
// salvamento) + controle de concorrência via _version (internal/views.
// UpdateView) para o critério de aceite "conflito de edição é apresentado
// sem sobrescrever silenciosamente" — duas EDIÇÕES DE VERDADE diferentes
// com o mesmo _version obsoleto (chaves de idempotência diferentes, já
// que o corpo é diferente) continuam colidindo em 409 ErrVersionConflict.
func updateViewHandler(tracker *shutdown.Tracker, db *database.DB) http.HandlerFunc {
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
		var req updateViewRequest
		if err := json.Unmarshal(body, &req); err != nil {
			writeAPIError(w, http.StatusBadRequest, "invalid_json", "corpo da requisição não é um JSON válido")
			return
		}
		if req.Version == "" {
			writeAPIError(w, http.StatusBadRequest, "version_required", "_version é obrigatório")
			return
		}

		var payload map[string]any
		_ = json.Unmarshal(body, &payload)

		var updated any
		err = db.WithTenant(r.Context(), tenant, func(ctx context.Context, tx pgx.Tx) error {
			role, ok := resolveActorRole(ctx, tx, r, w)
			if !ok {
				return errHandled
			}
			update := views.ViewUpdate{Configuration: req.Configuration, Template: req.Template}
			if req.MinRole != nil {
				minRole := identity.RoleID(*req.MinRole)
				update.MinRole = &minRole
			}
			result, _, doErr := outbox.Do(ctx, tx, idempotencyKey, payload,
				func(ctx context.Context, tx pgx.Tx) (any, []outbox.Event, error) {
					v, err := views.UpdateView(ctx, tx, role, id, req.Version, update)
					if err != nil {
						return nil, nil, err
					}
					return viewToResponse(v), nil, nil
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
			writeViewsOrMetadataError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, updated)
	}
}

// writeViewsOrMetadataError classifica os erros sentinela de
// internal/views (e de internal/metadata, usado por createViewHandler ao
// resolver a tabela) — mesma disciplina de writeRecordsError/
// writeMetadataError: nunca a mensagem crua de um erro Go.
func writeViewsOrMetadataError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, views.ErrNotAuthorized), errors.Is(err, metadata.ErrNotAuthorized):
		writeAPIError(w, http.StatusForbidden, "not_authorized", "ator não tem papel suficiente para esta operação")
	case errors.Is(err, views.ErrViewNotFound), errors.Is(err, metadata.ErrTableNotFound):
		writeAPIError(w, http.StatusNotFound, "not_found", "recurso não encontrado")
	case errors.Is(err, views.ErrVersionConflict):
		writeAPIError(w, http.StatusConflict, "version_conflict", "a view foi modificada por outra transação — releia e tente novamente")
	case errors.Is(err, views.ErrDuplicateName):
		writeAPIError(w, http.StatusConflict, "duplicate_name", "já existe uma view com este nome")
	default:
		writeAPIError(w, http.StatusBadGateway, "database_error", "erro ao processar a operação")
	}
}
