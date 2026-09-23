// Comando `cli e2e-seed` (GO-021) — prepara um tenant descartável para o
// harness de E2E de navegador (migracao/e2e/): schema de catálogo/views/
// outbox, um usuário admin, e ownership de Go para as capacidades que o
// fluxo criar/publicar/operar usa. Não existe hoje nenhum endpoint HTTP
// para bootstrap de tenant (criar o primeiro usuário/conceder ownership é,
// por natureza, uma ação anterior a qualquer requisição autenticada) —
// este comando é o mínimo necessário para isso, mesmo espírito de
// `healthcheck` (CLI reaproveitando os mesmos pacotes do servidor, não
// duplicando lógica). Paridade com `saltcorn create-user`/`reset-schema`
// do CLI legado (matriz GO-001 §2.6) — versão mínima, focada no que o
// harness de E2E precisa, não um comando de administração completo.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/vjuliani/saltcorn/migracao/backend/internal/config"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/files"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/identity"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/metadata"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/platform/cutover"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/platform/database"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/platform/outbox"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/platform/tenancy"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/triggers"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/views"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/workflow"
)

type e2eSeedResult struct {
	Tenant      string `json:"tenant"`
	AdminUserID int    `json:"admin_user_id"`
	AdminRoleID int    `json:"admin_role_id"`
}

func e2eSeed(args []string) error {
	fs := flag.NewFlagSet("e2e-seed", flag.ExitOnError)
	dsn := fs.String("dsn", "", "DSN do Postgres de teste (obrigatório)")
	tenantName := fs.String("tenant", "e2e", "nome do tenant/schema a preparar (recriado do zero se já existir)")
	email := fs.String("email", "e2e-admin@example.com", "e-mail do usuário admin semeado")
	password := fs.String("password", "e2e-password-not-used-by-any-login-yet", "senha do usuário admin semeado — sem endpoint de login ainda (ver bff README), não usada por nenhum fluxo de autenticação; a sessão do harness é semeada diretamente (ver migracao/e2e/scripts/start-bff.mjs)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *dsn == "" {
		return fmt.Errorf("--dsn é obrigatório")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	db, err := database.Open(ctx, *dsn)
	if err != nil {
		return fmt.Errorf("conectar ao Postgres: %w", err)
	}
	defer db.Close()

	tenant := tenancy.Tenant(*tenantName)

	// Schema do tenant do zero — um harness de E2E precisa de um estado
	// conhecido a cada execução, não de acumular sobras de execuções
	// anteriores.
	if err := db.WithTenant(ctx, "public", func(ctx context.Context, tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, fmt.Sprintf(`DROP SCHEMA IF EXISTS %s CASCADE`, pgx.Identifier{string(tenant)}.Sanitize())); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, fmt.Sprintf(`CREATE SCHEMA %s`, pgx.Identifier{string(tenant)}.Sanitize()))
		return err
	}); err != nil {
		return fmt.Errorf("recriar schema do tenant %q: %w", tenant, err)
	}

	var adminID int
	if err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		if err := identity.EnsureSchema(ctx, tx); err != nil {
			return err
		}
		if err := metadata.EnsureSchema(ctx, database.AsTx(tx)); err != nil {
			return err
		}
		if err := outbox.EnsureSchema(ctx, tx); err != nil {
			return err
		}
		if err := views.EnsureSchema(ctx, tx); err != nil {
			return err
		}
		// triggers.EnsureSchema (GO-040): todo write real de registro agora
		// consulta _sc_triggers de verdade (Dispatcher.HooksFor, wireado em
		// cmd/server) — antes disso, hooks eram sempre nil e a ausência
		// deste schema nunca era exercitada por nenhum harness de E2E/carga.
		if err := triggers.EnsureSchema(ctx, tx); err != nil {
			return err
		}
		// config.EnsureSchema (GO-047): getActorHandler agora consulta
		// _sc_config (default_locale) em toda chamada — sem isso, o SELECT
		// falha e defaultLocaleOrFallback mascara o erro devolvendo "pt"
		// silenciosamente. Mesma classe de achado de GO-040/045 (schema de
		// framework que faltava neste bootstrap de E2E, não no
		// provisionamento real).
		if err := config.EnsureSchema(ctx, tx); err != nil {
			return err
		}
		// workflow.EnsureSchema (GO-048): getWorkflowHandler/runWorkflowHandler
		// consultam _sc_workflows/_sc_workflow_steps — mesma classe de achado
		// de GO-040/045/047, corrigida aqui proativamente (por inspeção, antes
		// de qualquer spec E2E de workflow existir) em vez de esperar uma
		// falha revelar a lacuna.
		if err := workflow.EnsureSchema(ctx, tx); err != nil {
			return err
		}
		// files.EnsureSchema (GO-051): uploadFileHandler/downloadFileHandler
		// e metadata.AddField de um campo FieldFile consultam/referenciam
		// _sc_files — mesma classe de achado de GO-040/045/047/048.
		if err := files.EnsureSchema(ctx, tx); err != nil {
			return err
		}
		hash, err := identity.HashPassword(*password)
		if err != nil {
			return err
		}
		adminID, err = identity.CreateUser(ctx, tx, *email, hash, identity.RoleAdmin)
		return err
	}); err != nil {
		return fmt.Errorf("semear catálogo/usuário no tenant %q: %w", tenant, err)
	}

	// Ownership de Go para as capacidades que o fluxo criar/publicar/operar
	// usa (main.go, GO-009/017/019) — SetOwner direto (não SwitchOwner):
	// não há servidor rodando ainda para drenar, este é um bootstrap
	// inicial, não uma troca de propriedade em produção (ver comentário de
	// escopo em internal/platform/cutover/registry.go).
	if err := db.WithTenant(ctx, "public", func(ctx context.Context, tx pgx.Tx) error {
		if err := cutover.EnsureSchema(ctx, tx); err != nil {
			return err
		}
		for _, capability := range []string{"tables.records", "tables.schema", "tables.views", "workflows", "files"} {
			if err := cutover.SetOwner(ctx, tx, tenant, capability, cutover.OwnerGo); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		return fmt.Errorf("registrar ownership de Go: %w", err)
	}

	return json.NewEncoder(os.Stdout).Encode(e2eSeedResult{
		Tenant:      string(tenant),
		AdminUserID: adminID,
		AdminRoleID: int(identity.RoleAdmin),
	})
}
