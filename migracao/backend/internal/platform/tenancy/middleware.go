package tenancy

import (
	"encoding/json"
	"net/http"
	"strings"
)

// Middleware resolve tenant e ator a partir da requisição e os injeta no
// context.Context, replicando a regra do contrato (`internal-api.yaml`,
// securityScheme ServiceIdentity): o tenant do path `{tenant}` (Go 1.22
// http.ServeMux, via r.PathValue) precisa bater com o claim `tenant` do
// token de identidade delegada — nunca confia só na URL nem só no token. A
// assinatura do token é verificada por v (GO-008) — não aceita mais
// qualquer token bem formado como GO-007 fazia.
//
// Requer que o handler esteja registrado com um padrão de rota que declare
// `{tenant}` (ex.: "GET /v1/tenants/{tenant}/tables/{table}/records").
func Middleware(v *Verifier, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		urlTenant := r.PathValue("tenant")
		if urlTenant == "" {
			writeError(w, http.StatusBadRequest, "missing_tenant", "tenant ausente na URL")
			return
		}

		token, ok := bearerToken(r.Header.Get("Authorization"))
		if !ok {
			writeError(w, http.StatusUnauthorized, "invalid_identity_token", "cabeçalho Authorization ausente ou mal formatado")
			return
		}

		identity, err := v.ParseDelegatedIdentity(token)
		if err != nil {
			writeError(w, http.StatusUnauthorized, "invalid_identity_token", err.Error())
			return
		}

		if string(identity.Tenant) != urlTenant {
			writeError(w, http.StatusForbidden, "tenant_mismatch", ErrTenantMismatch.Error())
			return
		}

		ctx := WithTenant(r.Context(), identity.Tenant)
		ctx = WithActor(ctx, identity.Actor)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func bearerToken(authHeader string) (string, bool) {
	const prefix = "Bearer "
	if !strings.HasPrefix(authHeader, prefix) {
		return "", false
	}
	token := strings.TrimSpace(strings.TrimPrefix(authHeader, prefix))
	if token == "" {
		return "", false
	}
	return token, true
}

// errorEnvelope replica o schema Error de migracao/contracts/openapi/common.yaml.
type errorEnvelope struct {
	Error struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	var env errorEnvelope
	env.Error.Code = code
	env.Error.Message = message
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(env)
}
