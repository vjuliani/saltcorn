package telemetry

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestMiddleware_GeneratesTraceparentWhenAbsent(t *testing.T) {
	registry := NewRegistry()
	metrics := NewHTTPMetrics(registry)
	handler := Middleware("test_route", metrics, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, ok := TraceIDFromContext(r.Context()); !ok {
			t.Error("handler não recebeu trace_id no contexto")
		}
		w.WriteHeader(http.StatusOK)
	}))

	srv := httptest.NewServer(handler)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/qualquer")
	if err != nil {
		t.Fatalf("requisição falhou: %v", err)
	}
	defer resp.Body.Close()

	traceparent := resp.Header.Get("traceparent")
	if traceparent == "" {
		t.Fatal("resposta não trouxe header traceparent")
	}
	if _, _, err := ParseTraceparent(traceparent); err != nil {
		t.Errorf("traceparent devolvido é inválido: %v", err)
	}
}

// TestMiddleware_ReusesIncomingTraceID é a prova direta de "uma operação é
// rastreável entre runtimes": um chamador (o futuro BFF) que já iniciou um
// trace deve ver o MESMO trace_id na resposta, com um span novo — não um
// trace completamente desconectado.
func TestMiddleware_ReusesIncomingTraceID(t *testing.T) {
	registry := NewRegistry()
	metrics := NewHTTPMetrics(registry)
	var gotTraceID TraceID
	handler := Middleware("test_route", metrics, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotTraceID, _ = TraceIDFromContext(r.Context())
		w.WriteHeader(http.StatusOK)
	}))

	srv := httptest.NewServer(handler)
	defer srv.Close()

	incomingTrace := NewTraceID()
	incomingSpan := NewSpanID()
	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/qualquer", nil)
	req.Header.Set("traceparent", FormatTraceparent(incomingTrace, incomingSpan))

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("requisição falhou: %v", err)
	}
	defer resp.Body.Close()

	if gotTraceID != incomingTrace {
		t.Errorf("trace_id visto pelo handler = %s, esperado %s (o do chamador)", gotTraceID, incomingTrace)
	}

	respTrace, respSpan, err := ParseTraceparent(resp.Header.Get("traceparent"))
	if err != nil {
		t.Fatalf("traceparent de resposta inválido: %v", err)
	}
	if respTrace != incomingTrace {
		t.Errorf("trace_id da resposta = %s, esperado %s", respTrace, incomingTrace)
	}
	if respSpan == incomingSpan {
		t.Error("span_id da resposta é igual ao do chamador — deveria ser um span novo para esta operação")
	}
}

func TestMiddleware_RecordsMetricsByStatusClass(t *testing.T) {
	registry := NewRegistry()
	metrics := NewHTTPMetrics(registry)
	handler := Middleware("tenant_records", metrics, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusConflict) // 409, classe 4xx
	}))

	srv := httptest.NewServer(handler)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/qualquer")
	if err != nil {
		t.Fatalf("requisição falhou: %v", err)
	}
	defer resp.Body.Close()

	var buf bytes.Buffer
	registry.WriteText(&buf)
	out := buf.String()
	if !strings.Contains(out, `http_requests_total{method="GET",route="tenant_records",status_class="4xx"} 1`) {
		t.Errorf("métrica de requisições não reflete a classe 4xx:\n%s", out)
	}
}

func TestMiddleware_LogsRequestSummary(t *testing.T) {
	registry := NewRegistry()
	metrics := NewHTTPMetrics(registry)
	var logBuf bytes.Buffer
	baseLogger := slog.New(NewHandler(&logBuf, slog.LevelInfo))

	handler := Middleware("tenant_records", metrics, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := WithLogger(r.Context(), baseLogger)
		handler.ServeHTTP(w, r.WithContext(ctx))
	}))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/v1/tenants/acme/tables/guitars/records")
	if err != nil {
		t.Fatalf("requisição falhou: %v", err)
	}
	defer resp.Body.Close()

	out := logBuf.String()
	if !strings.Contains(out, `"route":"tenant_records"`) {
		t.Errorf("log não contém a rota:\n%s", out)
	}
	if !strings.Contains(out, `"status":200`) {
		t.Errorf("log não contém o status:\n%s", out)
	}
	if !strings.Contains(out, `"trace_id"`) {
		t.Errorf("log não contém trace_id:\n%s", out)
	}
}
