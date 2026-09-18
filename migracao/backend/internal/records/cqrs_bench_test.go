// Benchmark de GO-016 — "medir necessidade de projeções CQRS": compara o
// custo de uma consulta agregada CUSTOSA (listar N tabelas-pai com uma
// contagem de linhas filhas cada, via a agregação de GO-012/GO-015 — uma
// subquery correlacionada por linha) contra duas alternativas: a mesma
// consulta direta com um índice na coluna de chave estrangeira (que HOJE
// internal/metadata não cria — nenhum FieldKey ganha índice automático), e
// uma projeção pré-computada (o que uma projeção CQRS assíncrona real
// entregaria no lado da leitura). Não faz parte da suíte de correção
// rotineira: fica atrás de SALTCORN_GO_RUN_CQRS_BENCH=1, porque mede tempo,
// não corretude, e seu resultado depende do ambiente — ver
// docs/migracao-go/adr/0010-projecoes-cqrs.md para os números capturados e
// a decisão.
package records

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/vjuliani/saltcorn/migracao/backend/internal/identity"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/metadata"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/platform/database"
)

const (
	cqrsBenchAuthors        = 300
	cqrsBenchBooksPerAuthor = 200 // 60.000 livros no total — fan-out uniforme, não um único "hot row"
	cqrsBenchRepeats        = 5   // repetições por variante, para reportar min/mediana em vez de uma amostra só
)

