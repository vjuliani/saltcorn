// Governança de plugins (GO-046, CAP-079) — camada de governança sobre o
// catálogo de nome/versão que SyncPlugins (GO-032) já mantinha em
// _sc_plugin_versions: validação de versão (semver), compatibilidade de
// engine declarada (`engines.saltcorn` de package.json, análogo ao
// `engines.saltcorn` do legado) e trilha de auditoria de upgrade (via
// _sc_metadata, ver metadata.go).
//
// Decisões de escopo explícitas (nenhum porte é "completo" aqui):
//
//   - Descoberta via registro npm (`npm-registry-fetch` do legado,
//     buscando `engines.saltcorn` remotamente e listando versões
//     disponíveis) fica FORA de escopo: este checkout não tem nenhum
//     plugin de terceiro real para validar contra um registro (mesmo
//     achado já registrado desde GO-001/003/004/029) e adicionar uma
//     dependência de rede a uma operação administrativa offline
//     (`internal/installation` nunca faz chamada de rede hoje, fora
//     Postgres/HTTP local de health-check) seria um acoplamento novo sem
//     nenhum consumidor real para testar. A verificação de compatibilidade
//     aqui é 100% LOCAL: lê `engines.saltcorn` do `package.json` já
//     presente no diretório do plugin (a mesma leitura que
//     `pluginInventory` já fazia para nome/versão) e compara contra
//     PluginEngineVersion, a versão de engine que ESTE backend Go declara.
//   - Checagem de "views dependentes antes de remover um plugin" (o outro
//     item nomeado em CAP-079) é DELIBERADAMENTE VAZIA nesta entrega: hoje
//     todo viewtemplate (List/Show/Edit/Feed, GO-020/039/051) é NATIVO do
//     backend Go — nenhum plugin de terceiro registra viewtemplate,
//     tipo de campo ou fieldview algum (internal/pluginhost, GO-022, só
//     executa expressão/ação em processo isolado, nunca registra um
//     viewtemplate). Não existe, portanto, nenhum caso real em que
//     remover um plugin poderia quebrar uma view — construir uma checagem
//     que nunca pode encontrar uma dependência real seria trabalho de
//     fachada. Fica reservada para quando (se) um mecanismo real de
//     viewtemplate fornecido por plugin existir.
package installation

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/vjuliani/saltcorn/migracao/backend/internal/platform/database"
)

// PluginEngineVersion é a versão de engine que ESTE backend Go declara —
// plugins que restringem `engines.saltcorn` a um intervalo que não cobre
// este valor são recusados no sync. Plugins do legado (motor Node) nunca
// declaram uma versão compatível com um engine Go — são um universo
// diferente por natureza; este mecanismo é para o futuro de um plugin
// GO-nativo, não uma ponte de compatibilidade com o ecossistema legado.
const PluginEngineVersion = "1.0.0"

// semver é [major, minor, patch] — sem suporte a pre-release/build
// metadata (o subconjunto exato usado por engines.saltcorn dos plugins
// reais que este backend já tem, nenhum usa qualificador; adicionar um
// parser completo de semver sem um caso real que precise dele seria
// especulativo).
type semver [3]int

func parseSemver(s string) (semver, error) {
	var v semver
	parts := strings.SplitN(s, ".", 3)
	if len(parts) != 3 {
		return v, fmt.Errorf("installation: versão semver inválida: %q", s)
	}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return v, fmt.Errorf("installation: versão semver inválida: %q", s)
		}
		v[i] = n
	}
	return v, nil
}

// compareSemver devolve -1/0/1 (a<b, a==b, a>b).
func compareSemver(a, b semver) int {
	for i := 0; i < 3; i++ {
		if a[i] != b[i] {
			if a[i] < b[i] {
				return -1
			}
			return 1
		}
	}
	return 0
}

// engineCompatible avalia constraint (uma lista de termos separados por
// espaço, todos exigidos — o mesmo "E" implícito do legado) contra
// version. Operadores suportados: "", "=" (igualdade exata), ">=", ">",
// "<=", "<", "^" (mesmo major, >= a versão exata), "~" (mesmo
// major.minor, >= a versão exata) — cobre o uso comum de faixas de engine
// sem reimplementar a gramática completa de faixas do npm (sem "||", sem
// "x"/"*" coringa, sem intervalos hifenizados "1.0.0 - 2.0.0").
func engineCompatible(constraint, version string) (bool, error) {
	v, err := parseSemver(version)
	if err != nil {
		return false, err
	}
	for _, term := range strings.Fields(constraint) {
		op, rest := "=", term
		for _, candidate := range []string{">=", "<=", "^", "~", ">", "<", "="} {
			if strings.HasPrefix(term, candidate) {
				op, rest = candidate, strings.TrimPrefix(term, candidate)
				break
			}
		}
		bound, err := parseSemver(rest)
		if err != nil {
			return false, fmt.Errorf("installation: restrição de engine inválida: %q", term)
		}
		cmp := compareSemver(v, bound)
		var ok bool
		switch op {
		case "=":
			ok = cmp == 0
		case ">=":
			ok = cmp >= 0
		case ">":
			ok = cmp > 0
		case "<=":
			ok = cmp <= 0
		case "<":
			ok = cmp < 0
		case "^":
			ok = cmp >= 0 && v[0] == bound[0]
		case "~":
			ok = cmp >= 0 && v[0] == bound[0] && v[1] == bound[1]
		}
		if !ok {
			return false, nil
		}
	}
	return true, nil
}

