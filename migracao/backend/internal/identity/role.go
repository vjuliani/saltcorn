package identity

// RoleID replica a convenção da produção Node: role_id 1 é admin, 100 é
// público/anônimo, papéis intermediários (staff/usuário) são configuráveis
// (matriz GO-001 §2.1, `models/role.ts`). O modelo em si é deliberadamente
// simples — id + nome — o enforcement é que fica espalhado pelas regras de
// cada operação, não neste tipo.
type RoleID int

const (
	// RoleAdmin tem acesso irrestrito (equivalente a role_id=1 na produção).
	RoleAdmin RoleID = 1
	// RolePublic é o papel de um ator não autenticado (equivalente a
	// role_id=100 na produção) — usado como piso de min_role_read por
	// padrão quando um recurso não define o próprio mínimo.
	RolePublic RoleID = 100
)

// Role é um papel nomeado, com o mesmo shape simples da produção Node.
type Role struct {
	ID   RoleID
	Name string
}

// CanRead reporta se um ator com role atende ao mínimo de leitura exigido —
// quanto MENOR o RoleID, mais privilegiado (1=admin é o mais privilegiado,
// 100=público é o menos). Um role_id maior que o mínimo exigido é negado.
func CanRead(actorRole, minRoleRead RoleID) bool {
	return actorRole <= minRoleRead
}

// CanWrite tem a mesma semântica de CanRead, para o mínimo de escrita.
func CanWrite(actorRole, minRoleWrite RoleID) bool {
	return actorRole <= minRoleWrite
}
