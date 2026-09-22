// Testa CollectMaps isoladamente — exige Postgres real, pula (t.Skip) se
// SALTCORN_GO_TEST_DATABASE_URL não estiver definida, mesmo padrão de
// database_test.go. Não depende dos schemas fixos acme/beta/probe (só
// testDSN/openTestDB) — cria seu próprio schema/tabela descartável.
package database

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/vjuliani/saltcorn/migracao/backend/internal/platform/tenancy"
)

// TestCollectMaps_NormalizesTimeToUTC é a regressão do achado real de
// GO-042: pgx decodifica `timestamptz` para `time.Time` no fuso
// time.Local do PROCESSO CLIENTE (nunca UTC, nunca o fuso da sessão
// Postgres) — um valor gravado como meia-noite UTC voltava, num processo
// rodando num fuso negativo (ex.: America/Sao_Paulo, UTC-3), como 21h do
// dia ANTERIOR. Fixa time.Local para um fuso negativo conhecido DENTRO
// do teste (determinístico, independente de em que fuso o CI realmente
// roda) para reproduzir o bug de forma confiável.
func TestCollectMaps_NormalizesTimeToUTC(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	tenant := tenancy.Tenant("tx_test_utc_normalize")

	saoPaulo, err := time.LoadLocation("America/Sao_Paulo")
	if err != nil {
		t.Skipf("fuso America/Sao_Paulo indisponível neste ambiente: %v", err)
	}
	original := time.Local
	time.Local = saoPaulo
	t.Cleanup(func() { time.Local = original })

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

	// Meia-noite UTC — o mesmo valor que internal/records.coerceJSONValue
	// produz para um campo `date` submetido via HTTP.
	midnightUTC := time.Date(2024, time.March, 20, 0, 0, 0, 0, time.UTC)

	if err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `CREATE TABLE probe_ts (id serial primary key, marked_at timestamptz not null)`); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `INSERT INTO probe_ts (marked_at) VALUES ($1)`, midnightUTC)
		return err
	}); err != nil {
		t.Fatalf("gravar valor de teste: %v", err)
	}

	var rows []map[string]any
	if err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		r, err := tx.Query(ctx, `SELECT marked_at FROM probe_ts`)
		if err != nil {
			return err
		}
		rows, err = CollectMaps(pgxRows{rows: r})
		return err
	}); err != nil {
		t.Fatalf("ler valor de teste: %v", err)
	}

	if len(rows) != 1 {
		t.Fatalf("linhas = %d, esperado 1", len(rows))
	}
	got, ok := rows[0]["marked_at"].(time.Time)
	if !ok {
		t.Fatalf("marked_at = %T, esperado time.Time", rows[0]["marked_at"])
	}
	if got.Location() != time.UTC {
		t.Errorf("Location() = %v, esperado UTC — CollectMaps deveria normalizar, nunca devolver o fuso local do processo", got.Location())
	}
	if !got.Equal(midnightUTC) {
		t.Errorf("valor = %v, esperado o mesmo instante que %v", got, midnightUTC)
	}
	// A prova concreta do bug: sem a normalização, Day() teria voltado 19
	// (a mesma instância às 21h do dia anterior no fuso -03:00), nunca 20.
	if got.Day() != 20 || got.Month() != time.March || got.Year() != 2024 {
		t.Errorf("data = %s, esperado 2024-03-20 — um campo de data não pode mudar de dia por causa do fuso do processo servidor", got.Format("2006-01-02"))
	}
}
