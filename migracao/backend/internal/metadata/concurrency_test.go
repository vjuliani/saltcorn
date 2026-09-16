// Testes deste arquivo exigem Postgres real (mesma DSN de catalog_test.go).
package metadata

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/vjuliani/saltcorn/migracao/backend/internal/identity"
)

// TestCreateTable_ConcurrentDistinctNames é a metade "concorrência normal"
// do critério de aceite: N goroutines criando tabelas com nomes distintos
// ao mesmo tempo, todas devem suceder, e a versão final do catálogo deve
// bater exatamente com o número de tabelas criadas — nenhum incremento
// perdido por corrida no contador (lockCatalog serializa por tenant).
func TestCreateTable_ConcurrentDistinctNames(t *testing.T) {
	db := testDB(t)
	tenant := testTenant(t, db)
	ctx := context.Background()

	const n = 20
	var wg sync.WaitGroup
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
				_, err := CreateTable(ctx, tx, identity.RoleAdmin, fmt.Sprintf("table_%d", i), TableOptions{})
				return err
			})
			if err != nil {
				errs <- err
			}
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Errorf("CreateTable concorrente falhou: %v", err)
	}

	var version int64
	if err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		version, err = CurrentVersion(ctx, tx)
		return err
	}); err != nil {
		t.Fatalf("CurrentVersion: %v", err)
	}
	if version != n {
		t.Errorf("versão do catálogo = %d, esperado %d (uma tabela criada = um incremento, sem perda por corrida)", version, n)
	}

	for i := 0; i < n; i++ {
		if !physicalTableExists(t, db, tenant, fmt.Sprintf("table_%d", i)) {
			t.Errorf("tabela física table_%d não existe", i)
		}
	}
}

// TestCreateTable_ConcurrentSameNameIsSerializedAndIdempotent é a metade
// "concorrência adversarial" do critério de aceite: muitas goroutines
// tentando criar a MESMA tabela ao mesmo tempo — lockCatalog serializa,
// então exatamente uma faz a criação real (versão incrementa só uma vez) e
// todas retornam o mesmo ID de tabela, sem erro de violação de constraint
// única escapando para quem chamou.
func TestCreateTable_ConcurrentSameNameIsSerializedAndIdempotent(t *testing.T) {
	db := testDB(t)
	tenant := testTenant(t, db)
	ctx := context.Background()

	const n = 20
	var wg sync.WaitGroup
	ids := make([]int, n)
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
				tbl, err := CreateTable(ctx, tx, identity.RoleAdmin, "shared_table", TableOptions{})
				if err != nil {
					return err
				}
				ids[i] = tbl.ID
				return nil
			})
			if err != nil {
				errs <- err
			}
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Errorf("CreateTable concorrente (mesmo nome) falhou: %v", err)
	}

	first := ids[0]
	for i, id := range ids {
		if id != first {
			t.Errorf("ids[%d] = %d, esperado %d (todas as chamadas deveriam ver a mesma tabela)", i, id, first)
		}
	}

	var version int64
	if err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		version, err = CurrentVersion(ctx, tx)
		return err
	}); err != nil {
		t.Fatalf("CurrentVersion: %v", err)
	}
	if version != 1 {
		t.Errorf("versão do catálogo = %d, esperado 1 (só a primeira criação real deveria contar)", version)
	}
}

// TestAddField_ConcurrentSameFieldIsSerializedAndIdempotent replica o
// mesmo cuidado de concorrência para AddField, não só CreateTable.
func TestAddField_ConcurrentSameFieldIsSerializedAndIdempotent(t *testing.T) {
	db := testDB(t)
	tenant := testTenant(t, db)
	ctx := context.Background()

	var tableID int
	if err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		tbl, err := CreateTable(ctx, tx, identity.RoleAdmin, "books", TableOptions{})
		tableID = tbl.ID
		return err
	}); err != nil {
		t.Fatalf("setup: %v", err)
	}

	const n = 20
	var wg sync.WaitGroup
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
				_, err := AddField(ctx, tx, identity.RoleAdmin, tableID, FieldDef{Name: "title", Type: FieldText})
				return err
			})
			if err != nil {
				errs <- err
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Errorf("AddField concorrente falhou: %v", err)
	}

	if !physicalColumnExists(t, db, tenant, "books", "title") {
		t.Error("coluna física \"title\" não existe após AddField concorrente")
	}
}
