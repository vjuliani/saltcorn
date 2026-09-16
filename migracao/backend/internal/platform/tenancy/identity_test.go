package tenancy

import (
	"errors"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// mintTestToken monta um JWT com as claims dadas, assinado com uma chave
// qualquer — ParseDelegatedIdentity não verifica assinatura (documentado em
// identity.go), então o valor da chave é irrelevante para estes testes;
// existe só para produzir um token bem formado.
func mintTestToken(t *testing.T, claims jwt.MapClaims) string {
	t.Helper()
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, err := token.SignedString([]byte("chave-de-teste-irrelevante"))
	if err != nil {
		t.Fatalf("mintTestToken: %v", err)
	}
	return signed
}

func TestParseDelegatedIdentity_Valid(t *testing.T) {
	token := mintTestToken(t, jwt.MapClaims{
		"sub":    "42",
		"tenant": "acme",
		"iat":    time.Now().Unix(),
		"exp":    time.Now().Add(time.Minute).Unix(),
	})

	identity, err := ParseDelegatedIdentity(token)
	if err != nil {
		t.Fatalf("ParseDelegatedIdentity() erro inesperado: %v", err)
	}
	if identity.Actor != "42" {
		t.Errorf("Actor = %q, esperado 42", identity.Actor)
	}
	if identity.Tenant != "acme" {
		t.Errorf("Tenant = %q, esperado acme", identity.Tenant)
	}
}

func TestParseDelegatedIdentity_Empty(t *testing.T) {
	_, err := ParseDelegatedIdentity("")
	if !errors.Is(err, ErrMissingToken) {
		t.Errorf("erro = %v, esperado ErrMissingToken", err)
	}
}

func TestParseDelegatedIdentity_Malformed(t *testing.T) {
	_, err := ParseDelegatedIdentity("isto-nao-e-um-jwt")
	if !errors.Is(err, ErrMalformedToken) {
		t.Errorf("erro = %v, esperado ErrMalformedToken", err)
	}
}

func TestParseDelegatedIdentity_MissingClaims(t *testing.T) {
	token := mintTestToken(t, jwt.MapClaims{
		"exp": time.Now().Add(time.Minute).Unix(),
		// sub e tenant ausentes de propósito
	})
	_, err := ParseDelegatedIdentity(token)
	if !errors.Is(err, ErrMalformedToken) {
		t.Errorf("erro = %v, esperado ErrMalformedToken", err)
	}
}

func TestParseDelegatedIdentity_Expired(t *testing.T) {
	token := mintTestToken(t, jwt.MapClaims{
		"sub":    "42",
		"tenant": "acme",
		"exp":    time.Now().Add(-time.Minute).Unix(),
	})
	_, err := ParseDelegatedIdentity(token)
	if !errors.Is(err, ErrTokenExpired) {
		t.Errorf("erro = %v, esperado ErrTokenExpired", err)
	}
}

func TestParseDelegatedIdentity_MissingExp(t *testing.T) {
	token := mintTestToken(t, jwt.MapClaims{
		"sub":    "42",
		"tenant": "acme",
	})
	_, err := ParseDelegatedIdentity(token)
	if !errors.Is(err, ErrMalformedToken) {
		t.Errorf("erro = %v, esperado ErrMalformedToken (exp ausente)", err)
	}
}
