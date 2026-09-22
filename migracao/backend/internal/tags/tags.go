// Package tags implementa o sistema de tags (GO-045, `models/tag.ts` +
// `models/tag_entry.ts`) — CRUD de tag e associação tag↔entidade, usado
// principalmente para filtrar um export de Pack (GO-027) a um subconjunto
// da aplicação.
//
// Divergência deliberada do legado, documentada (não um esquecimento):
// TagEntry aqui só referencia Table/View, nunca Page/Trigger.
//   - Page: páginas nunca foram portadas para Go (achado repetido desde
//     GO-001) — não há o que uma TagEntry de página referenciaria aqui.
//   - Trigger: `internal/triggers.Trigger` (GO-024) nunca modelou um
//     campo `Name` — só TableID+When+Action (achado desta tarefa, ao
//     tentar espelhar `tag_pack`'s `trigger.name`, que o legado usa para
//     tornar a referência portável entre tenants). Toda referência de Pack
//     é por NOME (nunca por ID interno, ver internal/pack/types.go) —
//     sem nome, uma TagEntry de trigger não seria portável, quebrando essa
//     invariante para o único tipo de entidade envolvido. Candidata a
//     tarefa futura se/quando triggers ganharem nome.
package tags

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/vjuliani/saltcorn/migracao/backend/internal/identity"
)

// ErrTagNotFound, ErrEntryNotFound, ErrNotAuthorized, ErrInvalidEntry são
// os sentinelas deste pacote — nunca a mensagem crua de um erro Go.
var (
	ErrTagNotFound   = errors.New("tags: tag não encontrada")
	ErrEntryNotFound = errors.New("tags: associação não encontrada")
	ErrNotAuthorized = errors.New("tags: ator não tem papel suficiente para esta operação")
	// ErrInvalidEntry é devolvido quando uma TagEntryRef não referencia
	// exatamente UMA entidade (nem zero, nem as duas) — mesma disciplina
	// de "nunca ambíguo" já aplicada em todo o resto desta migração.
	ErrInvalidEntry = errors.New("tags: uma associação precisa referenciar exatamente uma entidade (table_id XOR view_id)")
)

const createTagsSQL = `
CREATE TABLE IF NOT EXISTS _sc_tags (
	id serial PRIMARY KEY,
	name text NOT NULL UNIQUE,
	created_at timestamptz NOT NULL DEFAULT now()
)`

const createTagEntriesSQL = `
CREATE TABLE IF NOT EXISTS _sc_tag_entries (
	id serial PRIMARY KEY,
	tag_id int NOT NULL REFERENCES _sc_tags(id) ON DELETE CASCADE,
	table_id int REFERENCES _sc_tables(id) ON DELETE CASCADE,
	view_id int REFERENCES _sc_views(id) ON DELETE CASCADE
)`

// createTagEntriesTableUniqueSQL/createTagEntriesViewUniqueSQL — achado
// real: uma UNIQUE (ou índice único) composta sobre (tag_id, table_id,
// view_id) NUNCA detecta conflito quando table_id/view_id é NULL (regra
// SQL padrão: NULL não é igual a NULL na checagem de unicidade) — exatamente
// o caso comum aqui, já que TagEntryRef.validate() garante que SEMPRE um
// dos dois é nil. AddEntry chamado duas vezes com a MESMA TagEntryRef
// simplesmente inseria uma segunda linha idêntica (pego pelo teste desta
// task, TestAddEntry_TableAndView_IdempotentAndListable). Corrigido com
// dois índices únicos PARCIAIS (WHERE ... IS NOT NULL) — cada um só
// enxerga as linhas onde a coluna correspondente é preenchida, então a
// checagem de unicidade nunca esbarra em NULL.
const createTagEntriesTableUniqueSQL = `
CREATE UNIQUE INDEX IF NOT EXISTS _sc_tag_entries_table_uniq
	ON _sc_tag_entries (tag_id, table_id) WHERE table_id IS NOT NULL`

