package notify

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"time"
)

// defaultWebhookTimeout é aplicado quando o chamador não fornece um
// *http.Client próprio — o legado (base-plugin/actions.ts, ação
// `webhook`) não tem NENHUM timeout configurado (achado de preflight); um
// destino que nunca responde travaria a ação de trigger indefinidamente.
const defaultWebhookTimeout = 10 * time.Second

// WebhookRequest é uma chamada HTTP de saída — Method vazio usa POST
// (mesmo padrão de formulário HTML, e o mais comum na ação `webhook` do
// legado).
type WebhookRequest struct {
	Method  string
	URL     string
	Headers map[string]string
	Body    []byte
}

// SendWebhook faz a requisição, com timeout explícito e uma checagem de
// SSRF ANTES de conectar: resolve o host de destino via DNS de verdade
// (nunca confia só na sintaxe da URL — um hostname público que resolve
// para 127.0.0.1 via DNS manipulado também é bloqueado) e recusa se
// QUALQUER endereço resolvido for privado/loopback/link-local/não
// especificado/multicast. client nil usa um *http.Client com
// defaultWebhookTimeout.
func SendWebhook(ctx context.Context, client *http.Client, req WebhookRequest) error {
	if client == nil {
		client = &http.Client{Timeout: defaultWebhookTimeout}
	}
	u, err := url.Parse(req.URL)
	if err != nil {
		return fmt.Errorf("notify: URL de webhook inválida: %w", err)
	}
	if err := guardHost(ctx, u.Hostname()); err != nil {
		return err
	}

	method := req.Method
	if method == "" {
		method = http.MethodPost
	}
	httpReq, err := http.NewRequestWithContext(ctx, method, req.URL, bytes.NewReader(req.Body))
	if err != nil {
		return fmt.Errorf("notify: montar requisição de webhook: %w", err)
	}
	for k, v := range req.Headers {
		httpReq.Header.Set(k, v)
	}

	resp, err := client.Do(httpReq)
	if err != nil {
		return fmt.Errorf("notify: enviar webhook: %w", err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	if resp.StatusCode >= 300 {
		return fmt.Errorf("%w: status %d", ErrWebhookFailed, resp.StatusCode)
	}
	return nil
}

// lookupIPAddr indireciona a resolução DNS — permite que os testes deste
// pacote exercitem o caminho de sucesso de SendWebhook contra um
// httptest.NewServer (que só escuta em loopback, o que a checagem de SSRF
// bloquearia de verdade) sem enfraquecer a checagem real: os testes que
// provam o bloqueio de SSRF usam o resolver verdadeiro, sem overrides.
var lookupIPAddr = net.DefaultResolver.LookupIPAddr

func guardHost(ctx context.Context, host string) error {
	if host == "" {
		return fmt.Errorf("%w: host vazio", ErrSSRFBlocked)
	}
	ips, err := lookupIPAddr(ctx, host)
	if err != nil {
		return fmt.Errorf("notify: resolver host do webhook: %w", err)
	}
	if len(ips) == 0 {
		return fmt.Errorf("%w: nenhum endereço resolvido para %q", ErrSSRFBlocked, host)
	}
	for _, addr := range ips {
		if isBlockedIP(addr.IP) {
			return fmt.Errorf("%w: %s resolve para %s", ErrSSRFBlocked, host, addr.IP)
		}
	}
	return nil
}

func isBlockedIP(ip net.IP) bool {
	return ip.IsLoopback() ||
		ip.IsPrivate() ||
		ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() ||
		ip.IsUnspecified() ||
		ip.IsMulticast()
}
