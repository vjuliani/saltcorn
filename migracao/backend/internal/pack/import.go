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

// Import aplica p inteiro dentro de tx — TUDO ou NADA: a primeira etapa é
// SEMPRE Validate (nunca emite um único INSERT antes de confirmar que
// todas as dependências de plugin estão presentes), e qualquer erro
// durante a aplicação (referência a tabela desconhecida, conflito de
// nome, etc.) propaga imediatamente, sem tentar continuar com o resto do
// pack (divergência deliberada do legado: install_pack no legado
// continua instalando o resto mesmo quando um plugin falha ao carregar,
// só loga — ver docs/migracao-go/execucoes/GO-027.md). Como toda a
// função roda na MESMA tx que o chamador controla (mesmo padrão de
// CreateRecord/Advance/RunDue em todas as tarefas anteriores), um erro
// aqui garante que o chamador (via db.WithTenant) desfaz TUDO — nenhuma
// tabela, view, trigger, item de biblioteca ou config parcialmente
// aplicado sobrevive a uma falha no meio do caminho.
//
// Ordem de aplicação (tabelas SEM campos primeiro, depois campos de
// TODAS as tabelas): necessário para que um FieldKey possa referenciar
// QUALQUER tabela do mesmo pack, mesmo uma declarada depois dele no
// array (ou uma referência circular entre duas tabelas do mesmo pack) —
// no momento de adicionar campos, toda tabela do pack já existe.
func Import(ctx context.Context, tx pgx.Tx, actorRole identity.RoleID, p Pack, availablePlugins map[string]string) error {
	if p.Version > Version {
		return fmt.Errorf("%w: pack versão %d, backend suporta até %d", ErrUnsupportedVersion, p.Version, Version)
	}
	if missing := Validate(p, availablePlugins); len(missing) > 0 {
		return &MissingDependenciesError{Missing: missing}
	}

	for _, tp := range p.Tables {
		if _, err := metadata.CreateTable(ctx, database.AsTx(tx), actorRole, tp.Name, metadata.TableOptions{
			MinRoleRead: tp.MinRoleRead, MinRoleWrite: tp.MinRoleWrite,
		}); err != nil {
			return fmt.Errorf("pack: criar tabela %q: %w", tp.Name, err)
		}
	}

	for _, tp := range p.Tables {
		table, err := metadata.GetTable(ctx, database.AsTx(tx), tp.Name)
		if err != nil {
			return fmt.Errorf("pack: reler tabela %q recém-criada: %w", tp.Name, err)
		}
		for _, fp := range tp.Fields {
			if _, err := metadata.AddField(ctx, database.AsTx(tx), actorRole, table.ID, metadata.FieldDef{
				Name: fp.Name, Type: fp.Type, Required: fp.Required, Unique: fp.Unique, References: fp.References,
			}); err != nil {
				return fmt.Errorf("pack: adicionar campo %q.%q: %w", tp.Name, fp.Name, err)
			}
		}
	}

	for _, lp := range p.Library {
		if _, err := library.CreateOrReplace(ctx, tx, lp.Name, lp.Icon, lp.Layout); err != nil {
			return fmt.Errorf("pack: criar item de biblioteca %q: %w", lp.Name, err)
		}
	}

	for k, v := range p.Config {
		if err := config.Set(ctx, tx, k, v); err != nil {
			return fmt.Errorf("pack: gravar configuração %q: %w", k, err)
		}
	}

	for _, vp := range p.Views {
		table, err := metadata.GetTable(ctx, database.AsTx(tx), vp.TableName)
		if err != nil {
			return fmt.Errorf("pack: view %q referencia tabela desconhecida %q: %w", vp.Name, vp.TableName, err)
		}
		if _, err := views.CreateView(ctx, tx, actorRole, vp.Name, table.ID, vp.Template, vp.Configuration, views.ViewOptions{MinRole: vp.MinRole}); err != nil {
			return fmt.Errorf("pack: criar view %q: %w", vp.Name, err)
		}
	}

	for _, tp := range p.Triggers {
		table, err := metadata.GetTable(ctx, database.AsTx(tx), tp.TableName)
		if err != nil {
			return fmt.Errorf("pack: trigger em %q referencia tabela desconhecida: %w", tp.TableName, err)
		}
		if _, err := triggers.CreateTrigger(ctx, tx, triggers.Trigger{
			TableID: table.ID, When: tp.When, Action: tp.Action, OnlyIf: tp.OnlyIf, AfterCommit: tp.AfterCommit,
			Configuration: tp.Configuration,
		}); err != nil {
			return fmt.Errorf("pack: criar trigger em %q: %w", tp.TableName, err)
		}
	}

	for _, sp := range p.ScheduledTriggers {
		if _, err := scheduler.CreateScheduledTrigger(ctx, tx, sp.Name, sp.Action, sp.CronExpr, sp.Timezone); err != nil {
			return fmt.Errorf("pack: criar trigger agendado %q: %w", sp.Name, err)
		}
	}

	if len(p.Tags) > 0 {
		if err := importTagPacks(ctx, tx, actorRole, p.Tags); err != nil {
			return err
		}
	}

	return nil
}

// importTagPacks cria/reaproveita cada TagPack (CreateTag é idempotente
// por nome, AddEntry idempotente pela mesma combinação) e resolve
// Tables/Views por NOME para o ID que AddEntry exige — SEMPRE depois que
// p.Tables/p.Views já foram aplicados acima (uma tag nunca referencia uma
// entidade que este MESMO Import ainda vai criar depois dela).
func importTagPacks(ctx context.Context, tx pgx.Tx, actorRole identity.RoleID, tagPacks []TagPack) error {
	allViews, err := views.ListViews(ctx, tx, actorRole, 0)
	if err != nil {
		return fmt.Errorf("pack: listar views para resolver tags: %w", err)
	}
	viewIDByName := make(map[string]int, len(allViews))
	for _, v := range allViews {
		viewIDByName[v.Name] = v.ID
	}

	for _, tp := range tagPacks {
		tag, err := tags.CreateTag(ctx, tx, actorRole, tp.Name)
		if err != nil {
			return fmt.Errorf("pack: criar tag %q: %w", tp.Name, err)
		}
		for _, tableName := range tp.Tables {
			table, err := metadata.GetTable(ctx, database.AsTx(tx), tableName)
			if err != nil {
				return fmt.Errorf("pack: tag %q referencia tabela desconhecida %q: %w", tp.Name, tableName, err)
			}
			tableID := table.ID
			if _, err := tags.AddEntry(ctx, tx, actorRole, tag.ID, tags.TagEntryRef{TableID: &tableID}); err != nil {
				return fmt.Errorf("pack: associar tag %q à tabela %q: %w", tp.Name, tableName, err)
			}
		}
		for _, viewName := range tp.Views {
			viewID, ok := viewIDByName[viewName]
			if !ok {
				return fmt.Errorf("pack: tag %q referencia view desconhecida %q", tp.Name, viewName)
			}
			if _, err := tags.AddEntry(ctx, tx, actorRole, tag.ID, tags.TagEntryRef{ViewID: &viewID}); err != nil {
				return fmt.Errorf("pack: associar tag %q à view %q: %w", tp.Name, viewName, err)
			}
		}
	}
	return nil
}
