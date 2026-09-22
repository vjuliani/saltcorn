// Testes HTTP de i18n (GO-047) — exigem Postgres real, pulam (t.Skip) se
// SALTCORN_GO_TEST_DATABASE_URL não estiver definida, mesmo padrão de
// records_test.go.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/vjuliani/saltcorn/migracao/backend/internal/config"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/identity"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/platform/tenancy"
)

func TestGetActorHandler_DefaultLocaleFallbackWhenTenantNeverConfigured(t *testing.T) {
	db := testDB(t)
	fx := newTestFixture(t, db, identity.RoleAdmin)
	verifier, err := tenancy.NewVerifier([]byte(testServiceIdentitySecret))
	if err != nil {
		t.Fatalf("NewVerifier: %v", err)
	}
	handler := tenancy.Middleware(verifier, getActorHandler(fx.tracker, db))
	token := mintServiceIdentity(t, testServiceIdentitySecret, strconv.Itoa(fx.userID), fx.tenant, time.Minute)

	req := httptest.NewRequest(http.MethodGet, "/v1/tenants/"+string(fx.tenant)+"/actor", nil)
	req.SetPathValue("tenant", string(fx.tenant))
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, corpo = %s, esperado 200", rec.Code, rec.Body.String())
	}
	var body actorResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decodificar resposta: %v", err)
	}
	if body.Language != "" {
		t.Errorf("Language = %q, esperado \"\" (usuário nunca definiu preferência)", body.Language)
	}
	if body.DefaultLocale != "pt" {
		t.Errorf("DefaultLocale = %q, esperado \"pt\" (fallback, tenant nunca configurou default_locale)", body.DefaultLocale)
	}
}

func TestGetActorHandler_DefaultLocaleFromTenantConfig(t *testing.T) {
	db := testDB(t)
	fx := newTestFixture(t, db, identity.RoleAdmin)
	verifier, err := tenancy.NewVerifier([]byte(testServiceIdentitySecret))
	if err != nil {
		t.Fatalf("NewVerifier: %v", err)
	}
	if err := db.WithTenant(context.Background(), fx.tenant, func(ctx context.Context, tx pgx.Tx) error {
		return config.Set(ctx, tx, "default_locale", "en")
	}); err != nil {
		t.Fatalf("config.Set: %v", err)
	}

	handler := tenancy.Middleware(verifier, getActorHandler(fx.tracker, db))
	token := mintServiceIdentity(t, testServiceIdentitySecret, strconv.Itoa(fx.userID), fx.tenant, time.Minute)
	req := httptest.NewRequest(http.MethodGet, "/v1/tenants/"+string(fx.tenant)+"/actor", nil)
	req.SetPathValue("tenant", string(fx.tenant))
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	var body actorResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decodificar resposta: %v", err)
	}
	if body.DefaultLocale != "en" {
		t.Errorf("DefaultLocale = %q, esperado \"en\" (configurado explicitamente pelo tenant)", body.DefaultLocale)
	}
}

