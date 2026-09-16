// Comando worker: processo de background do backend Go. Nesta fundação
// (GO-005) processa um job vazio periodicamente só para exercitar o padrão
// de encerramento gracioso — a automação real (triggers/workflow/scheduler)
// entra em GO-024/GO-025, reutilizando o mesmo internal/platform/* usado
// aqui (ADR-0001: "CLI e worker reutilizam os mesmos serviços").
package main

import (
	"context"
	"log"
	"os/signal"
	"syscall"
	"time"

	"github.com/vjuliani/saltcorn/migracao/backend/internal/platform/config"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/platform/shutdown"
)

// jobInterval é fixo nesta fundação; vira configurável quando houver jobs
// reais com frequências próprias (GO-025).
const jobInterval = 5 * time.Second

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("configuração inválida: %v", err)
	}

	tracker := shutdown.NewTracker()
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	ticker := time.NewTicker(jobInterval)
	defer ticker.Stop()

	log.Printf("saltcorn-go worker (ambiente=%s) iniciado, intervalo=%s", cfg.Environment, jobInterval)

runLoop:
	for {
		select {
		case <-ctx.Done():
			break runLoop
		case <-ticker.C:
			end, err := tracker.Begin()
			if err != nil {
				// Shutdown já em andamento: não inicia mais um ciclo.
				continue
			}
			runPlaceholderJob()
			end()
		}
	}

	stop()
	log.Printf("sinal de encerramento recebido, aguardando job em curso (timeout %s)...", cfg.ShutdownTimeout)

	drainCtx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
	defer cancel()
	if err := tracker.Drain(drainCtx); err != nil {
		log.Printf("aviso: job em curso não terminou dentro do timeout de shutdown: %v", err)
	}
	log.Printf("encerrado")
}

// runPlaceholderJob simula uma unidade de trabalho ("transação") com
// duração perceptível, só para que o shutdown gracioso tenha algo real para
// esperar. Nenhuma automação de produto existe aqui ainda.
func runPlaceholderJob() {
	time.Sleep(50 * time.Millisecond)
}
