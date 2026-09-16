// Testes deste arquivo exigem Postgres real — pulam (t.Skip) se
// SALTCORN_GO_TEST_DATABASE_URL não estiver definida. Cada teste cria seu
// próprio schema de tenant isolado (nome derivado do nome do teste) e
// limpa ao final, mesmo padrão de internal/identity/store_test.go.
package metadata

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/vjuliani/saltcorn/migracao/backend/internal/identity"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/platform/database"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/platform/tenancy"
)

func testDB(t *testing.T) *database.DB {
	t.Helper()
	dsn := os.Getenv("SALTCORN_GO_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("SALTCORN_GO_TEST_DATABASE_URL não definida — pulando teste que exige Postgres real")
	}
	db, err := database.Open(context.Background(), dsn)
	if err != nil {
		t.Fatalf("database.Open() erro inesperado: %v", err)
	}
	t.Cleanup(db.Close)
	return db
}

// testTenant cria um schema isolado por teste (evita que testes
// concorrentes do pacote colidam em _sc_tables/_sc_fields) e garante o
// schema de metadados dentro dele.
func testTenant(t *testing.T, db *database.DB) tenancy.Tenant {
	t.Helper()
	tenant := tenancy.Tenant(fmt.Sprintf("metadata_test_%s", sanitizeForSchema(t.Name())))
	ctx := context.Background()

	if err := db.WithTenant(ctx, "public", func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, fmt.Sprintf(`CREATE SCHEMA IF NOT EXISTS %s`, pgx.Identifier{string(tenant)}.Sanitize()))
		return err
	}); err != nil {
		t.Fatalf("criar schema de teste: %v", err)
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
		t.Fatalf("EnsureSchema: %v", err)
	}
	return tenant
}

func sanitizeForSchema(name string) string {
	out := make([]rune, 0, len(name))
	for _, r := range name {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			out = append(out, r)
		} else {
			out = append(out, '_')
		}
	}
	return string(out)
}

// physicalTableExists confere diretamente no information_schema que a
// tabela física existe no schema do tenant — a prova de que catálogo e DDL
// realmente concordam, não só que o catálogo diz que deveriam.
func physicalTableExists(t *testing.T, db *database.DB, tenant tenancy.Tenant, name string) bool {
	t.Helper()
	var exists bool
	err := db.WithTenant(context.Background(), tenant, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx,
			"SELECT EXISTS(SELECT 1 FROM information_schema.tables WHERE table_schema = current_schema() AND table_name = $1)",
			name,
		).Scan(&exists)
	})
	if err != nil {
		t.Fatalf("physicalTableExists: %v", err)
	}
	return exists
}

func physicalColumnExists(t *testing.T, db *database.DB, tenant tenancy.Tenant, table, column string) bool {
	t.Helper()
	var exists bool
	err := db.WithTenant(context.Background(), tenant, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx,
			"SELECT EXISTS(SELECT 1 FROM information_schema.columns WHERE table_schema = current_schema() AND table_name = $1 AND column_name = $2)",
			table, column,
		).Scan(&exists)
	})
	if err != nil {
		t.Fatalf("physicalColumnExists: %v", err)
	}
	return exists
}

// TestCreateTable_CatalogAndDDLAgree cobre o critério de aceite "criar/
// alterar schema mantém catálogo e DDL consistentes": depois de
// CreateTable, tanto a linha de catálogo quanto a tabela física existem e
// concordam.
func TestCreateTable_CatalogAndDDLAgree(t *testing.T) {
	db := testDB(t)
	tenant := testTenant(t, db)
	ctx := context.Background()

	var created *Table
	if err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		created, err = CreateTable(ctx, tx, identity.RoleAdmin, "guitars", TableOptions{})
		return err
	}); err != nil {
		t.Fatalf("CreateTable: %v", err)
	}
	if created.Name != "guitars" {
		t.Errorf("Name = %q, esperado guitars", created.Name)
	}
	if !physicalTableExists(t, db, tenant, "guitars") {
		t.Error("tabela física \"guitars\" não existe apesar do catálogo dizer que sim")
	}
}

