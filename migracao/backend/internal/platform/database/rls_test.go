// Testes deste arquivo exigem Postgres real, o schema `acme` com uma tabela
// `rls_probe` com RLS habilitado, E uma conexão que NÃO seja superusuário
// (ver migracao/backend/README.md "Testes de RLS" para o SQL de
// preparação) — superusuários sempre ignoram RLS no Postgres,
// independentemente de `FORCE ROW LEVEL SECURITY`; testar com a conexão
// padrão (tipicamente `postgres`, superusuário) passaria mesmo com a
// política quebrada, um falso positivo silencioso. Por isso este arquivo
// usa SALTCORN_GO_TEST_DATABASE_URL_RLS, uma DSN separada, em vez de
// testDSN()/openTestDB() — pula (t.Skip) se não estiver definida.
//
// GO-008: prova que WithTenantAndActor propaga ator/papel de forma que uma
// política RLS nativa consegue negar acesso entre usuários diferentes no
// MESMO tenant/schema — a fronteira que WithTenant sozinho (GO-007) não
// cobre, porque GO-007 isola por schema, não por usuário dentro do schema.
package database

import (
	"context"
	"os"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/vjuliani/saltcorn/migracao/backend/internal/platform/tenancy"
)

func openRLSTestDB(t *testing.T) *DB {
	t.Helper()
	dsn := os.Getenv("SALTCORN_GO_TEST_DATABASE_URL_RLS")
	if dsn == "" {
		t.Skip("SALTCORN_GO_TEST_DATABASE_URL_RLS não definida — pulando teste de RLS (precisa de uma conexão não-superusuário)")
	}
	db, err := Open(context.Background(), dsn)
	if err != nil {
		t.Fatalf("Open() erro inesperado: %v", err)
	}
	t.Cleanup(db.Close)
	return db
}

// TestWithTenantAndActor_RLSDeniesCrossUserAccess é o teste central desta
// extensão: dois atores (user-1 dono de uma linha, user-2 dono de outra) no
// MESMO tenant `acme`. Com RLS habilitado em `rls_probe` (política
// comparando owner_id contra current_setting('app.current_user_id')),
// user-1 só pode ver sua própria linha, nunca a de user-2, mesmo estando na
// mesma transação/schema/tabela.
func TestWithTenantAndActor_RLSDeniesCrossUserAccess(t *testing.T) {
	db := openRLSTestDB(t)
	ctx := context.Background()

	const roleUser = 80 // papel comum, não-admin — RLS de ownership deve se aplicar

	var user1Rows, user2Rows []string
	if err := db.WithTenantAndActor(ctx, tenancy.Tenant("acme"), "user-1", roleUser, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx, "SELECT owner_id FROM rls_probe ORDER BY owner_id")
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var owner string
			if err := rows.Scan(&owner); err != nil {
				return err
			}
			user1Rows = append(user1Rows, owner)
		}
		return rows.Err()
	}); err != nil {
		t.Fatalf("WithTenantAndActor(user-1) erro inesperado: %v", err)
	}
	if len(user1Rows) != 1 || user1Rows[0] != "user-1" {
		t.Fatalf("user-1 viu linhas %v, esperado só [\"user-1\"] — RLS não isolou por dono", user1Rows)
	}

	if err := db.WithTenantAndActor(ctx, tenancy.Tenant("acme"), "user-2", roleUser, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx, "SELECT owner_id FROM rls_probe ORDER BY owner_id")
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var owner string
			if err := rows.Scan(&owner); err != nil {
				return err
			}
			user2Rows = append(user2Rows, owner)
		}
		return rows.Err()
	}); err != nil {
		t.Fatalf("WithTenantAndActor(user-2) erro inesperado: %v", err)
	}
	if len(user2Rows) != 1 || user2Rows[0] != "user-2" {
		t.Fatalf("user-2 viu linhas %v, esperado só [\"user-2\"] — RLS não isolou por dono", user2Rows)
	}
}

// TestWithTenantAndActor_AdminRoleBypassesOwnershipRLS confirma o outro lado
// da política: um ator com papel privilegiado o bastante (min_role_read,
// aqui simulado com role=1, admin) vê todas as linhas, não só a própria —
// replica a política `sc_rls_elevated` da produção Node (matriz GO-001
// §2.1), que combina ownership OR papel suficiente, não só ownership.
func TestWithTenantAndActor_AdminRoleBypassesOwnershipRLS(t *testing.T) {
	db := openRLSTestDB(t)
	ctx := context.Background()

	const roleAdmin = 1

	var rowCount int
	err := db.WithTenantAndActor(ctx, tenancy.Tenant("acme"), "someone-else-entirely", roleAdmin, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, "SELECT count(*) FROM rls_probe").Scan(&rowCount)
	})
	if err != nil {
		t.Fatalf("WithTenantAndActor(admin) erro inesperado: %v", err)
	}
	if rowCount < 2 {
		t.Errorf("admin viu %d linhas, esperado ver todas (>=2) — política elevada não está funcionando", rowCount)
	}
}

// TestWithTenantAndActor_ConcurrentUsersNoLeak é a versão "acesso cruzado"
// do teste de reuso de conexão de GO-007, agora no nível de usuário: muitas
// goroutines concorrentes alternando entre dois USUÁRIOS do mesmo tenant
// sobre um pool pequeno — nenhuma deveria ver a linha da outra.
func TestWithTenantAndActor_ConcurrentUsersNoLeak(t *testing.T) {
	db := openRLSTestDB(t)
	ctx := context.Background()
	const roleUser = 80

	users := []string{"user-1", "user-2"}
	const iterations = 30

	errs := make(chan error, len(users)*iterations)
	done := make(chan struct{}, len(users)*iterations)

	for _, u := range users {
		u := u
		for i := 0; i < iterations; i++ {
			go func() {
				var owner string
				err := db.WithTenantAndActor(ctx, tenancy.Tenant("acme"), u, roleUser, func(ctx context.Context, tx pgx.Tx) error {
					return tx.QueryRow(ctx, "SELECT owner_id FROM rls_probe LIMIT 1").Scan(&owner)
				})
				if err != nil {
					errs <- err
					return
				}
				if owner != u {
					errs <- errUnexpectedOwner(u, owner)
					return
				}
				done <- struct{}{}
			}()
		}
	}

	total := len(users) * iterations
	for i := 0; i < total; i++ {
		select {
		case err := <-errs:
			t.Error(err)
		case <-done:
		}
	}
}

type ownerMismatchError struct {
	expected, got string
}

func (e *ownerMismatchError) Error() string {
	return "esperava ver owner_id=" + e.expected + ", viu " + e.got + " — vazamento de identidade entre atores concorrentes"
}

func errUnexpectedOwner(expected, got string) error {
	return &ownerMismatchError{expected: expected, got: got}
}
