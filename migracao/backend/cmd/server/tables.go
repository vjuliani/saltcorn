// Rotas de tabelas/campos (GO-019) — internal/metadata.CreateTable/AddField
// (GO-011) nunca tinham exposição HTTP: só eram chamados de dentro de
// testes Go. "Conectar tabelas, campos" (escopo de GO-019) exige isso
// primeiro — não dá para o editor criar uma tabela via BFF sem uma rota
// real aqui.
//
// Sem Idempotency-Key/outbox.Do aqui, ao contrário de createRecord
// (GO-017): CreateTable/AddField já são idempotentes por definição própria
// (uma definição idêntica a uma já existente é um no-op bem-sucedido, ver
// README "Catálogo de metadados", GO-011) — um retry de rede não duplica
// nada, então a maquinaria de outbox seria redundante aqui.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/jackc/pgx/v5"

	"github.com/vjuliani/saltcorn/migracao/backend/internal/identity"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/metadata"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/platform/database"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/platform/shutdown"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/platform/tenancy"
)

type createTableRequest struct {
	Name         string `json:"name"`
	MinRoleRead  *int   `json:"min_role_read"`
	MinRoleWrite *int   `json:"min_role_write"`
}

type tableResponse struct {
	ID           int    `json:"id"`
	Name         string `json:"name"`
	MinRoleRead  int    `json:"min_role_read"`
	MinRoleWrite int    `json:"min_role_write"`
}

func tableToResponse(t metadata.Table) tableResponse {
	return tableResponse{ID: t.ID, Name: t.Name, MinRoleRead: int(t.MinRoleRead), MinRoleWrite: int(t.MinRoleWrite)}
}

// createTableHandler implementa POST /v1/tenants/{tenant}/tables.
func createTableHandler(tracker *shutdown.Tracker, db *database.DB) http.HandlerFunc {
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
		err = db.WithTenant(r.Context(), tenant, func(ctx context.Context, tx pgx.Tx) error {
			role, ok := resolveActorRole(ctx, tx, r, w)
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

type addFieldRequest struct {
	Name       string `json:"name"`
	Type       string `json:"type"`
	Required   bool   `json:"required"`
	Unique     bool   `json:"unique"`
	References string `json:"references"`
}

type fieldResponse struct {
	ID              int    `json:"id"`
	TableID         int    `json:"table_id"`
	Name            string `json:"name"`
	Type            string `json:"type"`
	Required        bool   `json:"required"`
	Unique          bool   `json:"unique"`
	ReferencesTable int    `json:"references_table_id,omitempty"`
}

func fieldToResponse(f metadata.Field) fieldResponse {
	return fieldResponse{
		ID: f.ID, TableID: f.TableID, Name: f.Name, Type: string(f.Type),
		Required: f.Required, Unique: f.Unique, ReferencesTable: f.ReferencesTable,
	}
}

// addFieldHandler implementa POST /v1/tenants/{tenant}/tables/{table}/fields.
func addFieldHandler(tracker *shutdown.Tracker, db *database.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		end, err := tracker.Begin()
		if err != nil {
			http.Error(w, err.Error(), http.StatusServiceUnavailable)
			return
		}
		defer end()

		tenant, _ := tenancy.TenantFromContext(r.Context())
		tableName := r.PathValue("table")
		if db == nil {
			writeAPIError(w, http.StatusBadGateway, "database_unavailable", "banco não configurado nesta instância")
			return
		}

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
		err = db.WithTenant(r.Context(), tenant, func(ctx context.Context, tx pgx.Tx) error {
			role, ok := resolveActorRole(ctx, tx, r, w)
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

// writeMetadataError classifica os erros sentinela de internal/metadata
// para o formato de resposta do contrato — mesma disciplina de
// writeRecordsError (records.go): nunca a mensagem crua de um erro Go.
func writeMetadataError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, metadata.ErrNotAuthorized):
		writeAPIError(w, http.StatusForbidden, "not_authorized", "ator não tem papel suficiente para esta operação")
	case errors.Is(err, metadata.ErrTableNotFound):
		writeAPIError(w, http.StatusNotFound, "not_found", "tabela não encontrada")
	case errors.Is(err, metadata.ErrInvalidName),
		errors.Is(err, metadata.ErrFieldTypeMismatch),
		errors.Is(err, metadata.ErrMissingReference),
		errors.Is(err, metadata.ErrReferencedTableNotFound),
		errors.Is(err, metadata.ErrUnsupportedFieldType):
		writeAPIError(w, http.StatusBadRequest, "invalid_input", err.Error())
	default:
		writeAPIError(w, http.StatusBadGateway, "database_error", "erro ao processar a operação")
	}
}