func TestCreateTable_Idempotent(t *testing.T) {
	db := testDB(t)
	tenant := testTenant(t, db)
	ctx := context.Background()

	create := func() *Table {
		var tbl *Table
		if err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
			var err error
			tbl, err = CreateTable(ctx, tx, identity.RoleAdmin, "books", TableOptions{})
			return err
		}); err != nil {
			t.Fatalf("CreateTable: %v", err)
		}
		return tbl
	}

	first := create()
	second := create()
	if first.ID != second.ID {
		t.Errorf("CreateTable idempotente retornou IDs diferentes: %d, %d", first.ID, second.ID)
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
		t.Errorf("versão do catálogo = %d após CreateTable chamado 2x com o mesmo nome, esperado 1 (só a primeira chamada muda algo)", version)
	}
}

func TestCreateTable_MaliciousNameSanitizedSafely(t *testing.T) {
	db := testDB(t)
	tenant := testTenant(t, db)
	ctx := context.Background()

	var created *Table
	if err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		created, err = CreateTable(ctx, tx, identity.RoleAdmin, "students; DROP TABLE _sc_tables;--", TableOptions{})
		return err
	}); err != nil {
		t.Fatalf("CreateTable com nome malicioso: %v", err)
	}
	// A tabela _sc_tables precisa continuar existindo (não foi derrubada).
	if !physicalTableExists(t, db, tenant, "_sc_tables") {
		t.Fatal("_sc_tables foi derrubada — nome malicioso não foi neutralizado")
	}
	if !physicalTableExists(t, db, tenant, created.Name) {
		t.Errorf("tabela sanitizada %q não foi criada fisicamente", created.Name)
	}
}

func TestCreateTable_EmptyNameAfterSanitizationFails(t *testing.T) {
	db := testDB(t)
	tenant := testTenant(t, db)
	ctx := context.Background()

	err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		_, err := CreateTable(ctx, tx, identity.RoleAdmin, ";;;---", TableOptions{})
		return err
	})
	if !errors.Is(err, ErrInvalidName) {
		t.Errorf("CreateTable com nome só de símbolos = %v, esperado ErrInvalidName", err)
	}
}

// TestCreateTable_RequiresAdmin cobre "autorização" da rotina de validação:
// um ator não-admin não pode mutar o catálogo.
func TestCreateTable_RequiresAdmin(t *testing.T) {
	db := testDB(t)
	tenant := testTenant(t, db)
	ctx := context.Background()

	err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		_, err := CreateTable(ctx, tx, identity.RolePublic, "should_not_exist", TableOptions{})
		return err
	})
	if !errors.Is(err, ErrNotAuthorized) {
		t.Errorf("CreateTable com ator não-admin = %v, esperado ErrNotAuthorized", err)
	}
	if physicalTableExists(t, db, tenant, "should_not_exist") {
		t.Error("tabela foi criada fisicamente apesar da autorização ter sido negada")
	}
}

func TestAddField_CatalogAndDDLAgree(t *testing.T) {
	db := testDB(t)
	tenant := testTenant(t, db)
	ctx := context.Background()

	var tableID int
	if err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		tbl, err := CreateTable(ctx, tx, identity.RoleAdmin, "books", TableOptions{})
		if err != nil {
			return err
		}
		tableID = tbl.ID
		_, err = AddField(ctx, tx, identity.RoleAdmin, tableID, FieldDef{Name: "title", Type: FieldText, Required: true})
		return err
	}); err != nil {
		t.Fatalf("setup: %v", err)
	}

	if !physicalColumnExists(t, db, tenant, "books", "title") {
		t.Error("coluna física \"title\" não existe apesar do catálogo dizer que sim")
	}
}

func TestAddField_Idempotent(t *testing.T) {
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

	addTitle := func() *Field {
		var f *Field
		if err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
			var err error
			f, err = AddField(ctx, tx, identity.RoleAdmin, tableID, FieldDef{Name: "title", Type: FieldText})
			return err
		}); err != nil {
			t.Fatalf("AddField: %v", err)
		}
		return f
	}

	first := addTitle()
	second := addTitle()
	if first.ID != second.ID {
		t.Errorf("AddField idempotente retornou IDs diferentes: %d, %d", first.ID, second.ID)
	}
}

