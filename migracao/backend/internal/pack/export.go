package pack

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/vjuliani/saltcorn/migracao/backend/internal/config"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/identity"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/library"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/metadata"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/scheduler"
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
	tables, err := metadata.ListTables(ctx, tx)
	if err != nil {
		return Pack{}, fmt.Errorf("pack: listar tabelas: %w", err)
	}

	var tablePacks []TablePack
	for _, t := range tables {
		fields, err := metadata.ListFields(ctx, tx, t.ID)
		if err != nil {
			return Pack{}, fmt.Errorf("pack: listar campos de %q: %w", t.Name, err)
		}
		var fieldPacks []FieldPack
		for _, f := range fields {
			fp := FieldPack{Name: f.Name, Type: f.Type, Required: f.Required, Unique: f.Unique}
			if f.Type == metadata.FieldKey {
				refTable, err := metadata.GetTableByID(ctx, tx, f.ReferencesTable)
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
		table, err := metadata.GetTableByID(ctx, tx, v.TableID)
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
		table, err := metadata.GetTableByID(ctx, tx, tr.TableID)
		if err != nil {
			return Pack{}, fmt.Errorf("pack: resolver tabela do trigger: %w", err)
		}
		triggerPacks = append(triggerPacks, TriggerPack{
			TableName: table.Name, When: tr.When, Action: tr.Action, OnlyIf: tr.OnlyIf, AfterCommit: tr.AfterCommit,
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

	return Pack{
		Version:           Version,
		Tables:            tablePacks,
		Views:             viewPacks,
		Triggers:          triggerPacks,
		ScheduledTriggers: scheduledPacks,
		Library:           libraryPacks,
		Config:            cfg,
		Plugins:           plugins,
	}, nil
}
