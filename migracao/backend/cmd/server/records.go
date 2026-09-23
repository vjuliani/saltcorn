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
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/vjuliani/saltcorn/migracao/backend/internal/config"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/identity"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/platform/database"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/platform/outbox"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/platform/shutdown"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/platform/tenancy"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/records"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/triggers"
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

// resolveActorUser busca o registro completo do ator (o `sub` da
// identidade delegada é o ID do usuário, nunca o papel em si —
// ADR-0007) dentro da MESMA transação que a operação de domínio vai
// usar. Retorna false quando a resposta de erro já foi escrita (ator
// inexistente neste tenant é tratado como Forbidden, não uma categoria
// de erro nova fora do contrato). resolveActorRole (abaixo) é o atalho
// que a maioria dos handlers usa quando só o papel importa — só
// getActorHandler/setActorLanguageHandler (GO-047) precisam do registro
// inteiro (Language).
func resolveActorUser(ctx context.Context, tx pgx.Tx, r *http.Request, w http.ResponseWriter) (*identity.User, bool) {
	actorSub, _ := tenancy.ActorFromContext(ctx)
	actorID, err := strconv.Atoi(actorSub)
	if err != nil {
		writeAPIError(w, http.StatusForbidden, "actor_invalid", "identidade delegada com sub inválido")
		return nil, false
	}
	user, err := identity.FindUserByID(ctx, tx, actorID)
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

// resolveActorRole busca só o papel atual do ator — atalho de
// resolveActorUser para os handlers (a maioria) que não precisam do
// registro completo.
func resolveActorRole(ctx context.Context, tx pgx.Tx, r *http.Request, w http.ResponseWriter) (identity.RoleID, bool) {
	user, ok := resolveActorUser(ctx, tx, r, w)
	if !ok {
		return 0, false
	}
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

		var resp actorResponse
		err = db.WithTenant(r.Context(), tenant, func(ctx context.Context, tx pgx.Tx) error {
			user, ok := resolveActorUser(ctx, tx, r, w)
			if !ok {
				return errHandled
			}
			resp = actorResponse{ID: user.ID, RoleID: int(user.RoleID), Language: user.Language, DefaultLocale: defaultLocaleOrFallback(ctx, tx)}
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

// actorResponse é o shape de getActor (Actor de internal-api.yaml) —
// Language (GO-047) é a preferência EXPLÍCITA do usuário ("" = sem
// preferência); DefaultLocale é sempre preenchido (fallback "pt" —
// mesmo idioma "nativo" deste port, ver README), nunca vazio, para o
// BFF nunca precisar de uma segunda chamada só para resolver o idioma
// efetivo do tenant.
type actorResponse struct {
	ID            int    `json:"id"`
	RoleID        int    `json:"role_id"`
	Language      string `json:"language"`
	DefaultLocale string `json:"default_locale"`
}

// defaultLocaleDefault é o fallback quando o tenant nunca configurou
// "default_locale" — mesmo idioma em que todo este port foi escrito
// desde GO-018, nunca "en" como suposição arbitrária.
const defaultLocaleDefault = "pt"

func defaultLocaleOrFallback(ctx context.Context, tx pgx.Tx) string {
	value, ok, err := config.Get(ctx, tx, "default_locale")
	if err != nil || !ok {
		return defaultLocaleDefault
	}
	s, ok := value.(string)
	if !ok || s == "" {
		return defaultLocaleDefault
	}
	return s
}

// setActorLanguageRequest é o corpo de PATCH .../actor.
type setActorLanguageRequest struct {
	Language *string `json:"language"`
}

// setActorLanguageHandler implementa PATCH /v1/tenants/{tenant}/actor
// (setActorLanguage de internal-api.yaml, GO-047) — self-service: o ator
// muda a PRÓPRIA preferência de idioma, nunca a de outro usuário (nunca
// há um userID no path/body, só a identidade delegada da própria
// requisição, mesmo modelo de getActor). Language "" ou ausente LIMPA a
// preferência (volta ao fallback de cookie/default_locale do tenant).
func setActorLanguageHandler(tracker *shutdown.Tracker, db *database.DB) http.HandlerFunc {
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

		body, err := io.ReadAll(io.LimitReader(r.Body, 1<<12))
		if err != nil {
			writeAPIError(w, http.StatusBadRequest, "invalid_body", "não foi possível ler o corpo da requisição")
			return
		}
		var req setActorLanguageRequest
		if err := json.Unmarshal(body, &req); err != nil {
			writeAPIError(w, http.StatusBadRequest, "invalid_json", "corpo da requisição não é um JSON válido")
			return
		}
		language := ""
		if req.Language != nil {
			language = *req.Language
		}

		var resp actorResponse
		err = db.WithTenant(r.Context(), tenant, func(ctx context.Context, tx pgx.Tx) error {
			user, ok := resolveActorUser(ctx, tx, r, w)
			if !ok {
				return errHandled
			}
			if err := identity.SetUserLanguage(ctx, tx, user.ID, language); err != nil {
				return err
			}
			resp = actorResponse{ID: user.ID, RoleID: int(user.RoleID), Language: language, DefaultLocale: defaultLocaleOrFallback(ctx, tx)}
			return nil
		})
		if err != nil {
			if errors.Is(err, errHandled) {
				return
			}
			writeAPIError(w, http.StatusBadGateway, "database_error", "erro ao gravar a preferência de idioma")
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

// actorUserContext monta o "user" exposto a only_if/run_js_code
// (internal/expression.Request.User) — só id e role_id, os dois campos que
// as fórmulas de trigger do pack piloto guitars realmente usam (`user.id`,
// `user.role`). Nunca o e-mail: nenhuma fórmula existente precisa dele, e
// buscar de novo seria uma segunda consulta a identity.FindUserByID só
// para isso — resolveActorRole já pagou o custo de resolver o papel.
func actorUserContext(ctx context.Context, role identity.RoleID) map[string]any {
	actorSub, _ := tenancy.ActorFromContext(ctx)
	actorID, _ := strconv.Atoi(actorSub)
	return map[string]any{"id": actorID, "role_id": int(role)}
}

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
func createRecordHandler(tracker *shutdown.Tracker, db *database.DB, dispatcher *triggers.Dispatcher) http.HandlerFunc {
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
					rec, err := records.CreateRecord(ctx, tx, role, table, input, dispatcher.HooksFor(tenant, role, actorUserContext(ctx, role)))
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

// updateRecordHandler implementa PATCH .../records/{id} (updateRecord de
// internal-api.yaml) — GO-040: a rota já existia no contrato desde GO-006,
// mas nunca tinha handler nem um jeito real de transmitir a versão
// esperada (achado de preflight, corrigido no contrato junto com esta
// rota). `_version` no corpo é obrigatório (mesma convenção de
// updateViewHandler/submitViewHandler); campos ausentes permanecem
// inalterados — internal/records.UpdateRecordTx já trata `values` como um
// PATCH parcial, nunca uma sobrescrita completa. Idempotência via
// outbox.Do: mesmo Idempotency-Key + mesmo corpo reaproveita o resultado,
// nunca reaplica a escrita (sem isso, um retry de rede pegaria um 409
// version_conflict falso — a versão já teria avançado na primeira
// tentativa bem-sucedida).
func updateRecordHandler(tracker *shutdown.Tracker, db *database.DB, dispatcher *triggers.Dispatcher) http.HandlerFunc {
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
		err = db.WithTenant(r.Context(), tenant, func(ctx context.Context, tx pgx.Tx) error {
			role, ok := resolveActorRole(ctx, tx, r, w)
			if !ok {
				return errHandled
			}
			result, _, doErr := outbox.Do(ctx, tx, idempotencyKey, input,
				func(ctx context.Context, tx pgx.Tx) (any, []outbox.Event, error) {
					rec, err := records.UpdateRecord(ctx, tx, role, table, id, expectedVersion, values, dispatcher.HooksFor(tenant, role, actorUserContext(ctx, role)))
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

// deleteRecordHandler implementa DELETE .../records/{id} (deleteRecord de
// internal-api.yaml) — GO-040, mesma correção de `_version` de
// updateRecordHandler acima. `?version=` é obrigatório. Sem
// Idempotency-Key: idempotente por natureza (mesma convenção de
// deleteViewRowHandler desde GO-039) — remover um registro já removido
// devolve 404, um estado final consistente, não um efeito duplicado.
func deleteRecordHandler(tracker *shutdown.Tracker, db *database.DB, dispatcher *triggers.Dispatcher) http.HandlerFunc {
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
			return records.DeleteRecord(ctx, tx, role, table, id, expectedVersion, dispatcher.HooksFor(tenant, role, actorUserContext(ctx, role)))
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

// recordHistoryVersionResponse é uma linha de RecordHistoryVersion
// (internal-api.yaml, GO-045).
type recordHistoryVersionResponse struct {
	Version          int            `json:"version"`
	Time             string         `json:"time"`
	RestoreOfVersion *int           `json:"restore_of_version,omitempty"`
	Record           map[string]any `json:"record"`
}

// getRecordHistoryHandler implementa GET .../records/{id}/history
// (getRecordHistory de internal-api.yaml, GO-045) — lista os snapshots de
// versionamento de um registro, mais recente primeiro.
func getRecordHistoryHandler(tracker *shutdown.Tracker, db *database.DB) http.HandlerFunc {
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
		if db == nil {
			writeAPIError(w, http.StatusBadGateway, "database_unavailable", "banco não configurado nesta instância")
			return
		}

		var resp []recordHistoryVersionResponse
		err = db.WithTenant(r.Context(), tenant, func(ctx context.Context, tx pgx.Tx) error {
			role, ok := resolveActorRole(ctx, tx, r, w)
			if !ok {
				return errHandled
			}
			versions, err := records.GetHistory(ctx, tx, role, table, id)
			if err != nil {
				return err
			}
			resp = make([]recordHistoryVersionResponse, 0, len(versions))
			for _, v := range versions {
				item := recordHistoryVersionResponse{Version: v.Version, RestoreOfVersion: v.RestoreOfVersion, Record: v.Record}
				if t, ok := v.Time.(time.Time); ok {
					item.Time = t.UTC().Format(time.RFC3339)
				}
				resp = append(resp, item)
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
		writeJSON(w, http.StatusOK, map[string]any{"versions": resp})
	}
}

// restoreRecordVersionRequest é o corpo de POST .../records/{id}/restore.
type restoreRecordVersionRequest struct {
	Version *int `json:"version"`
}

// restoreRecordVersionHandler implementa POST .../records/{id}/restore
// (restoreRecordVersion de internal-api.yaml, GO-045) — restaura o
// registro para um snapshot anterior; sempre ADITIVO (grava um NOVO
// snapshot marcado restore_of_version, nunca apaga histórico).
func restoreRecordVersionHandler(tracker *shutdown.Tracker, db *database.DB) http.HandlerFunc {
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
		if db == nil {
			writeAPIError(w, http.StatusBadGateway, "database_unavailable", "banco não configurado nesta instância")
			return
		}

		body, err := io.ReadAll(io.LimitReader(r.Body, 1<<16))
		if err != nil {
			writeAPIError(w, http.StatusBadRequest, "invalid_body", "não foi possível ler o corpo da requisição")
			return
		}
		var req restoreRecordVersionRequest
		if err := json.Unmarshal(body, &req); err != nil {
			writeAPIError(w, http.StatusBadRequest, "invalid_json", "corpo da requisição não é um JSON válido")
			return
		}
		if req.Version == nil {
			writeAPIError(w, http.StatusBadRequest, "version_required", "version é obrigatório")
			return
		}

		var restored map[string]any
		err = db.WithTenant(r.Context(), tenant, func(ctx context.Context, tx pgx.Tx) error {
			role, ok := resolveActorRole(ctx, tx, r, w)
			if !ok {
				return errHandled
			}
			var err error
			restored, err = records.RestoreRowVersion(ctx, tx, role, table, id, *req.Version, nil)
			return err
		})
		if err != nil {
			if errors.Is(err, errHandled) {
				return
			}
			writeRecordsError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, restored)
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
	case errors.Is(err, records.ErrUnknownTable), errors.Is(err, records.ErrUnknownField), errors.Is(err, records.ErrRecordNotFound):
		writeAPIError(w, http.StatusNotFound, "not_found", "recurso não encontrado")
	case errors.Is(err, records.ErrVersionConflict):
		writeAPIError(w, http.StatusConflict, "version_conflict", "o registro foi modificado por outra transação — releia e tente novamente")
	case errors.Is(err, records.ErrTableNotVersioned):
		writeAPIError(w, http.StatusConflict, "table_not_versioned", "tabela não é versionada (versioned=false)")
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
