// Package config carrega a configuração do backend Go a partir de variáveis
// de ambiente. server, worker e cli compartilham este pacote (ADR-0001:
// "CLI e worker reutilizam os mesmos serviços") em vez de cada um definir
// suas próprias variáveis.
package config

import (
	"fmt"
	"os"
	"strconv"
	"time"
)

// Config são os parâmetros de execução comuns a server/worker/cli. Cresce
// conforme as tarefas seguintes (GO-007+) adicionarem identidade, tenancy e
// conexão de banco — esta fundação não presume nenhuma dessas ainda.
type Config struct {
	// HTTPAddr é o endereço em que cmd/server escuta (ex.: ":8090").
	HTTPAddr string
	// ShutdownTimeout é quanto tempo server/worker esperam trabalho em
	// curso terminar antes de encerrar de qualquer forma.
	ShutdownTimeout time.Duration
	// Environment rotula o ambiente de execução (não controla comportamento
	// nesta fundação; existe para logging/observabilidade futura).
	Environment string
}

const (
	envHTTPAddr        = "SALTCORN_GO_HTTP_ADDR"
	envShutdownTimeout = "SALTCORN_GO_SHUTDOWN_TIMEOUT_SECONDS"
	envEnvironment     = "SALTCORN_GO_ENV"

	defaultHTTPAddr        = ":8090"
	defaultShutdownTimeout = 15 * time.Second
	defaultEnvironment     = "development"
)

// Load lê a configuração do ambiente, aplicando padrões razoáveis quando uma
// variável não está definida. Retorna erro apenas quando uma variável
// definida tem valor inválido — nunca falha por ausência de configuração.
func Load() (Config, error) {
	cfg := Config{
		HTTPAddr:        getEnv(envHTTPAddr, defaultHTTPAddr),
		ShutdownTimeout: defaultShutdownTimeout,
		Environment:     getEnv(envEnvironment, defaultEnvironment),
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

	return cfg, nil
}

func getEnv(key, def string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return def
}
