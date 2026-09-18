package pack

import "fmt"

// MissingDependency descreve uma dependência de plugin ausente ou
// incompatível — AvailableVersion vazio significa "o plugin nem está
// disponível", não só uma versão diferente.
type MissingDependency struct {
	Name             string
	RequiredVersion  string
	AvailableVersion string
}

func (m MissingDependency) String() string {
	if m.AvailableVersion == "" {
		return fmt.Sprintf("%s@%s (não disponível)", m.Name, m.RequiredVersion)
	}
	return fmt.Sprintf("%s@%s (disponível: %s)", m.Name, m.RequiredVersion, m.AvailableVersion)
}

// Validate confere as dependências de plugin de p contra
// availablePlugins (nome → versão disponível) — PURA, sem acesso a
// banco: roda ANTES de qualquer mutação de catálogo (ver Import), nunca
// depois de já ter aplicado parte do pack. Devolve TODAS as ausências de
// uma vez (não para na primeira) — quem chama recebe o quadro completo
// para decidir, não uma falha por vez.
//
// Como não existe inventário real de plugins de terceiro instalados
// neste checkout (achado repetido desde GO-001/003/004), availablePlugins
// é sempre fornecido explicitamente por quem chama Import — nunca
// descoberto automaticamente por este pacote.
func Validate(p Pack, availablePlugins map[string]string) []MissingDependency {
	var missing []MissingDependency
	for _, dep := range p.Plugins {
		avail, ok := availablePlugins[dep.Name]
		if !ok {
			missing = append(missing, MissingDependency{Name: dep.Name, RequiredVersion: dep.Version})
			continue
		}
		if dep.Version != "" && dep.Version != avail {
			missing = append(missing, MissingDependency{Name: dep.Name, RequiredVersion: dep.Version, AvailableVersion: avail})
		}
	}
	return missing
}
