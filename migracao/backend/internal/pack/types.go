// Package pack implementa o formato de export/import de aplicação
// inteira (GO-027) — o equivalente reduzido do Pack do legado
// (packages/saltcorn-admin-models/models/pack.ts, tipo `Pack` em
// packages/saltcorn-types/base_types.ts:779-794).
//
// Cobre só as entidades que JÁ existem como catálogo real no backend Go:
// tabelas/campos (GO-011), views (GO-019/020), triggers (GO-024),
// triggers agendados (GO-025), biblioteca e configuração (novos nesta
// tarefa). O Pack do legado também carrega pages/page_groups/roles/tags/
// models/model_instances/event_logs/code_pages — nenhuma dessas
// entidades tem catálogo Go ainda (páginas nunca foram portadas; roles
// são papéis FIXOS por constante, não um catálogo dinâmico; tags/models/
// event_logs/code_pages não têm equivalente). Um Pack só pode conter o
// que o backend Go realmente sabe recriar — a mesma disciplina de
// "subconjunto prioritário" já aplicada em toda esta série de tarefas.
//
// Todas as referências entre entidades são por NOME (nunca por ID
// interno) — mesma convenção do legado: um Pack é portável entre
// tenants/instalações, onde os IDs internos nunca coincidem.
package pack

import (
	"github.com/vjuliani/saltcorn/migracao/backend/internal/identity"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/metadata"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/triggers"
)

// Version é a versão atual do formato de Pack produzido por Export — o
// legado NÃO tem nenhum campo de versão de schema de pack (achado de
// preflight: compatibilidade entre versões de pack é best-effort,
// campos ausentes silenciosamente viram zero-value). Acrescentar Version
// aqui é uma divergência deliberada: permite Import recusar
// explicitamente um pack de uma versão futura desconhecida, em vez de
// silenciosamente importar pela metade.
const Version = 1

// FieldPack é um campo de TablePack — References é o NOME da tabela
// referenciada (vazio se Type != metadata.FieldKey), nunca um ID interno.
type FieldPack struct {
	Name       string
	Type       metadata.FieldType
	Required   bool
	Unique     bool
	References string
}

// TablePack é uma tabela com seus campos.
type TablePack struct {
	Name         string
	MinRoleRead  identity.RoleID
	MinRoleWrite identity.RoleID
	Fields       []FieldPack
}

// ViewPack é uma view — TableName é o nome da tabela associada.
type ViewPack struct {
	Name          string
	TableName     string
	Template      string
	MinRole       identity.RoleID
	Configuration map[string]any
}

// TriggerPack é um trigger ligado a comando de registro (GO-024) —
// TableName é o nome da tabela associada.
type TriggerPack struct {
	TableName     string
	When          triggers.WhenTrigger
	Action        string
	OnlyIf        string
	AfterCommit   bool
	Configuration map[string]any
}

// ScheduledTriggerPack é um trigger agendado (GO-025) — só a DEFINIÇÃO
// (nome/ação/expressão cron/fuso), nunca o estado de execução
// (NextRunAt/LastRunAt/LastError são estado de runtime, não definição de
// aplicação — Import sempre recalcula um NextRunAt novo a partir do
// momento da instalação, mesmo espírito de CreateScheduledTrigger já
// usado em qualquer criação).
type ScheduledTriggerPack struct {
	Name     string
	Action   string
	CronExpr string
	Timezone string
}

// LibraryPack é um componente de biblioteca — Layout é o JSON opaco do
// layout, sem nenhuma transformação (mesmo comportamento de
// library_pack() do legado).
type LibraryPack struct {
	Name   string
	Icon   string
	Layout map[string]any
}

// PluginDependency é uma dependência de plugin declarada pelo Pack —
// Version é uma string livre (mesma convenção do legado, sem semver
// estruturado). Como não existe inventário real de plugins de terceiro
// neste checkout (achado repetido desde GO-001/003/004), Export nunca
// descobre isto sozinho — é informado explicitamente por quem chama
// Export (ver export.go).
type PluginDependency struct {
	Name    string
	Version string
}

// Pack é uma aplicação inteira exportável/importável.
type Pack struct {
	Version           int
	Tables            []TablePack
	Views             []ViewPack
	Triggers          []TriggerPack
	ScheduledTriggers []ScheduledTriggerPack
	Library           []LibraryPack
	Config            map[string]any
	Plugins           []PluginDependency
}
