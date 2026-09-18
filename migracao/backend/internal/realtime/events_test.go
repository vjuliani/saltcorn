package realtime

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/vjuliani/saltcorn/migracao/backend/internal/platform/database"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/platform/tenancy"
)

func TestPublish_Broadcast_NoUserIDs_OK(t *testing.T) {
	db, tenant := realtimeFixture(t)
	ctx := context.Background()

	err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		ev, err := Publish(ctx, tx, AudienceBroadcast, nil, map[string]any{"type": "ping"})
		if err != nil {
			return err
		}
		if ev.ID == 0 {
			t.Fatalf("esperava um ID atribuído, veio 0")
		}
		if ev.Audience != AudienceBroadcast {
			t.Fatalf("Audience = %q, esperava %q", ev.Audience, AudienceBroadcast)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}
}

func TestPublish_Users_RequiresUserIDs(t *testing.T) {
	db, tenant := realtimeFixture(t)
	ctx := context.Background()

	err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		_, err := Publish(ctx, tx, AudienceUsers, nil, map[string]any{"type": "notification"})
		return err
	})
	if !errors.Is(err, ErrInvalidUserIDs) {
		t.Fatalf("Publish(AudienceUsers, nil, ...) erro = %v, esperava ErrInvalidUserIDs", err)
	}
}

func TestPublish_Broadcast_RejectsUserIDs(t *testing.T) {
	db, tenant := realtimeFixture(t)
	ctx := context.Background()

	err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		_, err := Publish(ctx, tx, AudienceBroadcast, []int{1}, map[string]any{"type": "ping"})
		return err
	})
	if !errors.Is(err, ErrInvalidUserIDs) {
		t.Fatalf("Publish(AudienceBroadcast, [1], ...) erro = %v, esperava ErrInvalidUserIDs", err)
	}
}

func TestPublish_InvalidAudience_Rejected(t *testing.T) {
	db, tenant := realtimeFixture(t)
	ctx := context.Background()

	err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		_, err := Publish(ctx, tx, Audience("qualquer-coisa"), nil, map[string]any{})
		return err
	})
	if !errors.Is(err, ErrInvalidAudience) {
		t.Fatalf("Publish(audience inválida) erro = %v, esperava ErrInvalidAudience", err)
	}
}

// TestListSinceForActor_Ordering_ReturnsInPublishOrder é o teste do
// critério de aceite "ordenação" de GO-028: publica eventos intercalando
// broadcast/users e confirma que voltam em EXATAMENTE ordem de
// publicação, nunca reordenados por audience ou payload.
func TestListSinceForActor_Ordering_ReturnsInPublishOrder(t *testing.T) {
	db, tenant := realtimeFixture(t)
	ctx := context.Background()
	const actorID = 7

	var published []int64
	err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		for i := 0; i < 5; i++ {
			audience := AudienceBroadcast
			var userIDs []int
			if i%2 == 0 {
				audience = AudienceUsers
				userIDs = []int{actorID}
			}
			ev, err := Publish(ctx, tx, audience, userIDs, map[string]any{"seq": i})
			if err != nil {
				return err
			}
			published = append(published, ev.ID)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("publicar eventos: %v", err)
	}

	var got []Event
	err = db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		got, err = ListSinceForActor(ctx, tx, 0, actorID, 100)
		return err
	})
	if err != nil {
		t.Fatalf("ListSinceForActor: %v", err)
	}
	if len(got) != len(published) {
		t.Fatalf("recebeu %d eventos, esperava %d", len(got), len(published))
	}
	for i, ev := range got {
		if ev.ID != published[i] {
			t.Fatalf("evento %d: ID=%d, esperava %d (ordem de publicação quebrada)", i, ev.ID, published[i])
		}
		if seq, _ := ev.Payload["seq"].(float64); int(seq) != i {
			t.Fatalf("evento %d: payload.seq=%v, esperava %d", i, ev.Payload["seq"], i)
		}
	}
}

// TestListSinceForActor_UsersAudience_OnlyVisibleToListedActor é o teste
// do critério de aceite "isolamento por tenant" na sua forma mais
// granular (isolamento por DESTINATÁRIO dentro do mesmo tenant) — o
// isolamento entre tenants em si é estrutural (schema separado,
// TestListSinceForActor_NeverLeaksAcrossTenantSchemas abaixo confirma).
func TestListSinceForActor_UsersAudience_OnlyVisibleToListedActor(t *testing.T) {
	db, tenant := realtimeFixture(t)
	ctx := context.Background()
	const targetActor = 42
	const otherActor = 43

	err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		_, err := Publish(ctx, tx, AudienceUsers, []int{targetActor}, map[string]any{"type": "notification"})
		return err
	})
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}

	var forTarget, forOther []Event
	err = db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		if forTarget, err = ListSinceForActor(ctx, tx, 0, targetActor, 100); err != nil {
			return err
		}
		forOther, err = ListSinceForActor(ctx, tx, 0, otherActor, 100)
		return err
	})
	if err != nil {
		t.Fatalf("ListSinceForActor: %v", err)
	}
	if len(forTarget) != 1 {
		t.Fatalf("ator alvo: recebeu %d eventos, esperava 1", len(forTarget))
	}
	if len(forOther) != 0 {
		t.Fatalf("ator não listado recebeu %d eventos endereçados a outro usuário — vazamento", len(forOther))
	}
}

