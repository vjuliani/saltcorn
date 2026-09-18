// Package library persiste componentes reutilizáveis de layout do builder
// (GO-027) — o equivalente reduzido de models/library.ts do legado
// (_sc_library: name/icon/layout). Deliberadamente NÃO resolve
// referências de layout em runtime (Library.resolveSegment do legado,
// substituir um segmento {type:"library"} pela definição real durante a
// renderização de uma view) — isso é responsabilidade de GO-020
// (internal/views/render.go), não de import/export. Este pacote só
// guarda e devolve o JSON de layout como está, opaco, exatamente como o
// legado faz em library_pack (sem nenhuma transformação de referência).
package library

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/jackc/pgx/v5"
)

const createLibraryTableSQL = `
CREATE TABLE IF NOT EXISTS _sc_library (
	id serial PRIMARY KEY,
	name text NOT NULL UNIQUE,
	icon text,
	layout jsonb NOT NULL DEFAULT '{}',
	created_at timestamptz NOT NULL DEFAULT now()
)`

// EnsureSchema cria o catálogo de biblioteca, idempotente — chamar dentro
// de db.WithTenant, uma vez por tenant.
func EnsureSchema(ctx context.Context, tx pgx.Tx) error {
	_, err := tx.Exec(ctx, createLibraryTableSQL)
	return err
}

// Item é uma entrada de _sc_library.
type Item struct {
	ID     int
	Name   string
	Icon   string
	Layout map[string]any
}

// CreateOrReplace insere um item de biblioteca, ou substitui o layout/ícone
// de um item já existente com o mesmo nome — idempotente por nome, mesmo
// espírito de import de pack precisar rodar mais de uma vez sem duplicar
// (reinstalação do mesmo pack).
func CreateOrReplace(ctx context.Context, tx pgx.Tx, name, icon string, layout map[string]any) (Item, error) {
	if layout == nil {
		layout = map[string]any{}
	}
	layoutJSON, err := json.Marshal(layout)
	if err != nil {
		return Item{}, fmt.Errorf("library: codificar layout: %w", err)
	}

	item := Item{Name: name, Icon: icon, Layout: layout}
	err = tx.QueryRow(ctx,
		`INSERT INTO _sc_library (name, icon, layout) VALUES ($1, $2, $3)
		 ON CONFLICT (name) DO UPDATE SET icon = EXCLUDED.icon, layout = EXCLUDED.layout
		 RETURNING id`,
		name, nullableText(icon), layoutJSON,
	).Scan(&item.ID)
	if err != nil {
		return Item{}, err
	}
	return item, nil
}

// ListAll lê todos os itens de biblioteca do tenant, em ordem de criação.
func ListAll(ctx context.Context, tx pgx.Tx) ([]Item, error) {
	rows, err := tx.Query(ctx, `SELECT id, name, COALESCE(icon, ''), layout FROM _sc_library ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Item
	for rows.Next() {
		var it Item
		var layoutJSON []byte
		if err := rows.Scan(&it.ID, &it.Name, &it.Icon, &layoutJSON); err != nil {
			return nil, err
		}
		it.Layout = map[string]any{}
		if len(layoutJSON) > 0 {
			if err := json.Unmarshal(layoutJSON, &it.Layout); err != nil {
				return nil, fmt.Errorf("library: decodificar layout de %q: %w", it.Name, err)
			}
		}
		out = append(out, it)
	}
	return out, rows.Err()
}

func nullableText(s string) any {
	if s == "" {
		return nil
	}
	return s
}
