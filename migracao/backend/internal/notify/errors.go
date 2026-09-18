package notify

import "errors"

var (
	// ErrSSRFBlocked é devolvido por SendWebhook quando o host de destino
	// resolve para um endereço privado/loopback/link-local/não
	// especificado — nunca uma tentativa de conexão chega a acontecer.
	// Proteção que o legado NÃO tem (achado de preflight: a ação
	// `webhook` do legado passa a URL direto para `fetch` sem nenhuma
	// checagem) — decisão de segurança deliberada desta tarefa, não uma
	// port de comportamento existente.
	ErrSSRFBlocked = errors.New("notify: destino do webhook resolve para um endereço bloqueado (rede privada/loopback/link-local)")
	// ErrWebhookFailed é devolvido quando o destino responde com status
	// >= 300 — erros de rede/timeout são devolvidos com sua própria causa
	// (envolvida), nunca convertidos para este sentinela genérico.
	ErrWebhookFailed = errors.New("notify: webhook respondeu com status de erro")
)
