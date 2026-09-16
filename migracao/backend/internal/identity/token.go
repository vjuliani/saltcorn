package identity

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
)

// tokenBytes é o tamanho, em bytes de entropia, de um token de API gerado —
// 32 bytes (256 bits) é bem acima do necessário para inviabilizar força
// bruta, e o dobro do que a maioria das bibliotecas de sessão usa como piso.
const tokenBytes = 32

// GenerateAPIToken cria um novo token de API. Retorna o token em texto
// plano (mostrado ao usuário **uma única vez**, no momento da criação — não
// é recuperável depois) e o hash correspondente (o que efetivamente é
// armazenado). Isso é uma melhoria deliberada sobre a produção Node, que
// guarda o token em texto plano na tabela `_sc_api_tokens` (matriz GO-001
// §2.1) — aqui, um vazamento do banco não expõe tokens utilizáveis
// diretamente. Comportamento externo (o token que o cliente usa) não muda.
func GenerateAPIToken() (plaintext string, hash string, err error) {
	buf := make([]byte, tokenBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", "", fmt.Errorf("identity: gerar token: %w", err)
	}
	plaintext = hex.EncodeToString(buf)
	return plaintext, HashAPIToken(plaintext), nil
}

// HashAPIToken produz o hash determinístico de um token em texto plano, para
// comparação em busca por igualdade (nunca por prefixo, o que vazaria
// informação por timing) — ver VerifyAPIToken.
func HashAPIToken(plaintext string) string {
	sum := sha256.Sum256([]byte(plaintext))
	return hex.EncodeToString(sum[:])
}

// VerifyAPIToken compara um token em texto plano (recebido em uma
// requisição) contra um hash já conhecido (ex.: lido do banco), em tempo
// constante — evita um ataque de timing que meça quantos bytes do hash
// batem para adivinhar o token byte a byte.
func VerifyAPIToken(plaintext, knownHash string) bool {
	computed := HashAPIToken(plaintext)
	return subtle.ConstantTimeCompare([]byte(computed), []byte(knownHash)) == 1
}
