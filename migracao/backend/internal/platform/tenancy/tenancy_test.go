package tenancy

import (
	"context"
	"testing"
)

func TestWithTenant_RoundTrip(t *testing.T) {
	ctx := WithTenant(context.Background(), Tenant("acme"))
	got, ok := TenantFromContext(ctx)
	if !ok {
		t.Fatal("TenantFromContext: ok = false, esperado true")
	}
	if got != "acme" {
		t.Errorf("TenantFromContext = %q, esperado acme", got)
	}
}

func TestTenantFromContext_AbsentByDefault(t *testing.T) {
	_, ok := TenantFromContext(context.Background())
	if ok {
		t.Error("TenantFromContext em contexto vazio: ok = true, esperado false")
	}
}

func TestWithActor_RoundTrip(t *testing.T) {
	ctx := WithActor(context.Background(), "user-42")
	got, ok := ActorFromContext(ctx)
	if !ok {
		t.Fatal("ActorFromContext: ok = false, esperado true")
	}
	if got != "user-42" {
		t.Errorf("ActorFromContext = %q, esperado user-42", got)
	}
}

// TestContext_DoesNotLeakBetweenDerivedContexts confirma a garantia mais
// básica de que este pacote depende: context.Context é imutável — derivar
// um contexto com um tenant diferente nunca afeta o contexto original. Isso
// é comportamento da stdlib, não deste pacote, mas é exatamente a premissa
// que torna WithTenant seguro para uso concorrente sem lock nenhum aqui.
func TestContext_DoesNotLeakBetweenDerivedContexts(t *testing.T) {
	base := WithTenant(context.Background(), Tenant("acme"))
	derived := WithTenant(base, Tenant("beta"))

	baseTenant, _ := TenantFromContext(base)
	derivedTenant, _ := TenantFromContext(derived)

	if baseTenant != "acme" {
		t.Errorf("contexto base mudou para %q após derivar outro contexto — vazamento", baseTenant)
	}
	if derivedTenant != "beta" {
		t.Errorf("contexto derivado = %q, esperado beta", derivedTenant)
	}
}

func TestSchemaName(t *testing.T) {
	cases := []struct {
		tenant Tenant
		want   string
	}{
		{"acme", "acme"},
		{"acme-corp", "acmecorp"},      // hífen removido (mesma regra do sqlsanitize legado)
		{"9tenant", "_9tenant"},        // não pode começar com dígito
		{"tenant_name", "tenant_name"}, // underscore preservado
		{"DROP TABLE;--", "DROPTABLE"}, // caracteres perigosos removidos, não escapados
		{"", "_"},                      // string vazia não pode virar identificador vazio
		{"café", "café"},               // letra Unicode preservada (mesma regra \p{L} do legado)
	}
	for _, c := range cases {
		got := SchemaName(c.tenant)
		if got != c.want {
			t.Errorf("SchemaName(%q) = %q, esperado %q", c.tenant, got, c.want)
		}
	}
}
