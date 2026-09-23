package views

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/vjuliani/saltcorn/migracao/backend/internal/identity"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/metadata"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/platform/database"
)

const sqlstateUniqueViolation = "23505"

func classifyPgError(err error) error {
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		return err
	}
	if pgErr.Code == sqlstateUniqueViolation {
		return fmt.Errorf("%w: %s", ErrDuplicateName, pgErr.ConstraintName)
	}
	return err
}

// requireAdmin é a autorização de escrita de todo comando deste pacote —
// só admin cria/edita/publica views (mesma regra de
// internal/metadata: estrutura de uma aplicação não é editável por um
// papel menor).
func requireAdmin(actorRole identity.RoleID) error {
	if !identity.CanWrite(actorRole, identity.RoleAdmin) {
		return ErrNotAuthorized
	}
	return nil
}

func scanView(row pgx.Row) (View, error) {
	var v View
	var minRole int
	var configJSON []byte
	err := row.Scan(&v.ID, &v.Name, &v.TableID, &v.Template, &minRole, &configJSON, &v.Version)
	if err != nil {
		return View{}, err
	}
	v.MinRole = identity.RoleID(minRole)
	v.Configuration = map[string]any{}
	if len(configJSON) > 0 {
		if err := json.Unmarshal(configJSON, &v.Configuration); err != nil {
			return View{}, fmt.Errorf("views: decodificar configuration: %w", err)
		}
	}
	return v, nil
}

const viewColumns = `id, name, table_id, template, min_role, configuration, xmin::text AS "_version"`

// CreateView grava uma view nova — MinRole ausente (zero-value) vira
// identity.RoleAdmin (ver ViewOptions), nunca publicada por omissão. Se o
// chamador pedir MinRole != RoleAdmin já na criação (publicar direto, sem
// passar por um rascunho admin-only primeiro), o mesmo bloqueio de layout
// incompatível de UpdateView (GO-020) se aplica aqui — "bloqueiam
// publicação" vale para qualquer forma de publicar, não só a mais comum.
func CreateView(ctx context.Context, tx pgx.Tx, actorRole identity.RoleID, name string, tableID int, template string, configuration map[string]any, opts ViewOptions) (View, error) {
	if err := requireAdmin(actorRole); err != nil {
		return View{}, err
	}
	minRole := opts.MinRole
	if minRole == 0 {
		minRole = identity.RoleAdmin
	}
	if minRole != identity.RoleAdmin {
		fields, err := metadata.ListFields(ctx, database.AsTx(tx), tableID)
		if err != nil {
			return View{}, err
		}
		if err := ClassifyView(View{Template: template, Configuration: configuration}, fields); err != nil {
			return View{}, err
		}
	}
	configJSON, err := json.Marshal(configuration)
	if err != nil {
		return View{}, fmt.Errorf("views: codificar configuration: %w", err)
	}

	row := tx.QueryRow(ctx, fmt.Sprintf(`
		INSERT INTO _sc_views (name, table_id, template, min_role, configuration)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING %s
	`, viewColumns), name, tableID, template, int(minRole), configJSON)

	v, err := scanView(row)
	if err != nil {
		return View{}, classifyPgError(err)
	}
	return v, nil
}

// GetView busca uma view por ID, checando identity.CanRead(actorRole,
// view.MinRole) — uma view não publicada (MinRole = RoleAdmin) só é
// visível para um ator admin, exatamente o mecanismo que "publica com
// dois papéis" (critério de aceite) exercita.
func GetView(ctx context.Context, tx pgx.Tx, actorRole identity.RoleID, id int) (View, error) {
	row := tx.QueryRow(ctx, fmt.Sprintf(`SELECT %s FROM _sc_views WHERE id = $1`, viewColumns), id)
	v, err := scanView(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return View{}, ErrViewNotFound
		}
		return View{}, err
	}
	if !identity.CanRead(actorRole, v.MinRole) {
		return View{}, ErrNotAuthorized
	}
	return v, nil
}

// GetViewByName busca uma view pelo NOME — necessário para resolver uma
// referência textual a outra view (GO-051: o nó de layout `type: "view"`
// de uma view aninhada, e os campos `show_view`/`view_to_create` do
// viewtemplate Feed guardam o NOME da view referenciada, nunca o id).
// Mesma checagem de MinRole de GetView — uma view aninhada/embutida não
// publicada permanece invisível a um ator sem papel suficiente, mesmo
// que a view PAI seja visível.
func GetViewByName(ctx context.Context, tx pgx.Tx, actorRole identity.RoleID, name string) (View, error) {
	row := tx.QueryRow(ctx, fmt.Sprintf(`SELECT %s FROM _sc_views WHERE name = $1`, viewColumns), name)
	v, err := scanView(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return View{}, ErrViewNotFound
		}
		return View{}, err
	}
	if !identity.CanRead(actorRole, v.MinRole) {
		return View{}, ErrNotAuthorized
	}
	return v, nil
}

