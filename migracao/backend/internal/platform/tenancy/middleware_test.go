package tenancy

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

func newTestServer(t *testing.T) *httptest.Server {
	t.Helper()
	v := testVerifier(t)
	mux := http.NewServeMux()
	mux.Handle("GET /v1/tenants/{tenant}/tables/{table}/records", Middleware(v, http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			tenant, _ := TenantFromContext(r.Context())
			actor, _ := ActorFromContext(r.Context())
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(string(tenant) + "/" + actor))
		},
	)))
	return httptest.NewServer(mux)
}

func tokenFor(t *testing.T, sub, tenant string, exp time.Time) string {
	t.Helper()
	return mintTestToken(t, testSecret, jwt.MapClaims{"sub": sub, "tenant": tenant, "exp": exp.Unix()})
}

// TestMiddleware_Positive é o exemplo positivo exigido pelo critério de
// aceite de GO-006/GO-007: identidade delegada válida (assinatura conferida,
// GO-008), tenant do token bate com o tenant da URL — a requisição passa e o
// handler recebe tenant+ator no contexto.
func TestMiddleware_Positive(t *testing.T) {
	srv := newTestServer(t)
	defer srv.Close()

	token := tokenFor(t, "42", "acme", time.Now().Add(time.Minute))
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

func TestMiddleware_Negative_MalformedToken(t *testing.T) {
	srv := newTestServer(t)
	defer srv.Close()

	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/v1/tenants/acme/tables/guitars/records", nil)
	req.Header.Set("Authorization", "Bearer token-nao-e-um-jwt-valido")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("requisição falhou: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, esperado 401", resp.StatusCode)
	}
}

// TestMiddleware_Negative_ForgedSignature é o negativo "token forjado" de
// ponta a ponta (GO-008): claims bem formadas, tenant correto, mas assinado
// com uma chave que não é a do Verifier — não era distinguível de um token
// malformado até esta tarefa (GO-007 usava jwt.ParseUnverified, que nunca
// checava assinatura nenhuma).
func TestMiddleware_Negative_ForgedSignature(t *testing.T) {
	srv := newTestServer(t)
	defer srv.Close()

	token := mintTestToken(t, "chave-forjada-que-nao-e-a-do-verifier32", jwt.MapClaims{
		"sub":    "42",
		"tenant": "acme",
		"exp":    time.Now().Add(time.Minute).Unix(),
	})
	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/v1/tenants/acme/tables/guitars/records", nil)
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("requisição falhou: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, esperado 401 (assinatura forjada)", resp.StatusCode)
	}
}

// TestMiddleware_Negative_TenantMismatch é o segundo negativo exigido pelo
// critério de aceite: identidade válida (assinatura confere), mas o tenant
// do token não bate com o tenant pedido na URL.
func TestMiddleware_Negative_TenantMismatch(t *testing.T) {
	srv := newTestServer(t)
	defer srv.Close()

	token := tokenFor(t, "42", "beta", time.Now().Add(time.Minute))
	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/v1/tenants/acme/tables/guitars/records", nil)
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("requisição falhou: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status = %d, esperado 403", resp.StatusCode)
	}
}

func TestMiddleware_MissingAuthorizationHeader(t *testing.T) {
	srv := newTestServer(t)
	defer srv.Close()

	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/v1/tenants/acme/tables/guitars/records", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("requisição falhou: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, esperado 401", resp.StatusCode)
	}
}

func TestMiddleware_ExpiredToken(t *testing.T) {
	srv := newTestServer(t)
	defer srv.Close()

	token := tokenFor(t, "42", "acme", time.Now().Add(-time.Minute))
	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/v1/tenants/acme/tables/guitars/records", nil)
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("requisição falhou: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, esperado 401", resp.StatusCode)
	}
}
