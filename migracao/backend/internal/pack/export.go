package pack

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/vjuliani/saltcorn/migracao/backend/internal/config"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/identity"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/library"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/metadata"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/platform/database"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/scheduler"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/tags"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/triggers"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/views"
)

// Export serializa a aplicação inteira do tenant atual (schema já
// escopado pela tx do chamador, via db.WithTenant) num Pack — sempre em
// ordem de criação (ORDER BY id em cada catálogo), para que um
// Export→Import→Export produza a MESMA ordem de volta (round-trip
// estável, ver export_test.go). plugins é informado pelo CHAMADOR (não
// existe inventário real de plugins de terceiro para Export descobrir
// sozinho, ver comentário de PluginDependency).
func Export(ctx context.Context, tx pgx.Tx, actorRole identity.RoleID, plugins []PluginDependency) (Pack, error) {
	tables, err := metadata.ListTables(ctx, database.AsTx(tx))
	if err != nil {
		return Pack{}, fmt.Errorf("pack: listar tabelas: %w", err)
	}

	var tablePacks []TablePack
	for _, t := range tables {
		fields, err := metadata.ListFields(ctx, database.AsTx(tx), t.ID)
		if err != nil {
			return Pack{}, fmt.Errorf("pack: listar campos de %q: %w", t.Name, err)
		}
		var fieldPacks []FieldPack
		for _, f := range fields {
			fp := FieldPack{Name: f.Name, Type: f.Type, Required: f.Required, Unique: f.Unique}
			if f.Type == metadata.FieldKey {
				refTable, err := metadata.GetTableByID(ctx, database.AsTx(tx), f.ReferencesTable)
				if err != nil {
					return Pack{}, fmt.Errorf("pack: resolver tabela referenciada pelo campo %q.%q: %w", t.Name, f.Name, err)
				}
				fp.References = refTable.Name
			}
			fieldPacks = append(fieldPacks, fp)
		}
		tablePacks = append(tablePacks, TablePack{
			Name: t.Name, MinRoleRead: t.MinRoleRead, MinRoleWrite: t.MinRoleWrite, Fields: fieldPacks,
		})
	}

	viewList, err := views.ListViews(ctx, tx, actorRole, 0)
	if err != nil {
		return Pack{}, fmt.Errorf("pack: listar views: %w", err)
	}
	var viewPacks []ViewPack
	for _, v := range viewList {
		table, err := metadata.GetTableByID(ctx, database.AsTx(tx), v.TableID)
		if err != nil {
			return Pack{}, fmt.Errorf("pack: resolver tabela da view %q: %w", v.Name, err)
		}
		viewPacks = append(viewPacks, ViewPack{
			Name: v.Name, TableName: table.Name, Template: v.Template, MinRole: v.MinRole, Configuration: v.Configuration,
		})
	}

	triggerList, err := triggers.ListAll(ctx, tx)
	if err != nil {
		return Pack{}, fmt.Errorf("pack: listar triggers: %w", err)
	}
	var triggerPacks []TriggerPack
	for _, tr := range triggerList {
		table, err := metadata.GetTableByID(ctx, database.AsTx(tx), tr.TableID)
		if err != nil {
			return Pack{}, fmt.Errorf("pack: resolver tabela do trigger: %w", err)
		}
		triggerPacks = append(triggerPacks, TriggerPack{
			TableName: table.Name, When: tr.When, Action: tr.Action, OnlyIf: tr.OnlyIf, AfterCommit: tr.AfterCommit,
			Configuration: tr.Configuration,
		})
	}

	scheduledList, err := scheduler.ListAll(ctx, tx)
	if err != nil {
		return Pack{}, fmt.Errorf("pack: listar triggers agendados: %w", err)
	}
	var scheduledPacks []ScheduledTriggerPack
	for _, st := range scheduledList {
		scheduledPacks = append(scheduledPacks, ScheduledTriggerPack{
			Name: st.Name, Action: st.Action, CronExpr: st.CronExpr, Timezone: st.Timezone,
		})
	}

	libraryList, err := library.ListAll(ctx, tx)
	if err != nil {
		return Pack{}, fmt.Errorf("pack: listar biblioteca: %w", err)
	}
	var libraryPacks []LibraryPack
	for _, item := range libraryList {
		libraryPacks = append(libraryPacks, LibraryPack{Name: item.Name, Icon: item.Icon, Layout: item.Layout})
	}

	cfg, err := config.ListAll(ctx, tx)
	if err != nil {
		return Pack{}, fmt.Errorf("pack: listar configuração: %w", err)
	}

	tagPacks, err := exportTagPacks(ctx, tx, actorRole)
	if err != nil {
		return Pack{}, err
	}

	return Pack{
		Version:           Version,
		Tables:            tablePacks,
		Views:             viewPacks,
		Triggers:          triggerPacks,
		ScheduledTriggers: scheduledPacks,
		Library:           libraryPacks,
		Config:            cfg,
		Plugins:           plugins,
		Tags:              tagPacks,
	}, nil
}

// ErrTagNotFound é devolvido por ExportFilteredByTag quando a tag não
// existe — nunca um Pack vazio ambíguo entre "tag sem nenhuma entidade" e
// "tag inexistente".
var ErrTagNotFound = tags.ErrTagNotFound

