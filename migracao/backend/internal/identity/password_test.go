package identity

import "testing"

func TestHashPassword_CheckPassword_RoundTrip(t *testing.T) {
	hash, err := HashPassword("hunter2")
	if err != nil {
		t.Fatalf("HashPassword() erro inesperado: %v", err)
	}
	if hash == "hunter2" {
		t.Fatal("hash igual à senha em texto plano — não foi hasheada")
	}
	if !CheckPassword(hash, "hunter2") {
		t.Error("CheckPassword com a senha correta retornou false")
	}
}

func TestCheckPassword_WrongPassword(t *testing.T) {
	hash, _ := HashPassword("hunter2")
	if CheckPassword(hash, "senha-errada") {
		t.Error("CheckPassword com senha errada retornou true")
	}
}

func TestCheckPassword_MalformedHash(t *testing.T) {
	if CheckPassword("isto-nao-e-um-hash-bcrypt", "qualquer-coisa") {
		t.Error("CheckPassword com hash malformado retornou true")
	}
}

func TestHashPassword_DifferentSaltsEachTime(t *testing.T) {
	h1, _ := HashPassword("hunter2")
	h2, _ := HashPassword("hunter2")
	if h1 == h2 {
		t.Error("dois hashes da mesma senha são idênticos — bcrypt deveria usar salt aleatório")
	}
}
