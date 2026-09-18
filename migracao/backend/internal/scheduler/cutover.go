package scheduler

// Capability é o nome de capacidade usado com internal/platform/cutover
// para decidir se o scheduler de triggers agendados de um tenant é servido
// pelo Go novo ou pelo legado — mesmo mecanismo já em produção desde
// GO-009 (tables.records/schema/views, GO-017/019/020) e desde GO-022/023
// para o host de plugins, reaproveitado aqui, não reinventado.
//
// Enquanto nenhuma chamada a cutover.SwitchOwner mencionar esta
// capacidade, cutover.OwnerOf devolve OwnerLegacy para QUALQUER tenant
// (comportamento padrão seguro, já testado genericamente desde GO-009,
// TestOwnerOf_DefaultsToLegacy) — o scheduler antigo do tenant continua
// integralmente responsável, e o job novo de cmd/worker (ver
// runScheduledTriggersJob) nunca toca nesse tenant: "scheduler antigo é
// desativado por escopo" (critério de aceite de GO-025) vale por
// construção, não por uma checagem inventada aqui.
const Capability = "scheduler.triggers"
