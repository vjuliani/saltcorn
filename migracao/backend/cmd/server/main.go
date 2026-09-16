// Comando server: processo HTTP do backend Go. Nesta fundação (GO-005) não
// tem nenhuma rota de domínio — identidade/tenancy/metadados/etc. entram nas
// tarefas seguintes (GO-007+). O que existe aqui é a estrutura que o resto se
// apoia: configuração, health/readiness e encerramento gracioso sem perder
// trabalho em curso.
package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os/signal"
	"syscall"

	"github.com/vjuliani/saltcorn/migracao/backend/internal/platform/config"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/platform/health"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/platform/shutdown"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("configuração inválida: %v", err)
	}

	checker := &health.Checker{}
	tracker := shutdown.NewTracker()

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", checker.LivenessHandler())
	mux.HandleFunc("/readyz", checker.ReadinessHandler())
	mux.HandleFunc("/", placeholderHandler(tracker))

	srv := &http.Server{Addr: cfg.HTTPAddr, Handler: mux}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	serveErr := make(chan error, 1)
	go func() {
		checker.SetReady(true)
		log.Printf("saltcorn-go server (ambiente=%s) ouvindo em %s", cfg.Environment, cfg.HTTPAddr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serveErr <- err
			return
		}
		serveErr <- nil
	}()

	select {
	case <-ctx.Done():
	case err := <-serveErr:
		if err != nil {
			log.Fatalf("erro no servidor HTTP: %v", err)
		}
		return
	}

	stop() // para de reagir a um segundo sinal enquanto já estamos encerrando
	checker.SetReady(false)
	log.Printf("sinal de encerramento recebido, drenando (timeout %s)...", cfg.ShutdownTimeout)

	shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
	defer cancel()

	// srv.Shutdown para de aceitar novas conexões e espera as respostas HTTP
	// em curso terminarem. tracker.Drain espera qualquer trabalho registrado
	// explicitamente que sobreviva além da resposta HTTP (relevante quando
	// GO-007+ introduzir transações de domínio que não terminam no momento
	// em que a resposta é escrita).
	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Printf("aviso: srv.Shutdown não concluiu a tempo: %v", err)
	}
	if err := tracker.Drain(shutdownCtx); err != nil {
		log.Printf("aviso: trabalho em curso não terminou dentro do timeout de shutdown: %v", err)
	}
	log.Printf("encerrado")
}

// placeholderHandler demonstra o padrão que rotas de domínio vão seguir a
// partir de GO-007+: registrar a unidade de trabalho no tracker antes de
// processar, para que um shutdown gracioso saiba esperar por ela. Não há
// regra de negócio real aqui ainda.
func placeholderHandler(tracker *shutdown.Tracker) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		end, err := tracker.Begin()
		if err != nil {
			http.Error(w, err.Error(), http.StatusServiceUnavailable)
			return
		}
		defer end()
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("saltcorn-go: fundação (GO-005) — sem regras de domínio ainda\n"))
	}
}
