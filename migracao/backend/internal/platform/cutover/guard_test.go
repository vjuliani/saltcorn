package cutover

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/vjuliani/saltcorn/migracao/backend/internal/platform/tenancy"
)

const testCap = "test.capability"

func TestGuard_BeginWithoutOwnerFailsClosed(t *testing.T) {
	g := NewGuard()
	if _, err := g.Begin(tenancy.Tenant("acme"), testCap); !errors.Is(err, ErrNotOwner) {
		t.Fatalf("Begin() sem owner registrado = %v, esperado ErrNotOwner", err)
	}
}

func TestGuard_ResumeWithOwnerThenBeginSucceeds(t *testing.T) {
	g := NewGuard()
	tenant := tenancy.Tenant("acme")
	g.resumeWithOwner(tenant, testCap, OwnerGo)

	end, err := g.Begin(tenant, testCap)
	if err != nil {
		t.Fatalf("Begin() erro inesperado: %v", err)
	}
	end()
	if got := g.InFlight(tenant, testCap); got != 0 {
		t.Errorf("InFlight() = %d após end(), esperado 0", got)
	}
}

// TestGuard_DrainBlocksNewBeginUntilResumeWithOwner confirma que, durante a
// janela de drenagem, nenhum trabalho novo é aceito, e que quando a chave é
// liberada de volta (resumeWithOwner) o owner novo é o que vale — inclusive
// no caso de rollback (owner novo = legacy), onde Begin volta a falhar,
// agora por ErrNotOwner em vez de ErrRouteDraining.
func TestGuard_DrainBlocksNewBeginUntilResumeWithOwner(t *testing.T) {
	g := NewGuard()
	tenant := tenancy.Tenant("acme")
	g.resumeWithOwner(tenant, testCap, OwnerGo)

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := g.Drain(ctx, tenant, testCap); err != nil {
		t.Fatalf("Drain() erro inesperado: %v", err)
	}

	if _, err := g.Begin(tenant, testCap); !errors.Is(err, ErrRouteDraining) {
		t.Fatalf("Begin() durante drenagem = %v, esperado ErrRouteDraining", err)
	}

	g.resumeWithOwner(tenant, testCap, OwnerLegacy)
	if _, err := g.Begin(tenant, testCap); !errors.Is(err, ErrNotOwner) {
		t.Fatalf("Begin() após resumeWithOwner(legacy) = %v, esperado ErrNotOwner (owner mudou no rollback)", err)
	}
}

// TestGuard_DrainWaitsForInFlightWork replica o teste central de
// shutdown.Tracker (GO-005) no nível de chave: Drain só retorna depois que
// todo trabalho já admitido realmente terminou, nunca antes — a base do
// critério de aceite "requisições em andamento são drenadas" de GO-009.
func TestGuard_DrainWaitsForInFlightWork(t *testing.T) {
	g := NewGuard()
	tenant := tenancy.Tenant("acme")
	g.resumeWithOwner(tenant, testCap, OwnerGo)

	const n = 50
	var completed atomic.Int32
	var wg sync.WaitGroup
	started := make(chan struct{}, n)

	for i := 0; i < n; i++ {
		end, err := g.Begin(tenant, testCap)
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
	for i := 0; i < n; i++ {
		<-started
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := g.Drain(ctx, tenant, testCap); err != nil {
		t.Fatalf("Drain() erro inesperado: %v", err)
	}
	if got := completed.Load(); got != n {
		t.Fatalf("completed = %d, esperado %d — Drain retornou antes de todo trabalho terminar", got, n)
	}
	wg.Wait()
}

// TestGuard_KeysAreIndependent confirma que drenar uma chave não afeta
// outra — cada tenant/capacidade tem sua própria janela de corte,
// independente das demais.
func TestGuard_KeysAreIndependent(t *testing.T) {
	g := NewGuard()
	tenantA := tenancy.Tenant("acme")
	tenantB := tenancy.Tenant("beta")
	g.resumeWithOwner(tenantA, testCap, OwnerGo)
	g.resumeWithOwner(tenantB, testCap, OwnerGo)

	// Nada em curso para A: Drain(A) retorna de imediato, e serve só para
	// marcar a chave A como drenando (o que passa a recusar Begin(A)).
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := g.Drain(ctx, tenantA, testCap); err != nil {
		t.Fatalf("Drain(A): %v", err)
	}

	if _, err := g.Begin(tenantA, testCap); !errors.Is(err, ErrRouteDraining) {
		t.Fatalf("Begin(A) após Drain(A) = %v, esperado ErrRouteDraining", err)
	}

	// B não deveria ser afetado por A estar drenando.
	endB, err := g.Begin(tenantB, testCap)
	if err != nil {
		t.Fatalf("Begin(B) durante drenagem de A = %v, esperado sucesso (chaves independentes)", err)
	}
	endB()
}

// TestGuard_ConcurrentBeginAndEnd é o teste mais relevante para o race
// detector: muitas goroutines chamando Begin/end simultaneamente para a
// mesma chave, sem nenhuma ordenação externa.
func TestGuard_ConcurrentBeginAndEnd(t *testing.T) {
	g := NewGuard()
	tenant := tenancy.Tenant("acme")
	g.resumeWithOwner(tenant, testCap, OwnerGo)

	const workers = 100
	const iterations = 200
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < iterations; j++ {
				end, err := g.Begin(tenant, testCap)
				if err != nil {
					t.Errorf("Begin() erro inesperado: %v", err)
					return
				}
				end()
			}
		}()
	}
	wg.Wait()
	if got := g.InFlight(tenant, testCap); got != 0 {
		t.Fatalf("InFlight() = %d ao final, esperado 0", got)
	}
}
