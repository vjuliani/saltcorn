package telemetry

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"

	"github.com/vjuliani/saltcorn/migracao/backend/internal/platform/tenancy"
)

// TestNewHandler_RedactsSensitiveAttributes é a prova direta do critério
// de aceite "sem registrar tokens ou dados sensíveis" (GO-010): qualquer
// atributo cujo NOME contenha um trecho sensível (token, senha, secret,
// authorization, cookie) tem o VALOR substituído por "REDACTED" na saída,
// não importa o que quem chamou log.Info tentou registrar.
func TestNewHandler_RedactsSensitiveAttributes(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(NewHandler(&buf, slog.LevelInfo))

	logger.Info("evento de teste",
		"api_token", "segredo-nao-deveria-aparecer",
		"password", "hunter2",
		"Authorization", "Bearer abc.def.ghi",
		"session_cookie", "sid=xyz",
		"totp_secret", "JBSWY3DPEHPK3PXP",
		"user_email", "ada@example.com", // não sensível — deve passar normal
	)

	var record map[string]any
	if err := json.Unmarshal(buf.Bytes(), &record); err != nil {
		t.Fatalf("saída não é JSON válido: %v\n%s", err, buf.String())
	}

	sensitiveKeys := []string{"api_token", "password", "Authorization", "session_cookie", "totp_secret"}
	for _, key := range sensitiveKeys {
		got, ok := record[key]
		if !ok {
			t.Errorf("atributo %q ausente na saída", key)
			continue
		}
		if got != "REDACTED" {
			t.Errorf("atributo %q = %q, esperado \"REDACTED\"", key, got)
		}
	}

	if got := record["user_email"]; got != "ada@example.com" {
		t.Errorf("atributo não-sensível user_email foi alterado: %q", got)
	}

	// Garantia mais forte: o segredo real não aparece em lugar nenhum da
	// linha de log, nem por acidente de serialização.
	raw := buf.String()
	for _, secret := range []string{"hunter2", "segredo-nao-deveria-aparecer", "Bearer abc.def.ghi", "JBSWY3DPEHPK3PXP"} {
		if strings.Contains(raw, secret) {
			t.Errorf("valor sensível %q vazou na saída de log: %s", secret, raw)
		}
	}
}

func TestLoggerFor_IncludesTraceTenantActor(t *testing.T) {
	var buf bytes.Buffer
	base := slog.New(NewHandler(&buf, slog.LevelInfo))

	ctx := WithLogger(context.Background(), base)
	ctx = WithTraceID(ctx, NewTraceID())
	ctx = tenancy.WithTenant(ctx, tenancy.Tenant("acme"))
	ctx = tenancy.WithActor(ctx, "42")

	LoggerFor(ctx).Info("operação concluída")

	var record map[string]any
	if err := json.Unmarshal(buf.Bytes(), &record); err != nil {
		t.Fatalf("saída não é JSON válido: %v\n%s", err, buf.String())
	}
	if record["tenant"] != "acme" {
		t.Errorf("tenant = %v, esperado acme", record["tenant"])
	}
	if record["actor"] != "42" {
		t.Errorf("actor = %v, esperado 42", record["actor"])
	}
	if _, ok := record["trace_id"]; !ok {
		t.Error("trace_id ausente na saída")
	}
}

func TestLoggerFor_WithoutContextValuesFallsBackToDefault(t *testing.T) {
	// Não deveria entrar em pânico nem exigir contexto anotado.
	logger := LoggerFor(context.Background())
	if logger == nil {
		t.Fatal("LoggerFor retornou nil")
	}
}
