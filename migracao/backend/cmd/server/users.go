// Rotas de administração de usuários (GO-044) — a superfície HTTP que
// faltava para internal/identity.{ListUsers,UpdateUserRole,SetPassword,
// DeleteUser,ListAPITokensForUser,StartImpersonation,EndImpersonation}
// (GO-011/GO-008 nunca tinham exposição HTTP para essas operações, só
// CreateUser/Authenticate via o fluxo de login). Mesmo padrão de
// tables.go/records.go: sem Idempotency-Key/outbox — todas as operações
// aqui já são idempotentes por definição própria (ver comentário de cada
// função em internal/identity).
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
	"github.com/vjuliani/saltcorn/migracao/backend/internal/platform/database"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/platform/shutdown"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/platform/tenancy"
)

type userResponse struct {
	ID     int `json:"id"`
	RoleID int `json:"role_id"`
	// Email nunca incluído por padrão numa listagem administrativa ampla
	// seria razoável, mas como cada linha já é uma identidade de usuário
	// (não um segredo), incluímos — nunca PasswordHash/TOTPSecret.
	Email string `json:"email"`
}

func userToResponse(u identity.User) userResponse {
	return userResponse{ID: u.ID, RoleID: int(u.RoleID), Email: u.Email}
}

// listUsersHandler implementa GET /v1/tenants/{tenant}/users.
func listUsersHandler(tracker *shutdown.Tracker, db *database.DB) http.HandlerFunc {
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

		var resp []userResponse
		err = db.WithTenant(r.Context(), tenant, func(ctx context.Context, tx pgx.Tx) error {
			role, ok := resolveActorRole(ctx, tx, r, w)
			if !ok {
				return errHandled
			}
			users, err := identity.ListUsers(ctx, tx, role)
			if err != nil {
				return err
			}
			resp = make([]userResponse, len(users))
			for i, u := range users {
				resp[i] = userToResponse(u)
			}
			return nil
		})
		if err != nil {
			if errors.Is(err, errHandled) {
				return
			}
			writeIdentityError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, resp)
	}
}

type updateUserRequest struct {
	RoleID *int `json:"role_id"`
}