const createTagEntriesViewUniqueSQL = `
CREATE UNIQUE INDEX IF NOT EXISTS _sc_tag_entries_view_uniq
	ON _sc_tag_entries (tag_id, view_id) WHERE view_id IS NOT NULL`

// EnsureSchema cria o catálogo de tags, idempotente — chamar dentro de
// db.WithTenant, uma vez por tenant. Depende de _sc_tables (internal/
// metadata) e _sc_views (internal/views) já existirem (FOREIGN KEY) —
// EnsureSchema destes dois pacotes deve rodar ANTES deste, mesma ordem já
// documentada em internal/installation/migrate.go.
func EnsureSchema(ctx context.Context, tx pgx.Tx) error {
	if _, err := tx.Exec(ctx, createTagsSQL); err != nil {
		return fmt.Errorf("tags: criar _sc_tags: %w", err)
	}
	if _, err := tx.Exec(ctx, createTagEntriesSQL); err != nil {
		return fmt.Errorf("tags: criar _sc_tag_entries: %w", err)
	}
	if _, err := tx.Exec(ctx, createTagEntriesTableUniqueSQL); err != nil {
		return fmt.Errorf("tags: criar índice único de table_id: %w", err)
	}
	if _, err := tx.Exec(ctx, createTagEntriesViewUniqueSQL); err != nil {
		return fmt.Errorf("tags: criar índice único de view_id: %w", err)
	}
	return nil
}

func requireAdmin(actorRole identity.RoleID) error {
	if !identity.CanWrite(actorRole, identity.RoleAdmin) {
		return ErrNotAuthorized
	}
	return nil
}

// Tag é uma entrada de _sc_tags.
type Tag struct {
	ID   int
	Name string
}

// TagEntry é uma associação tag↔entidade — exatamente um de TableID/ViewID
// é não-nil (nunca os dois, nunca nenhum, ver ErrInvalidEntry).
type TagEntry struct {
	ID      int
	TagID   int
	TableID *int
	ViewID  *int
}

// TagEntryRef é a entrada de AddEntry — mesmo shape de TagEntry menos
// ID/TagID (que AddEntry já sabe pelo parâmetro/preenche).
type TagEntryRef struct {
	TableID *int
	ViewID  *int
}

func (r TagEntryRef) validate() error {
	count := 0
	if r.TableID != nil {
		count++
	}
	if r.ViewID != nil {
		count++
	}
	if count != 1 {
		return ErrInvalidEntry
	}
	return nil
}

// CreateTag cria uma tag — idempotente por nome (mesma convenção de
// metadata.CreateTable): uma tag já existente com o mesmo nome é
// devolvida sem erro, nenhuma mudança real.
func CreateTag(ctx context.Context, tx pgx.Tx, actorRole identity.RoleID, name string) (*Tag, error) {
	if err := requireAdmin(actorRole); err != nil {
		return nil, err
	}
	if name == "" {
		return nil, fmt.Errorf("tags: nome vazio")
	}
	if existing, err := GetTag(ctx, tx, name); err == nil {
		return existing, nil
	} else if !errors.Is(err, ErrTagNotFound) {
		return nil, err
	}
	t := &Tag{Name: name}
	err := tx.QueryRow(ctx, `INSERT INTO _sc_tags (name) VALUES ($1) RETURNING id`, name).Scan(&t.ID)
	if err != nil {
		return nil, fmt.Errorf("tags: inserir tag: %w", err)
	}
	return t, nil
}

// GetTag busca uma tag pelo nome.
func GetTag(ctx context.Context, tx pgx.Tx, name string) (*Tag, error) {
	t := &Tag{}
	err := tx.QueryRow(ctx, `SELECT id, name FROM _sc_tags WHERE name = $1`, name).Scan(&t.ID, &t.Name)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrTagNotFound
		}
		return nil, err
	}
	return t, nil
}

