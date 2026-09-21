// Testes de administração de usuários (GO-044) — exigem Postgres real,
// reaproveita os helpers de records_test.go (testDB, newTestFixture,
// mintServiceIdentity, testServiceIdentitySecret), mesmo pacote.
package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/vjuliani/saltcorn/migracao/backend/internal/identity"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/platform/cutover"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/platform/database"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/platform/tenancy"
)

func switchUsersAdminOwnershipOnDB(t *testing.T, db *database.DB, fx testFixture) {
	t.Helper()
	if err := cutover.SwitchOwner(context.Background(), db, fx.guard, fx.tenant, usersAdminCapability, cutover.OwnerGo, 2*time.Second); err != nil {
		t.Fatalf("cutover.SwitchOwner(usersAdminCapability): %v", err)
	}
}

func buildUsersAdminHandler(t *testing.T, verifier *tenancy.Verifier, guard *cutover.Guard, next http.Handler) http.Handler {
	t.Helper()
	return tenancy.Middleware(verifier, cutover.RequireOwnership(guard, usersAdminCapability, next))
}

func TestListUsersHandler_Success(t *testing.T) {
	db := testDB(t)
	fx := newTestFixture(t, db, identity.RoleAdmin)
	switchUsersAdminOwnershipOnDB(t, db, fx)
	verifier, err := tenancy.NewVerifier([]byte(testServiceIdentitySecret))
	if err != nil {
		t.Fatalf("NewVerifier: %v", err)
	}

	var otherID int
	if err := db.WithTenant(context.Background(), fx.tenant, func(ctx context.Context, tx pgx.Tx) error {
		hash, err := identity.HashPassword("hunter2")
		if err != nil {
			return err
		}
		otherID, err = identity.CreateUser(ctx, tx, "colega@example.com", hash, identity.RoleID(80))
		return err
	}); err != nil {
		t.Fatalf("criar segundo usuário: %v", err)
	}

	handler := buildUsersAdminHandler(t, verifier, fx.guard, listUsersHandler(fx.tracker, db))
	token := mintServiceIdentity(t, testServiceIdentitySecret, strconv.Itoa(fx.userID), fx.tenant, time.Minute)

	req := httptest.NewRequest(http.MethodGet, "/v1/tenants/"+string(fx.tenant)+"/users", nil)
	req.SetPathValue("tenant", string(fx.tenant))
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, corpo = %s, esperado 200", rec.Code, rec.Body.String())
	}
	var users []userResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &users); err != nil {
		t.Fatalf("decodificar resposta: %v", err)
	}
	if len(users) != 2 {
		t.Fatalf("len(users) = %d, esperado 2 (admin do fixture + colega)", len(users))
	}
	foundOther := false
	for _, u := range users {
		if u.ID == otherID {
			foundOther = true
			if u.RoleID != 80 {
				t.Errorf("colega.RoleID = %d, esperado 80", u.RoleID)
			}
		}
	}
	if !foundOther {
		t.Errorf("usuário colega (id=%d) não apareceu na listagem: %+v", otherID, users)
	}
}

func TestListUsersHandler_NonAdminRejected(t *testing.T) {
	db := testDB(t)
	fx := newTestFixture(t, db, identity.RolePublic)
	switchUsersAdminOwnershipOnDB(t, db, fx)
	verifier, err := tenancy.NewVerifier([]byte(testServiceIdentitySecret))
	if err != nil {
		t.Fatalf("NewVerifier: %v", err)
	}
	handler := buildUsersAdminHandler(t, verifier, fx.guard, listUsersHandler(fx.tracker, db))
	token := mintServiceIdentity(t, testServiceIdentitySecret, strconv.Itoa(fx.userID), fx.tenant, time.Minute)

	req := httptest.NewRequest(http.MethodGet, "/v1/tenants/"+string(fx.tenant)+"/users", nil)
	req.SetPathValue("tenant", string(fx.tenant))
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, corpo = %s, esperado 403", rec.Code, rec.Body.String())
	}
}