func TestCQRSBenchmark_AggregationDirectVsIndexVsProjection(t *testing.T) {
	if os.Getenv("SALTCORN_GO_RUN_CQRS_BENCH") == "" {
		t.Skip("SALTCORN_GO_RUN_CQRS_BENCH não definida — benchmark de GO-016 (mede tempo, não corretude); ver docs/migracao-go/adr/0010-projecoes-cqrs.md")
	}
	db := testDB(t)
	tenant := testTenant(t, db)
	ctx := context.Background()

	if err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		authors, err := metadata.CreateTable(ctx, database.AsTx(tx), identity.RoleAdmin, "authors", metadata.TableOptions{})
		if err != nil {
			return err
		}
		if _, err := metadata.AddField(ctx, database.AsTx(tx), identity.RoleAdmin, authors.ID, metadata.FieldDef{Name: "name", Type: metadata.FieldText, Required: true}); err != nil {
			return err
		}
		books, err := metadata.CreateTable(ctx, database.AsTx(tx), identity.RoleAdmin, "books", metadata.TableOptions{})
		if err != nil {
			return err
		}
		if _, err := metadata.AddField(ctx, database.AsTx(tx), identity.RoleAdmin, books.ID, metadata.FieldDef{Name: "title", Type: metadata.FieldText, Required: true}); err != nil {
			return err
		}
		if _, err := metadata.AddField(ctx, database.AsTx(tx), identity.RoleAdmin, books.ID, metadata.FieldDef{Name: "pages", Type: metadata.FieldInteger, Required: true}); err != nil {
			return err
		}
		_, err = metadata.AddField(ctx, database.AsTx(tx), identity.RoleAdmin, books.ID, metadata.FieldDef{Name: "author", Type: metadata.FieldKey, References: "authors"})
		return err
	}); err != nil {
		t.Fatalf("setup do catálogo: %v", err)
	}

	// Seed em massa via SQL (não um insert por linha em Go) — o dataset em
	// si não é o que está sendo medido, só precisa existir rápido.
	if err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `INSERT INTO authors (name) SELECT 'author-' || g FROM generate_series(1, $1) g`, cqrsBenchAuthors); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `
			INSERT INTO books (title, pages, author)
			SELECT 'book-' || g, 100 + (g % 400), a.id
			FROM authors a, generate_series(1, $1) g
		`, cqrsBenchBooksPerAuthor)
		return err
	}); err != nil {
		t.Fatalf("seed em massa: %v", err)
	}

	directQuery := Query{
		Table: "authors",
		Aggregations: []Aggregation{
			{Alias: "book_count", ChildTable: "books", FKField: "author", Function: Count},
		},
	}

	// Variante A: consulta direta como o catálogo entrega hoje — SEM
	// índice na coluna de chave estrangeira "books.author", porque
	// internal/metadata (GO-011) não cria índice para nenhum FieldKey.
	// Cada linha de "authors" dispara uma subquery correlacionada que
	// varre "books" inteira (seq scan) — o pior caso realista, não
	// fabricado: é o comportamento de produção do compilador hoje.
	directNoIndex := timeRepeated(t, cqrsBenchRepeats, func() int {
		rows, err := runQuery(t, db, tenant, identity.RolePublic, directQuery)
		if err != nil {
			t.Fatalf("consulta direta (sem índice): %v", err)
		}
		return len(rows)
	})

	// Variante B: a MESMA consulta direta, mas com um índice manual em
	// books.author — isolando quanto do custo de A é "falta de índice"
	// (um fix de schema trivial) vs. quanto é inerente a computar o
	// agregado no momento da leitura (o que só uma projeção resolveria).
	if err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `CREATE INDEX ON books (author)`)
		return err
	}); err != nil {
		t.Fatalf("criar índice: %v", err)
	}
	directWithIndex := timeRepeated(t, cqrsBenchRepeats, func() int {
		rows, err := runQuery(t, db, tenant, identity.RolePublic, directQuery)
		if err != nil {
			t.Fatalf("consulta direta (com índice): %v", err)
		}
		return len(rows)
	})

	// Variante C: projeção pré-computada — o que uma projeção CQRS
	// assíncrona real entregaria no lado da leitura: uma tabela resumo já
	// materializada, lida com um SELECT simples, sem agregação em tempo
	// de leitura.
	if err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `
			CREATE TABLE author_book_counts AS
			SELECT author AS author_id, COUNT(*) AS book_count FROM books GROUP BY author
		`)
		return err
	}); err != nil {
		t.Fatalf("materializar projeção: %v", err)
	}
	projection := timeRepeated(t, cqrsBenchRepeats, func() int {
		var n int
		if err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
			rows, err := tx.Query(ctx, `SELECT author_id, book_count FROM author_book_counts`)
			if err != nil {
				return err
			}
			defer rows.Close()
			for rows.Next() {
				n++
			}
			return rows.Err()
		}); err != nil {
			t.Fatalf("ler projeção: %v", err)
		}
		return n
	})

	if directNoIndex.rows != cqrsBenchAuthors || directWithIndex.rows != cqrsBenchAuthors || projection.rows != cqrsBenchAuthors {
		t.Fatalf("contagem de linhas inconsistente entre variantes: sem índice=%d, com índice=%d, projeção=%d, esperado %d",
			directNoIndex.rows, directWithIndex.rows, projection.rows, cqrsBenchAuthors)
	}

	t.Logf("GO-016 benchmark (autores=%d, livros/autor=%d, total_livros=%d, repetições=%d)",
		cqrsBenchAuthors, cqrsBenchBooksPerAuthor, cqrsBenchAuthors*cqrsBenchBooksPerAuthor, cqrsBenchRepeats)
	t.Logf("  direto SEM índice   : min=%s mediana=%s max=%s", directNoIndex.min, directNoIndex.median, directNoIndex.max)
	t.Logf("  direto COM índice   : min=%s mediana=%s max=%s", directWithIndex.min, directWithIndex.median, directWithIndex.max)
	t.Logf("  projeção pré-computada: min=%s mediana=%s max=%s", projection.min, projection.median, projection.max)
	t.Logf("  aceleração índice sobre sem-índice: %.1fx (mediana)", float64(directNoIndex.median)/float64(directWithIndex.median))
	t.Logf("  aceleração projeção sobre com-índice: %.1fx (mediana)", float64(directWithIndex.median)/float64(projection.median))

	// Contrapartida de escrita: uma projeção mantida em tempo real (não
	// reconstruída sob demanda) precisa de uma atualização extra a cada
	// escrita na tabela filha — mede quanto isso custa por inserção,
	// contra a mesma inserção sem manutenção de projeção nenhuma.
	const writeIters = 1000

	writeNoProjection := timeRepeated(t, cqrsBenchRepeats, func() int {
		if err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
			for i := 0; i < writeIters; i++ {
				var authorID int
				if err := tx.QueryRow(ctx, `SELECT id FROM authors LIMIT 1`).Scan(&authorID); err != nil {
					return err
				}
				if _, err := tx.Exec(ctx, `INSERT INTO books (title, pages, author) VALUES ('w', 1, $1)`, authorID); err != nil {
					return err
				}
			}
			return nil
		}); err != nil {
			t.Fatalf("escrita sem projeção: %v", err)
		}
		return writeIters
	})

	writeWithProjection := timeRepeated(t, cqrsBenchRepeats, func() int {
		if err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
			for i := 0; i < writeIters; i++ {
				var authorID int
				if err := tx.QueryRow(ctx, `SELECT id FROM authors LIMIT 1`).Scan(&authorID); err != nil {
					return err
				}
				if _, err := tx.Exec(ctx, `INSERT INTO books (title, pages, author) VALUES ('w', 1, $1)`, authorID); err != nil {
					return err
				}
				if _, err := tx.Exec(ctx, `UPDATE author_book_counts SET book_count = book_count + 1 WHERE author_id = $1`, authorID); err != nil {
					return err
				}
			}
			return nil
		}); err != nil {
			t.Fatalf("escrita com manutenção de projeção: %v", err)
		}
		return writeIters
	})

	perInsertNoProjection := writeNoProjection.median / writeIters
	perInsertWithProjection := writeWithProjection.median / writeIters
	t.Logf("  escrita SEM manutenção de projeção : %s/insert (mediana de %d)", perInsertNoProjection, writeIters)
	t.Logf("  escrita COM manutenção de projeção : %s/insert (mediana de %d)", perInsertWithProjection, writeIters)
	t.Logf("  overhead de escrita da projeção: %.1fx", float64(perInsertWithProjection)/float64(perInsertNoProjection))
}

type benchResult struct {
	rows             int
	min, median, max time.Duration
}

func timeRepeated(t *testing.T, repeats int, fn func() int) benchResult {
	t.Helper()
	samples := make([]time.Duration, repeats)
	var rows int
	for i := 0; i < repeats; i++ {
		start := time.Now()
		rows = fn()
		samples[i] = time.Since(start)
	}
	min, max := samples[0], samples[0]
	for _, s := range samples {
		if s < min {
			min = s
		}
		if s > max {
			max = s
		}
	}
	// mediana simples (repeats é pequeno e ímpar por convenção — 5)
	sorted := append([]time.Duration(nil), samples...)
	for i := 1; i < len(sorted); i++ {
		for j := i; j > 0 && sorted[j-1] > sorted[j]; j-- {
			sorted[j-1], sorted[j] = sorted[j], sorted[j-1]
		}
	}
	return benchResult{rows: rows, min: min, median: sorted[len(sorted)/2], max: max}
}
