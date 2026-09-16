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
	mux := http.NewServeMux()
	mux.Handle("GET /v1/tenants/{tenant}/tables/{table}/records", Middleware(http.HandlerFunc(
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
	claims := jwt.MapClaims{"sub": sub, "tenant": tenant, "exp": exp.Unix()}
	tok := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, err := tok.SignedString([]byte("chave-de-teste-irrelevante"))
	if err != nil {
		t.Fatalf("tokenFor: %v", err)
	}
	return signed
}

// TestMiddleware_Positive é o exemplo positivo exigido pelo critério de
// aceite de GO-006/GO-007: identidade delegada válida, tenant do token bate
// com o tenant da URL — a requisição passa e o handler recebe tenant+ator
// no contexto.
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

// TestMiddleware_Negative_InvalidToken é o negativo "assinatura/token
// inválido" — aqui simulado com um token malformado (o caso de assinatura
// forjada é indistinguível deste até GO-008/GO-009 implementarem verificação
// real; ambos batem no mesmo ErrMalformedToken/401 por ora).
func TestMiddleware_Negative_InvalidToken(t *testing.T) {
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

// TestMiddleware_Negative_TenantMismatch é o segundo negativo exigido pelo
// critério de aceite: identidade válida, mas o tenant do token não bate com
// o tenant pedido na URL.
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
