package identity

import (
	"time"

	"github.com/pquerna/otp"
	"github.com/pquerna/otp/totp"
)

// GenerateTOTPSecret cria um novo segredo TOTP (RFC 6238) para MFA — mesma
// família de algoritmo usada pela produção Node (`passport-totp` +
// `thirty-two` + `notp`, matriz GO-001 §2.1). accountName é tipicamente o
// e-mail do usuário; issuer identifica o sistema e aparece no app
// autenticador. otpauthURL pode virar um QR code do lado de quem chama
// (geração de imagem fica fora deste pacote — é apresentação, não domínio).
func GenerateTOTPSecret(issuer, accountName string) (secret string, otpauthURL string, err error) {
	key, err := totp.Generate(totp.GenerateOpts{
		Issuer:      issuer,
		AccountName: accountName,
	})
	if err != nil {
		return "", "", err
	}
	return key.Secret(), key.URL(), nil
}

// ValidateTOTPCode confirma um código de 6 dígitos contra o segredo do
// usuário, com uma janela de tolerância de ±1 período de 30s (Skew: 1) para
// absorver pequena dessincronia de relógio entre servidor e app autenticador
// — o padrão de `totp.Validate` sozinho não tolera nenhuma dessincronia.
func ValidateTOTPCode(secret, code string) bool {
	valid, err := totp.ValidateCustom(code, secret, time.Now(), totp.ValidateOpts{
		Period:    30,
		Skew:      1,
		Digits:    otp.DigitsSix,
		Algorithm: otp.AlgorithmSHA1,
	})
	if err != nil {
		return false
	}
	return valid
}
