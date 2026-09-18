// Corpus de GO-030: as MESMAS operações de catálogo (catalog_test.go,
// contra Postgres) rodando contra o adapter SQLite
// (internal/platform/sqlite) — a prova concreta do critério de aceite
// "mesmas fixtures de domínio passam PG/SQLite com divergências
// justificadas". Cada teste aqui tem um equivalente direto em
// catalog_test.go; divergências de sintaxe (id autoincrementado,
// serialização de mutação de catálogo) são internas a EnsureSchema/
// CreateTable/lockCatalog (schema.go, catalog.go) — invisíveis a partir
// desta API, exatamente o ponto do critério de aceite.
package metadata

import (
	"context"
	"errors"
	"testing"

	"github.com/vjuliani/saltcorn/migracao/backend/internal/identity"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/platform/database"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/platform/sqlite"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/platform/tenancy"
)

func sqliteTestDB(t *testing.T) *sqlite.DB {
	t.Helper()
	db, err := sqlite.Open(t.TempDir())
	if err != nil {
		t.Fatalf("sqlite.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func TestSQLite_EnsureSchema_Idempotente(t *testing.T) {
	db := sqliteTestDB(t)
	ctx := context.Background()
	tenant := tenancy.Tenant("acme")

	for i := 0; i < 2; i++ {
		if err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx database.Tx) error {
			return EnsureSchema(ctx, tx)
		}); err != nil {
			t.Fatalf("EnsureSchema (chamada %d): %v", i+1, err)
		}
	}
}

func TestSQLite_CreateTable_CriaTabelaFisicaComAutoincremento(t *testing.T) {
	db := sqliteTestDB(t)
	ctx := context.Background()
	tenant := tenancy.Tenant("acme")

	if err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx database.Tx) error {
		return EnsureSchema(ctx, tx)
	}); err != nil {
		t.Fatalf("EnsureSchema: %v", err)
	}

	var tableID int
	if err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx database.Tx) error {
		tbl, err := CreateTable(ctx, tx, identity.RoleAdmin, "guitars", TableOptions{})
		if err != nil {
			return err
		}
		tableID = tbl.ID
		if tbl.MinRoleRead != identity.RolePublic {
			t.Errorf("MinRoleRead = %v, esperado RolePublic (padrão)", tbl.MinRoleRead)
		}
		return nil
	}); err != nil {
		t.Fatalf("CreateTable: %v", err)
	}
	if tableID == 0 {
		t.Fatal("tableID = 0, esperado atribuído pelo catálogo")
	}

	// A tabela física precisa aceitar INSERT sem informar id (autoincremento
	// real, não só um nome de coluna "id" sem comportamento) — a divergência
	// de sintaxe que schema.go/catalog.go tratam (idColumnDDL) é exatamente
	// para isto funcionar de forma idêntica ao Postgres do ponto de vista
	// de quem usa a tabela.
	if err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx database.Tx) error {
		if err := tx.Exec(ctx, "INSERT INTO guitars DEFAULT VALUES"); err != nil {
			return err
		}
		var id int
		if err := tx.QueryRow(ctx, "SELECT id FROM guitars").Scan(&id); err != nil {
			return err
		}
		if id != 1 {
			t.Errorf("id da primeira linha = %d, esperado 1 (autoincremento)", id)
		}
		return nil
	}); err != nil {
		t.Fatalf("inserir na tabela física: %v", err)
	}
}

func TestSQLite_CreateTable_MesmoNomeEIdempotente(t *testing.T) {
	db := sqliteTestDB(t)
	ctx := context.Background()
	tenant := tenancy.Tenant("acme")
	if err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx database.Tx) error { return EnsureSchema(ctx, tx) }); err != nil {
		t.Fatalf("EnsureSchema: %v", err)
	}

	var firstID, secondID int
	if err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx database.Tx) error {
		tbl, err := CreateTable(ctx, tx, identity.RoleAdmin, "books", TableOptions{})
		if err != nil {
			return err
		}
		firstID = tbl.ID
		return nil
	}); err != nil {
		t.Fatalf("primeira CreateTable: %v", err)
	}
	if err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx database.Tx) error {
		tbl, err := CreateTable(ctx, tx, identity.RoleAdmin, "books", TableOptions{})
		if err != nil {
			return err
		}
		secondID = tbl.ID
		return nil
	}); err != nil {
		t.Fatalf("segunda CreateTable (idempotente): %v", err)
	}
	if firstID != secondID {
		t.Errorf("segunda CreateTable com o mesmo nome criou um ID diferente: %d != %d", firstID, secondID)
	}
}

