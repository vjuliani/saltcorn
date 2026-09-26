// Catálogo de ações nativas nomeadas (GO-029) — o "SDK de ações" que
// GO-024 deixou explicitamente reservado para esta tarefa ("GO-026 e
// GO-029 são os consumidores planejados de um catálogo real", ver
// comentário histórico de ActionFunc). Cobre o subconjunto do catálogo do
// legado (base-plugin/actions.ts, ~29 ações) que já tem infraestrutura Go
// pronta e testada: e-mail e webhook (internal/notify, GO-026). CRUD
// dirigido por trigger (`insert_any_row`/`modify_row`/`delete_rows`) e
// `run_js_code` ficam deliberadamente fora desta entrega — ver
// docs/migracao-go/execucoes/GO-029.md, decisões de escopo, para o
// motivo de cada um.
//
// GO-054 estende este catálogo com 5 das 15 ações de nicho decididas em
// GO-050 (§6.5 item 9a): sleep, duplicate_row, emit_event,
// set_user_language, loop_rows. As outras 10 nomeadas por GO-050 foram
// deliberadamente EXCLUÍDAS desta entrega — ver
// docs/migracao-go/execucoes/GO-054.md para a evidência de cada exclusão
// (achado real de preflight, não hipotético): toast/copy_to_clipboard/
// reload_embedded_view/progress_bar/duplicate_row_prefill_edit não têm
// NENHUM efeito do lado do servidor no legado (só diretivas para o
// navegador — `eval_js`/`goto`/`page_load_tag` — que ActionFuncTx não tem
// como carregar, e estender o contrato para isso é escopo maior que "5
// ações a mais"); refresh_user_session/step_control_flow não têm conceito
// equivalente no Go (sessão de servidor stateless; nenhum trigger
// multi-step existe para controlar); recalculate_stored_fields não tem
// NENHUM conceito de campo calculado/armazenado em internal/metadata;
// download_file_to_browser embute o arquivo inteiro em base64 no
// resultado da ação — um formato que ActionFuncTx (só devolve error) não
// tem como produzir; notify_user está bloqueada em internal/notify.Create,
// que GO-055 deixou deliberadamente pgx.Tx-only (dependência transitiva de
// internal/realtime, ainda não convertida) — fica para quando GO-056
// converter esse caminho, não para uma segunda tentativa nesta task.
package triggers

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/vjuliani/saltcorn/migracao/backend/internal/identity"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/metadata"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/notify"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/platform/database"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/platform/tenancy"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/records"
)

// Nomes das ações nativas — mesmos nomes do catálogo do legado
// (base-plugin/actions.ts) por continuidade de expectativa, mesmo que a
// forma de `Configuration` não seja idêntica byte a byte à do legado.
const (
	ActionSendEmail       = "send_email"
	ActionWebhook         = "webhook"
	ActionSleep           = "sleep"
	ActionDuplicateRow    = "duplicate_row"
	ActionEmitEvent       = "emit_event"
	ActionSetUserLanguage = "set_user_language"
	ActionLoopRows        = "loop_rows"
)

// BuiltinActions devolve o catálogo de ações nativas prontas para
// registrar em Dispatcher.Actions — quem monta um Dispatcher real
// (hoje, só em nível de pacote/teste; wiring em cmd/server/cmd/worker
// fica para uma tarefa futura, ver nota de escopo em
// docs/migracao-go/execucoes/GO-029.md) decide se usa este catálogo
// sozinho ou mesclado com ações próprias da aplicação.
func BuiltinActions() map[string]ActionFuncTx {
	return map[string]ActionFuncTx{
		ActionSendEmail:       sendEmailAction,
		ActionWebhook:         webhookAction,
		ActionSleep:           sleepAction,
		ActionDuplicateRow:    duplicateRowAction,
		ActionEmitEvent:       emitEventAction,
		ActionSetUserLanguage: setUserLanguageAction,
		ActionLoopRows:        loopRowsAction,
	}
}

