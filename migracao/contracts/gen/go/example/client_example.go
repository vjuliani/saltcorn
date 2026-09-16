// Package example prova que o cliente Go gerado (internalapi/bffapi) é
// consumível de forma realista, não só que o pacote gerado compila
// isoladamente. Não é o backend Go de verdade — é a evidência de validação
// de GO-006: "clientes Go/TS validam ambos os contratos".
package example

import (
	"context"
	"fmt"
	"net/http"

	"github.com/vjuliani/saltcorn/migracao/contracts/gen/go/bffapi"
	"github.com/vjuliani/saltcorn/migracao/contracts/gen/go/internalapi"
)

// withServiceIdentity injeta o token de identidade delegada (ADR-0003) em
// toda requisição — o padrão que o BFF real vai seguir a partir de GO-017.
func withServiceIdentity(token string) internalapi.RequestEditorFn {
	return func(_ context.Context, req *http.Request) error {
		req.Header.Set("Authorization", "Bearer "+token)
		return nil
	}
}

// ListFirstPage exercita o cliente de query (ListRecords) tipado a partir do
// contrato interno, incluindo o parâmetro opaco de paginação por cursor.
// Retorna itens e cursor separadamente (não a struct anônima que o
// oapi-codegen gera para o `allOf` de Page — combinar página genérica com
// campos específicos por endpoint é uma limitação conhecida do gerador com
// esse padrão de schema, registrada como lacuna em GO-006, não corrigida
// aqui).
func ListFirstPage(ctx context.Context, baseURL, token, tenant, table string) ([]internalapi.Record, *string, error) {
	client, err := internalapi.NewClientWithResponses(baseURL)
	if err != nil {
		return nil, nil, fmt.Errorf("criar cliente: %w", err)
	}

	resp, err := client.ListRecordsWithResponse(
		ctx,
		internalapi.Tenant(tenant),
		table,
		&internalapi.ListRecordsParams{},
		withServiceIdentity(token),
	)
	if err != nil {
		return nil, nil, fmt.Errorf("listRecords: %w", err)
	}
	if resp.StatusCode() == http.StatusUnauthorized && resp.JSON401 != nil {
		return nil, nil, fmt.Errorf("identidade rejeitada: %s — %s", resp.JSON401.Error.Code, resp.JSON401.Error.Message)
	}
	if resp.StatusCode() == http.StatusForbidden && resp.JSON403 != nil {
		return nil, nil, fmt.Errorf("acesso negado: %s — %s", resp.JSON403.Error.Code, resp.JSON403.Error.Message)
	}
	if resp.JSON200 == nil {
		return nil, nil, fmt.Errorf("resposta inesperada: status %d", resp.StatusCode())
	}
	return resp.JSON200.Items, resp.JSON200.NextCursor, nil
}

// CreateOne exercita o cliente de comando (CreateRecord), incluindo o
// cabeçalho obrigatório Idempotency-Key (ADR-0001).
func CreateOne(ctx context.Context, baseURL, token, tenant, table, idempotencyKey string, body map[string]any) (*internalapi.Record, error) {
	client, err := internalapi.NewClientWithResponses(baseURL)
	if err != nil {
		return nil, fmt.Errorf("criar cliente: %w", err)
	}

	resp, err := client.CreateRecordWithResponse(
		ctx,
		internalapi.Tenant(tenant),
		table,
		&internalapi.CreateRecordParams{IdempotencyKey: idempotencyKey},
		body,
		withServiceIdentity(token),
	)
	if err != nil {
		return nil, fmt.Errorf("createRecord: %w", err)
	}
	if resp.JSON201 == nil {
		return nil, fmt.Errorf("resposta inesperada: status %d", resp.StatusCode())
	}
	return resp.JSON201, nil
}

// GetBootstrap exercita o cliente do segundo contrato (React -> BFF),
// confirmando que o pacote bffapi também é consumível da mesma forma.
func GetBootstrap(ctx context.Context, baseURL string) (*bffapi.Bootstrap, error) {
	client, err := bffapi.NewClientWithResponses(baseURL)
	if err != nil {
		return nil, fmt.Errorf("criar cliente: %w", err)
	}
	resp, err := client.GetBootstrapWithResponse(ctx)
	if err != nil {
		return nil, fmt.Errorf("getBootstrap: %w", err)
	}
	if resp.JSON200 == nil {
		return nil, fmt.Errorf("resposta inesperada: status %d", resp.StatusCode())
	}
	return resp.JSON200, nil
}
