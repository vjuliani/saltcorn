// Viewtemplate "Feed" (GO-051) — o mecanismo é literalmente "para cada
// linha da tabela, renderizar a view Show indicada (`show_view`), dentro
// de um card" (confirmado lendo `guitar_feed` do pack.json real do
// piloto guitars: `show_view: "show_guitar"`) — o MESMO problema de
// renderização de view aninhada de edit.go, só que iterando sobre TODAS
// as linhas (paginadas) em vez de uma relação filtrada por um registro
// pai. Reaproveita CompileShowPlan por linha em vez de duplicar a
// classificação de show.go.
package views

import (
	"context"
	"errors"
	"fmt"

	"github.com/vjuliani/saltcorn/migracao/backend/internal/identity"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/metadata"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/platform/database"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/records"
)

// FeedCard é uma linha renderizada pela view Show configurada em
// `show_view` — RecordID repetido (já está em Show.RecordID) só por
// conveniência de quem itera Cards sem precisar entrar em Show.
type FeedCard struct {
	RecordID int
	Show     ShowPlan
}

// FeedPlan é o DTO de renderização de uma view "Feed" — os cards da
// página atual (paginação por limit/offset, mesma convenção de
// CompileListPlan) e, quando configurado, a view de criação
// (`view_to_create`, ex.: "create_guitar") para o botão "+" do feed.
type FeedPlan struct {
	ViewID           int
	Table            string
	Cards            []FeedCard
	ViewToCreateID   int // 0 = nenhuma view de criação configurada/visível
	ViewToCreateName string
}

// CompileFeedPlan monta o FeedPlan de uma view Feed compatível — limit/
// offset e o retorno hasMore espelham exatamente CompileListPlan.
func CompileFeedPlanTx(ctx context.Context, tx database.Tx, actorRole identity.RoleID, viewID int, limit, offset int) (*FeedPlan, bool, error) {
	v, err := GetViewTx(ctx, tx, actorRole, viewID)
	if err != nil {
		return nil, false, err
	}
	if v.Template != "Feed" {
		return nil, false, &UnsupportedLayoutError{Reason: fmt.Sprintf("template %q não é \"Feed\"", v.Template)}
	}
	table, err := metadata.GetTableByID(ctx, tx, v.TableID)
	if err != nil {
		return nil, false, err
	}

	showViewName, _ := v.Configuration["show_view"].(string)
	if showViewName == "" {
		return nil, false, &UnsupportedLayoutError{Reason: "configuration.show_view ausente"}
	}
	showView, err := GetViewByNameTx(ctx, tx, actorRole, showViewName)
	if err != nil {
		return nil, false, err
	}
	if showView.Template != "Show" {
		return nil, false, &UnsupportedLayoutError{Reason: fmt.Sprintf("show_view %q usa template %q — só \"Show\" é suportado", showViewName, showView.Template)}
	}
	showTable, err := metadata.GetTableByID(ctx, tx, showView.TableID)
	if err != nil {
		return nil, false, err
	}
	if showTable.Name != table.Name {
		return nil, false, &UnsupportedLayoutError{Reason: fmt.Sprintf("show_view %q é da tabela %q, mas esta view Feed é da tabela %q", showViewName, showTable.Name, table.Name)}
	}

	orderField := "id"
	if of, ok := v.Configuration["order_field"].(string); ok && of != "" {
		orderField = of
	}
	descending, _ := v.Configuration["descending"].(bool)

	rows, err := records.RowsTx(ctx, tx, actorRole, records.Query{
		Table:   table.Name,
		OrderBy: []records.OrderTerm{{Field: orderField, Desc: descending}},
		Limit:   limit + 1,
		Offset:  offset,
	})
	if err != nil {
		return nil, false, err
	}
	hasMore := len(rows) > limit
	if hasMore {
		rows = rows[:limit]
	}

	cards := make([]FeedCard, 0, len(rows))
	for _, row := range rows {
		recordID := idAsInt(row["id"])
		showPlan, err := CompileShowPlanTx(ctx, tx, actorRole, showView.ID, recordID)
		if err != nil {
			return nil, false, err
		}
		cards = append(cards, FeedCard{RecordID: recordID, Show: *showPlan})
	}

	viewToCreateID := 0
	viewToCreateName, _ := v.Configuration["view_to_create"].(string)
	if viewToCreateName != "" {
		createView, err := GetViewByNameTx(ctx, tx, actorRole, viewToCreateName)
		switch {
		case err == nil:
			viewToCreateID = createView.ID
		case errors.Is(err, ErrViewNotFound), errors.Is(err, ErrNotAuthorized):
			// Melhor esforço: uma view de criação ausente/sem permissão
			// para este ator não deveria impedir o feed inteiro de
			// renderizar — só o botão "+" fica ausente (ViewToCreateID
			// == 0), nunca um erro de página inteira por um botão
			// secundário.
		default:
			return nil, false, err
		}
	}

	return &FeedPlan{
		ViewID:           v.ID,
		Table:            table.Name,
		Cards:            cards,
		ViewToCreateID:   viewToCreateID,
		ViewToCreateName: viewToCreateName,
	}, hasMore, nil
}
