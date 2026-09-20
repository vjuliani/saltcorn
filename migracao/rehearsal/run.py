#!/usr/bin/env python3
"""Ensaio GO-034 em PostgreSQL exclusivo; não recebe uma origem de produção."""
import argparse
import hashlib
import json
import os
from pathlib import Path
import subprocess
import time
from urllib.parse import urlparse

if not __debug__:
    raise RuntimeError('Execute Python sem -O/PYTHONOPTIMIZE')
root = Path(__file__).resolve().parents[2]
parser = argparse.ArgumentParser(description=__doc__)
parser.add_argument('--output', type=Path, required=True)
args = parser.parse_args()
out = args.output.resolve()
assert not out.is_relative_to(root), 'Use diretório novo fora do checkout'
assert os.environ.get('SALTCORN_GO_REHEARSAL_DISPOSABLE') == '1', 'Confirme cluster exclusivo descartável'
dsn = os.environ.get('SALTCORN_GO_TEST_INSTALLATION_DATABASE_URL', '')
u = urlparse(dsn)
assert u.scheme in ('postgres', 'postgresql') and u.hostname in ('localhost', '127.0.0.1') and u.path == '/go034_rehearsal', 'Exige banco go034_rehearsal em loopback'
out.mkdir(parents=True, exist_ok=False)
env = {**os.environ, 'SALTCORN_GO_TEST_DATABASE_URL': dsn, 'SALTCORN_GO_REHEARSAL_OUTPUT': str(out)}
paths = subprocess.check_output(['git', 'ls-files', '--cached', '--others', '--exclude-standard', '-z'], cwd=root).decode().split('\0')
inputs = {p: hashlib.sha256((root/p).read_bytes()).hexdigest() for p in sorted(set(paths))
          if p.startswith(('migracao/backend/', 'migracao/rehearsal/', '.github/workflows/migracao-rehearsal')) and (root/p).is_file()}
(out/'inputs.json').write_text(json.dumps(inputs, indent=2)+'\n')
cmd = ['go', 'test', '-json', '-race', '-count=1', './internal/installation', './internal/platform/cutover', './internal/pack', './cmd/cli']
start = time.time()
# As DSNs ficam somente no ambiente; nunca em argv, relatório ou logs públicos.
result = subprocess.run(cmd, cwd=root/'migracao/backend', env=env, stdout=subprocess.PIPE, stderr=subprocess.STDOUT, text=True, timeout=600)
text = result.stdout.replace(dsn, '[REDACTED]')
if u.password:
    text = text.replace(u.password, '[REDACTED]')
(out/'tests.log').write_text(text)
events = [json.loads(l) for l in text.splitlines() if l.startswith('{')]
passed = [e for e in events if e.get('Test') and e.get('Action') == 'pass']
failed = [e for e in events if e.get('Action') == 'fail']
skipped = [e for e in events if e.get('Test') and e.get('Action') == 'skip']
report = {'format': 1, 'commit': subprocess.check_output(['git','rev-parse','HEAD'],cwd=root,text=True).strip(),
          'command':cmd, 'cwd':'migracao/backend', 'exit':result.returncode, 'seconds':time.time()-start,
          'go':subprocess.check_output(['go','version'],text=True).strip(),
          'pg_tools':subprocess.check_output(['pg_dump','--version'],text=True).strip(),
          'passed':len(passed), 'failed':failed, 'skipped':skipped,
          'tests_sha256':hashlib.sha256((out/'tests.log').read_bytes()).hexdigest(),
          'inputs_sha256':hashlib.sha256((out/'inputs.json').read_bytes()).hexdigest()}
(out/'report.json').write_text(json.dumps(report,indent=2)+'\n')
assert result.returncode == 0 and passed and not failed and not skipped, 'Falha/skip; confira tests.log'
assert any(e['Test']=='TestMigrationPauseReconcileRecovery' for e in passed), 'Ensaio ausente'
checkpoint = json.loads((out/'checkpoint.json').read_text())
assert checkpoint['phase']=='complete' and checkpoint['rpo_lost_commits']==0 and checkpoint['rto_seconds']<=60
assert checkpoint['source_preserved'] and checkpoint['contraction_only_after_window'] and not checkpoint['legacy_traffic_allowed']
assert all((root/p).is_file() and hashlib.sha256((root/p).read_bytes()).hexdigest()==h for p,h in inputs.items()), 'Código alterado durante ensaio'
print(json.dumps({'passed':len(passed),'RPO_lost_commits':0,'RTO_seconds':checkpoint['rto_seconds'],'legacy_traffic_allowed':False}))
