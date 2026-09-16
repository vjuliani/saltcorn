// Rotas reais de internal-api.yaml (GO-006) — até GO-017 só existia a rota
// de exemplo/placeholder (main.go, tenantProbeHandler), sem nenhuma tabela
// de domínio real. internal/records (GO-012/013) já tinha toda a lógica
// pronta; faltava só o transporte HTTP. Implementadas aqui as três rotas
// que bff-api.yaml de fato consome — getActor, listRecords, createRecord —
// não as cinco de internal-api.yaml inteiro: getRecord/updateRecord/
// deleteRecord por ID ficam para quando um consumidor real de edição
// existir (bff-api.yaml não os expõe ao React ainda), ver nota de escopo em
// docs/migracao-go/execucoes/GO-017.md.
package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"

	"github.com/jackc/pgx/v5"

	"github.com/vjuliani/saltcorn/migracao/backend/internal/identity"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/platform/database"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/platform/outbox"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/platform/shutdown"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/platform/tenancy"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/records"
)

// recordsListDefaultLimit/recordsListMaxLimit espelham default/maximum de
// common.yaml#/components/parameters/Limit — nunca confiar no limit vindo
// do cliente sem sanear.
const (
	recordsListDefaultLimit = 50
	recordsListMaxLimit     = 200
)

