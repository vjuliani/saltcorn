package metadata

import (
	"testing"

	"github.com/vjuliani/saltcorn/migracao/backend/internal/platform/tenancy"
)

// TestCache_HitDoesNotCallLoader é a prova de que Tables não recarrega
// desnecessariamente quando a versão em cache já bate com a atual.
func TestCache_HitDoesNotCallLoader(t *testing.T) {
	c := NewCache()
	tenant := tenancy.Tenant("acme")
	loads := 0
	loader := func() ([]Table, error) {
		loads++
		return []Table{{ID: 1, Name: "guitars"}}, nil
	}

	if _, err := c.Tables(tenant, 1, loader); err != nil {
		t.Fatalf("Tables (primeira carga): %v", err)
	}
	if _, err := c.Tables(tenant, 1, loader); err != nil {
		t.Fatalf("Tables (mesma versão): %v", err)
	}
	if loads != 1 {
		t.Errorf("loader chamado %d vezes, esperado 1 (segunda chamada deveria ser cache hit)", loads)
	}
}

// TestCache_VersionBumpInvalidates é a prova direta do critério de aceite
// "invalidação de cache": quando a versão muda, Tables recarrega — nunca
// serve dado desatualizado silenciosamente.
func TestCache_VersionBumpInvalidates(t *testing.T) {
	c := NewCache()
	tenant := tenancy.Tenant("acme")
	loads := 0
	loader := func() ([]Table, error) {
		loads++
		return []Table{{ID: loads, Name: "guitars"}}, nil
	}

	first, err := c.Tables(tenant, 1, loader)
	if err != nil {
		t.Fatalf("Tables (versão 1): %v", err)
	}
	second, err := c.Tables(tenant, 2, loader)
	if err != nil {
		t.Fatalf("Tables (versão 2): %v", err)
	}
	if loads != 2 {
		t.Errorf("loader chamado %d vezes, esperado 2 (mudança de versão deveria forçar recarga)", loads)
	}
	if first[0].ID == second[0].ID {
		t.Error("segunda carga retornou o mesmo dado da primeira — cache não invalidou de verdade")
	}
}

func TestCache_DifferentTenantsAreIndependent(t *testing.T) {
	c := NewCache()
	loadsA, loadsB := 0, 0
	loaderA := func() ([]Table, error) { loadsA++; return []Table{{Name: "a"}}, nil }
	loaderB := func() ([]Table, error) { loadsB++; return []Table{{Name: "b"}}, nil }

	if _, err := c.Tables(tenancy.Tenant("acme"), 1, loaderA); err != nil {
		t.Fatalf("Tables(acme): %v", err)
	}
	if _, err := c.Tables(tenancy.Tenant("beta"), 1, loaderB); err != nil {
		t.Fatalf("Tables(beta): %v", err)
	}
	if loadsA != 1 || loadsB != 1 {
		t.Errorf("loadsA=%d loadsB=%d, esperado 1 e 1 (tenants independentes)", loadsA, loadsB)
	}

	// Segunda chamada de A, mesma versão: não deveria afetar B nem recarregar A.
	if _, err := c.Tables(tenancy.Tenant("acme"), 1, loaderA); err != nil {
		t.Fatalf("Tables(acme) de novo: %v", err)
	}
	if loadsA != 1 {
		t.Errorf("loadsA = %d após segunda chamada com mesma versão, esperado permanecer 1", loadsA)
	}
}

func TestCache_InvalidateForcesReload(t *testing.T) {
	c := NewCache()
	tenant := tenancy.Tenant("acme")
	loads := 0
	loader := func() ([]Table, error) { loads++; return []Table{{Name: "guitars"}}, nil }

	if _, err := c.Tables(tenant, 1, loader); err != nil {
		t.Fatalf("Tables: %v", err)
	}
	c.Invalidate(tenant)
	if _, err := c.Tables(tenant, 1, loader); err != nil { // mesma versão, mas cache foi limpo
		t.Fatalf("Tables após Invalidate: %v", err)
	}
	if loads != 2 {
		t.Errorf("loader chamado %d vezes, esperado 2 (Invalidate deveria forçar recarga mesmo com a mesma versão)", loads)
	}
}