func TestAddField_TypeMismatchRejected(t *testing.T) {
	db := testDB(t)
	tenant := testTenant(t, db)
	ctx := context.Background()

	var tableID int
	if err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		tbl, err := CreateTable(ctx, tx, identity.RoleAdmin, "books", TableOptions{})
		tableID = tbl.ID
		if err != nil {
			return err
		}
		_, err = AddField(ctx, tx, identity.RoleAdmin, tableID, FieldDef{Name: "pages", Type: FieldInteger})
		return err
	}); err != nil {
		t.Fatalf("setup: %v", err)
	}

	err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		_, err := AddField(ctx, tx, identity.RoleAdmin, tableID, FieldDef{Name: "pages", Type: FieldText})
		return err
	})
	if !errors.Is(err, ErrFieldTypeMismatch) {
		t.Errorf("AddField com tipo diferente para campo existente = %v, esperado ErrFieldTypeMismatch", err)
	}
}

// TestAddField_KeyRelation cobre "relações" do escopo: um campo do tipo
// FieldKey vira uma FOREIGN KEY real do Postgres, não só um metadado solto.
func TestAddField_KeyRelation(t *testing.T) {
	db := testDB(t)
	tenant := testTenant(t, db)
	ctx := context.Background()

	var bookID int
	if err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		if _, err := CreateTable(ctx, tx, identity.RoleAdmin, "publisher", TableOptions{}); err != nil {
			return err
		}
		book, err := CreateTable(ctx, tx, identity.RoleAdmin, "books", TableOptions{})
		if err != nil {
			return err
		}
		bookID = book.ID
		_, err = AddField(ctx, tx, identity.RoleAdmin, bookID, FieldDef{Name: "publisher", Type: FieldKey, References: "publisher"})
		return err
	}); err != nil {
		t.Fatalf("setup: %v", err)
	}

	if !physicalColumnExists(t, db, tenant, "books", "publisher") {
		t.Fatal("coluna \"publisher\" não existe")
	}

	// Confirma que a FK física de verdade existe e aponta para a tabela certa.
	var fkExists bool
	err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `
			SELECT EXISTS (
				SELECT 1
				FROM information_schema.table_constraints tc
				JOIN information_schema.constraint_column_usage ccu
					ON tc.constraint_name = ccu.constraint_name AND tc.table_schema = ccu.table_schema
				WHERE tc.constraint_type = 'FOREIGN KEY'
					AND tc.table_schema = current_schema()
					AND tc.table_name = 'books'
					AND ccu.table_name = 'publisher'
			)`).Scan(&fkExists)
	})
	if err != nil {
		t.Fatalf("consultar FK: %v", err)
	}
	if !fkExists {
		t.Error("FOREIGN KEY física de books.publisher -> publisher não foi encontrada")
	}
}

func TestAddField_ReferencedTableNotFound(t *testing.T) {
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

	err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		_, err := AddField(ctx, tx, identity.RoleAdmin, tableID, FieldDef{Name: "publisher", Type: FieldKey, References: "nao_existe"})
		return err
	})
	if !errors.Is(err, ErrReferencedTableNotFound) {
		t.Errorf("AddField referenciando tabela inexistente = %v, esperado ErrReferencedTableNotFound", err)
	}
}

