package config

import (
	"reflect"
	"testing"
	"time"
)

func TestLoad_Defaults(t *testing.T) {
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() erro inesperado: %v", err)
	}
	if cfg.HTTPAddr != defaultHTTPAddr {
		t.Errorf("HTTPAddr = %q, esperado %q", cfg.HTTPAddr, defaultHTTPAddr)
	}
	if cfg.ShutdownTimeout != defaultShutdownTimeout {
		t.Errorf("ShutdownTimeout = %v, esperado %v", cfg.ShutdownTimeout, defaultShutdownTimeout)
	}
	if cfg.Environment != defaultEnvironment {
		t.Errorf("Environment = %q, esperado %q", cfg.Environment, defaultEnvironment)
	}
	if cfg.DatabaseURL != "" {
		t.Errorf("DatabaseURL = %q, esperado vazio por padrão", cfg.DatabaseURL)
	}
	if len(cfg.WorkerTenants) != 0 {
		t.Errorf("WorkerTenants = %v, esperado vazio por padrão", cfg.WorkerTenants)
	}
}

func TestLoad_WorkerTenants(t *testing.T) {
	t.Setenv(envWorkerTenants, "acme, beta ,, gamma")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() erro inesperado: %v", err)
	}
	want := []string{"acme", "beta", "gamma"}
	if !reflect.DeepEqual(cfg.WorkerTenants, want) {
		t.Errorf("WorkerTenants = %v, esperado %v (espaços aparados, entradas vazias descartadas)", cfg.WorkerTenants, want)
	}
}

func TestLoad_DatabaseURL(t *testing.T) {
	t.Setenv(envDatabaseURL, "postgres://user:pass@localhost/db")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() erro inesperado: %v", err)
	}
	if cfg.DatabaseURL != "postgres://user:pass@localhost/db" {
		t.Errorf("DatabaseURL = %q, não refletiu a variável de ambiente", cfg.DatabaseURL)
	}
}

func TestLoad_OverridesFromEnv(t *testing.T) {
	t.Setenv(envHTTPAddr, ":9999")
	t.Setenv(envEnvironment, "production")
	t.Setenv(envShutdownTimeout, "30")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() erro inesperado: %v", err)
	}
	if cfg.HTTPAddr != ":9999" {
		t.Errorf("HTTPAddr = %q, esperado :9999", cfg.HTTPAddr)
	}
	if cfg.Environment != "production" {
		t.Errorf("Environment = %q, esperado production", cfg.Environment)
	}
	if cfg.ShutdownTimeout != 30*time.Second {
		t.Errorf("ShutdownTimeout = %v, esperado 30s", cfg.ShutdownTimeout)
	}
}

func TestLoad_InvalidShutdownTimeout(t *testing.T) {
	t.Setenv(envShutdownTimeout, "nao-e-um-numero")
	if _, err := Load(); err == nil {
		t.Error("esperava erro para timeout não numérico, obteve nil")
	}
}

func TestLoad_NonPositiveShutdownTimeout(t *testing.T) {
	t.Setenv(envShutdownTimeout, "0")
	if _, err := Load(); err == nil {
		t.Error("esperava erro para timeout não positivo, obteve nil")
	}

	t.Setenv(envShutdownTimeout, "-5")
	if _, err := Load(); err == nil {
		t.Error("esperava erro para timeout negativo, obteve nil")
	}
}

func TestLoad_EmptyEnvValueFallsBackToDefault(t *testing.T) {
	// Uma variável setada para string vazia deve se comportar como ausente,
	// não como um valor explícito vazio.
	t.Setenv(envHTTPAddr, "")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() erro inesperado: %v", err)
	}
	if cfg.HTTPAddr != defaultHTTPAddr {
		t.Errorf("HTTPAddr = %q, esperado o padrão %q", cfg.HTTPAddr, defaultHTTPAddr)
	}
}
