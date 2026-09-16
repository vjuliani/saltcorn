package identity

import "testing"

func TestGenerateAPIToken_VerifyRoundTrip(t *testing.T) {
	plaintext, hash, err := GenerateAPIToken()
	if err != nil {
		t.Fatalf("GenerateAPIToken() erro inesperado: %v", err)
	}
	if plaintext == "" || hash == "" {
		t.Fatal("plaintext ou hash vazios")
	}
	if plaintext == hash {
		t.Fatal("hash igual ao plaintext — token não foi hasheado")
	}
	if !VerifyAPIToken(plaintext, hash) {
		t.Error("VerifyAPIToken com o token correto retornou false")
	}
}

func TestVerifyAPIToken_WrongToken(t *testing.T) {
	_, hash, _ := GenerateAPIToken()
	if VerifyAPIToken("token-completamente-diferente", hash) {
		t.Error("VerifyAPIToken com token errado retornou true")
	}
}

func TestGenerateAPIToken_Uniqueness(t *testing.T) {
	p1, h1, _ := GenerateAPIToken()
	p2, h2, _ := GenerateAPIToken()
	if p1 == p2 || h1 == h2 {
		t.Error("dois tokens gerados são idênticos — fonte de entropia suspeita")
	}
}

func TestHashAPIToken_Deterministic(t *testing.T) {
	if HashAPIToken("abc") != HashAPIToken("abc") {
		t.Error("HashAPIToken não é determinístico para a mesma entrada")
	}
}
