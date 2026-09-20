#!/usr/bin/env python3
"""Executa a matriz em fixture explicitamente descartável; sai 1 se evidência faltar."""
import argparse
import hashlib
import json
import os
from pathlib import Path
import platform
import signal
import subprocess
import time
from datetime import datetime, timezone
from urllib.parse import urlparse, unquote
from checks import SUITES, evaluate, go_summary, tap_summary, validate_matrix, vitest_summary, playwright_summary

ROOT = Path(__file__).resolve().parents[2]

def stamp():
    return datetime.now(timezone.utc).isoformat()

def digest(data):
    return hashlib.sha256(data).hexdigest()

def inputs():
    names = subprocess.check_output(['git', 'ls-files', '--cached', '--others', '--exclude-standard', '-z'], cwd=ROOT).decode().split('\0')
    return {name: digest((ROOT / name).read_bytes()) for name in sorted(set(names))
            if name.startswith(('migracao/', 'packages/saltcorn-mobile-app/src/', '.github/workflows/migracao-',
                                'docs/migracao-go/inventario/')) and (ROOT / name).is_file()}

def preflight(env):
    assert env.get('SALTCORN_PARITY_DISPOSABLE') == '1', 'Exige SALTCORN_PARITY_DISPOSABLE=1 e cluster exclusivo'
    for name in ('SALTCORN_GO_TEST_DATABASE_URL', 'SALTCORN_GO_TEST_DATABASE_URL_RLS', 'SALTCORN_GO_TEST_INSTALLATION_DATABASE_URL'):
        url = urlparse(env.get(name, ''))
        assert url.scheme in ('postgres', 'postgresql') and url.hostname in ('localhost', '127.0.0.1'), name + ': exige fixture loopback'
        assert url.path == '/go033_parity', name + ': exige banco go033_parity em cluster descartável exclusivo'
    assert env.get('SALTCORN_GO_TEST_MOBILE_E2E') == '1', 'Mobile HTTP E2E obrigatório'
    assert subprocess.check_output(['node', '--version']).decode().startswith('v22.'), 'Node 22 obrigatório'


