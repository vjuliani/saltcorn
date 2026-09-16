package identity

import "testing"

func TestIsOwnerByField(t *testing.T) {
	if !IsOwnerByField("user-1", "user-1") {
		t.Error("mesmo ator deveria ser dono")
	}
	if IsOwnerByField("user-1", "user-2") {
		t.Error("ator diferente não deveria ser dono")
	}
	if IsOwnerByField("", "") {
		t.Error("dois valores vazios não deveriam contar como ownership válido")
	}
}
