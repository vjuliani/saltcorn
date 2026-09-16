package metadata

import (
	"sync"

	"github.com/vjuliani/saltcorn/migracao/backend/internal/platform/tenancy"
)

// Cache guarda, em memória e por tenant, o resultado da última carga do
// catálogo (ex.: a lista de tabelas), revalidando contra CurrentVersion —
// o consumidor concreto de "invalidação de cache" (critério de aceite de
// GO-011). Nenhum código de produção usa Cache ainda (GO-012, o compilador
// de consultas, é quem consumiria isto de verdade) — existe aqui como a
// peça testável e reutilizável, não uma integração fabricada.
type Cache struct {
	mu    sync.Mutex
	byKey map[tenancy.Tenant]*cachedEntry
}

type cachedEntry struct {
	version int64
	tables  []Table
}

// NewCache cria um Cache vazio.
func NewCache() *Cache {
	return &Cache{byKey: make(map[tenancy.Tenant]*cachedEntry)}
}

// Tables retorna as tabelas em cache para tenant se a versão em cache bater
// com currentVersion; caso contrário (nada em cache, ou versão
// desatualizada), chama loader para recarregar, atualiza o cache com o
// resultado e o retorna. loader normalmente é uma chamada a ListFields/
// GetTable dentro de uma transação já aberta — Cache em si nunca abre
// transação nem toca o banco diretamente.
func (c *Cache) Tables(tenant tenancy.Tenant, currentVersion int64, loader func() ([]Table, error)) ([]Table, error) {
	c.mu.Lock()
	entry, ok := c.byKey[tenant]
	if ok && entry.version == currentVersion {
		tables := entry.tables
		c.mu.Unlock()
		return tables, nil
	}
	c.mu.Unlock()

	tables, err := loader()
	if err != nil {
		return nil, err
	}

	c.mu.Lock()
	c.byKey[tenant] = &cachedEntry{version: currentVersion, tables: tables}
	c.mu.Unlock()
	return tables, nil
}

// Invalidate remove o cache de tenant explicitamente — normalmente
// desnecessário (Tables já revalida por versão), útil para testes ou para
// forçar recarga sem saber a versão atual.
func (c *Cache) Invalidate(tenant tenancy.Tenant) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.byKey, tenant)
}
