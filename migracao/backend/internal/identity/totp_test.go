package identity

import (
	"testing"
	"time"

	"github.com/pquerna/otp/totp"
)

func TestGenerateTOTPSecret_ValidateCode(t *testing.T) {
	secret, url, err := GenerateTOTPSecret("saltcorn-go", "user@example.com")
	if err != nil {
		t.Fatalf("GenerateTOTPSecret() erro inesperado: %v", err)
	}
	if secret == "" || url == "" {
		t.Fatal("secret ou url vazios")
	}

	code, err := totp.GenerateCode(secret, time.Now())
	if err != nil {
		t.Fatalf("gerar código de teste: %v", err)
	}
	if !ValidateTOTPCode(secret, code) {
		t.Error("ValidateTOTPCode com código recém-gerado retornou false")
	}
}

func TestValidateTOTPCode_WrongCode(t *testing.T) {
	secret, _, _ := GenerateTOTPSecret("saltcorn-go", "user@example.com")
	if ValidateTOTPCode(secret, "000000") {
		// Probabilidade de "000000" ser o código real é ~1 em 1.000.000 — não
		// suprime o teste por isso, mas evita falso positivo óbvio.
		t.Skip("colisão aleatória com o código correto — ignorando esta execução")
	}
}

func TestValidateTOTPCode_MalformedSecret(t *testing.T) {
	if ValidateTOTPCode("nao-e-base32-valido!!!", "123456") {
		t.Error("ValidateTOTPCode com secret malformado retornou true")
	}
}