// pluginEngineRanges lê engines.saltcorn (opcional) de cada plugin em
// root/plugins — leitura independente de pluginInventory (não reutiliza
// seu resultado) para não acoplar o contrato já testado de Manifest.Plugins
// (name->version) a este campo adicional.
func pluginEngineRanges(root string) (map[string]string, error) {
	result := map[string]string{}
	entries, err := os.ReadDir(filepath.Join(root, "plugins"))
	if err != nil {
		return nil, err
	}
	for _, entry := range entries {
		b, err := os.ReadFile(filepath.Join(root, "plugins", entry.Name(), "package.json"))
		if err != nil {
			return nil, err
		}
		var p struct {
			Name    string `json:"name"`
			Engines struct {
				Saltcorn string `json:"saltcorn"`
			} `json:"engines"`
		}
		if err = json.Unmarshal(b, &p); err != nil {
			return nil, err
		}
		if p.Engines.Saltcorn != "" {
			result[p.Name] = p.Engines.Saltcorn
		}
	}
	return result, nil
}

// validatePluginVersions recusa o sync inteiro se qualquer plugin tiver
// versão semver inválida ou uma faixa de engine declarada que não cobre
// PluginEngineVersion — fail-closed: nunca registra um plugin
// potencialmente incompatível silenciosamente (mesmo espírito de todo
// outro mecanismo deste backend que prefere erro explícito a aceitar
// dado suspeito).
func validatePluginVersions(root string, inv map[string]string) error {
	ranges, err := pluginEngineRanges(root)
	if err != nil {
		return err
	}
	names := make([]string, 0, len(inv))
	for name := range inv {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if _, err := parseSemver(inv[name]); err != nil {
			return fmt.Errorf("installation: plugin %q: %w", name, err)
		}
		if constraint, declared := ranges[name]; declared {
			ok, err := engineCompatible(constraint, PluginEngineVersion)
			if err != nil {
				return fmt.Errorf("installation: plugin %q: %w", name, err)
			}
			if !ok {
				return fmt.Errorf("installation: plugin %q exige engine %q, este backend declara %q", name, constraint, PluginEngineVersion)
			}
		}
	}
	return nil
}

// pluginUpgradeAudit compara previous (o catálogo ANTES do sync) com next
// (o catálogo depois de ler o diretório) e grava uma entrada
// "plugin_upgrade" em _sc_metadata por plugin adicionado, removido ou com
// versão alterada — equivalente reduzido do log de auditoria de upgrade
// de plugin do legado (EventLog), reaproveitando o mecanismo genérico já
// construído nesta mesma tarefa em vez de uma tabela dedicada nova.
func pluginUpgradeAudit(ctx context.Context, tx database.Tx, previous, next map[string]string) error {
	names := map[string]bool{}
	for name := range previous {
		names[name] = true
	}
	for name := range next {
		names[name] = true
	}
	sorted := make([]string, 0, len(names))
	for name := range names {
		sorted = append(sorted, name)
	}
	sort.Strings(sorted)
	for _, name := range sorted {
		oldVersion, hadOld := previous[name]
		newVersion, hasNew := next[name]
		if oldVersion == newVersion && hadOld == hasNew {
			continue
		}
		event := map[string]any{"name": name, "from_version": nil, "to_version": nil}
		if hadOld {
			event["from_version"] = oldVersion
		}
		if hasNew {
			event["to_version"] = newVersion
		}
		if _, err := WriteMetadata(ctx, tx, "plugin_upgrade", "plugin_upgrade", nil, event); err != nil {
			return err
		}
	}
	return nil
}

// currentPluginVersions lê o catálogo hoje registrado em
// _sc_plugin_versions, ANTES de SyncPlugins substituí-lo — usado só para
// calcular o diff de pluginUpgradeAudit.
func currentPluginVersions(ctx context.Context, tx database.Tx) (map[string]string, error) {
	rows, err := tx.Query(ctx, `SELECT name,version FROM _sc_plugin_versions`)
	if err != nil {
		if errors.Is(err, database.ErrNoRows) {
			return map[string]string{}, nil
		}
		return nil, err
	}
	defer rows.Close()
	result := map[string]string{}
	for rows.Next() {
		var name, version string
		if err := rows.Scan(&name, &version); err != nil {
			return nil, err
		}
		result[name] = version
	}
	return result, rows.Err()
}