func TestSQLite_AddField_ComReferenciaEIdempotencia(t *testing.T) {
	db := sqliteTestDB(t)
	ctx := context.Background()
	tenant := tenancy.Tenant("acme")
	if err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx database.Tx) error { return EnsureSchema(ctx, tx) }); err != nil {
		t.Fatalf("EnsureSchema: %v", err)
	}

	var authorsID, booksID int
	if err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx database.Tx) error {
		authors, err := CreateTable(ctx, tx, identity.RoleAdmin, "authors", TableOptions{})
		if err != nil {
			return err
		}
		authorsID = authors.ID
		books, err := CreateTable(ctx, tx, identity.RoleAdmin, "books", TableOptions{})
		if err != nil {
			return err
		}
		booksID = books.ID
		if _, err := AddField(ctx, tx, identity.RoleAdmin, booksID, FieldDef{Name: "title", Type: FieldText, Required: true}); err != nil {
			return err
		}
		f, err := AddField(ctx, tx, identity.RoleAdmin, booksID, FieldDef{Name: "author", Type: FieldKey, References: "authors"})
		if err != nil {
			return err
		}
		if f.ReferencesTable != authorsID {
			t.Errorf("ReferencesTable = %d, esperado %d (id de authors)", f.ReferencesTable, authorsID)
		}
		return nil
	}); err != nil {
		t.Fatalf("setup: %v", err)
	}

	// Idempotência: mesmo nome, mesmo tipo, não falha nem duplica.
	if err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx database.Tx) error {
		_, err := AddField(ctx, tx, identity.RoleAdmin, booksID, FieldDef{Name: "title", Type: FieldText, Required: true})
		return err
	}); err != nil {
		t.Fatalf("AddField idempotente: %v", err)
	}

	// Mesmo nome, tipo diferente: ErrFieldTypeMismatch, nunca altera silenciosamente.
	err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx database.Tx) error {
		_, err := AddField(ctx, tx, identity.RoleAdmin, booksID, FieldDef{Name: "title", Type: FieldInteger})
		return err
	})
	if !errors.Is(err, ErrFieldTypeMismatch) {
		t.Fatalf("err = %v, esperado ErrFieldTypeMismatch", err)
	}

	// A referência física (FOREIGN KEY) precisa existir de verdade na
	// tabela SQLite, não só no catálogo — insere um autor e um livro
	// referenciando-o, via a tabela física.
	if err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx database.Tx) error {
		if err := tx.Exec(ctx, "INSERT INTO authors DEFAULT VALUES"); err != nil {
			return err
		}
		return tx.Exec(ctx, "INSERT INTO books (title, author) VALUES ($1, $2)", "Dune", 1)
	}); err != nil {
		t.Fatalf("inserir usando a coluna de referência física: %v", err)
	}
}

func TestSQLite_ListTablesAndFields(t *testing.T) {
	db := sqliteTestDB(t)
	ctx := context.Background()
	tenant := tenancy.Tenant("acme")
	if err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx database.Tx) error { return EnsureSchema(ctx, tx) }); err != nil {
		t.Fatalf("EnsureSchema: %v", err)
	}

	if err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx database.Tx) error {
		books, err := CreateTable(ctx, tx, identity.RoleAdmin, "books", TableOptions{})
		if err != nil {
			return err
		}
		_, err = AddField(ctx, tx, identity.RoleAdmin, books.ID, FieldDef{Name: "title", Type: FieldText})
		return err
	}); err != nil {
		t.Fatalf("setup: %v", err)
	}

	if err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx database.Tx) error {
		tables, err := ListTables(ctx, tx)
		if err != nil {
			return err
		}
		if len(tables) != 1 || tables[0].Name != "books" {
			t.Fatalf("ListTables = %+v, esperado só \"books\"", tables)
		}
		fields, err := ListFields(ctx, tx, tables[0].ID)
		if err != nil {
			return err
		}
		if len(fields) != 1 || fields[0].Name != "title" {
			t.Fatalf("ListFields = %+v, esperado só \"title\"", fields)
		}
		byID, err := GetTableByID(ctx, tx, tables[0].ID)
		if err != nil {
			return err
		}
		if byID.Name != "books" {
			t.Errorf("GetTableByID.Name = %q, esperado \"books\"", byID.Name)
		}
		return nil
	}); err != nil {
		t.Fatalf("verificação: %v", err)
	}
}

