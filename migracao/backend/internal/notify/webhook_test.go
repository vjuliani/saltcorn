// Corpus de webhook — SSRF, timeout e status de erro, contra um
// httptest.NewServer real (sem Postgres).
package notify

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// withFakePublicResolver troca lookupIPAddr para devolver um IP
// PÚBLICO fixo para qualquer host, durante a duração do teste — permite
// exercitar o caminho de SUCESSO de SendWebhook contra um
// httptest.NewServer (que só escuta em loopback, o que a checagem de
// SSRF bloquearia de verdade) sem enfraquecer a checagem: os testes de
// bloqueio (abaixo) usam o resolver VERDADEIRO, sem este override.
func withFakePublicResolver(t *testing.T) {
	t.Helper()
	original := lookupIPAddr
	lookupIPAddr = func(ctx context.Context, host string) ([]net.IPAddr, error) {
		return []net.IPAddr{{IP: net.ParseIP("93.184.216.34")}}, nil
	}
	t.Cleanup(func() { lookupIPAddr = original })
}

func TestSendWebhook_HappyPath(t *testing.T) {
	withFakePublicResolver(t)

	var gotMethod, gotBody, gotHeader string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		gotHeader = r.Header.Get("X-Custom")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	err := SendWebhook(context.Background(), srv.Client(), WebhookRequest{
		URL:     srv.URL,
		Method:  http.MethodPost,
		Headers: map[string]string{"X-Custom": "valor"},
		Body:    []byte(`{"ok":true}`),
	})
	if err != nil {
		t.Fatalf("SendWebhook: %v", err)
	}
	if gotMethod != http.MethodPost {
		t.Errorf("method = %q, esperado POST", gotMethod)
	}
	if gotBody != `{"ok":true}` {
		t.Errorf("body = %q, esperado %q", gotBody, `{"ok":true}`)
	}
	if gotHeader != "valor" {
		t.Errorf("header X-Custom = %q, esperado \"valor\"", gotHeader)
	}
}

func TestSendWebhook_NonSuccessStatus_ReturnsErrWebhookFailed(t *testing.T) {
	withFakePublicResolver(t)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	err := SendWebhook(context.Background(), srv.Client(), WebhookRequest{URL: srv.URL})
	if !errors.Is(err, ErrWebhookFailed) {
		t.Fatalf("err = %v, esperado ErrWebhookFailed", err)
	}
}

// TestSendWebhook_SSRFBlocked_Loopback usa o resolver VERDADEIRO (sem
// override) — 127.0.0.1 é loopback de verdade, deveria ser bloqueado
// mesmo sem nenhum servidor escutando ali.
func TestSendWebhook_SSRFBlocked_Loopback(t *testing.T) {
	err := SendWebhook(context.Background(), nil, WebhookRequest{URL: "http://127.0.0.1:1/"})
	if !errors.Is(err, ErrSSRFBlocked) {
		t.Fatalf("err = %v, esperado ErrSSRFBlocked", err)
	}
}

func TestSendWebhook_SSRFBlocked_PrivateIP(t *testing.T) {
	err := SendWebhook(context.Background(), nil, WebhookRequest{URL: "http://10.1.2.3/"})
	if !errors.Is(err, ErrSSRFBlocked) {
		t.Fatalf("err = %v, esperado ErrSSRFBlocked", err)
	}
}

func TestSendWebhook_SSRFBlocked_LinkLocal(t *testing.T) {
	// 169.254.169.254 é o endereço clássico de metadata de nuvem (AWS/GCP/
	// Azure) — o alvo mais comum de um ataque SSRF real.
	err := SendWebhook(context.Background(), nil, WebhookRequest{URL: "http://169.254.169.254/latest/meta-data/"})
	if !errors.Is(err, ErrSSRFBlocked) {
		t.Fatalf("err = %v, esperado ErrSSRFBlocked", err)
	}
}

func TestSendWebhook_Timeout(t *testing.T) {
	withFakePublicResolver(t)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(200 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	client := &http.Client{Timeout: 20 * time.Millisecond}
	err := SendWebhook(context.Background(), client, WebhookRequest{URL: srv.URL})
	if err == nil {
		t.Fatal("esperado erro de timeout")
	}
}