// GetTagByID busca uma tag pelo ID.
func GetTagByID(ctx context.Context, tx pgx.Tx, id int) (*Tag, error) {
	t := &Tag{}
	err := tx.QueryRow(ctx, `SELECT id, name FROM _sc_tags WHERE id = $1`, id).Scan(&t.ID, &t.Name)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrTagNotFound
		}
		return nil, err
	}
	return t, nil
}

// ListTags lista todas as tags, em ordem alfabética (mesmo critério de
// Tag.find do legado, orderBy:"name").
func ListTags(ctx context.Context, tx pgx.Tx) ([]Tag, error) {
	rows, err := tx.Query(ctx, `SELECT id, name FROM _sc_tags ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Tag
	for rows.Next() {
		var t Tag
		if err := rows.Scan(&t.ID, &t.Name); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// DeleteTag remove uma tag e suas associações (ON DELETE CASCADE) —
// idempotente: remover uma tag que não existe é um no-op bem-sucedido.
func DeleteTag(ctx context.Context, tx pgx.Tx, actorRole identity.RoleID, id int) error {
	if err := requireAdmin(actorRole); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, `DELETE FROM _sc_tags WHERE id = $1`, id)
	return err
}

// AddEntry associa tagID a UMA entidade (ref) — idempotente: a MESMA
// associação (tag_id, table_id, view_id) já existente é devolvida sem
// duplicar (UNIQUE em _sc_tag_entries).
func AddEntry(ctx context.Context, tx pgx.Tx, actorRole identity.RoleID, tagID int, ref TagEntryRef) (*TagEntry, error) {
	if err := requireAdmin(actorRole); err != nil {
		return nil, err
	}
	if err := ref.validate(); err != nil {
		return nil, err
	}
	if _, err := GetTagByID(ctx, tx, tagID); err != nil {
		return nil, err
	}

	e := &TagEntry{TagID: tagID, TableID: ref.TableID, ViewID: ref.ViewID}
	var err error
	if ref.TableID != nil {
		err = tx.QueryRow(ctx,
			`INSERT INTO _sc_tag_entries (tag_id, table_id) VALUES ($1, $2)
			 ON CONFLICT (tag_id, table_id) WHERE table_id IS NOT NULL DO UPDATE SET tag_id = EXCLUDED.tag_id
			 RETURNING id`,
			tagID, *ref.TableID,
		).Scan(&e.ID)
	} else {
		err = tx.QueryRow(ctx,
			`INSERT INTO _sc_tag_entries (tag_id, view_id) VALUES ($1, $2)
			 ON CONFLICT (tag_id, view_id) WHERE view_id IS NOT NULL DO UPDATE SET tag_id = EXCLUDED.tag_id
			 RETURNING id`,
			tagID, *ref.ViewID,
		).Scan(&e.ID)
	}
	if err != nil {
		return nil, fmt.Errorf("tags: inserir associação: %w", err)
	}
	return e, nil
}

// RemoveEntry remove uma associação — idempotente (remover uma associação
// que não existe é um no-op bem-sucedido).
func RemoveEntry(ctx context.Context, tx pgx.Tx, actorRole identity.RoleID, entryID int) error {
	if err := requireAdmin(actorRole); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, `DELETE FROM _sc_tag_entries WHERE id = $1`, entryID)
	return err
}

// ListEntries lista as associações de tagID.
func ListEntries(ctx context.Context, tx pgx.Tx, tagID int) ([]TagEntry, error) {
	rows, err := tx.Query(ctx, `SELECT id, tag_id, table_id, view_id FROM _sc_tag_entries WHERE tag_id = $1 ORDER BY id`, tagID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []TagEntry
	for rows.Next() {
		var e TagEntry
		if err := rows.Scan(&e.ID, &e.TagID, &e.TableID, &e.ViewID); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}
