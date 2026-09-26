// Comando cli: operação da distribuição self-hosted (GO-032), usando os
// mesmos serviços de domínio e configuração do backend.
package main

import (
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/vjuliani/saltcorn/migracao/backend/internal/installation"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/platform/config"
)

// A versão acompanha o manifesto compatível da distribuição.
const version = installation.Release

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(1)
	}

	var err error
	switch os.Args[1] {
	case "setup", "migrate", "backup", "restore", "backup-schedule", "get-cfg", "set-cfg", "plugins", "check", "serve", "login":
		err = installationCommand(os.Args[1], os.Args[2:])
	case "version":
		fmt.Println(version)
	case "healthcheck":
		err = healthcheck()
	case "e2e-seed":
		err = e2eSeed(os.Args[2:])
	default:
		usage()
		os.Exit(1)
	}

	if err != nil {
		fmt.Fprintln(os.Stderr, "erro:", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "uso: cli <setup|migrate|backup|restore|backup-schedule|get-cfg|set-cfg|plugins|check|serve|login|version|healthcheck|e2e-seed>")
}

// healthcheck reutiliza internal/platform/config (o mesmo pacote de
// cmd/server) para descobrir o endereço do servidor e consultar /healthz —
// um exemplo mínimo, mas real, de CLI e server compartilhando o mesmo
// serviço em vez de duplicar a leitura de configuração.
func healthcheck() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	client := http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get("http://" + hostForClient(cfg.HTTPAddr) + "/healthz")
	if err != nil {
		return fmt.Errorf("não foi possível contatar o servidor em %s: %w", cfg.HTTPAddr, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("status inesperado do servidor: %s", resp.Status)
	}
	fmt.Println("ok")
	return nil
}

// hostForClient traduz um endereço de escuta (ex.: ":8090", que só faz
// sentido do lado do servidor) para um host que um cliente HTTP consegue
// discar (ex.: "localhost:8090").
func hostForClient(addr string) string {
	if strings.HasPrefix(addr, ":") {
		return "localhost" + addr
	}
	return addr
}