func TestSetActorLanguageHandler_PersistsAndReflectsInGetActor(t *testing.T) {
	db := testDB(t)
	fx := newTestFixture(t, db, identity.RoleAdmin)
	verifier, err := tenancy.NewVerifier([]byte(testServiceIdentitySecret))
	if err != nil {
		t.Fatalf("NewVerifier: %v", err)
	}
	token := mintServiceIdentity(t, testServiceIdentitySecret, strconv.Itoa(fx.userID), fx.tenant, time.Minute)

	setHandler := tenancy.Middleware(verifier, setActorLanguageHandler(fx.tracker, db))
	body := bytes.NewBufferString(`{"language":"en"}`)
	req := httptest.NewRequest(http.MethodPatch, "/v1/tenants/"+string(fx.tenant)+"/actor", body)
	req.SetPathValue("tenant", string(fx.tenant))
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	setHandler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("PATCH: status = %d, corpo = %s, esperado 200", rec.Code, rec.Body.String())
	}
	var setResp actorResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &setResp); err != nil {
		t.Fatalf("decodificar resposta do PATCH: %v", err)
	}
	if setResp.Language != "en" {
		t.Errorf("Language na resposta do PATCH = %q, esperado \"en\"", setResp.Language)
	}

	// Reler via getActor — a mudança precisa ter sido PERSISTIDA, não só
	// devolvida na resposta do próprio PATCH.
	getHandler := tenancy.Middleware(verifier, getActorHandler(fx.tracker, db))
	getReq := httptest.NewRequest(http.MethodGet, "/v1/tenants/"+string(fx.tenant)+"/actor", nil)
	getReq.SetPathValue("tenant", string(fx.tenant))
	getReq.Header.Set("Authorization", "Bearer "+token)
	getRec := httptest.NewRecorder()
	getHandler.ServeHTTP(getRec, getReq)
	var getResp actorResponse
	if err := json.Unmarshal(getRec.Body.Bytes(), &getResp); err != nil {
		t.Fatalf("decodificar resposta do GET: %v", err)
	}
	if getResp.Language != "en" {
		t.Errorf("Language após reler = %q, esperado \"en\" (persistido)", getResp.Language)
	}

	// Limpar a preferência (language="") — volta a "".
	clearBody := bytes.NewBufferString(`{"language":""}`)
	clearReq := httptest.NewRequest(http.MethodPatch, "/v1/tenants/"+string(fx.tenant)+"/actor", clearBody)
	clearReq.SetPathValue("tenant", string(fx.tenant))
	clearReq.Header.Set("Authorization", "Bearer "+token)
	clearRec := httptest.NewRecorder()
	setHandler.ServeHTTP(clearRec, clearReq)
	var clearResp actorResponse
	if err := json.Unmarshal(clearRec.Body.Bytes(), &clearResp); err != nil {
		t.Fatalf("decodificar resposta do PATCH (limpar): %v", err)
	}
	if clearResp.Language != "" {
		t.Errorf("Language após limpar = %q, esperado \"\"", clearResp.Language)
	}
}

// TestSetActorLanguageHandler_OnlyAffectsOwnUser prova que não há como
// mudar o idioma de OUTRO usuário — a identidade delegada da própria
// requisição é a ÚNICA fonte do userID afetado, nunca um parâmetro do
// corpo/path (que nem existe nesta rota).
func TestSetActorLanguageHandler_OnlyAffectsOwnUser(t *testing.T) {
	db := testDB(t)
	fxA := newTestFixture(t, db, identity.RoleAdmin)
	verifier, err := tenancy.NewVerifier([]byte(testServiceIdentitySecret))
	if err != nil {
		t.Fatalf("NewVerifier: %v", err)
	}
	tokenA := mintServiceIdentity(t, testServiceIdentitySecret, strconv.Itoa(fxA.userID), fxA.tenant, time.Minute)

	setHandler := tenancy.Middleware(verifier, setActorLanguageHandler(fxA.tracker, db))
	body := bytes.NewBufferString(`{"language":"en"}`)
	req := httptest.NewRequest(http.MethodPatch, "/v1/tenants/"+string(fxA.tenant)+"/actor", body)
	req.SetPathValue("tenant", string(fxA.tenant))
	req.Header.Set("Authorization", "Bearer "+tokenA)
	rec := httptest.NewRecorder()
	setHandler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, corpo = %s, esperado 200", rec.Code, rec.Body.String())
	}

	if err := db.WithTenant(context.Background(), fxA.tenant, func(ctx context.Context, tx pgx.Tx) error {
		user, err := identity.FindUserByID(ctx, tx, fxA.userID)
		if err != nil {
			return err
		}
		if user.Language != "en" {
			t.Errorf("Language gravado = %q, esperado \"en\" só para o próprio ator (id=%d)", user.Language, fxA.userID)
		}
		return nil
	}); err != nil {
		t.Fatalf("verificação: %v", err)
	}
}
