package cutover

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/vjuliani/saltcorn/migracao/backend/internal/platform/tenancy"
)

// RequireOwnership é o middleware que qualquer rota de escrita do backend
// Go deve usar: confere que Go é o proprietário registrado para
// tenant+capability antes de chamar o handler seguinte. Deve vir DEPOIS de
// tenancy.Middleware na cadeia — o tenant usado na checagem vem
// exclusivamente de tenancy.TenantFromContext, populado só por um token de
// identidade delegada com assinatura verificada (GO-008), nunca de um
// header da requisição. Isso é o que torna "identidade não pode ser
// forjada por headers" (critério de aceite de GO-009) verdadeiro nesta
// camada: mesmo que a requisição carregue um X-Tenant-Id ou X-Actor-Id
// forjado, este middleware nunca os lê — só existe tenant se
// tenancy.Middleware já o colocou no contexto depois de verificar a
// assinatura do token.
func RequireOwnership(guard *Guard, capability string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tenant, ok := tenancy.TenantFromContext(r.Context())
		if !ok {
			writeError(w, http.StatusUnauthorized, "missing_tenant_context",
				"cutover: nenhum tenant verificado no contexto — RequireOwnership deve vir depois de tenancy.Middleware")
			return
		}

		end, err := Acquire(guard, tenant, capability)
		if err != nil {
			switch {
			case errors.Is(err, ErrNotOwner):
				writeError(w, http.StatusConflict, "route_mismatch",
					"cutover: este backend não é o proprietário de escrita atual para esta capacidade/tenant")
			case errors.Is(err, ErrRouteDraining):
				writeError(w, http.StatusServiceUnavailable, "route_draining",
					"cutover: troca de rota em andamento, tente novamente em instantes")
			default:
				writeError(w, http.StatusInternalServerError, "cutover_check_failed", err.Error())
			}
			return
		}
		defer end()
		next.ServeHTTP(w, r)
	})
}

// errorEnvelope replica o schema Error de migracao/contracts/openapi/common.yaml
// (mesmo formato usado por internal/platform/tenancy.writeError — duplicado
// aqui porque tenancy não exporta seu helper, e os dois pacotes não devem
// depender um do outro só por causa de um formato de erro).
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
