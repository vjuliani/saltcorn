// Testes deste arquivo não exigem Postgres: RequireOwnership só consulta o
// cache em memória da Guard (ver guard.go), preenchido diretamente via
// resumeWithOwner nestes testes, sem passar por SwitchOwner/banco.
package cutover

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/vjuliani/saltcorn/migracao/backend/internal/platform/tenancy"
)

const mwTestSecret = "01234567890123456789012345678901"

func mwTestVerifier(t *testing.T) *tenancy.Verifier {
	t.Helper()
	v, err := tenancy.NewVerifier([]byte(mwTestSecret))
	if err != nil {
		t.Fatalf("tenancy.NewVerifier: %v", err)
	}
	return v
}

func mwMintToken(t *testing.T, secret, sub, tenant string, exp time.Time) string {
	t.Helper()
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"sub":    sub,
		"tenant": tenant,
		"exp":    exp.Unix(),
	})
	signed, err := token.SignedString([]byte(secret))
	if err != nil {
		t.Fatalf("mwMintToken: %v", err)
	}
	return signed
}

// newProtectedServer monta a cadeia real que uma rota de escrita usaria:
// tenancy.Middleware (verifica assinatura, popula tenant/ator no contexto)
// seguido de cutover.RequireOwnership (checa ownership do tenant já
// verificado) — a mesma composição que cmd/server aplica.
func newProtectedServer(t *testing.T, guard *Guard, capability string) *httptest.Server {
	t.Helper()
	v := mwTestVerifier(t)
	mux := http.NewServeMux()
	handler := RequireOwnership(guard, capability, http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			tenant, _ := tenancy.TenantFromContext(r.Context())
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("ok:" + string(tenant)))
		},
	))
	mux.Handle("GET /v1/tenants/{tenant}/tables/{table}/records", tenancy.Middleware(v, handler))
	return httptest.NewServer(mux)
}

func TestRequireOwnership_Positive(t *testing.T) {
	guard := NewGuard()
	guard.resumeWithOwner(tenancy.Tenant("acme"), testCap, OwnerGo)
	srv := newProtectedServer(t, guard, testCap)
	defer srv.Close()

	token := mwMintToken(t, mwTestSecret, "42", "acme", time.Now().Add(time.Minute))
	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/v1/tenants/acme/tables/guitars/records", nil)
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("requisição falhou: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, esperado 200", resp.StatusCode)
	}
}

// TestRequireOwnership_Negative_NotOwner confirma o padrão seguro: sem
// nenhum corte registrado para o tenant, a rota recusa com 409, mesmo com
// um token verificado válido — owner ausente equivale a legacy.
func TestRequireOwnership_Negative_NotOwner(t *testing.T) {
	guard := NewGuard() // nenhum owner registrado para "acme"
	srv := newProtectedServer(t, guard, testCap)
	defer srv.Close()

	token := mwMintToken(t, mwTestSecret, "42", "acme", time.Now().Add(time.Minute))
	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/v1/tenants/acme/tables/guitars/records", nil)
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("requisição falhou: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("status = %d, esperado 409 (route_mismatch)", resp.StatusCode)
	}
}

func TestRequireOwnership_Negative_Draining(t *testing.T) {
	guard := NewGuard()
	tenant := tenancy.Tenant("acme")
	guard.resumeWithOwner(tenant, testCap, OwnerGo)

	// Ocupa a chave e a coloca em drenagem antes da requisição chegar —
	// simula uma troca de rota em andamento.
	end, err := guard.Begin(tenant, testCap)
	if err != nil {
		t.Fatalf("Begin(): %v", err)
	}
	defer end()
	drainDone := make(chan struct{})
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = guard.Drain(ctx, tenant, testCap)
		close(drainDone)
	}()
	// Espera ativamente até draining=true ser observável via Begin recusando.
	deadline := time.Now().Add(time.Second)
	for {
		if _, err := guard.Begin(tenant, testCap); errors.Is(err, ErrRouteDraining) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("timeout esperando a chave entrar em modo de drenagem")
		}
		time.Sleep(time.Millisecond)
	}

	srv := newProtectedServer(t, guard, testCap)
	defer srv.Close()

	token := mwMintToken(t, mwTestSecret, "42", "acme", time.Now().Add(time.Minute))
	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/v1/tenants/acme/tables/guitars/records", nil)
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("requisição falhou: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, esperado 503 (route_draining)", resp.StatusCode)
	}
}

// TestRequireOwnership_Negative_ForgedHeaders_NoValidToken é a prova direta
// do critério de aceite "identidade não pode ser forjada por headers":
// mesmo com X-Tenant-Id/X-Actor-Id apontando para um tenant que TEM corte
// para Go, a ausência de um token verificado barra a requisição antes de
// RequireOwnership sequer rodar — tenancy.Middleware nunca lê esses
// headers.
func TestRequireOwnership_Negative_ForgedHeaders_NoValidToken(t *testing.T) {
	guard := NewGuard()
	guard.resumeWithOwner(tenancy.Tenant("acme"), testCap, OwnerGo)
	srv := newProtectedServer(t, guard, testCap)
	defer srv.Close()

	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/v1/tenants/acme/tables/guitars/records", nil)
	req.Header.Set("X-Tenant-Id", "acme")
	req.Header.Set("X-Actor-Id", "1")
	// De propósito: nenhum Authorization Bearer válido.

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("requisição falhou: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, esperado 401 — headers forjados nunca deveriam substituir o token verificado", resp.StatusCode)
	}
}

// TestRequireOwnership_ForgedHeaders_ValidTokenDifferentTenant_UsesTokenTenant
// reforça o mesmo critério de outro ângulo: mesmo com um token válido, um
// header de tenant diferente do claim do token não muda qual tenant é
// checado — beta (sem corte) continua recusado mesmo que o header diga
// "acme" (que tem corte), e vice-versa.
func TestRequireOwnership_ForgedHeaders_ValidTokenDifferentTenant_UsesTokenTenant(t *testing.T) {
	guard := NewGuard()
	guard.resumeWithOwner(tenancy.Tenant("acme"), testCap, OwnerGo)
	// "beta" deliberadamente sem corte (owner ausente = legacy).
	srv := newProtectedServer(t, guard, testCap)
	defer srv.Close()

	// Token real é para "beta" (sem corte) — a URL também precisa ser
	// "beta" para passar na checagem de tenant_mismatch de tenancy.Middleware;
	// o header forjado tenta afirmar "acme" (que tem corte), mas não deveria
	// ter efeito nenhum.
	token := mwMintToken(t, mwTestSecret, "42", "beta", time.Now().Add(time.Minute))
	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/v1/tenants/beta/tables/guitars/records", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("X-Tenant-Id", "acme")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("requisição falhou: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("status = %d, esperado 409 — a checagem deveria usar o tenant do token (beta, sem corte), não o header (acme)", resp.StatusCode)
	}
}
