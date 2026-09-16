package identity

import (
	"errors"
	"testing"
)

func TestRequireNativeStrategy_Native(t *testing.T) {
	for _, s := range []AuthStrategy{AuthStrategyPassword, AuthStrategyAPIToken, AuthStrategyTOTP} {
		if err := RequireNativeStrategy(s); err != nil {
			t.Errorf("RequireNativeStrategy(%q) erro inesperado: %v", s, err)
		}
	}
}

// TestRequireNativeStrategy_PluginBlocksCutover é o teste que sustenta
// diretamente o critério de aceite "estratégias de plugins sem suporte
// bloqueiam o corte": uma estratégia desconhecida (representando um plugin
// de OAuth/social login de terceiro) precisa falhar de forma explícita e
// classificável, não ser aceita nem silenciosamente ignorada.
func TestRequireNativeStrategy_PluginBlocksCutover(t *testing.T) {
	err := RequireNativeStrategy(AuthStrategy("oauth_google_plugin"))
	if err == nil {
		t.Fatal("esperava erro para estratégia de plugin desconhecida, obteve nil")
	}
	if !errors.Is(err, ErrUnsupportedAuthStrategy) {
		t.Errorf("erro = %v, esperado ErrUnsupportedAuthStrategy", err)
	}
}

func TestRequireNativeStrategy_EmptyStrategy(t *testing.T) {
	if err := RequireNativeStrategy(""); !errors.Is(err, ErrUnsupportedAuthStrategy) {
		t.Errorf("erro para estratégia vazia = %v, esperado ErrUnsupportedAuthStrategy", err)
	}
}
