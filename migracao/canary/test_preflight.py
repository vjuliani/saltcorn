import copy
import json
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path

from preflight import ROOT, INPUTS, evaluate, inspect_plan, inspect_repository, read_json


class PreflightTests(unittest.TestCase):
    def setUp(self):
        self.data = [read_json(ROOT / INPUTS[key]) for key in ('matrix', 'parity', 'load', 'recovery')]
        self.plan = {'environment': 'laboratorio', 'tenants': ['alpha', 'beta'],
                     'window_seconds': 300, 'slos': {'p95_ms': 500, 'p99_ms': 1000, 'recovery_seconds': 30}}

    def test_real_pilot_is_blocked_despite_green_suites(self):
        result = inspect_repository(ROOT, {})
        self.assertEqual(result['decision'], 'BLOCKED')
        self.assertEqual(result['capability_count'], 48)
        self.assertEqual(result['parity_blockers'], 39)
        self.assertFalse(result['canary_released'])
        self.assertTrue({'Edit', 'Show', 'Feed'} <= {v['template'] for v in result['pack_inventory']['views']})

    def test_aggregate_eligible_cannot_override_gaps(self):
        self.data[1]['gates']['pilot']['eligible'] = True
        result = evaluate(*self.data, self.plan)
        self.assertEqual(result['parity_blockers'], 39)

    def test_mobile_suite_does_not_block_web_postgres_pilot(self):
        before = evaluate(*self.data, self.plan)['capabilities']
        self.data[1]['suites']['mobile']['status'] = 'FAIL'
        self.assertEqual(before, evaluate(*self.data, self.plan)['capabilities'])

    def test_missing_web_evidence_removes_previous_pass(self):
        before = evaluate(*self.data, self.plan)['parity_blockers']
        self.data[1]['suites']['bff']['status'] = 'FAIL'
        self.assertGreater(evaluate(*self.data, self.plan)['parity_blockers'], before)

    def test_cannot_shrink_or_duplicate_scope(self):
        for change in ('remove', 'duplicate', 'pilot_flag'):
            with self.subTest(change=change):
                data = copy.deepcopy(self.data)
                if change == 'remove': data[0]['capabilities'].pop(0)
                elif change == 'duplicate': data[0]['capabilities'].append(data[0]['capabilities'][0])
                else: data[0]['capabilities'][0]['pilot'] = False
                with self.assertRaises(ValueError): evaluate(*data, self.plan)

    def test_laboratory_and_other_recovery_fixture_never_approve_target(self):
        codes = {b['code'] for b in evaluate(*self.data, self.plan)['blockers']}
        self.assertTrue({'LOAD_SCOPE', 'RECOVERY_SCOPE'} <= codes)

    def test_wildcard_duplicate_and_empty_tenants_rejected(self):
        self.assertEqual(inspect_plan(self.plan), [])
        for tenants in ([], ['*', 'beta'], ['alpha', 'alpha'], ['alpha'], ['alpha', 'beta', 'gamma']):
            plan = {**self.plan, 'tenants': tenants}
            self.assertTrue(inspect_plan(plan))

    def test_invalid_window_slos_and_unknown_commands_rejected(self):
        for extra in ({'window_seconds': True}, {'window_seconds': -1}, {'slos': {}}, {'apply': True}):
            self.assertTrue(inspect_plan({**self.plan, **extra}))

    def test_even_clean_repository_evidence_does_not_release_canary(self):
        for row in self.data[0]['capabilities']:
            row['gap'] = ''
            row['suites'] = ['backend']
            row['go_packages'] = []
        for row in self.data[1]['capabilities']:
            row['status'] = 'PASS'
        self.data[2].update(productionPromotion=True, labPassed=True, profile={'scope': 'pilot'})
        self.data[3].update(fixture='guitars', go_candidate_validated=True)
        result = evaluate(*self.data, self.plan)
        self.assertTrue(result['repository_checks_passed'])
        self.assertEqual(result['decision'], 'REQUIRES_OPERATIONAL_VALIDATION')
        self.assertFalse(result['canary_released'])
        self.assertFalse(result['runtime_verified'])
        self.assertTrue(result['pending_operational_checks'])

    def test_read_only_cli_refuses_overwrite_even_under_optimized_python(self):
        with tempfile.TemporaryDirectory() as tmp:
            output = Path(tmp) / 'report.json'
            command = [sys.executable, '-O', str(ROOT / 'migracao/canary/preflight.py'), '--output', str(output)]
            run = subprocess.run(command, capture_output=True)
            self.assertEqual(run.returncode, 2, run.stderr)
            before = output.read_bytes()
            self.assertFalse(json.loads(before)['canary_released'])
            run = subprocess.run(command, capture_output=True)
            self.assertNotEqual(run.returncode, 0)
            self.assertEqual(output.read_bytes(), before)

    def test_missing_plan_is_closed_failure(self):
        with tempfile.TemporaryDirectory() as tmp:
            output = Path(tmp) / 'report.json'
            run = subprocess.run([sys.executable, str(ROOT / 'migracao/canary/preflight.py'),
                                  '--plan', str(Path(tmp) / 'missing.json'), '--output', str(output)], capture_output=True)
            self.assertEqual(run.returncode, 2)
            self.assertEqual(json.loads(output.read_text())['blockers'][0]['code'], 'INVALID_EVIDENCE')


if __name__ == '__main__':
    unittest.main()