def markdown(report):
    lines = ['# GO-033 — Resultado da matriz', '', f"Execução UTC: {report['started']}; commit `{report['commit']}`.",
             f"SHA-256 dos inputs: `{report['input_sha256']}`. Logs e hashes em `report.json`.", '',
             '**PASS de suíte não significa paridade integral nem autorização de canário.**', '',
             '| Suíte | Resultado | Testes aprovados |', '| --- | --- | --- |']
    for name, result in report['suites'].items():
        lines.append(f"| {name} | {result['status']} | {result.get('summary', {}).get('passed', result.get('summary', {}).get('pass', 'ver log'))} |")
    for profile, gate in report['gates'].items():
        lines.extend(['', f"Perfil **{profile}**: {'APTO (somente paridade)' if gate['eligible'] else 'BLOQUEADO'}; {len(gate['blockers'])}/{gate['capabilities']} capacidades com lacuna/evidência ausente."])
    lines.extend(['', '| ID / capacidade | Piloto | Resultado | Evidência executada | Cobertura / lacuna |', '| --- | --- | --- | --- | --- |'])
    for row in report['capabilities']:
        evidence = ', '.join(row['suites']) or 'levantamento no inventário GO-001'
        if row['go_packages']:
            evidence += ': ' + ', '.join(row['go_packages'])
        detail = row['coverage'] + ('. **Lacuna:** ' + row['gap'] if row['gap'] else '')
        if row['missing']:
            detail += '. **Evidência ausente:** ' + ', '.join(row['missing'])
        lines.append(f"| {row['id']} — {row['capability']} | {'sim' if row['pilot'] else 'não'} | {row['status']} | {evidence} | {detail} |")
    return '\n'.join(lines) + '\n'


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--output', required=True, type=Path, help='Diretório NOVO, fora do checkout; preserva tentativas anteriores')
    parser.add_argument('--gate', choices=('audit', 'pilot', 'integral'), default='audit')
    args = parser.parse_args()
    out = args.output.resolve()
    assert not out.is_relative_to(ROOT), 'Artefatos devem ficar fora do checkout'
    env = os.environ.copy()
    preflight(env)
    matrix = json.loads((ROOT / 'migracao/parity/matrix.json').read_text())
    validate_matrix(matrix, ROOT)
    out.mkdir(parents=True, exist_ok=False)
    env.update(CI='true', FORCE_COLOR='0', SALTCORN_E2E_TENANT='go033_web',
               PLAYWRIGHT_JSON_OUTPUT_FILE=str(out / 'web.json'))
    secrets = [env[k] for k in env if k.startswith('SALTCORN_') and 'DATABASE_URL' in k]
    secrets += [unquote(urlparse(s).password or '') for s in secrets.copy()]
    def redact(text):
        for secret in sorted(filter(None, secrets), key=len, reverse=True):
            text = text.replace(secret, '[REDACTED]')
        return text
    source = inputs()
    (out / 'inputs.json').write_text(json.dumps(source, indent=2) + '\n')
    report = {'schema': 1, 'started': stamp(), 'commit': subprocess.check_output(['git', 'rev-parse', 'HEAD'], cwd=ROOT).decode().strip(),
              'input_sha256': digest((out / 'inputs.json').read_bytes()), 'environment': {'platform': platform.platform()}, 'suites': {}}
    for tool, command in {'go': ['go', 'version'], 'node': ['node', '--version'], 'npm': ['npm', '--version'], 'postgres_client': ['pg_dump', '--version'], 'generator': ['oapi-codegen', '--version']}.items():
        report['environment'][tool] = subprocess.check_output(command, stderr=subprocess.STDOUT).decode().strip()
    def persist():
        (out / 'report.json').write_text(json.dumps(report, ensure_ascii=False, indent=2) + '\n')
    def command(suite, index, argv, cwd='.', timeout=900):
        log = out / f'{suite}-{index}.log'
        started = time.monotonic()
        # Arquivo temporário privado: redação antes de publicar logs no artefato.
        raw = out / '.current-output'
        fd = os.open(raw, os.O_WRONLY | os.O_CREAT | os.O_TRUNC, 0o600)
        with os.fdopen(fd, 'w') as stream:
            child = subprocess.Popen(argv, cwd=ROOT / cwd, env=env, stdout=stream, stderr=subprocess.STDOUT, start_new_session=True)
            try:
                code = child.wait(timeout=timeout)
            except (subprocess.TimeoutExpired, KeyboardInterrupt):
                os.killpg(child.pid, signal.SIGTERM)
                try:
                    child.wait(timeout=15)
                except subprocess.TimeoutExpired:
                    os.killpg(child.pid, signal.SIGKILL)
                    child.wait()
                code = 124
        text = redact(raw.read_text(errors='replace'))
        raw.unlink()
        log.write_text(text)
        result = {'argv': argv, 'cwd': cwd, 'exit': code, 'seconds': round(time.monotonic() - started, 2), 'log': log.name, 'sha256': digest(log.read_bytes())}
        return result, text
    specs = {
        'contracts': [(['npm', 'ci'], 'migracao/contracts'), (['bash', 'migracao/parity/contracts.sh'], '.')],
        'pluginhost': [(['npm', 'ci'], 'migracao/packages/pluginhost'), (['npm', 'test'], 'migracao/packages/pluginhost')],
        'bff': [(['npm', 'ci'], 'migracao/packages/bff'), (['npm', 'test'], 'migracao/packages/bff')],
        'frontend': [(['npm', 'ci'], 'migracao/packages/frontend'), (['npm', 'test', '--', '--reporter=json', '--outputFile=' + str(out / 'frontend.json')], 'migracao/packages/frontend')],
        'backend': [(['go', 'vet', './...'], 'migracao/backend'), (['go', 'build', './...'], 'migracao/backend'), (['go', 'test', '-json', '-race', '-count=1', './...'], 'migracao/backend')],
        'mobile': [(['node', '--test', 'migracao/packages/mobile-sync-test/client.test.mjs'], '.')],
        'web': [(['npm', 'ci'], 'migracao/e2e'), (['bash', 'migracao/e2e/run.sh', '--reporter=json'], '.')],
        'distribution': [(['bash', 'migracao/distribution/build.sh', str(out / 'release')], '.'), (['node', 'migracao/distribution/smoke.mjs', str(out / 'release')], '.')],
    }
    for name, commands in specs.items():
        print(f'{stamp()} {name}: iniciando', flush=True)
        entry = {'status': 'FAIL', 'commands': []}
        report['suites'][name] = entry
        try:
            for index, (argv, cwd) in enumerate(commands):
                result, text = command(name, index, argv, cwd)
                entry['commands'].append(result)
                persist()
                assert result['exit'] == 0, f"exit {result['exit']}; ver {result['log']}"
            if name == 'backend':
                entry['summary'] = go_summary(text)
            elif name in ('bff', 'pluginhost', 'mobile'):
                entry['summary'] = tap_summary(text)
            elif name == 'frontend':
                entry['summary'] = vitest_summary(json.loads((out / 'frontend.json').read_text()))
            elif name == 'web':
                entry['summary'] = playwright_summary(json.loads((out / 'web.json').read_text()))
            elif name == 'distribution':
                assert 'PASS release:' in text
                report['release'] = json.loads((out / 'release/release.json').read_text())
            entry['status'] = 'PASS'
        except (AssertionError, OSError, ValueError, KeyError) as error:
            entry['error'] = redact(str(error))
        persist()
        print(f"{stamp()} {name}: {entry['status']} {entry.get('error', '')}", flush=True)
    report['capabilities'], report['gates'] = evaluate(matrix, report['suites'])
    report['inputs_unchanged'] = source == inputs()
    report['audit_passed'] = report['inputs_unchanged'] and set(report['suites']) == SUITES and all(s['status'] == 'PASS' for s in report['suites'].values()) and not any(r['missing'] for r in report['capabilities'])
    report['finished'] = stamp()
    persist()
    (out / 'RESULTS.md').write_text(markdown(report))
    print(json.dumps({'audit_passed': report['audit_passed'], 'gates': report['gates']}, ensure_ascii=False))
    return 0 if report['audit_passed'] and (args.gate == 'audit' or report['gates'][args.gate]['eligible']) else 1

if __name__ == '__main__':
    raise SystemExit(main())
