package cutover

import "github.com/vjuliani/saltcorn/migracao/backend/internal/platform/tenancy"

// Acquire é o ponto de entrada único usado tanto por rotas HTTP
// (RequireOwnership) quanto por jobs de background (cmd/worker) para pedir
// permissão de registrar uma unidade de trabalho de escrita — "bloquear
// caminhos alternativos, inclusive jobs" (critério de aceite de GO-009) é
// isto: os dois caminhos passam pela mesma checagem, não duas
// implementações separadas. Consulta só o cache em memória da Guard (ver
// LoadFromRegistry e SwitchOwner), nunca o banco diretamente.
func Acquire(guard *Guard, tenant tenancy.Tenant, capability string) (func(), error) {
	return guard.Begin(tenant, capability)
}
