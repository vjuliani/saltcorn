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
package triggers

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/vjuliani/saltcorn/migracao/backend/internal/metadata"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/notify"
)

// Nomes das ações nativas — mesmos nomes do catálogo do legado
// (base-plugin/actions.ts) por continuidade de expectativa, mesmo que a
// forma de `Configuration` não seja idêntica byte a byte à do legado.
const (
	ActionSendEmail = "send_email"
	ActionWebhook   = "webhook"
)

// BuiltinActions devolve o catálogo de ações nativas prontas para
// registrar em Dispatcher.Actions — quem monta um Dispatcher real
// (hoje, só em nível de pacote/teste; wiring em cmd/server/cmd/worker
// fica para uma tarefa futura, ver nota de escopo em
// docs/migracao-go/execucoes/GO-029.md) decide se usa este catálogo
// sozinho ou mesclado com ações próprias da aplicação.
func BuiltinActions() map[string]ActionFunc {
	return map[string]ActionFunc{
		ActionSendEmail: sendEmailAction,
		ActionWebhook:   webhookAction,
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
func sendEmailAction(ctx context.Context, tx pgx.Tx, table metadata.Table, row map[string]any, config map[string]any) error {
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
	return notify.EnqueueEmail(ctx, tx, key, notify.EmailMessage{To: to, Subject: subject, Body: body})
}

// webhookAction posta a linha que disparou o trigger, como JSON, para a
// URL configurada — idempotência pelo mesmo raciocínio de sendEmailAction.
func webhookAction(ctx context.Context, tx pgx.Tx, table metadata.Table, row map[string]any, config map[string]any) error {
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
	return notify.EnqueueWebhook(ctx, tx, key, notify.WebhookRequest{
		Method:  "POST",
		URL:     url,
		Headers: map[string]string{"Content-Type": "application/json"},
		Body:    body,
	})
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