func TestUpdateUserHandler_ChangesRole(t *testing.T) {
	db := testDB(t)
	fx := newTestFixture(t, db, identity.RoleAdmin)
	switchUsersAdminOwnershipOnDB(t, db, fx)
	verifier, err := tenancy.NewVerifier([]byte(testServiceIdentitySecret))
	if err != nil {
		t.Fatalf("NewVerifier: %v", err)
	}

	var targetID int
	if err := db.WithTenant(context.Background(), fx.tenant, func(ctx context.Context, tx pgx.Tx) error {
		hash, err := identity.HashPassword("hunter2")
		if err != nil {
			return err
		}
		targetID, err = identity.CreateUser(ctx, tx, "promover@example.com", hash, identity.RoleID(80))
		return err
	}); err != nil {
		t.Fatalf("criar usuário alvo: %v", err)
	}

	handler := buildUsersAdminHandler(t, verifier, fx.guard, updateUserHandler(fx.tracker, db))
	token := mintServiceIdentity(t, testServiceIdentitySecret, strconv.Itoa(fx.userID), fx.tenant, time.Minute)

	body := strings.NewReader(`{"role_id":1}`)
	req := httptest.NewRequest(http.MethodPatch, "/v1/tenants/"+string(fx.tenant)+"/users/"+strconv.Itoa(targetID), body)
	req.SetPathValue("tenant", string(fx.tenant))
	req.SetPathValue("id", strconv.Itoa(targetID))
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, corpo = %s, esperado 204", rec.Code, rec.Body.String())
	}

	if err := db.WithTenant(context.Background(), fx.tenant, func(ctx context.Context, tx pgx.Tx) error {
		u, err := identity.FindUserByID(ctx, tx, targetID)
		if err != nil {
			return err
		}
		if u.RoleID != identity.RoleAdmin {
			t.Errorf("RoleID = %v, esperado RoleAdmin", u.RoleID)
		}
		return nil
	}); err != nil {
		t.Fatalf("verificação: %v", err)
	}
}

func TestDeleteUserHandler_RemovesUser(t *testing.T) {
	db := testDB(t)
	fx := newTestFixture(t, db, identity.RoleAdmin)
	switchUsersAdminOwnershipOnDB(t, db, fx)
	verifier, err := tenancy.NewVerifier([]byte(testServiceIdentitySecret))
	if err != nil {
		t.Fatalf("NewVerifier: %v", err)
	}

	var targetID int
	if err := db.WithTenant(context.Background(), fx.tenant, func(ctx context.Context, tx pgx.Tx) error {
		hash, err := identity.HashPassword("hunter2")
		if err != nil {
			return err
		}
		targetID, err = identity.CreateUser(ctx, tx, "remover@example.com", hash, identity.RoleID(80))
		return err
	}); err != nil {
		t.Fatalf("criar usuário alvo: %v", err)
	}

	handler := buildUsersAdminHandler(t, verifier, fx.guard, deleteUserHandler(fx.tracker, db))
	token := mintServiceIdentity(t, testServiceIdentitySecret, strconv.Itoa(fx.userID), fx.tenant, time.Minute)

	req := httptest.NewRequest(http.MethodDelete, "/v1/tenants/"+string(fx.tenant)+"/users/"+strconv.Itoa(targetID), nil)
	req.SetPathValue("tenant", string(fx.tenant))
	req.SetPathValue("id", strconv.Itoa(targetID))
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, corpo = %s, esperado 204", rec.Code, rec.Body.String())
	}

	if err := db.WithTenant(context.Background(), fx.tenant, func(ctx context.Context, tx pgx.Tx) error {
		if _, err := identity.FindUserByID(ctx, tx, targetID); err == nil {
			t.Error("usuário ainda encontrado após DELETE")
		}
		return nil
	}); err != nil {
		t.Fatalf("verificação: %v", err)
	}
}

func TestResetPasswordHandler_RandomPassword_EnablesNewLogin(t *testing.T) {
	db := testDB(t)
	fx := newTestFixture(t, db, identity.RoleAdmin)
	switchUsersAdminOwnershipOnDB(t, db, fx)
	verifier, err := tenancy.NewVerifier([]byte(testServiceIdentitySecret))
	if err != nil {
		t.Fatalf("NewVerifier: %v", err)
	}

	var targetID int
	if err := db.WithTenant(context.Background(), fx.tenant, func(ctx context.Context, tx pgx.Tx) error {
		hash, err := identity.HashPassword("senha-antiga")
		if err != nil {
			return err
		}
		targetID, err = identity.CreateUser(ctx, tx, "resetar@example.com", hash, identity.RoleID(80))
		return err
	}); err != nil {
		t.Fatalf("criar usuário alvo: %v", err)
	}

	handler := buildUsersAdminHandler(t, verifier, fx.guard, resetPasswordHandler(fx.tracker, db))
	token := mintServiceIdentity(t, testServiceIdentitySecret, strconv.Itoa(fx.userID), fx.tenant, time.Minute)

	req := httptest.NewRequest(http.MethodPost, "/v1/tenants/"+string(fx.tenant)+"/users/"+strconv.Itoa(targetID)+"/reset-password", nil)
	req.SetPathValue("tenant", string(fx.tenant))
	req.SetPathValue("id", strconv.Itoa(targetID))
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, corpo = %s, esperado 200", rec.Code, rec.Body.String())
	}
	var resp resetPasswordResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decodificar resposta: %v", err)
	}
	if resp.Password == "" {
		t.Fatal("password vazio na resposta")
	}

	if err := db.WithTenant(context.Background(), fx.tenant, func(ctx context.Context, tx pgx.Tx) error {
		if _, err := identity.Authenticate(ctx, tx, "resetar@example.com", "senha-antiga"); err == nil {
			t.Error("senha antiga ainda funciona após reset")
		}
		if _, err := identity.Authenticate(ctx, tx, "resetar@example.com", resp.Password); err != nil {
			t.Errorf("Authenticate com a senha devolvida na resposta falhou: %v", err)
		}
		return nil
	}); err != nil {
		t.Fatalf("verificação: %v", err)
	}
}

