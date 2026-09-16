// Package config carrega a configuração do backend Go a partir de variáveis
// de ambiente. server, worker e cli compartilham este pacote (ADR-0001:
// "CLI e worker reutilizam os mesmos serviços") em vez de cada um definir
// suas próprias variáveis.
package config

import (
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"
)

// Config são os parâmetros de execução comuns a server/worker/cli. Cresce
// conforme as tarefas seguintes adicionam capacidades — esta fundação não
// presume nenhuma além do que já está implementado.
type Config struct {
	// HTTPAddr é o endereço em que cmd/server escuta (ex.: ":8090").
	HTTPAddr string
	// ShutdownTimeout é quanto tempo server/worker esperam trabalho em
	// curso terminar antes de encerrar de qualquer forma.
	ShutdownTimeout time.Duration
	// Environment rotula o ambiente de execução (não controla comportamento
	// nesta fundação; existe para logging/observabilidade futura).
	Environment string
	// DatabaseURL é a DSN do Postgres (ex.: "postgres://user:pass@host/db").
	// Vazio significa "sem banco configurado" — server/worker continuam
	// funcionando sem banco (GO-005), só não expõem as rotas/jobs que
	// dependem de internal/platform/database (GO-007).
	DatabaseURL string
	// WorkerTenants é a lista de tenants que cmd/worker processa a cada
	// ciclo (GO-007: propagação de tenant em jobs). Fundação temporária —
	// GO-024/GO-025 substituem isso por descoberta real de tenants e fila
	// de jobs, não uma lista fixa em configuração.
	WorkerTenants []string
	// ServiceIdentitySecret é o segredo compartilhado (HMAC-SHA256) usado
	// para verificar a assinatura do token de identidade delegada entre BFF
	// e backend Go (GO-008, ADR-0003). Vazio significa "não configurado" —
	// cmd/server decide se isso impede subir (rotas que exigem identidade
	// delegada não podem existir sem um segredo válido).
	ServiceIdentitySecret string
	// LogLevel controla o nível mínimo de log estruturado (GO-010) —
	// "DEBUG", "INFO", "WARN" ou "ERROR" (case-insensitive). Padrão INFO.
	LogLevel slog.Level
}

const (
	envHTTPAddr              = "SALTCORN_GO_HTTP_ADDR"
	envShutdownTimeout       = "SALTCORN_GO_SHUTDOWN_TIMEOUT_SECONDS"
	envEnvironment           = "SALTCORN_GO_ENV"
	envDatabaseURL           = "SALTCORN_GO_DATABASE_URL"
	envWorkerTenants         = "SALTCORN_GO_WORKER_TENANTS"
	envServiceIdentitySecret = "SALTCORN_GO_SERVICE_IDENTITY_SECRET"
	envLogLevel              = "SALTCORN_GO_LOG_LEVEL"

	defaultHTTPAddr        = ":8090"
	defaultShutdownTimeout = 15 * time.Second
	defaultEnvironment     = "development"
	defaultLogLevel        = slog.LevelInfo
)

// Load lê a configuração do ambiente, aplicando padrões razoáveis quando uma
// variável não está definida. Retorna erro apenas quando uma variável
// definida tem valor inválido — nunca falha por ausência de configuração.
func Load() (Config, error) {
	cfg := Config{
		HTTPAddr:              getEnv(envHTTPAddr, defaultHTTPAddr),
		ShutdownTimeout:       defaultShutdownTimeout,
		Environment:           getEnv(envEnvironment, defaultEnvironment),
		DatabaseURL:           getEnv(envDatabaseURL, ""),
		ServiceIdentitySecret: getEnv(envServiceIdentitySecret, ""),
		LogLevel:              defaultLogLevel,
	}

	if v, ok := os.LookupEnv(envShutdownTimeout); ok && v != "" {
		secs, err := strconv.Atoi(v)
		if err != nil {
			return Config{}, fmt.Errorf("%s inválido (%q): %w", envShutdownTimeout, v, err)
		}
		if secs <= 0 {
			return Config{}, fmt.Errorf("%s deve ser positivo, recebido %d", envShutdownTimeout, secs)
		}
		cfg.ShutdownTimeout = time.Duration(secs) * time.Second
	}

	if v, ok := os.LookupEnv(envWorkerTenants); ok && v != "" {
		for _, t := range strings.Split(v, ",") {
			t = strings.TrimSpace(t)
			if t != "" {
				cfg.WorkerTenants = append(cfg.WorkerTenants, t)
			}
		}
	}

	if v, ok := os.LookupEnv(envLogLevel); ok && v != "" {
		var lvl slog.Level
		if err := lvl.UnmarshalText([]byte(v)); err != nil {
			return Config{}, fmt.Errorf("%s inválido (%q): %w", envLogLevel, v, err)
		}
		cfg.LogLevel = lvl
	}

	return cfg, nil
}

func getEnv(key, def string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return def
}
