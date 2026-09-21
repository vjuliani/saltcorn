#!/usr/bin/env python3
"""Read-only GO-036 diagnostic. Never changes routes, ownership or task status."""
import argparse
import hashlib
import json
import re
import subprocess
import sys
import zipfile
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
INPUTS = {
    'matrix': 'migracao/parity/matrix.json',
    'parity': 'docs/migracao-go/paridade/report.json',
    'load': 'docs/migracao-go/operacao/report.json',
    'recovery': 'docs/migracao-go/recuperacao/checkpoint.json',
    'pack': 'deploy/playwright_mobile/backups/guitars_backup.zip',
}
OPERATIONAL_CHECKS = [
    'Identificar edge, artefatos, banco/schema, sessões, réplicas e workers do alvo.',
    'Comprovar escritor único e drenagem em TODOS os processos do escopo.',
    'Validar identidade/tenant no edge e roteamento por allowlist, sem fallback de mutações.',
    'Ensaiar recuperação/reconciliação com dados e versões do alvo.',
    'Executar carga do piloto e comparar ao baseline HTTP no mesmo ambiente.',
    'Observar a janela acordada e registrar incidentes, métricas e reconciliação final.',
]


def read_json(path):
    def reject_constant(value):
        raise ValueError('JSON não finito: ' + value)
    return json.loads(path.read_text(), parse_constant=reject_constant)


def inspect_plan(plan):
    """An explicit allowlist describes intent; it cannot authorize deployment."""
    missing = []
    if not isinstance(plan, dict):
        return ['Plano deve ser um objeto']
    if set(plan) - {'environment', 'tenants', 'window_seconds', 'slos'}:
        missing.append('Campos desconhecidos no plano; não incluir credenciais ou comandos')
    identifier = lambda v: isinstance(v, str) and bool(re.fullmatch(r'[a-zA-Z0-9][a-zA-Z0-9_.-]{0,62}', v))
    if not identifier(plan.get('environment')):
        missing.append('Ambiente alvo não identificado')
    tenants = plan.get('tenants')
    if (not isinstance(tenants, list) or len(tenants) != 2 or
            not all(identifier(v) for v in tenants) or len(set(tenants)) != 2):
        missing.append('Piloto exige allowlist de dois tenants distintos; curingas não são aceitos')
    if type(plan.get('window_seconds')) is not int or plan['window_seconds'] <= 0:
        missing.append('Janela de observação não acordada')
    slos = plan.get('slos')
    keys = {'p95_ms', 'p99_ms', 'recovery_seconds'}
    if (not isinstance(slos, dict) or set(slos) != keys or
            any(type(v) not in (int, float) or v <= 0 for v in slos.values())):
        missing.append('SLOs de latência/recuperação não acordados')
    elif slos['p95_ms'] > slos['p99_ms']:
        missing.append('SLO p95 não pode exceder p99')
    return missing


def pilot_capabilities(matrix, parity):
    """Recompute the pilot subset, ignoring an aggregate eligible=true flag.

    Mobile/SQLite suites outside this subset cannot block a web/PG pilot.
    A removed matrix row or changed pilot flag must not silently shrink scope.
    """
    rows, recorded = matrix['capabilities'], parity['capabilities']
    current = {r['id']: r for r in rows}
    previous = {r['id']: r for r in recorded}
    if len(current) != len(rows) or len(previous) != len(recorded) or set(current) != set(previous):
        raise ValueError('Inventário e evidência divergem: IDs ausentes/duplicados')
    for key, row in current.items():
        if (type(row['pilot']) is not bool or row['pilot'] != previous[key]['pilot'] or
                row['capability'] != previous[key]['capability']):
            raise ValueError('Escopo do piloto alterado sem revalidação: ' + key)
    selected = [r for r in rows if r['pilot']]
    if not selected:
        raise ValueError('Subconjunto piloto vazio')
    suites = parity['suites']
    packages = suites.get('backend', {}).get('summary', {}).get('packages', {})
    results = []
    for row in selected:
        missing = [s for s in row['suites'] if suites.get(s, {}).get('status') != 'PASS']
        missing += [p for p in row['go_packages'] if not packages.get(p)]
        if not row['suites']:
            missing.append('nenhuma suíte executada')
        status = 'PASS' if not missing and not row['gap'] and previous[row['id']]['status'] == 'PASS' else 'BLOCKED'
        results.append({'id': row['id'], 'capability': row['capability'], 'status': status,
                        'gap': row['gap'], 'missing': missing})
    return results


