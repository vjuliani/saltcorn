// Package identity implementa o domínio de identidade e autorização do
// backend Go: senhas, papéis, ownership, tokens de API e MFA/TOTP (GO-008).
// É um pacote de domínio (não `internal/platform/*`) — tem suas próprias
// regras de negócio, ao contrário de tenancy/database/config/health/shutdown,
// que são infraestrutura cross-cutting reutilizada por todo domínio
// (ADR-0001).
package identity

import "golang.org/x/crypto/bcrypt"

// HashPassword produz um hash bcrypt da senha em texto plano — mesmo
// algoritmo da produção Node (`bcryptjs`, matriz GO-001 §2.1), então uma
// migração de dados real pode reutilizar os hashes existentes sem forçar
// reset de senha de todo usuário.
func HashPassword(plain string) (string, error) {
	hash, err := bcrypt.GenerateFromPassword([]byte(plain), bcrypt.DefaultCost)
	if err != nil {
		return "", err
	}
	return string(hash), nil
}

// CheckPassword compara uma senha em texto plano com um hash bcrypt
// existente. Retorna false tanto para senha errada quanto para hash
// malformado — quem chama não precisa (e não deve) distinguir os dois casos
// para o usuário final.
func CheckPassword(hash, plain string) bool {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(plain)) == nil
}
