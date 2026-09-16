package identity

import (
	"errors"
	"fmt"
)

// AuthStrategy identifica o mecanismo de autenticação de uma tentativa de
// login/verificação. As estratégias nativas são as únicas que este backend
// sabe processar — qualquer outra (tipicamente login social/OAuth fornecido
// por um plugin de terceiro, matriz GO-001 §2.1) precisa ser rejeitada
// explicitamente, nunca tratada como sucesso nem ignorada silenciosamente.
type AuthStrategy string

const (
	AuthStrategyPassword AuthStrategy = "password"
	AuthStrategyAPIToken AuthStrategy = "api_token"
	AuthStrategyTOTP     AuthStrategy = "totp"
)

var nativeStrategies = map[AuthStrategy]bool{
	AuthStrategyPassword: true,
	AuthStrategyAPIToken: true,
	AuthStrategyTOTP:     true,
}

// ErrUnsupportedAuthStrategy é o erro retornado para qualquer estratégia que
// não seja uma das nativas — um plugin de autenticação de terceiro não
// portado (ADR-0005) é o caso típico. Isso **bloqueia o corte** para
// qualquer aplicação/tenant que dependa dessa estratégia (ADR-0006): a
// aplicação continua no caminho legado até a estratégia ser portada, virar
// adapter, ou ser explicitamente aceita como bloqueador de escopo — nunca
// silenciosamente autenticada nem silenciosamente recusada sem essa
// classificação.
var ErrUnsupportedAuthStrategy = errors.New(
	"identity: estratégia de autenticação não suportada neste backend — bloqueador de corte, ver ADR-0005/ADR-0006",
)

// RequireNativeStrategy falha fechado: retorna erro para qualquer valor que
// não seja uma constante AuthStrategy* nativa. Quem chama nunca deve tratar
// uma estratégia desconhecida como "sem preferência" ou "assume password" —
// isso mascararia exatamente o cenário que este guard existe para pegar.
func RequireNativeStrategy(s AuthStrategy) error {
	if !nativeStrategies[s] {
		return fmt.Errorf("%w: %q", ErrUnsupportedAuthStrategy, s)
	}
	return nil
}
