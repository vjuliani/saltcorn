package identity

import "testing"

func TestCanRead(t *testing.T) {
	cases := []struct {
		actorRole, minRoleRead RoleID
		want                   bool
	}{
		{RoleAdmin, RolePublic, true},  // admin sempre passa em qualquer mínimo mais permissivo
		{RolePublic, RoleAdmin, false}, // público não passa em um mínimo mais restrito
		{RoleAdmin, RoleAdmin, true},   // igual ao mínimo passa
		{RolePublic, RolePublic, true}, // público passa no mínimo público
	}
	for _, c := range cases {
		if got := CanRead(c.actorRole, c.minRoleRead); got != c.want {
			t.Errorf("CanRead(%d, %d) = %v, esperado %v", c.actorRole, c.minRoleRead, got, c.want)
		}
	}
}

func TestCanWrite(t *testing.T) {
	if !CanWrite(RoleAdmin, RolePublic) {
		t.Error("admin deveria poder escrever mesmo com min_role_write permissivo")
	}
	if CanWrite(RolePublic, RoleAdmin) {
		t.Error("público não deveria poder escrever com min_role_write restrito a admin")
	}
}