// ExportFilteredByTag exporta só o subconjunto da aplicação marcado com
// tagName (GO-045, critério de aceite "tags... usadas para filtrar um
// export de Pack") — Tables/Views diretamente na tag, MAIS o fechamento
// transitivo de tabelas referenciadas por FieldKey (sem isso, uma tabela
// filtrada com um campo-chave apontando para fora do filtro produziria
// um Pack que o próprio Import rejeitaria com "tabela desconhecida" — um
// Pack filtrado só é útil se for, ele mesmo, importável de ponta a
// ponta). Triggers/ScheduledTriggers/Library/Config NUNCA entram num
// export filtrado por tag — tags aqui só cobrem Table/View (ver
// TagPack), e as demais categorias são propriedades globais da
// aplicação, não de uma tabela/view específica.
func ExportFilteredByTag(ctx context.Context, tx pgx.Tx, actorRole identity.RoleID, tagName string, plugins []PluginDependency) (Pack, error) {
	tag, err := tags.GetTag(ctx, tx, tagName)
	if err != nil {
		return Pack{}, err
	}
	entries, err := tags.ListEntries(ctx, tx, tag.ID)
	if err != nil {
		return Pack{}, fmt.Errorf("pack: listar associações da tag %q: %w", tagName, err)
	}

	full, err := Export(ctx, tx, actorRole, plugins)
	if err != nil {
		return Pack{}, err
	}
	tableByName := make(map[string]TablePack, len(full.Tables))
	for _, tp := range full.Tables {
		tableByName[tp.Name] = tp
	}

	// includedTables acumula o fechamento transitivo (tabelas na tag +
	// referenciadas por FieldKey); tagPack.Tables/Views guardam só o que a
	// tag referencia DIRETAMENTE (o TagPack exportado tem que continuar
	// batendo com as entries reais, nunca inflado pelo fechamento).
	includedTables := map[string]bool{}
	tagPack := TagPack{Name: tag.Name}
	var includedViews []ViewPack
	for _, e := range entries {
		switch {
		case e.TableID != nil:
			t, err := metadata.GetTableByID(ctx, database.AsTx(tx), *e.TableID)
			if err != nil {
				return Pack{}, fmt.Errorf("pack: tag %q resolver tabela: %w", tagName, err)
			}
			includedTables[t.Name] = true
			tagPack.Tables = append(tagPack.Tables, t.Name)
		case e.ViewID != nil:
			v, err := views.GetView(ctx, tx, actorRole, *e.ViewID)
			if err != nil {
				return Pack{}, fmt.Errorf("pack: tag %q resolver view: %w", tagName, err)
			}
			ownerTable, err := metadata.GetTableByID(ctx, database.AsTx(tx), v.TableID)
			if err != nil {
				return Pack{}, fmt.Errorf("pack: tag %q resolver tabela da view %q: %w", tagName, v.Name, err)
			}
			includedTables[ownerTable.Name] = true
			for _, vp := range full.Views {
				if vp.Name == v.Name {
					includedViews = append(includedViews, vp)
					break
				}
			}
			tagPack.Views = append(tagPack.Views, v.Name)
		}
	}

	// Fechamento transitivo: toda tabela referenciada (FieldKey) por uma
	// tabela já incluída também precisa estar no Pack, senão Import falha
	// com "tabela desconhecida" ao tentar recriar o campo-chave.
	for changed := true; changed; {
		changed = false
		for name := range includedTables {
			for _, f := range tableByName[name].Fields {
				if f.References != "" && !includedTables[f.References] {
					includedTables[f.References] = true
					changed = true
				}
			}
		}
	}

	// Preserva a ORDEM de full.Tables (já determinística, ORDER BY id) em
	// vez da ordem de iteração de um map — mesmo espírito de "round-trip
	// estável" documentado em Export.
	var filteredTables []TablePack
	for _, tp := range full.Tables {
		if includedTables[tp.Name] {
			filteredTables = append(filteredTables, tp)
		}
	}

	return Pack{
		Version: Version,
		Tables:  filteredTables,
		Views:   includedViews,
		Plugins: plugins,
		Tags:    []TagPack{tagPack},
	}, nil
}

// exportTagPacks resolve cada Tag + suas TagEntry para o formato
// portável por NOME (tag_pack() do legado) — sempre lê table_id/view_id
// de volta para o Name real via metadata.GetTableByID/views.GetView,
// nunca serializa o ID interno (que não sobrevive a um Import em outra
// instalação/tenant).
func exportTagPacks(ctx context.Context, tx pgx.Tx, actorRole identity.RoleID) ([]TagPack, error) {
	allTags, err := tags.ListTags(ctx, tx)
	if err != nil {
		return nil, fmt.Errorf("pack: listar tags: %w", err)
	}
	var out []TagPack
	for _, tg := range allTags {
		entries, err := tags.ListEntries(ctx, tx, tg.ID)
		if err != nil {
			return nil, fmt.Errorf("pack: listar associações da tag %q: %w", tg.Name, err)
		}
		tp := TagPack{Name: tg.Name}
		for _, e := range entries {
			switch {
			case e.TableID != nil:
				table, err := metadata.GetTableByID(ctx, database.AsTx(tx), *e.TableID)
				if err != nil {
					return nil, fmt.Errorf("pack: tag %q resolver tabela: %w", tg.Name, err)
				}
				tp.Tables = append(tp.Tables, table.Name)
			case e.ViewID != nil:
				v, err := views.GetView(ctx, tx, actorRole, *e.ViewID)
				if err != nil {
					return nil, fmt.Errorf("pack: tag %q resolver view: %w", tg.Name, err)
				}
				tp.Views = append(tp.Views, v.Name)
			}
		}
		out = append(out, tp)
	}
	return out, nil
}
