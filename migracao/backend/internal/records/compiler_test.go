// Testes deste arquivo exigem Postgres real — pulam (t.Skip) se
// SALTCORN_GO_TEST_DATABASE_URL não estiver definida. Cada teste cria seu
// próprio schema de tenant isolado, mesmo padrão de
// internal/metadata/catalog_test.go.
//
// O corpus replica o fixture books/publisher/patients referenciado em
// GO-001/GO-002 (`db/fixtures.ts`) — não é uma comparação ao vivo contra o
// legado Node (nenhum processo Node roda neste ambiente de execução), mas
// o mesmo shape de dados, testado quanto ao comportamento SQL correto
// contra Postgres real (ver nota de escopo em execucoes/GO-012.md).
package records

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/vjuliani/saltcorn/migracao/backend/internal/identity"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/metadata"
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

func testTenant(t *testing.T, db *database.DB) tenancy.Tenant {
	t.Helper()
	tenant := tenancy.Tenant(fmt.Sprintf("records_test_%s", sanitizeForSchema(t.Name())))
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
		return metadata.EnsureSchema(ctx, tx)
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

// corpus é o catálogo do fixture books/publisher/patients criado para um
// teste — os IDs de tabela resolvidos, para montar Query.Table/ChildTable
// pelos nomes sem precisar redescobrir tudo em cada teste.
type corpus struct {
	publisherID int
	booksID     int
}

// seedCorpus cria publisher(name) e books(author, pages, publisher key,
// assessment_date, available) — o mesmo shape de campos do fixture legado
// (author/pages em books; favbook/parent como key em patients, aqui
// representado por books.publisher apontando para publisher).
func seedCorpus(t *testing.T, db *database.DB, tenant tenancy.Tenant) corpus {
	t.Helper()
	ctx := context.Background()
	var c corpus
	if err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		pub, err := metadata.CreateTable(ctx, tx, identity.RoleAdmin, "publisher", metadata.TableOptions{})
		if err != nil {
			return err
		}
		c.publisherID = pub.ID
		if _, err := metadata.AddField(ctx, tx, identity.RoleAdmin, pub.ID, metadata.FieldDef{Name: "name", Type: metadata.FieldText, Required: true}); err != nil {
			return err
		}

		books, err := metadata.CreateTable(ctx, tx, identity.RoleAdmin, "books", metadata.TableOptions{})
		if err != nil {
			return err
		}
		c.booksID = books.ID
		if _, err := metadata.AddField(ctx, tx, identity.RoleAdmin, books.ID, metadata.FieldDef{Name: "author", Type: metadata.FieldText, Required: true}); err != nil {
			return err
		}
		if _, err := metadata.AddField(ctx, tx, identity.RoleAdmin, books.ID, metadata.FieldDef{Name: "pages", Type: metadata.FieldInteger, Required: true}); err != nil {
			return err
		}
		if _, err := metadata.AddField(ctx, tx, identity.RoleAdmin, books.ID, metadata.FieldDef{Name: "publisher", Type: metadata.FieldKey, References: "publisher"}); err != nil {
			return err
		}
		if _, err := metadata.AddField(ctx, tx, identity.RoleAdmin, books.ID, metadata.FieldDef{Name: "assessment_date", Type: metadata.FieldDate}); err != nil {
			return err
		}
		if _, err := metadata.AddField(ctx, tx, identity.RoleAdmin, books.ID, metadata.FieldDef{Name: "available", Type: metadata.FieldBoolean}); err != nil {
			return err
		}
		return nil
	}); err != nil {
		t.Fatalf("seedCorpus: %v", err)
	}
	return c
}

func insertPublisher(t *testing.T, db *database.DB, tenant tenancy.Tenant, name string) int {
	t.Helper()
	var id int
	if err := db.WithTenant(context.Background(), tenant, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `INSERT INTO publisher (name) VALUES ($1) RETURNING id`, name).Scan(&id)
	}); err != nil {
		t.Fatalf("insertPublisher: %v", err)
	}
	return id
}

type bookRow struct {
	author         string
	pages          int
	publisherID    *int
	assessmentDate *time.Time
	available      *bool
}

