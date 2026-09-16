package tenancy

import (
	"errors"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// testSecret é o segredo usado por padrão nos testes deste arquivo — 32
// bytes, o piso exigido por NewVerifier.
const testSecret = "01234567890123456789012345678901"

func testVerifier(t *testing.T) *Verifier {
	t.Helper()
	v, err := NewVerifier([]byte(testSecret))
	if err != nil {
		t.Fatalf("NewVerifier: %v", err)
	}
	return v
}

// mintTestToken monta um JWT com as claims dadas, assinado com secret.
func mintTestToken(t *testing.T, secret string, claims jwt.MapClaims) string {
	t.Helper()
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, err := token.SignedString([]byte(secret))
	if err != nil {
		t.Fatalf("mintTestToken: %v", err)
	}
	return signed
}

func TestNewVerifier_RejectsShortSecret(t *testing.T) {
	if _, err := NewVerifier([]byte("segredo-curto-demais")); err == nil {
		t.Error("NewVerifier com segredo curto deveria falhar")
	}
}

func TestParseDelegatedIdentity_Valid(t *testing.T) {
	v := testVerifier(t)
	token := mintTestToken(t, testSecret, jwt.MapClaims{
		"sub":    "42",
		"tenant": "acme",
		"iat":    time.Now().Unix(),
		"exp":    time.Now().Add(time.Minute).Unix(),
	})

	identity, err := v.ParseDelegatedIdentity(token)
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
	v := testVerifier(t)
	if _, err := v.ParseDelegatedIdentity(""); !errors.Is(err, ErrMissingToken) {
		t.Errorf("erro = %v, esperado ErrMissingToken", err)
	}
}

func TestParseDelegatedIdentity_Malformed(t *testing.T) {
	v := testVerifier(t)
	if _, err := v.ParseDelegatedIdentity("isto-nao-e-um-jwt"); !errors.Is(err, ErrMalformedToken) {
		t.Errorf("erro = %v, esperado ErrMalformedToken", err)
	}
}

func TestParseDelegatedIdentity_MissingClaims(t *testing.T) {
	v := testVerifier(t)
	token := mintTestToken(t, testSecret, jwt.MapClaims{
		"exp": time.Now().Add(time.Minute).Unix(),
		// sub e tenant ausentes de propósito
	})
	if _, err := v.ParseDelegatedIdentity(token); !errors.Is(err, ErrMalformedToken) {
		t.Errorf("erro = %v, esperado ErrMalformedToken", err)
	}
}

func TestParseDelegatedIdentity_Expired(t *testing.T) {
	v := testVerifier(t)
	token := mintTestToken(t, testSecret, jwt.MapClaims{
		"sub":    "42",
		"tenant": "acme",
		"exp":    time.Now().Add(-time.Minute).Unix(),
	})
	if _, err := v.ParseDelegatedIdentity(token); !errors.Is(err, ErrTokenExpired) {
		t.Errorf("erro = %v, esperado ErrTokenExpired", err)
	}
}

func TestParseDelegatedIdentity_MissingExp(t *testing.T) {
	v := testVerifier(t)
	token := mintTestToken(t, testSecret, jwt.MapClaims{
		"sub":    "42",
		"tenant": "acme",
	})
	if _, err := v.ParseDelegatedIdentity(token); !errors.Is(err, ErrMalformedToken) {
		t.Errorf("erro = %v, esperado ErrMalformedToken (exp ausente)", err)
	}
}

// TestParseDelegatedIdentity_WrongSignature é o teste que GO-007 não podia
// escrever (usava jwt.ParseUnverified, que nunca checa assinatura): um token
// com claims corretas, mas assinado com uma chave diferente da que o
// Verifier conhece — simula um token forjado por quem não é o BFF real.
func TestParseDelegatedIdentity_WrongSignature(t *testing.T) {
	v := testVerifier(t)
	token := mintTestToken(t, "outra-chave-completamente-diferente-32b", jwt.MapClaims{
		"sub":    "42",
		"tenant": "acme",
		"exp":    time.Now().Add(time.Minute).Unix(),
	})
	_, err := v.ParseDelegatedIdentity(token)
	if !errors.Is(err, ErrInvalidSignature) {
		t.Errorf("erro = %v, esperado ErrInvalidSignature", err)
	}
}

// TestParseDelegatedIdentity_AlgNoneRejected confirma que um token que tenta
// usar o algoritmo "none" (sem assinatura nenhuma — um ataque clássico
// contra bibliotecas JWT mal configuradas) é rejeitado, porque o Verifier
// restringe explicitamente os algoritmos aceitos a HS256
// (jwt.WithValidMethods).
func TestParseDelegatedIdentity_AlgNoneRejected(t *testing.T) {
	v := testVerifier(t)
	claims := jwt.MapClaims{
		"sub":    "42",
		"tenant": "acme",
		"exp":    time.Now().Add(time.Minute).Unix(),
	}
	token := jwt.NewWithClaims(jwt.SigningMethodNone, claims)
	signed, err := token.SignedString(jwt.UnsafeAllowNoneSignatureType)
	if err != nil {
		t.Fatalf("mintar token alg=none: %v", err)
	}
	if _, err := v.ParseDelegatedIdentity(signed); err == nil {
		t.Error("token com alg=none deveria ser rejeitado, foi aceito")
	}
}
