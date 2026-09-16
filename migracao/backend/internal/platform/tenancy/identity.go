package tenancy

import (
	"errors"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// DelegatedIdentity são as claims extraídas do token de identidade delegada
// (ADR-0003; contrato `ServiceIdentity` em
// migracao/contracts/openapi/internal-api.yaml).
type DelegatedIdentity struct {
	Actor  string
	Tenant Tenant
}

var (
	ErrMissingToken   = errors.New("tenancy: token de identidade delegada ausente")
	ErrMalformedToken = errors.New("tenancy: token de identidade delegada malformado")
	ErrTokenExpired   = errors.New("tenancy: token de identidade delegada expirado")
	ErrTenantMismatch = errors.New("tenancy: tenant do token não corresponde ao tenant do recurso")
)

// ParseDelegatedIdentity lê as claims `sub`/`tenant`/`exp` de um JWT.
//
// ATENÇÃO — não valida a assinatura do token. `jwt.ParseUnverified` decodifica
// as claims sem verificar que quem assinou é realmente o BFF; um token com
// qualquer assinatura (inclusive nenhuma) passa por aqui. A verificação
// criptográfica (chave pública/segredo compartilhado, GO-008/GO-009) ainda
// não existe neste código — isso é uma lacuna deliberada e documentada desta
// tarefa (GO-007 cobre propagação de contexto, não autenticação), não um
// descuido. Nenhum caminho de escrita real deve depender só disto até
// GO-008/GO-009 substituírem esta função por uma verificação completa.
func ParseDelegatedIdentity(bearerToken string) (DelegatedIdentity, error) {
	if bearerToken == "" {
		return DelegatedIdentity{}, ErrMissingToken
	}

	claims := jwt.MapClaims{}
	parser := jwt.NewParser()
	if _, _, err := parser.ParseUnverified(bearerToken, claims); err != nil {
		return DelegatedIdentity{}, fmt.Errorf("%w: %v", ErrMalformedToken, err)
	}

	sub, _ := claims["sub"].(string)
	tenantClaim, _ := claims["tenant"].(string)
	if sub == "" || tenantClaim == "" {
		return DelegatedIdentity{}, fmt.Errorf("%w: claims sub/tenant ausentes ou vazias", ErrMalformedToken)
	}

	expFloat, ok := claims["exp"].(float64)
	if !ok {
		return DelegatedIdentity{}, fmt.Errorf("%w: claim exp ausente", ErrMalformedToken)
	}
	if time.Now().After(time.Unix(int64(expFloat), 0)) {
		return DelegatedIdentity{}, ErrTokenExpired
	}

	return DelegatedIdentity{Actor: sub, Tenant: Tenant(tenantClaim)}, nil
}
