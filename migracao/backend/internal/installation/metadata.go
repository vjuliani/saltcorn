// _sc_metadata (GO-046) — equivalente reduzido de models/metadata.ts do
// legado: um armazém key/value/blob genérico, de uso interno da própria
// instalação (nunca exposto a um tenant de aplicação — vive só dentro de
// internal/installation, o mesmo escopo de _sc_config/_sc_plugin_versions
// já existentes aqui desde GO-032). NÃO confundir com dois conceitos
// homônimos já existentes no backend: `internal/metadata` (catálogo de
// tabelas/campos de aplicação, um pacote totalmente diferente) e
// `_sc_metadata_version` (contador de invalidação de cache do catálogo,
// dentro de internal/metadata) — nenhum dos dois tem relação com esta
// tabela.
//
// Uso real, os dois casos que o legado também cobre: (1) versão do core
// registrada a cada `migrate`/`setup` bem-sucedido (name="core_version");
// (2) payload/metadado de cada backup bem-sucedido (name="backup",
// gravado por Backup()). Estendido nesta mesma tarefa para um terceiro
// uso real: trilha de auditoria de upgrade de plugin (name="plugin_upgrade",
// ver plugins.go) — o legado usa uma tabela de EventLog dedicada para
// isso; aqui reaproveita-se o mesmo mecanismo genérico em vez de criar uma
// segunda tabela só para uma variação do mesmo padrão "id/tipo/quando/o
// que aconteceu".
package installation

import (
	"context"
	"encoding/json"
	"time"

	"github.com/vjuliani/saltcorn/migracao/backend/internal/platform/database"
)

const createMetadataTableSQL = `
CREATE TABLE _sc_metadata (
	id text PRIMARY KEY,
	name text NOT NULL,
	type text NOT NULL,
	user_id integer,
	body text NOT NULL,
	written_at text NOT NULL
)`

const createMetadataNameIndexSQL = `
CREATE INDEX idx_sc_metadata_name ON _sc_metadata (name, written_at)`

// EnsureMetadataSchema cria _sc_metadata — chamada de dentro de Migrate()
// (schema version 3), sob o mesmo gate de versão de _sc_config/
// _sc_plugin_versions: DDL SEM "IF NOT EXISTS" de propósito, para que uma
// segunda execução fora do gate de _sc_migrations falhe alto (mesma
// disciplina de defesa em profundidade já usada pelas duas tabelas
// irmãs), nunca diretamente por um chamador externo ao pacote.
func EnsureMetadataSchema(ctx context.Context, tx database.Tx) error {
	if err := tx.Exec(ctx, createMetadataTableSQL); err != nil {
		return err
	}
	return tx.Exec(ctx, createMetadataNameIndexSQL)
}

// MetadataEntry é uma linha de _sc_metadata já decodificada.
type MetadataEntry struct {
	ID        string
	Name      string
	Type      string
	UserID    *int
	Body      json.RawMessage
	WrittenAt time.Time
}

// WriteMetadata grava uma entrada NOVA (nunca sobrescreve uma anterior de
// mesmo name — cada chamada é um evento próprio, mesmo espírito de
// EventLog do legado, não um key/value substituível como _sc_config).
// value é serializado para JSON antes de gravar.
func WriteMetadata(ctx context.Context, tx database.Tx, name, typ string, userID *int, value any) (string, error) {
	body, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	id := RandomID()
	writtenAt := time.Now().UTC().Format(time.RFC3339Nano)
	if err := tx.Exec(ctx,
		`INSERT INTO _sc_metadata(id,name,type,user_id,body,written_at) VALUES ($1,$2,$3,$4,$5,$6)`,
		id, name, typ, userID, string(body), writtenAt,
	); err != nil {
		return "", err
	}
	return id, nil
}

// ListMetadata devolve todas as entradas de name, mais recente primeiro —
// usado por auditoria (ex.: histórico de upgrades de um plugin) e testes.
func ListMetadata(ctx context.Context, tx database.Tx, name string) ([]MetadataEntry, error) {
	rows, err := tx.Query(ctx,
		`SELECT id,name,type,user_id,body,written_at FROM _sc_metadata WHERE name=$1 ORDER BY written_at DESC`,
		name,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []MetadataEntry
	for rows.Next() {
		var e MetadataEntry
		var body, writtenAt string
		var userID *int
		if err := rows.Scan(&e.ID, &e.Name, &e.Type, &userID, &body, &writtenAt); err != nil {
			return nil, err
		}
		e.UserID = userID
		e.Body = json.RawMessage(body)
		t, err := time.Parse(time.RFC3339Nano, writtenAt)
		if err != nil {
			return nil, err
		}
		e.WrittenAt = t
		out = append(out, e)
	}
	return out, rows.Err()
}

// LatestMetadata devolve a entrada mais recente de name — ok=false se
// nenhuma existir (ausência é estado normal, nunca erro, mesmo espírito
// de internal/config.Get).
func LatestMetadata(ctx context.Context, tx database.Tx, name string) (entry MetadataEntry, ok bool, err error) {
	entries, err := ListMetadata(ctx, tx, name)
	if err != nil || len(entries) == 0 {
		return MetadataEntry{}, false, err
	}
	return entries[0], true, nil
}
