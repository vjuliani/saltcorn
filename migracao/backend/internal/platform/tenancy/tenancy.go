// Package tenancy resolve e propaga tenant e ator via context.Context — a
// substituição explícita do padrão AsyncLocalStorage da produção Node
// (packages/db-common/multi-tenant.ts), que não tem equivalente direto em
// Go (matriz de capacidades GO-001 §2.1). Nenhuma chamada de banco ou HTTP
// deve inferir tenant/ator de outro lugar que não o contexto propagado por
// este pacote.
package tenancy

import "context"

type contextKey int

const (
	tenantKey contextKey = iota
	actorKey
)

// Tenant identifica um tenant já resolvido e validado (ver Middleware) —
// nunca um valor aceito diretamente de um header ou parâmetro sem checagem
// cruzada com a identidade delegada.
type Tenant string

// WithTenant retorna um contexto derivado carregando o tenant informado.
func WithTenant(ctx context.Context, t Tenant) context.Context {
	return context.WithValue(ctx, tenantKey, t)
}

// TenantFromContext lê o tenant do contexto, se presente.
func TenantFromContext(ctx context.Context) (Tenant, bool) {
	t, ok := ctx.Value(tenantKey).(Tenant)
	return t, ok
}

// WithActor retorna um contexto derivado carregando o identificador do ator
// (claim `sub` do token de identidade delegada).
func WithActor(ctx context.Context, actor string) context.Context {
	return context.WithValue(ctx, actorKey, actor)
}

// ActorFromContext lê o ator do contexto, se presente.
func ActorFromContext(ctx context.Context) (string, bool) {
	a, ok := ctx.Value(actorKey).(string)
	return a, ok
}