// sendEmailAction lê destinatário/assunto/corpo de Configuration —
// `to` aceita uma string única ou uma lista; `subject`/`body` suportam
// placeholders `{{campo}}` substituídos pelo valor do campo homônimo da
// linha que disparou o trigger (equivalente reduzido da interpolação de
// template do legado, sem o motor completo de handlebars). Idempotência
// (GO-014, via notify.EnqueueEmail): a chave inclui um hash do conteúdo
// de Configuration — dois triggers DIFERENTES com o MESMO nome de ação
// sobre a MESMA linha nunca colidem na mesma chave, porque suas
// configurações (destinatário, assunto) são o que os distingue; dois
// disparos com a configuração IDÊNTICA sobre a mesma linha são,
// razoavelmente, o MESMO evento.
func sendEmailAction(ctx context.Context, tx database.Tx, d *Dispatcher, tenant tenancy.Tenant, actorRole identity.RoleID, table metadata.Table, row map[string]any, config map[string]any) error {
	to := configStringSlice(config, "to")
	if len(to) == 0 {
		return fmt.Errorf("%w: ação %q exige configuration.to", ErrActionConfigInvalid, ActionSendEmail)
	}
	subject := interpolate(configString(config, "subject"), row)
	body := interpolate(configString(config, "body"), row)

	key, err := actionIdempotencyKey(ActionSendEmail, config, row)
	if err != nil {
		return err
	}
	return notify.EnqueueEmailTx(ctx, tx, key, notify.EmailMessage{To: to, Subject: subject, Body: body})
}

// webhookAction posta a linha que disparou o trigger, como JSON, para a
// URL configurada — idempotência pelo mesmo raciocínio de sendEmailAction.
func webhookAction(ctx context.Context, tx database.Tx, d *Dispatcher, tenant tenancy.Tenant, actorRole identity.RoleID, table metadata.Table, row map[string]any, config map[string]any) error {
	url := configString(config, "url")
	if url == "" {
		return fmt.Errorf("%w: ação %q exige configuration.url", ErrActionConfigInvalid, ActionWebhook)
	}
	body, err := json.Marshal(row)
	if err != nil {
		return err
	}
	key, err := actionIdempotencyKey(ActionWebhook, config, row)
	if err != nil {
		return err
	}
	return notify.EnqueueWebhookTx(ctx, tx, key, notify.WebhookRequest{
		Method:  "POST",
		URL:     url,
		Headers: map[string]string{"Content-Type": "application/json"},
		Body:    body,
	})
}

// maxSleepSeconds limita a ação sleep (GO-054): ela roda DENTRO da mesma
// transação que disparou o trigger (RunOneTx, ver dispatch.go), então um
// configuration.seconds grande mantém uma conexão/transação Postgres
// aberta pelo tempo todo — um risco real de esgotamento do pool de
// conexões que o legado não tem do mesmo jeito (lá a ação roda fora de
// qualquer transação SQL aberta). Divergência deliberada: um valor acima
// do limite é um erro explícito de configuration, nunca truncado
// silenciosamente para o máximo.
const maxSleepSeconds = 30

