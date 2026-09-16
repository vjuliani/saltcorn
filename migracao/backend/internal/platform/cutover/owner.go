// Package cutover implementa o registro de ownership de escrita por
// tenant/capacidade e a guarda de admissão/drenagem que qualquer caminho de
// escrita do backend Go (rota HTTP ou job) deve consultar antes de agir —
// o mecanismo central do critério de aceite de GO-009 ("teste comprova
// escritor único e rollback de rota; identidade não pode ser forjada por
// headers; requisições em andamento são drenadas").
//
// Este pacote não implementa um proxy HTTP real para o backend Node
// legado — isso é infraestrutura de borda (gateway/nginx), fora do alcance
// deste módulo (ver ADR-0008). O que existe aqui é o registro de quem tem
// permissão de escrever, por tenant+capacidade, e a garantia de que o
// próprio backend Go se recusa a agir quando não é o proprietário
// registrado — a metade do mecanismo que é testável e que o lado Go
// realmente precisa ter, hoje, para o corte gradual (README §3 item 6,
// "canário por tenant/capacidade") ser seguro.
package cutover

import "errors"

// Owner identifica qual backend tem permissão de escrita para um
// tenant+capacidade. Não existe um terceiro valor "nenhum": toda
// tenant+capacidade tem um proprietário, mesmo que nunca tenha sido
// registrado explicitamente (ver OwnerOf, padrão seguro é OwnerLegacy).
type Owner string

const (
	// OwnerLegacy é o padrão seguro: nada é servido por Go sem uma decisão
	// explícita de corte (ADR-0006, "não seguem em uso sem decisão").
	OwnerLegacy Owner = "legacy"
	// OwnerGo indica que o backend Go é o proprietário de escrita atual
	// para esse tenant+capacidade.
	OwnerGo Owner = "go"
)

// ErrNotOwner é retornado por Acquire quando este backend (Go) não é o
// proprietário de escrita registrado para o tenant+capacidade pedido —
// quem chama deve recusar a operação (ex.: responder 409 numa rota HTTP,
// pular o job), nunca prosseguir mesmo assim.
var ErrNotOwner = errors.New("cutover: este backend não é o proprietário de escrita registrado para este tenant/capacidade")

// ErrRouteDraining é retornado por Acquire quando uma troca de owner
// (SwitchOwner) está em andamento para este tenant+capacidade — a janela
// entre bloquear admissão nova e efetivamente trocar o registro. Quem
// chama deve tratar como indisponibilidade transitória (ex.: 503),
// diferente de ErrNotOwner (que é uma decisão estável, não uma corrida).
var ErrRouteDraining = errors.New("cutover: troca de rota em andamento para este tenant/capacidade, tente novamente em instantes")