func insertBook(t *testing.T, db *database.DB, tenant tenancy.Tenant, b bookRow) int {
	t.Helper()
	var id int
	if err := db.WithTenant(context.Background(), tenant, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx,
			`INSERT INTO books (author, pages, publisher, assessment_date, available) VALUES ($1, $2, $3, $4, $5) RETURNING id`,
			b.author, b.pages, b.publisherID, b.assessmentDate, b.available,
		).Scan(&id)
	}); err != nil {
		t.Fatalf("insertBook: %v", err)
	}
	return id
}

func intPtr(i int) *int              { return &i }
func timePtr(t time.Time) *time.Time { return &t }
func boolPtr(b bool) *bool           { return &b }

func runQuery(t *testing.T, db *database.DB, tenant tenancy.Tenant, actorRole identity.RoleID, q Query) ([]map[string]any, error) {
	t.Helper()
	var results []map[string]any
	err := db.WithTenant(context.Background(), tenant, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		results, err = Rows(ctx, tx, actorRole, q)
		return err
	})
	return results, err
}

func TestCompile_EqAndTypes(t *testing.T) {
	db := testDB(t)
	tenant := testTenant(t, db)
	seedCorpus(t, db, tenant)

	pubID := insertPublisher(t, db, tenant, "O'Reilly")
	when := time.Date(2020, 1, 15, 0, 0, 0, 0, time.UTC)
	insertBook(t, db, tenant, bookRow{author: "Ada Lovelace", pages: 320, publisherID: intPtr(pubID), assessmentDate: timePtr(when), available: boolPtr(true)})
	insertBook(t, db, tenant, bookRow{author: "Alan Turing", pages: 210, publisherID: nil, assessmentDate: nil, available: boolPtr(false)})

	rows, err := runQuery(t, db, tenant, identity.RolePublic, Query{Table: "books", Where: Eq{Field: "author", Value: "Ada Lovelace"}})
	if err != nil {
		t.Fatalf("Rows: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("len(rows) = %d, esperado 1", len(rows))
	}
	if rows[0]["author"] != "Ada Lovelace" {
		t.Errorf("author = %v", rows[0]["author"])
	}
	if rows[0]["pages"] != int32(320) {
		t.Errorf("pages = %v (%T), esperado int32(320)", rows[0]["pages"], rows[0]["pages"])
	}
	if rows[0]["available"] != true {
		t.Errorf("available = %v", rows[0]["available"])
	}
	gotTime, ok := rows[0]["assessment_date"].(time.Time)
	if !ok || !gotTime.Equal(when) {
		t.Errorf("assessment_date = %v, esperado %v", rows[0]["assessment_date"], when)
	}
}

func TestCompile_NullHandling(t *testing.T) {
	db := testDB(t)
	tenant := testTenant(t, db)
	seedCorpus(t, db, tenant)

	insertBook(t, db, tenant, bookRow{author: "No Publisher", pages: 1, publisherID: nil})
	pubID := insertPublisher(t, db, tenant, "Has Publisher")
	insertBook(t, db, tenant, bookRow{author: "Has Publisher Book", pages: 2, publisherID: intPtr(pubID)})

	rows, err := runQuery(t, db, tenant, identity.RolePublic, Query{Table: "books", Where: Eq{Field: "publisher", Value: nil}})
	if err != nil {
		t.Fatalf("Rows: %v", err)
	}
	if len(rows) != 1 || rows[0]["author"] != "No Publisher" {
		t.Fatalf("filtro IS NULL retornou %v, esperado só \"No Publisher\"", rows)
	}
}

func TestCompile_Between(t *testing.T) {
	db := testDB(t)
	tenant := testTenant(t, db)
	seedCorpus(t, db, tenant)

	older := time.Date(2010, 1, 1, 0, 0, 0, 0, time.UTC)
	inRange := time.Date(2020, 6, 1, 0, 0, 0, 0, time.UTC)
	newer := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
	insertBook(t, db, tenant, bookRow{author: "Old", pages: 1, assessmentDate: timePtr(older)})
	insertBook(t, db, tenant, bookRow{author: "InRange", pages: 1, assessmentDate: timePtr(inRange)})
	insertBook(t, db, tenant, bookRow{author: "New", pages: 1, assessmentDate: timePtr(newer)})

	rows, err := runQuery(t, db, tenant, identity.RolePublic, Query{
		Table: "books",
		Where: Between{Field: "assessment_date", Min: time.Date(2015, 1, 1, 0, 0, 0, 0, time.UTC), Max: time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)},
	})
	if err != nil {
		t.Fatalf("Rows: %v", err)
	}
	if len(rows) != 1 || rows[0]["author"] != "InRange" {
		t.Fatalf("Between retornou %v, esperado só \"InRange\"", rows)
	}
}