// TestAddField_IntermediateFailureIsRecoverable é a prova direta do
// critério de aceite "falha intermediária é recuperável": força a etapa de
// DDL a falhar (a coluna física já existe fora do conhecimento do
// catálogo) depois que o INSERT no catálogo já rodaria — confirma que a
// transação inteira desfaz, sem deixar uma linha de catálogo órfã.
func TestAddField_IntermediateFailureIsRecoverable(t *testing.T) {
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

	// Simula drift: uma coluna física "isbn" existe sem o catálogo saber —
	// a etapa de ALTER TABLE ADD COLUMN de AddField vai falhar
	// (coluna já existe) depois que o INSERT no catálogo já teria
	// acontecido, exercitando o rollback de ponta a ponta.
	if err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `ALTER TABLE books ADD COLUMN isbn text`)
		return err
	}); err != nil {
		t.Fatalf("preparar drift: %v", err)
	}

	err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		_, err := AddField(ctx, tx, identity.RoleAdmin, tableID, FieldDef{Name: "isbn", Type: FieldText})
		return err
	})
	if err == nil {
		t.Fatal("AddField deveria ter falhado (coluna física já existe fora do catálogo)")
	}

	// A linha de catálogo NÃO deveria ter sobrevivido — rollback completo.
	err = db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		_, err := getFieldByName(ctx, tx, tableID, "isbn")
		return err
	})
	if !errors.Is(err, ErrFieldNotFound) {
		t.Errorf("getFieldByName após falha de DDL = %v, esperado ErrFieldNotFound (catálogo deveria ter revertido)", err)
	}

	var version int64
	if err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		version, err = CurrentVersion(ctx, tx)
		return err
	}); err != nil {
		t.Fatalf("CurrentVersion: %v", err)
	}
	if version != 1 { // só a criação da tabela "books" deveria ter incrementado
		t.Errorf("versão do catálogo = %d após falha de AddField, esperado 1 (o incremento da falha não deveria ter persistido)", version)
	}
}

func TestDropField_IdempotentAndRemovesPhysically(t *testing.T) {
	db := testDB(t)
	tenant := testTenant(t, db)
	ctx := context.Background()

	var tableID int
	if err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
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

	drop := func() {
		if err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
			return DropField(ctx, tx, identity.RoleAdmin, tableID, "title")
		}); err != nil {
			t.Fatalf("DropField: %v", err)
		}
	}
	drop()
	if physicalColumnExists(t, db, tenant, "books", "title") {
		t.Fatal("coluna física \"title\" ainda existe após DropField")
	}
	drop() // segunda chamada: idempotente, não deveria falhar
}

func TestDropTable_IdempotentAndRemovesPhysically(t *testing.T) {
	db := testDB(t)
	tenant := testTenant(t, db)
	ctx := context.Background()

	var tableID int
	if err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		tbl, err := CreateTable(ctx, tx, identity.RoleAdmin, "temp_table", TableOptions{})
		tableID = tbl.ID
		return err
	}); err != nil {
		t.Fatalf("setup: %v", err)
	}

	drop := func() {
		if err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
			return DropTable(ctx, tx, identity.RoleAdmin, tableID)
		}); err != nil {
			t.Fatalf("DropTable: %v", err)
		}
	}
	drop()
	if physicalTableExists(t, db, tenant, "temp_table") {
		t.Fatal("tabela física \"temp_table\" ainda existe após DropTable")
	}
	drop() // segunda chamada: idempotente
}

func TestGetTableByID(t *testing.T) {
	db := testDB(t)
	tenant := testTenant(t, db)
	ctx := context.Background()

	var created *Table
	if err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		created, err = CreateTable(ctx, tx, identity.RoleAdmin, "guitars", TableOptions{})
		return err
	}); err != nil {
		t.Fatalf("CreateTable: %v", err)
	}

	var got *Table
	if err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		got, err = GetTableByID(ctx, tx, created.ID)
		return err
	}); err != nil {
		t.Fatalf("GetTableByID: %v", err)
	}
	if got.Name != "guitars" {
		t.Errorf("GetTableByID Name = %q, esperado guitars", got.Name)
	}
}

func TestGetTableByID_NotFound(t *testing.T) {
	db := testDB(t)
	tenant := testTenant(t, db)
	ctx := context.Background()

	err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		_, err := GetTableByID(ctx, tx, 999999)
		return err
	})
	if !errors.Is(err, ErrTableNotFound) {
		t.Errorf("GetTableByID(id inexistente) = %v, esperado ErrTableNotFound", err)
	}
}