// updateUserHandler implementa PATCH /v1/tenants/{tenant}/users/{id} — hoje
// só muda o papel (o único campo que o legado edita por este caminho sem
// reenviar senha); reset de senha é uma rota própria abaixo, de propósito
// (uma ação sensível o bastante para não ser um campo opcional silencioso
// no meio de um PATCH genérico).
func updateUserHandler(tracker *shutdown.Tracker, db *database.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		end, err := tracker.Begin()
		if err != nil {
			http.Error(w, err.Error(), http.StatusServiceUnavailable)
			return
		}
		defer end()

		tenant, _ := tenancy.TenantFromContext(r.Context())
		userID, convErr := strconv.Atoi(r.PathValue("id"))
		if convErr != nil {
			writeAPIError(w, http.StatusBadRequest, "invalid_input", "id de usuário inválido")
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
		var req updateUserRequest
		if err := json.Unmarshal(body, &req); err != nil {
			writeAPIError(w, http.StatusBadRequest, "invalid_json", "corpo da requisição não é um JSON válido")
			return
		}
		if req.RoleID == nil {
			writeAPIError(w, http.StatusBadRequest, "invalid_input", "role_id é obrigatório")
			return
		}

		err = db.WithTenant(r.Context(), tenant, func(ctx context.Context, tx pgx.Tx) error {
			role, ok := resolveActorRole(ctx, tx, r, w)
			if !ok {
				return errHandled
			}
			return identity.UpdateUserRole(ctx, tx, role, userID, identity.RoleID(*req.RoleID))
		})
		if err != nil {
			if errors.Is(err, errHandled) {
				return
			}
			writeIdentityError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// deleteUserHandler implementa DELETE /v1/tenants/{tenant}/users/{id}.
func deleteUserHandler(tracker *shutdown.Tracker, db *database.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		end, err := tracker.Begin()
		if err != nil {
			http.Error(w, err.Error(), http.StatusServiceUnavailable)
			return
		}
		defer end()

		tenant, _ := tenancy.TenantFromContext(r.Context())
		userID, convErr := strconv.Atoi(r.PathValue("id"))
		if convErr != nil {
			writeAPIError(w, http.StatusBadRequest, "invalid_input", "id de usuário inválido")
			return
		}
		if db == nil {
			writeAPIError(w, http.StatusBadGateway, "database_unavailable", "banco não configurado nesta instância")
			return
		}

		err = db.WithTenant(r.Context(), tenant, func(ctx context.Context, tx pgx.Tx) error {
			role, ok := resolveActorRole(ctx, tx, r, w)
			if !ok {
				return errHandled
			}
			return identity.DeleteUser(ctx, tx, role, userID)
		})
		if err != nil {
			if errors.Is(err, errHandled) {
				return
			}
			writeIdentityError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

type resetPasswordRequest struct {
	// Password é opcional — quando vazio, uma senha aleatória é gerada
	// (equivalente a "set-random-password" do legado); quando informado,
	// equivale a "reset-password" com valor escolhido pelo admin.
	Password string `json:"password"`
}

type resetPasswordResponse struct {
	// Password é devolvido em texto plano UMA ÚNICA VEZ, só nesta resposta
	// — nunca fica recuperável depois (mesmo contrato de
	// CreateAPITokenForUser para o token de API).
	Password string `json:"password"`
}

// resetPasswordHandler implementa POST /v1/tenants/{tenant}/users/{id}/reset-password.
func resetPasswordHandler(tracker *shutdown.Tracker, db *database.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		end, err := tracker.Begin()
		if err != nil {
			http.Error(w, err.Error(), http.StatusServiceUnavailable)
			return
		}
		defer end()

		tenant, _ := tenancy.TenantFromContext(r.Context())
		userID, convErr := strconv.Atoi(r.PathValue("id"))
		if convErr != nil {
			writeAPIError(w, http.StatusBadRequest, "invalid_input", "id de usuário inválido")
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
		var req resetPasswordRequest
		if len(body) > 0 {
			if err := json.Unmarshal(body, &req); err != nil {
				writeAPIError(w, http.StatusBadRequest, "invalid_json", "corpo da requisição não é um JSON válido")
				return
			}
		}

		newPassword := req.Password
		if newPassword == "" {
			newPassword, err = identity.GenerateRandomPassword()
			if err != nil {
				writeAPIError(w, http.StatusBadGateway, "database_error", "erro ao gerar senha aleatória")
				return
			}
		}
		hash, err := identity.HashPassword(newPassword)
		if err != nil {
			writeAPIError(w, http.StatusBadGateway, "database_error", "erro ao calcular hash de senha")
			return
		}

		err = db.WithTenant(r.Context(), tenant, func(ctx context.Context, tx pgx.Tx) error {
			role, ok := resolveActorRole(ctx, tx, r, w)
			if !ok {
				return errHandled
			}
			return identity.SetPassword(ctx, tx, role, userID, hash)
		})
		if err != nil {
			if errors.Is(err, errHandled) {
				return
			}
			writeIdentityError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, resetPasswordResponse{Password: newPassword})
	}
}

type apiTokenResponse struct {
	ID        int    `json:"id"`
	CreatedAt string `json:"created_at"`
	Revoked   bool   `json:"revoked"`
}

// listUserTokensHandler implementa GET /v1/tenants/{tenant}/users/{id}/tokens.
func listUserTokensHandler(tracker *shutdown.Tracker, db *database.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		end, err := tracker.Begin()
		if err != nil {
			http.Error(w, err.Error(), http.StatusServiceUnavailable)
			return
		}
		defer end()

		tenant, _ := tenancy.TenantFromContext(r.Context())
		userID, convErr := strconv.Atoi(r.PathValue("id"))
		if convErr != nil {
			writeAPIError(w, http.StatusBadRequest, "invalid_input", "id de usuário inválido")
			return
		}
		if db == nil {
			writeAPIError(w, http.StatusBadGateway, "database_unavailable", "banco não configurado nesta instância")
			return
		}

		var resp []apiTokenResponse
		err = db.WithTenant(r.Context(), tenant, func(ctx context.Context, tx pgx.Tx) error {
			role, ok := resolveActorRole(ctx, tx, r, w)
			if !ok {
				return errHandled
			}
			tokens, err := identity.ListAPITokensForUser(ctx, tx, role, userID)
			if err != nil {
				return err
			}
			resp = make([]apiTokenResponse, len(tokens))
			for i, t := range tokens {
				resp[i] = apiTokenResponse{ID: t.ID, CreatedAt: t.CreatedAt, Revoked: t.Revoked}
			}
			return nil
		})
		if err != nil {
			if errors.Is(err, errHandled) {
				return
			}
			writeIdentityError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, resp)
	}
}

type impersonateResponse struct {
	LogID        int `json:"log_id"`
	TargetUserID int `json:"target_user_id"`
}

// startImpersonationHandler implementa POST /v1/tenants/{tenant}/users/{id}/impersonate
// — o admin é sempre o `sub` da identidade delegada (nunca um campo do
// corpo), e o BFF é quem de fato cria a sessão de navegador do usuário
// impersonado (ADR-0007) usando o log_id devolvido aqui para depois
// encerrar a impersonação — ver docs/migracao-go/execucoes/GO-044.md.
func startImpersonationHandler(tracker *shutdown.Tracker, db *database.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		end, err := tracker.Begin()
		if err != nil {
			http.Error(w, err.Error(), http.StatusServiceUnavailable)
			return
		}
		defer end()

		tenant, _ := tenancy.TenantFromContext(r.Context())
		targetUserID, convErr := strconv.Atoi(r.PathValue("id"))
		if convErr != nil {
			writeAPIError(w, http.StatusBadRequest, "invalid_input", "id de usuário inválido")
			return
		}
		if db == nil {
			writeAPIError(w, http.StatusBadGateway, "database_unavailable", "banco não configurado nesta instância")
			return
		}

		var resp impersonateResponse
		err = db.WithTenant(r.Context(), tenant, func(ctx context.Context, tx pgx.Tx) error {
			role, ok := resolveActorRole(ctx, tx, r, w)
			if !ok {
				return errHandled
			}
			actorSub, _ := tenancy.ActorFromContext(ctx)
			adminID, convErr := strconv.Atoi(actorSub)
			if convErr != nil {
				writeAPIError(w, http.StatusForbidden, "actor_invalid", "identidade delegada com sub inválido")
				return errHandled
			}
			logID, err := identity.StartImpersonation(ctx, tx, role, adminID, targetUserID)
			if err != nil {
				return err
			}
			resp = impersonateResponse{LogID: logID, TargetUserID: targetUserID}
			return nil
		})
		if err != nil {
			if errors.Is(err, errHandled) {
				return
			}
			writeIdentityError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, resp)
	}
}

// endImpersonationHandler implementa POST /v1/tenants/{tenant}/impersonations/{id}/end
// — sem checagem de papel do ator: quem chama isto é o BFF encerrando sua
// PRÓPRIA sessão de impersonação, nunca um usuário final escolhendo um
// log_id (ver identity.EndImpersonation).
func endImpersonationHandler(tracker *shutdown.Tracker, db *database.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		end, err := tracker.Begin()
		if err != nil {
			http.Error(w, err.Error(), http.StatusServiceUnavailable)
			return
		}
		defer end()

		tenant, _ := tenancy.TenantFromContext(r.Context())
		logID, convErr := strconv.Atoi(r.PathValue("id"))
		if convErr != nil {
			writeAPIError(w, http.StatusBadRequest, "invalid_input", "id de impersonação inválido")
			return
		}
		if db == nil {
			writeAPIError(w, http.StatusBadGateway, "database_unavailable", "banco não configurado nesta instância")
			return
		}

		err = db.WithTenant(r.Context(), tenant, func(ctx context.Context, tx pgx.Tx) error {
			return identity.EndImpersonation(ctx, tx, logID)
		})
		if err != nil {
			writeIdentityError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// writeIdentityError classifica os erros sentinela de internal/identity —
// mesma disciplina de writeMetadataError/writeRecordsError: nunca a
// mensagem crua de um erro Go.
func writeIdentityError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, identity.ErrNotAuthorized):
		writeAPIError(w, http.StatusForbidden, "not_authorized", "ator não tem papel suficiente para esta operação")
	case errors.Is(err, identity.ErrUserNotFound):
		writeAPIError(w, http.StatusNotFound, "not_found", "usuário não encontrado")
	case errors.Is(err, identity.ErrCannotImpersonateSelf):
		writeAPIError(w, http.StatusBadRequest, "invalid_input", err.Error())
	case errors.Is(err, identity.ErrImpersonationNotFound):
		writeAPIError(w, http.StatusNotFound, "not_found", "registro de impersonação não encontrado")
	default:
		writeAPIError(w, http.StatusBadGateway, "database_error", "erro ao processar a operação")
	}
}