func TestSQLite_DropFieldAndDropTable_Idempotentes(t *testing.T) {
	db := sqliteTestDB(t)
	ctx := context.Background()
	tenant := tenancy.Tenant("acme")
	if err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx database.Tx) error { return EnsureSchema(ctx, tx) }); err != nil {
		t.Fatalf("EnsureSchema: %v", err)
	}

	var tableID int
	if err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx database.Tx) error {
		tbl, err := CreateTable(ctx, tx, identity.RoleAdmin, "books", TableOptions{})
		if err != nil {
			return err
		}
		tableID = tbl.ID
		_, err = AddField(ctx, tx, identity.RoleAdmin, tableID, FieldDef{Name: "title", Type: FieldText})
		return err
	}); err != nil {
		t.Fatalf("setup: %v", err)
	}

	// DropField duas vezes — segunda é no-op bem-sucedido.
	for i := 0; i < 2; i++ {
		if err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx database.Tx) error {
			return DropField(ctx, tx, identity.RoleAdmin, tableID, "title")
		}); err != nil {
			t.Fatalf("DropField (chamada %d): %v", i+1, err)
		}
	}
	// DropTable duas vezes — mesma coisa.
	for i := 0; i < 2; i++ {
		if err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx database.Tx) error {
			return DropTable(ctx, tx, identity.RoleAdmin, tableID)
		}); err != nil {
			t.Fatalf("DropTable (chamada %d): %v", i+1, err)
		}
	}

	if err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx database.Tx) error {
		_, err := GetTableByID(ctx, tx, tableID)
		if !errors.Is(err, ErrTableNotFound) {
			t.Errorf("GetTableByID após DropTable = %v, esperado ErrTableNotFound", err)
		}
		return nil
	}); err != nil {
		t.Fatalf("verificação final: %v", err)
	}
}

func TestSQLite_CurrentVersion_IncrementaSoQuandoMuda(t *testing.T) {
	db := sqliteTestDB(t)
	ctx := context.Background()
	tenant := tenancy.Tenant("acme")
	if err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx database.Tx) error { return EnsureSchema(ctx, tx) }); err != nil {
		t.Fatalf("EnsureSchema: %v", err)
	}

	var v0, v1, v2 int64
	if err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx database.Tx) error {
		var err error
		v0, err = CurrentVersion(ctx, tx)
		return err
	}); err != nil {
		t.Fatalf("CurrentVersion inicial: %v", err)
	}

	if err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx database.Tx) error {
		_, err := CreateTable(ctx, tx, identity.RoleAdmin, "books", TableOptions{})
		if err != nil {
			return err
		}
		v1, err = CurrentVersion(ctx, tx)
		return err
	}); err != nil {
		t.Fatalf("CreateTable + CurrentVersion: %v", err)
	}
	if v1 != v0+1 {
		t.Errorf("versão após CreateTable = %d, esperado %d (v0+1)", v1, v0+1)
	}

	// CreateTable idempotente (mesmo nome) NÃO deveria incrementar de novo.
	if err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx database.Tx) error {
		_, err := CreateTable(ctx, tx, identity.RoleAdmin, "books", TableOptions{})
		if err != nil {
			return err
		}
		v2, err = CurrentVersion(ctx, tx)
		return err
	}); err != nil {
		t.Fatalf("CreateTable idempotente + CurrentVersion: %v", err)
	}
	if v2 != v1 {
		t.Errorf("versão após CreateTable idempotente = %d, esperado %d (sem incremento, nada mudou)", v2, v1)
	}
}

// TestSQLite_ConcorrenciaMesmoTenant_VersaoNuncaPerdeIncremento é o
// equivalente SQLite de TestConcurrentCreateTable_VersionNeverLosesIncrement
// (concurrency_test.go, Postgres) — o critério de aceite "concorrência ...
// verificada sem sintaxe exclusiva de PG": aqui a exclusão mútua vem de
// internal/platform/sqlite (uma conexão só por arquivo de tenant), não de
// pg_advisory_xact_lock (que lockCatalog explicitamente pula para SQLite,
// ver catalog.go) — mesmo resultado observável, mecanismo diferente.
func TestSQLite_ConcorrenciaMesmoTenant_VersaoNuncaPerdeIncremento(t *testing.T) {
	db := sqliteTestDB(t)
	ctx := context.Background()
	tenant := tenancy.Tenant("acme")
	if err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx database.Tx) error { return EnsureSchema(ctx, tx) }); err != nil {
		t.Fatalf("EnsureSchema: %v", err)
	}

	const n = 10
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		i := i
		go func() {
			errs <- db.WithTenant(ctx, tenant, func(ctx context.Context, tx database.Tx) error {
				_, err := CreateTable(ctx, tx, identity.RoleAdmin, tableNameFor(i), TableOptions{})
				return err
			})
		}()
	}
	for i := 0; i < n; i++ {
		if err := <-errs; err != nil {
			t.Fatalf("CreateTable concorrente: %v", err)
		}
	}

	if err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx database.Tx) error {
		version, err := CurrentVersion(ctx, tx)
		if err != nil {
			return err
		}
		if version != n {
			t.Errorf("versão final = %d, esperado %d (uma por CreateTable, nenhuma perdida)", version, n)
		}
		tables, err := ListTables(ctx, tx)
		if err != nil {
			return err
		}
		if len(tables) != n {
			t.Errorf("ListTables tem %d entradas, esperado %d", len(tables), n)
		}
		return nil
	}); err != nil {
		t.Fatalf("verificação final: %v", err)
	}
}

func tableNameFor(i int) string {
	return "t_" + string(rune('a'+i))
}
