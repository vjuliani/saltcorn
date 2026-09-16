package identity

import "errors"

// ErrOwnershipFormulaUnsupported é retornado por qualquer tentativa de
// avaliar ownership por fórmula JavaScript — essa capacidade depende do
// motor de expressões (ADR-0005, GO-004), que ainda não existe no backend
// Go. Isso é um bloqueador de corte explícito, não um TODO silencioso:
// qualquer tabela cujo ownership dependa de fórmula fica no caminho legado
// até o host temporário de plugins existir (GO-022) ou a fórmula ser
// reescrita como ownership por campo.
var ErrOwnershipFormulaUnsupported = errors.New(
	"identity: ownership por fórmula JS não é suportado neste backend — bloqueador de corte, ver ADR-0005",
)

// IsOwnerByField reporta se actorID é o dono de um registro cujo campo de
// ownership tem o valor ownerFieldValue — o caso simples (comparação direta
// de identificador), que cobre a maioria das tabelas na matriz GO-001 §2.1.
// Não faz nenhuma chamada de rede nem de banco: é uma função pura sobre
// valores já carregados.
func IsOwnerByField(actorID string, ownerFieldValue string) bool {
	return actorID != "" && actorID == ownerFieldValue
}