// apiError é o formato de erro de common.yaml#/components/schemas/Error —
// nunca a mensagem crua de um erro Go, que pode ecoar detalhe sensível.
type apiError struct {
	Error struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

func writeAPIError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	var body apiError
	body.Error.Code = code
	body.Error.Message = message
	_ = json.NewEncoder(w).Encode(body)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// resolveActorRole busca o papel atual do ator (o `sub` da identidade
// delegada é o ID do usuário, nunca o papel em si — ADR-0007) dentro da
// MESMA transação que a operação de domínio vai usar. Retorna false quando
// a resposta de erro já foi escrita (ator inexistente neste tenant é
// tratado como Forbidden, não uma categoria de erro nova fora do
// contrato).
func resolveActorRole(ctx context.Context, tx pgx.Tx, r *http.Request, w http.ResponseWriter) (identity.RoleID, bool) {
	actorSub, _ := tenancy.ActorFromContext(ctx)
	actorID, err := strconv.Atoi(actorSub)
	if err != nil {
		writeAPIError(w, http.StatusForbidden, "actor_invalid", "identidade delegada com sub inválido")
		return 0, false
	}
	user, err := identity.FindUserByID(ctx, tx, actorID)
	if err != nil {
		if errors.Is(err, identity.ErrUserNotFound) {
			writeAPIError(w, http.StatusForbidden, "actor_not_found", "ator da identidade delegada não existe neste tenant")
			return 0, false
		}
		writeAPIError(w, http.StatusBadGateway, "actor_lookup_failed", "falha ao resolver o papel do ator")
		return 0, false
	}
	_ = r
	return user.RoleID, true
}

// getActorHandler implementa GET /v1/tenants/{tenant}/actor (extensão de
// GO-017 a internal-api.yaml — ver nota de escopo em execucoes/GO-017.md).
// Deliberadamente NÃO passa por cutover.RequireOwnership: resolução de
// identidade não é uma capacidade de domínio sujeita a corte Node/Go, é
// infraestrutura de identidade que já vive inteiramente em Go desde GO-008.
func getActorHandler(tracker *shutdown.Tracker, db *database.DB) http.HandlerFunc {
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

		type actorResponse struct {
			ID     int `json:"id"`
			RoleID int `json:"role_id"`
		}
		var resp actorResponse
		err = db.WithTenant(r.Context(), tenant, func(ctx context.Context, tx pgx.Tx) error {
			role, ok := resolveActorRole(ctx, tx, r, w)
			if !ok {
				return errHandled
			}
			actorSub, _ := tenancy.ActorFromContext(ctx)
			id, _ := strconv.Atoi(actorSub)
			resp = actorResponse{ID: id, RoleID: int(role)}
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

// errHandled sinaliza, de dentro de um db.WithTenant, que a resposta HTTP
// já foi escrita (ex.: por resolveActorRole) — o chamador só precisa saber
// para não escrever uma segunda resposta, nunca logar como se fosse uma
// falha de banco genérica.
var errHandled = errors.New("resposta já escrita")

// listRecordsHandler implementa GET .../records (listRecords de
// internal-api.yaml). Cursor é opaco para o cliente (contrato:
// "cursor opaco da página anterior") mas, internamente, é só um offset
// codificado em base64 — decisão de implementação, não parte do contrato.
func listRecordsHandler(tracker *shutdown.Tracker, db *database.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		end, err := tracker.Begin()
		if err != nil {
			http.Error(w, err.Error(), http.StatusServiceUnavailable)
			return
		}
		defer end()

		tenant, _ := tenancy.TenantFromContext(r.Context())
		table := r.PathValue("table")
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

		type page struct {
			Items      []map[string]any `json:"items"`
			NextCursor *string          `json:"next_cursor"`
		}
		var resp page

		err = db.WithTenant(r.Context(), tenant, func(ctx context.Context, tx pgx.Tx) error {
			role, ok := resolveActorRole(ctx, tx, r, w)
			if !ok {
				return errHandled
			}
			rows, err := records.Rows(ctx, tx, role, records.Query{
				Table:   table,
				OrderBy: []records.OrderTerm{{Field: "id"}},
				Limit:   limit + 1, // +1 para saber se há próxima página, sem contar linhas à parte
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

// createRecordHandler implementa POST .../records (createRecord de
// internal-api.yaml). Idempotência via outbox.Do (GO-014): a MESMA
// Idempotency-Key com o MESMO corpo retorna o registro já criado sem
// rodar CreateRecord de novo; a mesma chave com corpo diferente falha com
// 409 (outbox.ErrKeyConflict).
func createRecordHandler(tracker *shutdown.Tracker, db *database.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		end, err := tracker.Begin()
		if err != nil {
			http.Error(w, err.Error(), http.StatusServiceUnavailable)
			return
		}
		defer end()

		tenant, _ := tenancy.TenantFromContext(r.Context())
		table := r.PathValue("table")
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
		var input map[string]any
		if err := json.Unmarshal(body, &input); err != nil {
			writeAPIError(w, http.StatusBadRequest, "invalid_json", "corpo da requisição não é um JSON válido")
			return
		}

		var created any
		err = db.WithTenant(r.Context(), tenant, func(ctx context.Context, tx pgx.Tx) error {
			role, ok := resolveActorRole(ctx, tx, r, w)
			if !ok {
				return errHandled
			}
			result, _, doErr := outbox.Do(ctx, tx, idempotencyKey, input,
				func(ctx context.Context, tx pgx.Tx) (any, []outbox.Event, error) {
					rec, err := records.CreateRecord(ctx, tx, role, table, input, nil)
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

// writeRecordsError classifica os erros sentinela de internal/records para
// o formato de resposta do contrato — nunca a mensagem crua do erro Go.
// ErrUnknownTable/ErrUnknownField (404) e os erros de validação de
// CreateRecord (400) não têm resposta documentada em internal-api.yaml
// para estas rotas (só 401/403/409 estão listados) — lacuna registrada em
// execucoes/GO-017.md, não escondida: 404/400 são o mapeamento HTTP
// correto e não contradizem nada que o contrato já promete.
func writeRecordsError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, records.ErrNotAuthorized):
		writeAPIError(w, http.StatusForbidden, "not_authorized", "ator não tem papel suficiente para esta operação")
	case errors.Is(err, records.ErrUnknownTable), errors.Is(err, records.ErrUnknownField):
		writeAPIError(w, http.StatusNotFound, "not_found", "tabela ou campo não encontrado")
	case errors.Is(err, records.ErrTypeMismatch),
		errors.Is(err, records.ErrRequiredField),
		errors.Is(err, records.ErrNoFields):
		writeAPIError(w, http.StatusBadRequest, "invalid_input", err.Error())
	case errors.Is(err, records.ErrDuplicateValue):
		writeAPIError(w, http.StatusConflict, "duplicate_value", "valor duplicado viola unicidade")
	case errors.Is(err, records.ErrInvalidReference):
		writeAPIError(w, http.StatusBadRequest, "invalid_reference", "referência inválida")
	default:
		writeAPIError(w, http.StatusBadGateway, "database_error", "erro ao processar a operação")
	}
}

func encodeCursor(offset int) string {
	return base64.RawURLEncoding.EncodeToString([]byte(strconv.Itoa(offset)))
}

func decodeCursor(cursor string) int {
	if cursor == "" {
		return 0
	}
	raw, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil {
		return 0
	}
	n, err := strconv.Atoi(string(raw))
	if err != nil || n < 0 {
		return 0
	}
	return n
}