def evaluate(matrix, parity, load, recovery, plan):
    capabilities = pilot_capabilities(matrix, parity)
    blockers = [{'code': 'PARITY', 'detail': r['id'] + ': ' + (r['gap'] or ', '.join(r['missing']) or 'evidência anterior sem PASS')}
                for r in capabilities if r['status'] != 'PASS']
    if parity.get('audit_passed') is not True or parity.get('inputs_unchanged') is not True:
        blockers.append({'code': 'PARITY_EVIDENCE', 'detail': 'Auditoria inválida ou inputs mudaram durante a coleta'})
    # Lab success is not an agreement about a production workload or its SLOs.
    if load.get('productionPromotion') is not True or load.get('profile', {}).get('scope') != 'pilot':
        blockers.append({'code': 'LOAD_SCOPE', 'detail': 'GO-035 comprova laboratório, não carga/SLO e baseline do piloto alvo'})
    if load.get('labPassed') is not True:
        blockers.append({'code': 'LOAD_EVIDENCE', 'detail': 'Ensaio de carga/falhas não aprovado'})
    if recovery.get('fixture') != 'guitars' or recovery.get('go_candidate_validated') is not True:
        blockers.append({'code': 'RECOVERY_SCOPE', 'detail': 'GO-034 não ensaiou recuperação do piloto guitars no ambiente alvo'})
    blockers += [{'code': 'TARGET_PLAN', 'detail': value} for value in inspect_plan(plan)]
    return {
        'format': 1, 'task': 'GO-036', 'pilot': 'guitars',
        'decision': 'BLOCKED' if blockers else 'REQUIRES_OPERATIONAL_VALIDATION',
        'canary_released': False, 'runtime_verified': False,
        'repository_checks_passed': not blockers,
        'capability_count': len(capabilities),
        'parity_blockers': sum(r['status'] != 'PASS' for r in capabilities),
        'capabilities': capabilities, 'blockers': blockers,
        'pending_operational_checks': OPERATIONAL_CHECKS,
        'legacy_return_certified': recovery.get('legacy_traffic_allowed') is True,
        'note': 'Diagnóstico documental; não consulta o ambiente nem certifica seu estado atual. Plano preenchido não autoriza corte.',
    }


def inspect_repository(root, plan):
    paths = {key: root / value for key, value in INPUTS.items()}
    result = evaluate(*(read_json(paths[key]) for key in ('matrix', 'parity', 'load', 'recovery')), plan)
    with zipfile.ZipFile(paths['pack']) as archive:
        names = [n for n in archive.namelist() if n == 'pack.json' or n.endswith('/pack.json')]
        if len(names) != 1 or archive.getinfo(names[0]).file_size > 10_000_000:
            raise ValueError('Pack piloto ausente, ambíguo ou acima do limite')
        pack = json.loads(archive.read(names[0]))
    result['pack_inventory'] = {
        'views': [{'name': v['name'], 'template': v['viewtemplate']} for v in pack['views']],
        'triggers': [{'name': t['name'], 'action': t['action'], 'event': t['when_trigger']} for t in pack['triggers']],
    }
    result['diagnostic_sha256'] = hashlib.sha256(Path(__file__).read_bytes()).hexdigest()
    result['inputs_sha256'] = {INPUTS[key]: hashlib.sha256(path.read_bytes()).hexdigest() for key, path in paths.items()}
    result['plan_sha256'] = hashlib.sha256(json.dumps(plan, sort_keys=True, allow_nan=False).encode()).hexdigest()
    return result


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--plan', type=Path, help='Aliases de ambiente/tenants e SLOs; nunca credenciais')
    parser.add_argument('--output', required=True, type=Path, help='Novo arquivo de evidência, sem sobrescrever tentativas')
    args = parser.parse_args()
    try:
        result = inspect_repository(ROOT, read_json(args.plan) if args.plan else {})
        result['commit'] = subprocess.check_output(['git', 'rev-parse', 'HEAD'], cwd=ROOT, text=True).strip()
    except (OSError, ValueError, KeyError, TypeError, zipfile.BadZipFile) as error:
        result = {'format': 1, 'task': 'GO-036', 'decision': 'BLOCKED', 'canary_released': False,
                  'blockers': [{'code': 'INVALID_EVIDENCE', 'detail': str(error)}]}
    with args.output.open('x') as stream:
        json.dump(result, stream, indent=2, ensure_ascii=False, allow_nan=False)
        stream.write('\n')
    print(json.dumps({'decision': result['decision'], 'canary_released': False,
                      'blockers': len(result.get('blockers', []))}))
    return 2 if result['decision'] == 'BLOCKED' else 0


if __name__ == '__main__':
    sys.exit(main())
