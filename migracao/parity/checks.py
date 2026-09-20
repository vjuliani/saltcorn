"""Validação de evidência; nenhum resultado de suíte autoriza promoção sozinho."""
import json
import re

# As verificações usam assert; recusar execução otimizada, que os removeria.
if not __debug__:
    raise RuntimeError("Paridade exige Python sem -O/PYTHONOPTIMIZE")

SUITES = {'contracts', 'backend', 'pluginhost', 'bff', 'frontend', 'mobile', 'web', 'distribution'}
PREFIX = 'github.com/vjuliani/saltcorn/migracao/backend/'
ALLOWED_SKIPS = {
    (PREFIX + 'internal/records', 'TestCQRSBenchmark_AggregationDirectVsIndexVsProjection'): 'Benchmark de desempenho, escopo GO-035; não é teste de corretude.',
    (PREFIX + 'internal/platform/sqlite', 'TestSQLiteCrashHelper'): 'Helper executado pelo teste pai TestSQLiteCrashRecovery em subprocessos.',
}

def inventory_names(root):
    text = (root / 'docs/migracao-go/inventario/GO-001-matriz-capacidades.md').read_text()
    section = text.split('## 2. Matriz de capacidades')[1].split('## 3. Escopo do piloto')[0]
    return [line.split('|')[1].strip() for line in section.splitlines()
            if line.startswith('| ') and not line.startswith(('| Capacidade', '| ---'))]

def validate_matrix(matrix, root):
    rows = matrix['capabilities']
    assert matrix['schema'] == 1
    assert set(matrix['profiles']) == {'pilot', 'integral'}
    assert [r['capability'] for r in rows] == inventory_names(root), 'Inventário sem cobertura ou alterado'
    assert len({r['id'] for r in rows}) == len(rows), 'IDs duplicados'
    for row in rows:
        assert re.fullmatch(r'CAP-\d{3}', row['id']) and type(row['pilot']) is bool
        assert row['coverage'] and (root / row['source']).is_file()
        assert set(row['suites']) <= SUITES
        assert row['gap'] or row['suites'], 'Capacidade sem evidência nem lacuna'
        for package in row['go_packages']:
            assert 'backend' in row['suites']
            assert list((root / 'migracao/backend' / package).glob('*_test.go')), package


def go_summary(text):
    events = [json.loads(line) for line in text.splitlines() if line.startswith('{')]
    assert events, 'Sem eventos go test -json'
    assert not any(e.get('Action') == 'fail' for e in events), 'Falha Go'
    skipped = [e for e in events if e.get('Action') == 'skip' and e.get('Test')]
    for event in skipped:
        assert (event['Package'], event['Test']) in ALLOWED_SKIPS, 'Skip inesperado: ' + event['Test']
    passed = [e for e in events if e.get('Action') == 'pass' and e.get('Test')]
    assert passed, 'Nenhum teste Go executado'
    # Crash helper só é dispensável se o teste pai realmente terminou.
    assert any(e['Test'] == 'TestSQLiteCrashRecovery' for e in passed), 'Recuperação SQLite ausente'
    packages = {}
    for e in passed:
        packages.setdefault(e['Package'].removeprefix(PREFIX), []).append(e['Test'])
    return {'passed': len(passed), 'packages': packages,
            'allowed_skips': [{'package': e['Package'], 'test': e['Test'],
                              'reason': ALLOWED_SKIPS[e['Package'], e['Test']]} for e in skipped]}


def tap_summary(text):
    counts = {k: [int(x) for x in re.findall(r'^# ' + k + r' (\d+)\s*$', text, re.M)]
              for k in ('tests', 'pass', 'fail', 'cancelled', 'skipped', 'todo')}
    assert all(len(v) == 1 for v in counts.values()), 'Resumo TAP ausente/ambíguo'
    counts = {k: v[0] for k, v in counts.items()}
    assert counts['tests'] == counts['pass'] > 0 and not any(counts[k] for k in ('fail', 'cancelled', 'skipped', 'todo')), counts
    return counts


def vitest_summary(data):
    assert data['success'] and data['numPassedTests'] == data['numTotalTests'] > 0
    assert not any(data.get(k, 0) for k in ('numFailedTests', 'numPendingTests', 'numTodoTests'))
    return {'passed': data['numPassedTests']}


def playwright_summary(data):
    stats = data['stats']
    assert not data.get('errors') and stats['expected'] > 0
    assert not any(stats[k] for k in ('unexpected', 'flaky', 'skipped'))
    projects = {}
    def visit(suite):
        for spec in suite.get('specs', []):
            for test in spec['tests']:
                assert test['expectedStatus'] == 'passed' and test['status'] == 'expected'
                assert test['results'] and all(r['status'] == 'passed' for r in test['results'])
                projects[test['projectName']] = projects.get(test['projectName'], 0) + 1
        for child in suite.get('suites', []):
            visit(child)
    for suite in data['suites']:
        visit(suite)
    assert set(projects) == {'chromium', 'firefox'}, 'Navegador obrigatório ausente'
    assert sum(projects.values()) == stats['expected']
    return {'passed': stats['expected'], 'projects': projects}


def evaluate(matrix, suites):
    results = []
    for row in matrix['capabilities']:
        missing = [s for s in row['suites'] if suites.get(s, {}).get('status') != 'PASS']
        packages = suites.get('backend', {}).get('summary', {}).get('packages', {})
        missing += [p for p in row['go_packages'] if not packages.get(p)]
        status = 'MISSING_EVIDENCE' if missing else ('PARTIAL' if row['suites'] else 'BLOCKED') if row['gap'] else 'PASS'
        results.append({**row, 'status': status, 'missing': missing})
    gates = {}
    for profile in ('pilot', 'integral'):
        relevant = [r for r in results if profile == 'integral' or r['pilot']]
        blockers = [r['id'] for r in relevant if r['status'] != 'PASS']
        suite_failures = sorted(s for s in SUITES if suites.get(s, {}).get('status') != 'PASS')
        gates[profile] = {'eligible': not blockers and not suite_failures and bool(relevant), 'blockers': blockers, 'suite_failures': suite_failures, 'capabilities': len(relevant)}
    return results, gates