func TestListUserTokensHandler_ShowsRevokedStatus(t *testing.T) {
	db := testDB(t)
	fx := newTestFixture(t, db, identity.RoleAdmin)
	switchUsersAdminOwnershipOnDB(t, db, fx)
	verifier, err := tenancy.NewVerifier([]byte(testServiceIdentitySecret))
	if err != nil {
		t.Fatalf("NewVerifier: %v", err)
	}

	if err := db.WithTenant(context.Background(), fx.tenant, func(ctx context.Context, tx pgx.Tx) error {
		_, err := identity.CreateAPITokenForUser(ctx, tx, fx.userID)
		return err
	}); err != nil {
		t.Fatalf("criar token: %v", err)
	}

	handler := buildUsersAdminHandler(t, verifier, fx.guard, listUserTokensHandler(fx.tracker, db))
	token := mintServiceIdentity(t, testServiceIdentitySecret, strconv.Itoa(fx.userID), fx.tenant, time.Minute)

	req := httptest.NewRequest(http.MethodGet, "/v1/tenants/"+string(fx.tenant)+"/users/"+strconv.Itoa(fx.userID)+"/tokens", nil)
	req.SetPathValue("tenant", string(fx.tenant))
	req.SetPathValue("id", strconv.Itoa(fx.userID))
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, corpo = %s, esperado 200", rec.Code, rec.Body.String())
	}
	var tokens []apiTokenResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &tokens); err != nil {
		t.Fatalf("decodificar resposta: %v", err)
	}
	if len(tokens) != 1 || tokens[0].Revoked {
		t.Fatalf("tokens = %+v, esperado 1 token não revogado", tokens)
	}
}