func TestCompile_InNotIn(t *testing.T) {
	db := testDB(t)
	tenant := testTenant(t, db)
	seedCorpus(t, db, tenant)

	insertBook(t, db, tenant, bookRow{author: "A", pages: 100})
	insertBook(t, db, tenant, bookRow{author: "B", pages: 200})
	insertBook(t, db, tenant, bookRow{author: "C", pages: 300})

	rows, err := runQuery(t, db, tenant, identity.RolePublic, Query{Table: "books", Where: In{Field: "pages", Values: []any{100, 300}}})
	if err != nil {
		t.Fatalf("Rows (In): %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("len(rows) In = %d, esperado 2", len(rows))
	}

	rows, err = runQuery(t, db, tenant, identity.RolePublic, Query{Table: "books", Where: NotIn{Field: "pages", Values: []any{100, 300}}})
	if err != nil {
		t.Fatalf("Rows (NotIn): %v", err)
	}
	if len(rows) != 1 || rows[0]["author"] != "B" {
		t.Fatalf("NotIn retornou %v, esperado só \"B\"", rows)
	}

	rows, err = runQuery(t, db, tenant, identity.RolePublic, Query{Table: "books", Where: In{Field: "pages", Values: []any{}}})
	if err != nil {
		t.Fatalf("Rows (In vazio): %v", err)
	}
	if len(rows) != 0 {
		t.Errorf("In com lista vazia deveria retornar 0 linhas, retornou %d", len(rows))
	}
}

func TestCompile_LikeAndComparisons(t *testing.T) {
	db := testDB(t)
	tenant := testTenant(t, db)
	seedCorpus(t, db, tenant)

	insertBook(t, db, tenant, bookRow{author: "Ada Lovelace", pages: 100})
	insertBook(t, db, tenant, bookRow{author: "Alan Turing", pages: 200})
	insertBook(t, db, tenant, bookRow{author: "Grace Hopper", pages: 300})

	rows, err := runQuery(t, db, tenant, identity.RolePublic, Query{Table: "books", Where: Like{Field: "author", Substring: "ada"}})
	if err != nil {
		t.Fatalf("Rows (Like): %v", err)
	}
	if len(rows) != 1 || rows[0]["author"] != "Ada Lovelace" {
		t.Fatalf("Like case-insensitive falhou: %v", rows)
	}

	rows, err = runQuery(t, db, tenant, identity.RolePublic, Query{Table: "books", Where: Gt{Field: "pages", Value: 200}})
	if err != nil {
		t.Fatalf("Rows (Gt): %v", err)
	}
	if len(rows) != 1 || rows[0]["author"] != "Grace Hopper" {
		t.Fatalf("Gt falhou: %v", rows)
	}

	rows, err = runQuery(t, db, tenant, identity.RolePublic, Query{Table: "books", Where: Lte{Field: "pages", Value: 200}})
	if err != nil {
		t.Fatalf("Rows (Lte): %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("Lte retornou %d linhas, esperado 2", len(rows))
	}
}

// TestCompile_CompositeConditions cobre "chaves compostas" do critério de
// aceite: um filtro por várias colunas ao mesmo tempo (E implícito entre
// entradas do legado, aqui And explícito).
func TestCompile_CompositeConditions(t *testing.T) {
	db := testDB(t)
	tenant := testTenant(t, db)
	seedCorpus(t, db, tenant)

	insertBook(t, db, tenant, bookRow{author: "Ada Lovelace", pages: 100})
	insertBook(t, db, tenant, bookRow{author: "Ada Lovelace", pages: 200})
	insertBook(t, db, tenant, bookRow{author: "Alan Turing", pages: 100})

	rows, err := runQuery(t, db, tenant, identity.RolePublic, Query{
		Table: "books",
		Where: And{Eq{Field: "author", Value: "Ada Lovelace"}, Eq{Field: "pages", Value: 100}},
	})
	if err != nil {
		t.Fatalf("Rows (And composto): %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("condição composta retornou %d linhas, esperado 1", len(rows))
	}

	rows, err = runQuery(t, db, tenant, identity.RolePublic, Query{
		Table: "books",
		Where: Or{Eq{Field: "author", Value: "Alan Turing"}, Eq{Field: "pages", Value: 200}},
	})
	if err != nil {
		t.Fatalf("Rows (Or): %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("Or retornou %d linhas, esperado 2", len(rows))
	}

	rows, err = runQuery(t, db, tenant, identity.RolePublic, Query{
		Table: "books",
		Where: Not{Cond: Eq{Field: "author", Value: "Ada Lovelace"}},
	})
	if err != nil {
		t.Fatalf("Rows (Not): %v", err)
	}
	if len(rows) != 1 || rows[0]["author"] != "Alan Turing" {
		t.Fatalf("Not retornou %v, esperado só \"Alan Turing\"", rows)
	}
}

func TestCompile_OrderByLimitOffset(t *testing.T) {
	db := testDB(t)
	tenant := testTenant(t, db)
	seedCorpus(t, db, tenant)

	for i := 1; i <= 5; i++ {
		insertBook(t, db, tenant, bookRow{author: fmt.Sprintf("Author%d", i), pages: i * 10})
	}

	rows, err := runQuery(t, db, tenant, identity.RolePublic, Query{
		Table:   "books",
		OrderBy: []OrderTerm{{Field: "pages", Desc: true}},
		Limit:   2,
		Offset:  1,
	})
	if err != nil {
		t.Fatalf("Rows: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("len(rows) = %d, esperado 2", len(rows))
	}
	// Ordem desc por pages: 50,40,30,20,10 — limit 2 offset 1 => [40, 30]
	if rows[0]["pages"] != int32(40) || rows[1]["pages"] != int32(30) {
		t.Errorf("paginação/ordenação incorreta: %v", rows)
	}
}

func TestCompile_Join(t *testing.T) {
	db := testDB(t)
	tenant := testTenant(t, db)
	seedCorpus(t, db, tenant)

	pubID := insertPublisher(t, db, tenant, "Acme Press")
	insertBook(t, db, tenant, bookRow{author: "Ada Lovelace", pages: 100, publisherID: intPtr(pubID)})

	rows, err := runQuery(t, db, tenant, identity.RolePublic, Query{
		Table: "books",
		Joins: []Join{{Field: "publisher", Select: []string{"name"}}},
	})
	if err != nil {
		t.Fatalf("Rows (Join): %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("len(rows) = %d, esperado 1", len(rows))
	}
	if rows[0]["publisher__name"] != "Acme Press" {
		t.Errorf("publisher__name = %v, esperado Acme Press", rows[0]["publisher__name"])
	}
}

func TestCompile_Aggregation(t *testing.T) {
	db := testDB(t)
	tenant := testTenant(t, db)
	seedCorpus(t, db, tenant)

	pubID := insertPublisher(t, db, tenant, "Acme Press")
	insertBook(t, db, tenant, bookRow{author: "A", pages: 100, publisherID: intPtr(pubID)})
	insertBook(t, db, tenant, bookRow{author: "B", pages: 200, publisherID: intPtr(pubID)})
	insertBook(t, db, tenant, bookRow{author: "C", pages: 50}) // sem publisher

	rows, err := runQuery(t, db, tenant, identity.RolePublic, Query{
		Table: "publisher",
		Aggregations: []Aggregation{
			{Alias: "book_count", ChildTable: "books", FKField: "publisher", Function: Count},
			{Alias: "total_pages", ChildTable: "books", FKField: "publisher", Function: Sum, TargetField: "pages"},
		},
	})
	if err != nil {
		t.Fatalf("Rows (Aggregation): %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("len(rows) = %d, esperado 1", len(rows))
	}
	if rows[0]["book_count"] != int64(2) {
		t.Errorf("book_count = %v (%T), esperado 2", rows[0]["book_count"], rows[0]["book_count"])
	}
	if rows[0]["total_pages"] != int64(300) {
		t.Errorf("total_pages = %v (%T), esperado 300", rows[0]["total_pages"], rows[0]["total_pages"])
	}
}

// TestCompile_UnknownFieldRejected é metade da prova de "consultas
// inválidas não permitem injeção SQL": um nome de campo não catalogado —
// incluindo uma tentativa de injeção via nome — é rejeitado antes de
// qualquer SQL ser montado.
func TestCompile_UnknownFieldRejected(t *testing.T) {
	db := testDB(t)
	tenant := testTenant(t, db)
	seedCorpus(t, db, tenant)

	cases := []string{
		"nao_existe",
		`author"; DROP TABLE books; --`,
		"author, (SELECT 1)",
	}
	for _, field := range cases {
		_, err := runQuery(t, db, tenant, identity.RolePublic, Query{Table: "books", Where: Eq{Field: field, Value: "x"}})
		if !errors.Is(err, ErrUnknownField) {
			t.Errorf("campo %q = %v, esperado ErrUnknownField", field, err)
		}
	}

	// A tabela books precisa continuar existindo e utilizável.
	if _, err := runQuery(t, db, tenant, identity.RolePublic, Query{Table: "books"}); err != nil {
		t.Errorf("books deveria continuar consultável após tentativas de injeção via nome de campo: %v", err)
	}
}

func TestCompile_UnknownTableRejected(t *testing.T) {
	db := testDB(t)
	tenant := testTenant(t, db)
	seedCorpus(t, db, tenant)

	_, err := runQuery(t, db, tenant, identity.RolePublic, Query{Table: "nao_existe; DROP TABLE books; --"})
	if !errors.Is(err, ErrUnknownTable) {
		t.Errorf("tabela inexistente = %v, esperado ErrUnknownTable", err)
	}
}

// TestCompile_MaliciousValueIsParametrizedNotInjected é a outra metade da
// prova de "consultas inválidas não permitem injeção SQL": um VALOR de
// filtro com sintaxe SQL é sempre parametrizado, nunca interpolado —
// confirmado tanto pelo resultado (0 linhas, sem erro de sintaxe) quanto
// pela tabela sobrevivendo intacta.
func TestCompile_MaliciousValueIsParametrizedNotInjected(t *testing.T) {
	db := testDB(t)
	tenant := testTenant(t, db)
	seedCorpus(t, db, tenant)
	insertBook(t, db, tenant, bookRow{author: "Ada Lovelace", pages: 100})

	malicious := "x'; DROP TABLE books; --"
	rows, err := runQuery(t, db, tenant, identity.RolePublic, Query{Table: "books", Where: Eq{Field: "author", Value: malicious}})
	if err != nil {
		t.Fatalf("consulta com valor malicioso deveria só não achar nada, não falhar: %v", err)
	}
	if len(rows) != 0 {
		t.Errorf("esperado 0 linhas para valor malicioso não correspondente, obteve %d", len(rows))
	}

	// books precisa continuar existindo com o registro original intacto.
	rows, err = runQuery(t, db, tenant, identity.RolePublic, Query{Table: "books", Where: Eq{Field: "author", Value: "Ada Lovelace"}})
	if err != nil {
		t.Fatalf("books foi corrompida pela tentativa de injeção: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("registro original não sobreviveu: %v", rows)
	}
}

func TestCompile_TypeMismatchRejected(t *testing.T) {
	db := testDB(t)
	tenant := testTenant(t, db)
	seedCorpus(t, db, tenant)

	_, err := runQuery(t, db, tenant, identity.RolePublic, Query{Table: "books", Where: Eq{Field: "pages", Value: "not-a-number"}})
	if !errors.Is(err, ErrTypeMismatch) {
		t.Errorf("filtro com tipo incompatível = %v, esperado ErrTypeMismatch", err)
	}
}

// TestCompile_AuthorizationDenied cobre "permissões" do critério de
// aceite: uma tabela com min_role_read restrito a admin recusa um ator
// público.
func TestCompile_AuthorizationDenied(t *testing.T) {
	db := testDB(t)
	tenant := testTenant(t, db)
	ctx := context.Background()

	if err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		_, err := metadata.CreateTable(ctx, tx, identity.RoleAdmin, "secrets", metadata.TableOptions{MinRoleRead: identity.RoleAdmin, MinRoleWrite: identity.RoleAdmin})
		return err
	}); err != nil {
		t.Fatalf("setup: %v", err)
	}

	_, err := runQuery(t, db, tenant, identity.RolePublic, Query{Table: "secrets"})
	if !errors.Is(err, ErrNotAuthorized) {
		t.Errorf("consulta com ator público em tabela admin-only = %v, esperado ErrNotAuthorized", err)
	}

	_, err = runQuery(t, db, tenant, identity.RoleAdmin, Query{Table: "secrets"})
	if err != nil {
		t.Errorf("consulta com ator admin deveria suceder: %v", err)
	}
}
