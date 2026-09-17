package pluginhost

// ExpressionCapability é o nome de capacidade que uma tarefa futura
// (GO-023, "Portar tipos e expressões prioritários") deve usar com
// cutover.RequireOwnership/cutover.SwitchOwner ao decidir se o motor de
// expressões de um tenant é servido por este host (Go) ou pelo caminho
// legado — mesmo mecanismo já em produção desde GO-009 para
// tables.records/tables.schema/tables.views (GO-017/019), reaproveitado
// aqui, não reinventado por plugin.
//
// GO-022 não chama cutover.RequireOwnership em lugar nenhum (não há rota
// HTTP nem cmd/server usando este pacote ainda — ver nota de escopo 8 em
// docs/migracao-go/execucoes/GO-022.md) — esta constante só fixa o NOME
// que a integração futura deve usar, para o critério de aceite "plugin
// transacional incompatível mantém operação integral no legado" já ter
// uma âncora concreta: enquanto nenhuma chamada a cutover.SwitchOwner
// mencionar esta capacidade, cutover.OwnerOf devolve OwnerLegacy para
// QUALQUER tenant (comportamento padrão seguro já testado por GO-009,
// TestOwnerOf_DefaultsToLegacy) — nunca "Go por omissão".
const ExpressionCapability = "plugins.expr"