// ListViews lista views cujo table_id bate com tableID (0 = todas),
// filtrando pelas que o ator pode ler — nunca revela a existência de uma
// view não publicada a um ator sem papel suficiente (mesmo espírito do
// filtro de autorização em internal/records.Rows).
func ListViews(ctx context.Context, tx pgx.Tx, actorRole identity.RoleID, tableID int) ([]View, error) {
	var rows pgx.Rows
	var err error
	if tableID != 0 {
		rows, err = tx.Query(ctx, fmt.Sprintf(`SELECT %s FROM _sc_views WHERE table_id = $1 ORDER BY id`, viewColumns), tableID)
	} else {
		rows, err = tx.Query(ctx, fmt.Sprintf(`SELECT %s FROM _sc_views ORDER BY id`, viewColumns))
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []View
	for rows.Next() {
		v, err := scanView(rows)
		if err != nil {
			return nil, err
		}
		if identity.CanRead(actorRole, v.MinRole) {
			result = append(result, v)
		}
	}
	return result, rows.Err()
}

// UpdateView atualiza configuration/template/min_role de uma view,
// exigindo expectedVersion (o "_version"/xmin de uma leitura anterior)
// para controle de concorrência otimista — mesmo mecanismo (e mesma
// garantia) de internal/records.UpdateRecord (GO-013): se a view mudou
// desde a leitura, ErrVersionConflict, nunca uma sobrescrita silenciosa.
// "Publicar" é uma chamada desta função baixando MinRole (ex.: para
// identity.RolePublic) — não um mecanismo separado. Desde GO-020, publicar
// (deixar minRole != identity.RoleAdmin) exige que o layout resultante
// seja executável pelo runtime novo (ver render.go, ClassifyView) —
// *UnsupportedLayoutError se não for.
type ViewUpdate struct {
	Configuration *map[string]any
	Template      *string
	MinRole       *identity.RoleID
}

func UpdateView(ctx context.Context, tx pgx.Tx, actorRole identity.RoleID, id int, expectedVersion string, update ViewUpdate) (View, error) {
	if err := requireAdmin(actorRole); err != nil {
		return View{}, err
	}

	current, err := getViewForWrite(ctx, tx, id)
	if err != nil {
		return View{}, err
	}

	configuration := current.Configuration
	if update.Configuration != nil {
		configuration = *update.Configuration
	}
	template := current.Template
	if update.Template != nil {
		template = *update.Template
	}
	minRole := current.MinRole
	if update.MinRole != nil {
		minRole = *update.MinRole
	}

	// "Publicar" (GO-019) é justamente isto: o resultado final não fica
	// admin-only. Nesse caso — e só nesse caso, para não travar um admin
	// iterando numa view ainda não publicada — GO-020 exige que o layout
	// seja executável pelo runtime novo antes de permitir a escrita.
	// Nunca uma sobrescrita silenciosa de uma view incompatível "meio
	// publicada": ou passa por inteiro, ou falha com o motivo específico.
	if minRole != identity.RoleAdmin {
		fields, err := metadata.ListFields(ctx, database.AsTx(tx), current.TableID)
		if err != nil {
			return View{}, err
		}
		if err := ClassifyView(View{Template: template, Configuration: configuration}, fields); err != nil {
			return View{}, err
		}
	}

	configJSON, err := json.Marshal(configuration)
	if err != nil {
		return View{}, fmt.Errorf("views: codificar configuration: %w", err)
	}

	row := tx.QueryRow(ctx, fmt.Sprintf(`
		UPDATE _sc_views SET template = $1, min_role = $2, configuration = $3
		WHERE id = $4 AND xmin::text = $5
		RETURNING %s
	`, viewColumns), template, int(minRole), configJSON, id, expectedVersion)

	v, err := scanView(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return View{}, conflictOrNotFound(ctx, tx, id)
		}
		return View{}, classifyPgError(err)
	}
	return v, nil
}

// getViewForWrite busca a view SEM checar MinRole de leitura — quem já
// passou requireAdmin pode ler qualquer view para editá-la, mesmo uma
// ainda não publicada (só GetView, o caminho de leitura pública, filtra
// por MinRole).
func getViewForWrite(ctx context.Context, tx pgx.Tx, id int) (View, error) {
	row := tx.QueryRow(ctx, fmt.Sprintf(`SELECT %s FROM _sc_views WHERE id = $1`, viewColumns), id)
	v, err := scanView(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return View{}, ErrViewNotFound
		}
		return View{}, err
	}
	return v, nil
}

// conflictOrNotFound distingue "id não existe" de "existe, mas mudou
// desde a leitura" depois de um UPDATE condicional afetar zero linhas —
// mesmo padrão de internal/records.
func conflictOrNotFound(ctx context.Context, tx pgx.Tx, id int) error {
	var exists bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM _sc_views WHERE id = $1)`, id).Scan(&exists); err != nil {
		return err
	}
	if !exists {
		return ErrViewNotFound
	}
	return ErrVersionConflict
}
