// Package health fornece handlers HTTP de liveness e readiness reutilizáveis
// por cmd/server (e, futuramente, por qualquer outro processo que precise
// expor seu estado da mesma forma — ADR-0001: "CLI e worker reutilizam os
// mesmos serviços").
package health

import (
	"encoding/json"
	"net/http"
	"sync/atomic"
)

// Checker rastreia se o processo já terminou de inicializar (readiness),
// separado de estar vivo (liveness) — um processo pode estar vivo mas ainda
// não pronto (por exemplo, ainda conectando a dependências que as tarefas
// seguintes vão introduzir).
type Checker struct {
	ready atomic.Bool
}

// SetReady marca o processo como pronto (ou não) para receber tráfego.
func (c *Checker) SetReady(v bool) { c.ready.Store(v) }

// Ready informa se o processo está atualmente pronto.
func (c *Checker) Ready() bool { return c.ready.Load() }

// LivenessHandler responde 200 sempre que o processo consegue atender HTTP —
// não verifica dependências, só que o processo está executando.
func (c *Checker) LivenessHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		writeStatus(w, http.StatusOK, "ok")
	}
}

// ReadinessHandler responde 200 quando SetReady(true) foi chamado e ainda não
// foi revertido, e 503 caso contrário (ex.: durante shutdown gracioso).
func (c *Checker) ReadinessHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if c.Ready() {
			writeStatus(w, http.StatusOK, "ready")
			return
		}
		writeStatus(w, http.StatusServiceUnavailable, "not ready")
	}
}

func writeStatus(w http.ResponseWriter, code int, status string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]string{"status": status})
}