// sleepAction porta só o ramo "Server" do sleep do legado
// (configuration.sleep_where == "Server", pausa real do lado do
// servidor). O ramo padrão/"Client page" do legado devolve
// `{eval_js: "...setTimeout..."}` — uma pausa executada no NAVEGADOR, sem
// NENHUM efeito do lado do servidor — por isso vira no-op aqui: não há o
// que uma ação Go possa fazer para atrasar a resposta de uma página que
// ela nunca renderiza.
func sleepAction(ctx context.Context, tx database.Tx, d *Dispatcher, tenant tenancy.Tenant, actorRole identity.RoleID, table metadata.Table, row map[string]any, config map[string]any) error {
	if configString(config, "sleep_where") != "Server" {
		return nil
	}
	seconds := configFloat(config, "seconds")
	if seconds <= 0 {
		return nil
	}
	if seconds > maxSleepSeconds {
		return fmt.Errorf("%w: ação %q: seconds (%.0f) excede o máximo permitido (%d) — a ação roda dentro da transação do trigger", ErrActionConfigInvalid, ActionSleep, seconds, maxSleepSeconds)
	}
	select {
	case <-time.After(time.Duration(seconds * float64(time.Second))):
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// duplicateRowAction porta duplicate_row do legado: copia a linha que
// disparou o trigger para um novo registro na MESMA tabela, sem o campo
// id (sempre "id" nesta base — GO-011/GO-012 só suportam PK simples
// serial, nunca uma PK composta a traduzir como o legado faz para
// table.pk_name). Ao contrário do legado, o mapa `row` que chega aqui
// NUNCA tem valores de fkey aninhados como objeto (Query.Joins não é
// usado pelo despacho de trigger) — dispensa a etapa de achatamento de
// fkey que o legado precisa (`newRow[field.name] = newRow[field.name].id`).
// Escreve pela via autorizada (records.CreateRecordTx) com os MESMOS
// hooks do Dispatcher — a linha duplicada dispara seus próprios triggers
// de inserção, igual a table.insertRow(...) no legado.
//
// Risco real (confirmado por teste, não hipotético — igual no legado,
// nenhuma divergência aqui): registrar duplicate_row como trigger
// AfterInsert da MESMA tabela causa recursão infinita — a linha duplicada
// dispara o AfterInsert de novo, que duplica de novo, ad infinitum, até
// esgotar recursos (mesmo comportamento de table.insertRow(...) chamando
// os MESMOS hooks no legado). Quem cadastra este trigger é responsável
// por nunca o registrar em WhenInsert da própria tabela-alvo.
func duplicateRowAction(ctx context.Context, tx database.Tx, d *Dispatcher, tenant tenancy.Tenant, actorRole identity.RoleID, table metadata.Table, row map[string]any, config map[string]any) error {
	if table.Name == "" {
		return fmt.Errorf("%w: ação %q exige um trigger ligado a uma tabela", ErrActionConfigInvalid, ActionDuplicateRow)
	}
	newRow := make(map[string]any, len(row))
	for k, v := range row {
		// "id" e "_version" são colunas SINTÉTICAS que RowsTx/CreateRecordTx
		// sempre devolvem no mapa de linha (id de identidade, controle de
		// concorrência otimista — ver internal/records.VersionExpr) — nunca
		// campos catalogados que se possa definir num INSERT novo.
		// Achado real (não hipotético): a primeira versão desta ação
		// copiava só menos "id" e falhava com ErrUnknownField ao tentar
		// duplicar QUALQUER linha, porque "_version" sempre vem junto.
		if k == "id" || k == "_version" {
			continue
		}
		newRow[k] = v
	}
	_, err := records.CreateRecordTx(ctx, tx, actorRole, table.Name, newRow, d.HooksForTx(tenant, actorRole, nil))
	return err
}

// emitEventAction porta emit_event do legado: reemite a linha que
// disparou este trigger como um evento nomeado — cascata que chama
// Dispatcher.EmitEventTx (GO-052), o MESMO mecanismo de
// `POST .../events/{eventname}`, nunca um segundo caminho de despacho.
// Divergência deliberada: configuration.payload do legado (uma expressão
// JS que recalcula o payload a partir de row/user) não é avaliada — o
// payload emitido é sempre a própria `row`. internal/expression.Evaluator
// hoje só avalia para um ExpectedType escalar (metadata.Field*), sem
// suporte a "resultado é um objeto arbitrário"; portar isso exigiria
// estender o avaliador de expressão, fora do escopo desta ação.
func emitEventAction(ctx context.Context, tx database.Tx, d *Dispatcher, tenant tenancy.Tenant, actorRole identity.RoleID, table metadata.Table, row map[string]any, config map[string]any) error {
	eventType := configString(config, "eventType")
	if eventType == "" {
		return fmt.Errorf("%w: ação %q exige configuration.eventType", ErrActionConfigInvalid, ActionEmitEvent)
	}
	_, err := d.EmitEventTx(ctx, tx, tenant, actorRole, eventType, nil, row)
	return err
}

// setUserLanguageAction porta a metade gravável de set_user_language do
// legado (o resto — req.login para renovar o cookie de sessão Express,
// res.cookie para um visitante anônimo — não tem equivalente: o backend
// Go é stateless/JWT, sem sessão de servidor a renovar, ver
// internal/identity). Divergência deliberada: configuration.user_id é
// EXPLÍCITO (nunca implícito a partir de um "usuário da sessão atual"),
// porque o Dispatcher de triggers só propaga o PAPEL de quem disparou a
// operação (actorRole), nunca uma identidade de usuário específica —
// mesma limitação já documentada para RunJSCodeFuncTx. O legado já aceita
// esse formato para "uso programático" (comentário de notify_user,
// mesmo arquivo) — não é uma forma inventada.
func setUserLanguageAction(ctx context.Context, tx database.Tx, d *Dispatcher, tenant tenancy.Tenant, actorRole identity.RoleID, table metadata.Table, row map[string]any, config map[string]any) error {
	language := configString(config, "language")
	if language == "" {
		return fmt.Errorf("%w: ação %q exige configuration.language", ErrActionConfigInvalid, ActionSetUserLanguage)
	}
	userID := configInt(config, "user_id")
	if userID == 0 {
		return fmt.Errorf("%w: ação %q exige configuration.user_id (o Dispatcher de triggers só conhece o papel de quem disparou, nunca uma identidade de usuário específica)", ErrActionConfigInvalid, ActionSetUserLanguage)
	}
	return identity.SetUserLanguageTx(ctx, tx, userID, language)
}

// loopRowsAction porta loop_rows do legado: consulta linhas de
// configuration.table_name e, para cada uma, despacha o trigger
// configuration.trigger_id via d.RunOneTx — a MESMA primitiva de
// despacho usada por runBeforeTx/runAfterTx/EmitEventTx, nenhum segundo
// mecanismo de execução. Duas divergências deliberadas do legado:
//
//  1. configuration.where é um MAPA JSON de campo->valor (igualdade pura,
//     E lógico entre os campos), nunca a expressão JS arbitrária do
//     legado (eval_expression, que pode produzir qualquer operador —
//     ilike, gt, etc. via mkWhere). Cobre o caso de uso mais comum
//     ("status = Active") sem reintroduzir um segundo avaliador de
//     expressão dentro do catálogo de ações.
//  2. configuration.interval (pausa entre iterações do legado) NÃO é
//     suportado — o mesmo risco de segurar uma transação Postgres aberta
//     documentado em sleepAction se multiplica aqui por N linhas do
//     laço, um risco estritamente pior. As linhas são processadas em
//     sequência, sem pausa artificial entre elas.
//
// d.RunOneTx é chamado diretamente, sem reavaliar Trigger.OnlyIf do
// trigger alvo — mesmo espírito de EmitEventTx/enqueueAfterCommitTx, que
// só recebem controle DEPOIS de shouldFire já ter decidido; aqui o
// registro Trigger é usado puramente como um contêiner de Action a
// executar sobre cada linha do laço, não como um "ouvinte" com condição
// própria (o mesmo papel que trigger.runWithoutRow cumpre no legado).
func loopRowsAction(ctx context.Context, tx database.Tx, d *Dispatcher, tenant tenancy.Tenant, actorRole identity.RoleID, table metadata.Table, row map[string]any, config map[string]any) error {
	tableName := configString(config, "table_name")
	if tableName == "" {
		return fmt.Errorf("%w: ação %q exige configuration.table_name", ErrActionConfigInvalid, ActionLoopRows)
	}
	triggerID := configInt(config, "trigger_id")
	if triggerID == 0 {
		return fmt.Errorf("%w: ação %q exige configuration.trigger_id", ErrActionConfigInvalid, ActionLoopRows)
	}
	loopTable, err := metadata.GetTable(ctx, tx, tableName)
	if err != nil {
		return err
	}
	trig, err := GetTriggerByIDTx(ctx, tx, triggerID)
	if err != nil {
		return err
	}
	q := records.Query{Table: tableName, Where: equalityWhere(configMap(config, "where"))}
	if orderBy := configString(config, "orderBy"); orderBy != "" {
		q.OrderBy = []records.OrderTerm{{Field: orderBy, Desc: configBool(config, "orderDesc")}}
	}
	if limit := configInt(config, "limit"); limit > 0 {
		q.Limit = limit
	}
	rows, err := records.RowsTx(ctx, tx, actorRole, q)
	if err != nil {
		return err
	}
	for _, r := range rows {
		if err := d.RunOneTx(ctx, tx, tenant, actorRole, *loopTable, trig, r); err != nil {
			return err
		}
	}
	return nil
}

// equalityWhere traduz o mapa JSON configuration.where de loop_rows para
// uma condição records.Where de igualdade pura — ver divergência
// documentada em loopRowsAction.
func equalityWhere(where map[string]any) records.Where {
	if len(where) == 0 {
		return nil
	}
	conds := make(records.And, 0, len(where))
	for field, value := range where {
		conds = append(conds, records.Eq{Field: field, Value: value})
	}
	if len(conds) == 1 {
		return conds[0]
	}
	return conds
}

func actionIdempotencyKey(action string, config map[string]any, row map[string]any) (string, error) {
	configJSON, err := json.Marshal(config)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(configJSON)
	return fmt.Sprintf("trigger-action:%s:%v:%s", action, row["id"], hex.EncodeToString(sum[:8])), nil
}

func configString(config map[string]any, key string) string {
	if config == nil {
		return ""
	}
	s, _ := config[key].(string)
	return s
}

// configFloat lê um número de config — valores decodificados de JSON
// chegam como float64; aceita também int/json.Number por robustez a
// chamadores que montem config programaticamente.
func configFloat(config map[string]any, key string) float64 {
	if config == nil {
		return 0
	}
	switch v := config[key].(type) {
	case float64:
		return v
	case int:
		return float64(v)
	case json.Number:
		f, _ := v.Float64()
		return f
	default:
		return 0
	}
}

// configInt trunca configFloat — suficiente para ids/limites de
// configuration, que nunca têm fração.
func configInt(config map[string]any, key string) int {
	return int(configFloat(config, key))
}

// configBool lê um booleano de config — ausente/tipo errado é false,
// nunca um erro (mesmo espírito tolerante de configString).
func configBool(config map[string]any, key string) bool {
	if config == nil {
		return false
	}
	b, _ := config[key].(bool)
	return b
}

// configMap lê um sub-objeto de config (ex.: configuration.where de
// loop_rows) — ausente/tipo errado é nil, nunca um erro.
func configMap(config map[string]any, key string) map[string]any {
	if config == nil {
		return nil
	}
	m, _ := config[key].(map[string]any)
	return m
}

// configStringSlice aceita tanto uma string única quanto uma lista (o
// JSON de Configuration não distingue os dois na origem de quem cria o
// trigger) — sempre devolve uma lista, nunca nil para uma string não
// vazia.
func configStringSlice(config map[string]any, key string) []string {
	if config == nil {
		return nil
	}
	switch v := config[key].(type) {
	case string:
		if v == "" {
			return nil
		}
		return []string{v}
	case []any:
		out := make([]string, 0, len(v))
		for _, item := range v {
			if s, ok := item.(string); ok && s != "" {
				out = append(out, s)
			}
		}
		return out
	default:
		return nil
	}
}

var placeholderPattern = regexp.MustCompile(`\{\{(\w+)\}\}`)

// interpolate substitui `{{campo}}` pelo valor de row[campo] (formatado
// com fmt.Sprint) — um placeholder para um campo ausente vira string
// vazia, nunca um erro (mesmo espírito tolerante de um template de
// e-mail: um campo faltando não deveria impedir o envio do resto).
func interpolate(template string, row map[string]any) string {
	if template == "" {
		return ""
	}
	return placeholderPattern.ReplaceAllStringFunc(template, func(match string) string {
		field := strings.TrimSuffix(strings.TrimPrefix(match, "{{"), "}}")
		if v, ok := row[field]; ok && v != nil {
			return fmt.Sprint(v)
		}
		return ""
	})
}