func TestListSinceForActor_Broadcast_VisibleToAnyActor(t *testing.T) {
	db, tenant := realtimeFixture(t)
	ctx := context.Background()

	err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		_, err := Publish(ctx, tx, AudienceBroadcast, nil, map[string]any{"type": "ping"})
		return err
	})
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}

	for _, actorID := range []int{1, 999} {
		var got []Event
		err = db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
			var err error
			got, err = ListSinceForActor(ctx, tx, 0, actorID, 100)
			return err
		})
		if err != nil {
			t.Fatalf("ListSinceForActor(actor=%d): %v", actorID, err)
		}
		if len(got) != 1 {
			t.Fatalf("ator %d: recebeu %d eventos broadcast, esperava 1", actorID, len(got))
		}
	}
}

func TestListSinceForActor_AfterID_ExcludesAlreadySeen(t *testing.T) {
	db, tenant := realtimeFixture(t)
	ctx := context.Background()
	const actorID = 1

	var firstID int64
	err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		ev, err := Publish(ctx, tx, AudienceBroadcast, nil, map[string]any{"seq": 0})
		if err != nil {
			return err
		}
		firstID = ev.ID
		_, err = Publish(ctx, tx, AudienceBroadcast, nil, map[string]any{"seq": 1})
		return err
	})
	if err != nil {
		t.Fatalf("publicar eventos: %v", err)
	}

	var got []Event
	err = db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		got, err = ListSinceForActor(ctx, tx, firstID, actorID, 100)
		return err
	})
	if err != nil {
		t.Fatalf("ListSinceForActor: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("recebeu %d eventos após o cursor, esperava 1 (só o segundo)", len(got))
	}
	if seq, _ := got[0].Payload["seq"].(float64); int(seq) != 1 {
		t.Fatalf("payload.seq=%v, esperava 1 (o evento já visto pelo cursor não deveria voltar)", got[0].Payload["seq"])
	}
}

// TestListSinceForActor_NeverLeaksAcrossTenantSchemas é o teste mais
// direto do critério de aceite "isolamento por tenant": dois tenants
// distintos, cada um com seu próprio schema Postgres (mesma convenção de
// internal/platform/database.WithTenant usada por todo o resto do
// backend) — publicar no tenant A nunca deveria ser visível a partir de
// uma transação aberta no tenant B, mesmo com o MESMO actorID (números de
// usuário não são globalmente únicos entre tenants).
func TestListSinceForActor_NeverLeaksAcrossTenantSchemas(t *testing.T) {
	dbA, tenantA := realtimeFixture(t)
	_, tenantB := realtimeFixtureOnSameDB(t, dbA)
	ctx := context.Background()
	const actorID = 1

	err := dbA.WithTenant(ctx, tenantA, func(ctx context.Context, tx pgx.Tx) error {
		_, err := Publish(ctx, tx, AudienceBroadcast, nil, map[string]any{"tenant": "A"})
		return err
	})
	if err != nil {
		t.Fatalf("Publish no tenant A: %v", err)
	}

	var gotInB []Event
	err = dbA.WithTenant(ctx, tenantB, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		gotInB, err = ListSinceForActor(ctx, tx, 0, actorID, 100)
		return err
	})
	if err != nil {
		t.Fatalf("ListSinceForActor no tenant B: %v", err)
	}
	if len(gotInB) != 0 {
		t.Fatalf("tenant B enxergou %d evento(s) publicado(s) no tenant A — vazamento entre tenants", len(gotInB))
	}
}

// realtimeFixtureOnSameDB cria um SEGUNDO schema de tenant na mesma
// conexão de banco de realtimeFixture (t), com seu próprio EnsureSchema —
// evitar abrir uma segunda *database.DB só para este teste de isolamento.
func realtimeFixtureOnSameDB(t *testing.T, db *database.DB) (*database.DB, tenancy.Tenant) {
	t.Helper()
	ctx := context.Background()
	tenant := tenancy.Tenant(fmt.Sprintf("realtime_test_%s_b", shortSanitizedName(t.Name(), 40)))

	if err := db.WithTenant(ctx, "public", func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, fmt.Sprintf(`CREATE SCHEMA IF NOT EXISTS %s`, pgx.Identifier{string(tenant)}.Sanitize()))
		return err
	}); err != nil {
		t.Fatalf("criar segundo schema de teste: %v", err)
	}
	t.Cleanup(func() {
		_ = db.WithTenant(context.Background(), "public", func(ctx context.Context, tx pgx.Tx) error {
			_, err := tx.Exec(ctx, fmt.Sprintf(`DROP SCHEMA IF EXISTS %s CASCADE`, pgx.Identifier{string(tenant)}.Sanitize()))
			return err
		})
	})
	if err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		return EnsureSchema(ctx, tx)
	}); err != nil {
		t.Fatalf("EnsureSchema no segundo tenant: %v", err)
	}
	return db, tenant
}
