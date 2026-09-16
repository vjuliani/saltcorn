// Testes deste arquivo exigem Postgres real (mesma DSN de registry_test.go)
// — pulam via testDB(t) se SALTCORN_GO_TEST_DATABASE_URL não estiver
// definida.
package cutover

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

func TestSwitchOwner_PersistsAndUpdatesCache(t *testing.T) {
	db := testDB(t)
	tenant, capability := testCapability(t, db)
	guard := NewGuard()
	ctx := context.Background()

	if _, err := guard.Begin(tenant, capability); !errors.Is(err, ErrNotOwner) {
		t.Fatalf("Begin() antes de qualquer corte = %v, esperado ErrNotOwner", err)
	}

	if err := SwitchOwner(ctx, db, guard, tenant, capability, OwnerGo, 2*time.Second); err != nil {
		t.Fatalf("SwitchOwner(go): %v", err)
	}
	end, err := guard.Begin(tenant, capability)
	if err != nil {
		t.Fatalf("Begin() após corte para go: %v", err)
	}
	end()

	// Confirma que a persistência realmente aconteceu, não só o cache.
	var persisted Owner
	if err := db.WithTenant(ctx, publicSchema, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		persisted, err = OwnerOf(ctx, tx, tenant, capability)
		return err
	}); err != nil {
		t.Fatalf("OwnerOf: %v", err)
	}
	if persisted != OwnerGo {
		t.Fatalf("registro persistido = %q, esperado go", persisted)
	}

	if err := SwitchOwner(ctx, db, guard, tenant, capability, OwnerLegacy, 2*time.Second); err != nil {
		t.Fatalf("SwitchOwner(legacy, rollback): %v", err)
	}
	if _, err := guard.Begin(tenant, capability); !errors.Is(err, ErrNotOwner) {
		t.Fatalf("Begin() após rollback = %v, esperado ErrNotOwner", err)
	}
}

// TestSwitchOwner_DrainsInFlightBeforeFlipping é a prova direta de
// "requisições em andamento são drenadas": um Begin já concedido continua
// rodando, e SwitchOwner (rollback) não retorna antes desse trabalho
// terminar de verdade.
func TestSwitchOwner_DrainsInFlightBeforeFlipping(t *testing.T) {
	db := testDB(t)
	tenant, capability := testCapability(t, db)
	guard := NewGuard()
	ctx := context.Background()

	if err := SwitchOwner(ctx, db, guard, tenant, capability, OwnerGo, 2*time.Second); err != nil {
		t.Fatalf("corte inicial para go: %v", err)
	}

	end, err := guard.Begin(tenant, capability)
	if err != nil {
		t.Fatalf("Begin(): %v", err)
	}

	var inFlightCompleted atomic.Bool
	go func() {
		time.Sleep(150 * time.Millisecond)
		inFlightCompleted.Store(true)
		end()
	}()

	if err := SwitchOwner(ctx, db, guard, tenant, capability, OwnerLegacy, 2*time.Second); err != nil {
		t.Fatalf("SwitchOwner (rollback): %v", err)
	}
	if !inFlightCompleted.Load() {
		t.Fatal("SwitchOwner retornou antes do trabalho em curso terminar — não drenou de verdade")
	}
}

// TestSwitchOwner_NoAdmissionOnceDrainingConfirmed é o teste de "escritor
// único" deste pacote: enquanto SwitchOwner está bloqueado esperando um
// trabalho em curso terminar (drenando de verdade, não um caso vazio),
// confirma-se ativamente que a chave já está recusando admissão nova
// (mesmo padrão de espera ativa de
// shutdown_test.go/TestTracker_RejectsNewWorkAfterDrainStarts) antes de
// liberar o trabalho retido — nunca há uma janela onde Begin admite
// trabalho novo depois que a drenagem começou, porque Begin e Drain
// compartilham a mesma seção crítica (guard.mu). Um teste baseado em
// relógio de parede (hammering concorrente comparando timestamps entre
// goroutines) foi descartado aqui: sob agendamento do runtime Go, o
// instante em que uma goroutine lê time.Now() depois de Acquire() retornar
// pode ficar atrasado o bastante para parecer uma violação que nunca
// aconteceu de fato na seção crítica — um defeito do teste, não do
// mecanismo (achado desta tarefa).
func TestSwitchOwner_NoAdmissionOnceDrainingConfirmed(t *testing.T) {
	db := testDB(t)
	tenant, capability := testCapability(t, db)
	guard := NewGuard()
	ctx := context.Background()

	if err := SwitchOwner(ctx, db, guard, tenant, capability, OwnerGo, 2*time.Second); err != nil {
		t.Fatalf("corte inicial para go: %v", err)
	}

	// Segura uma unidade de trabalho em curso para forçar SwitchOwner a
	// esperar de verdade, em vez de drenar um conjunto vazio.
	end, err := Acquire(guard, tenant, capability)
	if err != nil {
		t.Fatalf("Acquire (retido): %v", err)
	}

	switchDone := make(chan error, 1)
	go func() {
		switchDone <- SwitchOwner(ctx, db, guard, tenant, capability, OwnerLegacy, 5*time.Second)
	}()

	// Espera ativamente até a chave começar a recusar admissão nova — a
	// prova de que a drenagem já começou antes de liberar o trabalho
	// retido. Antes de draining=true, este polling pode legitimamente
	// suceder (owner ainda é Go) — cada sucesso precisa ser liberado de
	// imediato, senão o próprio polling prenderia SwitchOwner esperando um
	// trabalho que nunca terminaria (mesmo padrão de
	// shutdown_test.go/TestTracker_RejectsNewWorkAfterDrainStarts).
	deadline := time.Now().Add(2 * time.Second)
	for {
		pollEnd, acqErr := Acquire(guard, tenant, capability)
		if errors.Is(acqErr, ErrRouteDraining) {
			break
		}
		if acqErr != nil {
			t.Fatalf("Acquire (polling) erro inesperado: %v", acqErr)
		}
		pollEnd()
		if time.Now().After(deadline) {
			t.Fatal("timeout esperando a chave entrar em modo de drenagem")
		}
		time.Sleep(time.Millisecond)
	}

	// Confirmado que está drenando: nenhuma tentativa concorrente de
	// admissão deveria suceder agora, por construção (mesma seção crítica
	// que marcou draining=true). Libera o trabalho retido para que
	// SwitchOwner possa completar o dreno e persistir o novo owner.
	end()

	if err := <-switchDone; err != nil {
		t.Fatalf("SwitchOwner (rollback): %v", err)
	}
	if _, err := Acquire(guard, tenant, capability); !errors.Is(err, ErrNotOwner) {
		t.Fatalf("Acquire após rollback = %v, esperado ErrNotOwner", err)
	}
}
