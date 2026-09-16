package shutdown

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// TestTracker_DrainWaitsForInFlightWork é o teste que sustenta diretamente o
// critério de aceite de GO-005 ("processo encerra sem perder transações em
// curso"): dispara N unidades de trabalho concorrentes, aciona Drain
// enquanto elas ainda estão rodando, e confirma que Drain só retorna depois
// que todas realmente terminaram — nunca antes.
func TestTracker_DrainWaitsForInFlightWork(t *testing.T) {
	tr := NewTracker()
	const n = 50

	var completed atomic.Int32
	var wg sync.WaitGroup
	started := make(chan struct{}, n)

	for i := 0; i < n; i++ {
		end, err := tr.Begin()
		if err != nil {
			t.Fatalf("Begin() #%d: erro inesperado: %v", i, err)
		}
		wg.Add(1)
		go func(end func()) {
			defer wg.Done()
			started <- struct{}{}
			time.Sleep(20 * time.Millisecond)
			completed.Add(1)
			end()
		}(end)
	}

	// Espera todas as goroutines começarem antes de drenar, para garantir
	// que o Drain realmente encontra trabalho em curso (não uma corrida onde
	// tudo já terminou antes do Drain começar).
	for i := 0; i < n; i++ {
		<-started
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := tr.Drain(ctx); err != nil {
		t.Fatalf("Drain() erro inesperado: %v", err)
	}

	if got := completed.Load(); got != n {
		t.Fatalf("completed = %d, esperado %d — Drain retornou antes de todo trabalho terminar", got, n)
	}
	if got := tr.InFlight(); got != 0 {
		t.Fatalf("InFlight() = %d após Drain, esperado 0", got)
	}

	wg.Wait() // sanity: nenhuma goroutine deveria seguir rodando
}

// TestTracker_RejectsNewWorkAfterDrainStarts confirma que, uma vez iniciada a
// drenagem, nenhum trabalho novo é aceito — quem chama deve recusar (ex.:
// responder 503), não enfileirar mais trabalho atrás do que já está saindo.
func TestTracker_RejectsNewWorkAfterDrainStarts(t *testing.T) {
	tr := NewTracker()

	block := make(chan struct{})
	end, err := tr.Begin()
	if err != nil {
		t.Fatalf("Begin() erro inesperado: %v", err)
	}
	go func() {
		<-block
		end()
	}()

	drainDone := make(chan error, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		drainDone <- tr.Drain(ctx)
	}()

	// Espera ativamente o Drain acima marcar draining=true. Antes disso,
	// Begin() legitimamente aceita trabalho (a goroutine do Drain ainda não
	// rodou) — cada aceite precisa ser encerrado para não vazar contagem;
	// só depois que draining=true é que Begin() deve começar a recusar.
	deadline := time.Now().Add(1 * time.Second)
	for {
		end2, err := tr.Begin()
		if errors.Is(err, ErrDraining) {
			break
		}
		if err != nil {
			t.Fatalf("Begin() erro inesperado (não ErrDraining): %v", err)
		}
		end2()
		if time.Now().After(deadline) {
			t.Fatal("timeout esperando o Tracker entrar em modo de drenagem")
		}
		time.Sleep(time.Millisecond)
	}

	close(block)
	if err := <-drainDone; err != nil {
		t.Fatalf("Drain() erro inesperado: %v", err)
	}
}

// TestTracker_DrainTimesOut confirma que Drain não bloqueia para sempre: se o
// trabalho em curso não termina dentro do prazo do contexto, Drain retorna o
// erro do contexto e quem chama decide o que fazer (ex.: encerrar o processo
// mesmo assim, registrando o aviso) — replica a decisão operacional descrita
// em cmd/server e cmd/worker.
func TestTracker_DrainTimesOut(t *testing.T) {
	tr := NewTracker()
	end, err := tr.Begin()
	if err != nil {
		t.Fatalf("Begin() erro inesperado: %v", err)
	}
	defer end() // limpa ao final do teste, mesmo já tendo estourado o timeout

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	err = tr.Drain(ctx)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Drain() erro = %v, esperado context.DeadlineExceeded", err)
	}
}

// TestTracker_DrainWithNoInFlightWorkReturnsImmediately garante que um
// shutdown limpo (sem nada em curso) não espera desnecessariamente.
func TestTracker_DrainWithNoInFlightWorkReturnsImmediately(t *testing.T) {
	tr := NewTracker()
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()

	start := time.Now()
	if err := tr.Drain(ctx); err != nil {
		t.Fatalf("Drain() erro inesperado: %v", err)
	}
	if elapsed := time.Since(start); elapsed > 100*time.Millisecond {
		t.Errorf("Drain() sem trabalho em curso levou %v, esperado retorno quase imediato", elapsed)
	}
}

// TestTracker_ConcurrentBeginAndEnd é o teste mais relevante para o race
// detector: muitas goroutines chamando Begin/end simultaneamente, sem
// nenhuma ordenação externa — exatamente o padrão de uso real em cmd/server
// (uma goroutine por requisição HTTP).
func TestTracker_ConcurrentBeginAndEnd(t *testing.T) {
	tr := NewTracker()
	const workers = 100
	const iterations = 200

	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < iterations; j++ {
				end, err := tr.Begin()
				if err != nil {
					// Só pode acontecer se Drain já tiver começado, o que
					// não ocorre neste teste — trata como falha.
					t.Errorf("Begin() erro inesperado: %v", err)
					return
				}
				end()
			}
		}()
	}
	wg.Wait()

	if got := tr.InFlight(); got != 0 {
		t.Fatalf("InFlight() = %d ao final, esperado 0", got)
	}
}

// TestTracker_DrainCalledTwiceIsSafe cobre o caso de Drain ser chamado mais
// de uma vez (ex.: cmd/server chamando Drain tanto após srv.Shutdown quanto
// em um caminho de erro) — não deve travar nem entrar em pânico.
func TestTracker_DrainCalledTwiceIsSafe(t *testing.T) {
	tr := NewTracker()
	end, err := tr.Begin()
	if err != nil {
		t.Fatalf("Begin() erro inesperado: %v", err)
	}

	go func() {
		time.Sleep(10 * time.Millisecond)
		end()
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	var wg sync.WaitGroup
	errs := make([]error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			errs[i] = tr.Drain(ctx)
		}(i)
	}
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Errorf("Drain() #%d erro inesperado: %v", i, err)
		}
	}
}
