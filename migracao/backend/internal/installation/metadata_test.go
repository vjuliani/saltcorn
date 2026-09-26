package installation

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/platform/database"
)

func TestMetadataWriteListLatest(t *testing.T) {
	for _, driver := range []string{"sqlite", "postgres"} {
		t.Run(driver, func(t *testing.T) {
			ctx := context.Background()
			root, c := newConfig(t, driver)

			must(t, transaction(ctx, root, c, func(tx database.Tx, _ pgx.Tx) error {
				if _, err := WriteMetadata(ctx, tx, "custom_event", "audit", nil, map[string]any{"seq": 1}); err != nil {
					return err
				}
				_, err := WriteMetadata(ctx, tx, "custom_event", "audit", nil, map[string]any{"seq": 2})
				return err
			}))

			must(t, transaction(ctx, root, c, func(tx database.Tx, _ pgx.Tx) error {
				entries, err := ListMetadata(ctx, tx, "custom_event")
				if err != nil {
					return err
				}
				if len(entries) != 2 {
					t.Fatalf("esperado 2 entradas, obtido %d", len(entries))
				}
				var newest map[string]any
				must(t, json.Unmarshal(entries[0].Body, &newest))
				if newest["seq"] != float64(2) {
					t.Fatalf("ordem incorreta (mais recente primeiro): %v", newest)
				}

				latest, ok, err := LatestMetadata(ctx, tx, "custom_event")
				if err != nil {
					return err
				}
				if !ok {
					t.Fatal("LatestMetadata: esperado ok=true")
				}
				var latestBody map[string]any
				must(t, json.Unmarshal(latest.Body, &latestBody))
				if latestBody["seq"] != float64(2) {
					t.Fatalf("LatestMetadata devolveu entrada errada: %v", latestBody)
				}

				_, ok, err = LatestMetadata(ctx, tx, "nunca_gravado")
				if err != nil {
					return err
				}
				if ok {
					t.Fatal("LatestMetadata: esperado ok=false para name inexistente")
				}
				return nil
			}))

			// Migrate() já escreveu pelo menos uma entrada "core_version" em
			// newConfig — confirma o caso de uso real do Aceite (versão do
			// core rastreada em _sc_metadata).
			must(t, transaction(ctx, root, c, func(tx database.Tx, _ pgx.Tx) error {
				entry, ok, err := LatestMetadata(ctx, tx, "core_version")
				if err != nil {
					return err
				}
				if !ok {
					t.Fatal("esperado ao menos uma entrada core_version escrita por Migrate")
				}
				var body map[string]any
				must(t, json.Unmarshal(entry.Body, &body))
				if body["release"] != Release {
					t.Fatalf("release incorreto em core_version: %v", body)
				}
				return nil
			}))
		})
	}
}
