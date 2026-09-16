package tenancy

import (
	"errors"
	"fmt"

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
	ErrMissingToken     = errors.New("tenancy: token de identidade delegada ausente")
	ErrMalformedToken   = errors.New("tenancy: token de identidade delegada malformado")
	ErrTokenExpired     = errors.New("tenancy: token de identidade delegada expirado")
	ErrInvalidSignature = errors.New("tenancy: assinatura do token de identidade delegada não confere")
	ErrTenantMismatch   = errors.New("tenancy: tenant do token não corresponde ao tenant do recurso")
)

// minSecretBytes é o piso de tamanho do segredo compartilhado HMAC — 32
// bytes (256 bits) é o mínimo recomendado para HS256; um segredo mais curto
// facilita força bruta offline sobre tokens capturados.
const minSecretBytes = 32

// Verifier valida e decodifica tokens de identidade delegada assinados com
// HMAC-SHA256, usando um segredo compartilhado entre o BFF (que assina) e
// este backend (que verifica) — GO-008 substitui o `jwt.ParseUnverified` de
// GO-007, que lia claims sem checar quem as assinou.
type Verifier struct {
	secret []byte
}

// NewVerifier valida o segredo antes de aceitá-lo — falhar cedo aqui evita
// subir um processo que aceitaria qualquer token por ter um segredo vazio
// ou fraco de menos.
func NewVerifier(secret []byte) (*Verifier, error) {
	if len(secret) < minSecretBytes {
		return nil, fmt.Errorf("tenancy: segredo de identidade delegada precisa ter pelo menos %d bytes, recebeu %d", minSecretBytes, len(secret))
	}
	return &Verifier{secret: secret}, nil
}

// ParseDelegatedIdentity verifica a assinatura HS256 do token contra o
// segredo do Verifier, valida a expiração (`exp` é obrigatório — um token
// sem `exp` é rejeitado, não tratado como "nunca expira"), e extrai as
// claims `sub`/`tenant`.
func (v *Verifier) ParseDelegatedIdentity(bearerToken string) (DelegatedIdentity, error) {
	if bearerToken == "" {
		return DelegatedIdentity{}, ErrMissingToken
	}

	claims := jwt.MapClaims{}
	token, err := jwt.ParseWithClaims(bearerToken, claims, func(t *jwt.Token) (interface{}, error) {
		return v.secret, nil
	}, jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}), jwt.WithExpirationRequired())

	if err != nil {
		if errors.Is(err, jwt.ErrTokenExpired) {
			return DelegatedIdentity{}, ErrTokenExpired
		}
		if errors.Is(err, jwt.ErrTokenSignatureInvalid) {
			return DelegatedIdentity{}, fmt.Errorf("%w: %v", ErrInvalidSignature, err)
		}
		return DelegatedIdentity{}, fmt.Errorf("%w: %v", ErrMalformedToken, err)
	}
	if !token.Valid {
		return DelegatedIdentity{}, ErrInvalidSignature
	}

	sub, _ := claims["sub"].(string)
	tenantClaim, _ := claims["tenant"].(string)
	if sub == "" || tenantClaim == "" {
		return DelegatedIdentity{}, fmt.Errorf("%w: claims sub/tenant ausentes ou vazias", ErrMalformedToken)
	}

	return DelegatedIdentity{Actor: sub, Tenant: Tenant(tenantClaim)}, nil
}