func TestImpersonation_StartThenEnd_RecordsAuditTrail(t *testing.T) {
	db := testDB(t)
	fx := newTestFixture(t, db, identity.RoleAdmin)
	switchUsersAdminOwnershipOnDB(t, db, fx)
	verifier, err := tenancy.NewVerifier([]byte(testServiceIdentitySecret))
	if err != nil {
		t.Fatalf("NewVerifier: %v", err)
	}

	var targetID int
	if err := db.WithTenant(context.Background(), fx.tenant, func(ctx context.Context, tx pgx.Tx) error {
		hash, err := identity.HashPassword("hunter2")
		if err != nil {
			return err
		}
		targetID, err = identity.CreateUser(ctx, tx, "suporte-alvo@example.com", hash, identity.RoleID(80))
		return err
	}); err != nil {
		t.Fatalf("criar usuário alvo: %v", err)
	}

	startHandler := buildUsersAdminHandler(t, verifier, fx.guard, startImpersonationHandler(fx.tracker, db))
	token := mintServiceIdentity(t, testServiceIdentitySecret, strconv.Itoa(fx.userID), fx.tenant, time.Minute)

	startReq := httptest.NewRequest(http.MethodPost, "/v1/tenants/"+string(fx.tenant)+"/users/"+strconv.Itoa(targetID)+"/impersonate", nil)
	startReq.SetPathValue("tenant", string(fx.tenant))
	startReq.SetPathValue("id", strconv.Itoa(targetID))
	startReq.Header.Set("Authorization", "Bearer "+token)
	startRec := httptest.NewRecorder()
	startHandler.ServeHTTP(startRec, startReq)

	if startRec.Code != http.StatusCreated {
		t.Fatalf("start: status = %d, corpo = %s, esperado 201", startRec.Code, startRec.Body.String())
	}
	var startResp impersonateResponse
	if err := json.Unmarshal(startRec.Body.Bytes(), &startResp); err != nil {
		t.Fatalf("decodificar resposta de start: %v", err)
	}
	if startResp.LogID == 0 || startResp.TargetUserID != targetID {
		t.Fatalf("startResp = %+v, esperado log_id != 0 e target_user_id = %d", startResp, targetID)
	}

	if err := db.WithTenant(context.Background(), fx.tenant, func(ctx context.Context, tx pgx.Tx) error {
		rec, err := identity.GetImpersonation(ctx, tx, startResp.LogID)
		if err != nil {
			return err
		}
		if !rec.StillActive || rec.AdminUserID != fx.userID || rec.TargetUserID != targetID {
			t.Errorf("registro de auditoria = %+v, esperado ativo, admin=%d, target=%d", rec, fx.userID, targetID)
		}
		return nil
	}); err != nil {
		t.Fatalf("verificação pós-start: %v", err)
	}

	// endImpersonationHandler passa por tenancy.Middleware (exige
	// ServiceIdentity válido) mas NUNCA por RequireOwnership/checagem de
	// papel — qualquer identidade delegada válida para o tenant basta,
	// mesmo contrato de main.go (ver comentário da rota).
	endHandler := tenancy.Middleware(verifier, endImpersonationHandler(fx.tracker, db))
	endReq := httptest.NewRequest(http.MethodPost, "/v1/tenants/"+string(fx.tenant)+"/impersonations/"+strconv.Itoa(startResp.LogID)+"/end", nil)
	endReq.SetPathValue("tenant", string(fx.tenant))
	endReq.SetPathValue("id", strconv.Itoa(startResp.LogID))
	endReq.Header.Set("Authorization", "Bearer "+token)
	endRec := httptest.NewRecorder()
	endHandler.ServeHTTP(endRec, endReq)

	if endRec.Code != http.StatusNoContent {
		t.Fatalf("end: status = %d, corpo = %s, esperado 204", endRec.Code, endRec.Body.String())
	}

	if err := db.WithTenant(context.Background(), fx.tenant, func(ctx context.Context, tx pgx.Tx) error {
		rec, err := identity.GetImpersonation(ctx, tx, startResp.LogID)
		if err != nil {
			return err
		}
		if rec.StillActive {
			t.Error("StillActive = true após end, esperado false")
		}
		return nil
	}); err != nil {
		t.Fatalf("verificação pós-end: %v", err)
	}
}

// TestListUsersHandler_WithoutOwnershipCutover_Rejected prova que a nova
// capacidade (usersAdminCapability) está genuinamente sujeita ao corte
// Node/Go — sem SwitchOwner, mesmo um ator RoleAdmin válido é recusado
// ANTES de alcançar a lógica de negócio.
func TestListUsersHandler_WithoutOwnershipCutover_Rejected(t *testing.T) {
	db := testDB(t)
	fx := newTestFixture(t, db, identity.RoleAdmin)
	// Deliberadamente NÃO chama switchUsersAdminOwnershipOnDB.
	verifier, err := tenancy.NewVerifier([]byte(testServiceIdentitySecret))
	if err != nil {
		t.Fatalf("NewVerifier: %v", err)
	}
	handler := buildUsersAdminHandler(t, verifier, fx.guard, listUsersHandler(fx.tracker, db))
	token := mintServiceIdentity(t, testServiceIdentitySecret, strconv.Itoa(fx.userID), fx.tenant, time.Minute)

	req := httptest.NewRequest(http.MethodGet, "/v1/tenants/"+string(fx.tenant)+"/users", nil)
	req.SetPathValue("tenant", string(fx.tenant))
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code == http.StatusOK {
		t.Fatalf("status = %d, esperado uma rejeição (capacidade não pertence a Go neste tenant)", rec.Code)
	}
}

func TestImpersonation_NonAdminRejected(t *testing.T) {
	db := testDB(t)
	fx := newTestFixture(t, db, identity.RolePublic)
	switchUsersAdminOwnershipOnDB(t, db, fx)
	verifier, err := tenancy.NewVerifier([]byte(testServiceIdentitySecret))
	if err != nil {
		t.Fatalf("NewVerifier: %v", err)
	}
	handler := buildUsersAdminHandler(t, verifier, fx.guard, startImpersonationHandler(fx.tracker, db))
	token := mintServiceIdentity(t, testServiceIdentitySecret, strconv.Itoa(fx.userID), fx.tenant, time.Minute)

	req := httptest.NewRequest(http.MethodPost, "/v1/tenants/"+string(fx.tenant)+"/users/999999/impersonate", nil)
	req.SetPathValue("tenant", string(fx.tenant))
	req.SetPathValue("id", "999999")
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, corpo = %s, esperado 403", rec.Code, rec.Body.String())
	}
}
