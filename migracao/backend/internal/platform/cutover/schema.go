package cutover

import (
	"context"

	"github.com/jackc/pgx/v5"
)

// _sc_capability_ownership vive no schema `public` (não por tenant): é
// metadado de controle sobre o próprio roteamento da migração, não dado de
// domínio de um tenant — diferente das tabelas de internal/identity, que
// são replicadas por schema de tenant. Chamar EnsureSchema dentro de
// db.WithTenant(ctx, "public", ...).
const createOwnershipTableSQL = `
CREATE TABLE IF NOT EXISTS _sc_capability_ownership (
	tenant text NOT NULL,
	capability text NOT NULL,
	owner text NOT NULL,
	updated_at timestamptz NOT NULL DEFAULT now(),
	PRIMARY KEY (tenant, capability)
)`

// EnsureSchema cria a tabela de ownership, idempotente. Não é o executor de
// migrations real do backend (GO-011); é o mínimo necessário para esta
// tarefa ter onde persistir o registro de ownership.
func EnsureSchema(ctx context.Context, tx pgx.Tx) error {
	_, err := tx.Exec(ctx, createOwnershipTableSQL)
	return err
}
