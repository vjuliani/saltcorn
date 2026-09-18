package notify

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"
)

func TestCreate_InsertsNotification(t *testing.T) {
	db, tenant := notifyFixture(t)
	ctx := context.Background()

	var created Notification
	if err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		created, err = Create(ctx, tx, Notification{UserID: 1, Title: "Olá", Body: "Corpo", Link: "/x"}, false, "")
		return err
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if created.ID == 0 {
		t.Fatal("created.ID = 0, esperado atribuído pelo catálogo")
	}
	if created.Read {
		t.Error("Read = true, esperado false para notificação recém-criada")
	}
}

// TestCreate_WithEmail_EnqueuesEmailAtomically prova que a notificação e
// o e-mail correspondente são atômicos entre si — divergência deliberada
// e mais forte que o legado (ver comentário de Create em
// notifications.go).
func TestCreate_WithEmail_EnqueuesEmailAtomically(t *testing.T) {
	db, tenant := notifyFixture(t)
	ctx := context.Background()

	if err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		_, err := Create(ctx, tx, Notification{UserID: 1, Title: "Aviso", Body: "Algo aconteceu"}, true, "usuario@example.com")
		return err
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}

	if err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		var count int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM _sc_outbox WHERE event_type = $1`, EventTypeEmail).Scan(&count); err != nil {
			return err
		}
		if count != 1 {
			t.Errorf("_sc_outbox tem %d evento(s) de e-mail, esperado 1", count)
		}
		return nil
	}); err != nil {
		t.Fatalf("verificação: %v", err)
	}
}

// TestCreate_WithEmail_RollbackDiscardsBoth prova a atomicidade no outro
// sentido: se a transação sofrer rollback, nem a notificação nem o
// e-mail enfileirado sobrevivem.
func TestCreate_WithEmail_RollbackDiscardsBoth(t *testing.T) {
	db, tenant := notifyFixture(t)
	ctx := context.Background()

	errDeliberateRollback := errors.New("rollback deliberado do teste")
	err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		if _, err := Create(ctx, tx, Notification{UserID: 1, Title: "Vai sumir"}, true, "x@example.com"); err != nil {
			return err
		}
		return errDeliberateRollback
	})
	if !errors.Is(err, errDeliberateRollback) {
		t.Fatalf("err = %v, esperado errDeliberateRollback", err)
	}

	if err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		var notifCount, outboxCount int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM _sc_notifications`).Scan(&notifCount); err != nil {
			return err
		}
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM _sc_outbox`).Scan(&outboxCount); err != nil {
			return err
		}
		if notifCount != 0 || outboxCount != 0 {
			t.Errorf("notifCount=%d outboxCount=%d, esperado 0 e 0 após rollback", notifCount, outboxCount)
		}
		return nil
	}); err != nil {
		t.Fatalf("verificação: %v", err)
	}
}

func TestMarkRead_And_ListForUser(t *testing.T) {
	db, tenant := notifyFixture(t)
	ctx := context.Background()

	var n1, n2 Notification
	if err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		n1, err = Create(ctx, tx, Notification{UserID: 5, Title: "Primeira"}, false, "")
		if err != nil {
			return err
		}
		n2, err = Create(ctx, tx, Notification{UserID: 5, Title: "Segunda"}, false, "")
		return err
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}

	if err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		return MarkRead(ctx, tx, n1.ID)
	}); err != nil {
		t.Fatalf("MarkRead: %v", err)
	}

	if err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		all, err := ListForUser(ctx, tx, 5, false, 10)
		if err != nil {
			return err
		}
		if len(all) != 2 {
			t.Fatalf("ListForUser(unreadOnly=false) = %d, esperado 2", len(all))
		}
		unread, err := ListForUser(ctx, tx, 5, true, 10)
		if err != nil {
			return err
		}
		if len(unread) != 1 || unread[0].ID != n2.ID {
			t.Fatalf("ListForUser(unreadOnly=true) = %+v, esperado só n2 (%d)", unread, n2.ID)
		}
		return nil
	}); err != nil {
		t.Fatalf("verificação: %v", err)
	}
}
