import copy
import json
from pathlib import Path
import unittest
import subprocess
import sys
from checks import (ALLOWED_SKIPS, PREFIX, SUITES, evaluate, go_summary,
                    playwright_summary, tap_summary, validate_matrix, vitest_summary)
from run import preflight

ROOT = Path(__file__).resolve().parents[2]

class EvidenceTests(unittest.TestCase):
    def test_optimized_python_cannot_disable_gates(self):
        result = subprocess.run([sys.executable, "-O", "migracao/parity/run.py", "--help"], cwd=ROOT, capture_output=True, text=True)
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("Python sem -O", result.stderr)

    def test_inventory_must_be_fully_accounted_for(self):
        matrix = json.loads((ROOT / 'migracao/parity/matrix.json').read_text())
        validate_matrix(matrix, ROOT)
        matrix['capabilities'].pop()
        with self.assertRaises(AssertionError):
            validate_matrix(matrix, ROOT)

    def test_missing_evidence_and_documented_gap_block_both_gates(self):
        row = dict(id='CAP-001', pilot=True, suites=['backend'], go_packages=['internal/identity'], gap='')
        matrix = {'capabilities': [row]}
        suites = {s: {'status': 'PASS'} for s in SUITES}
        results, gates = evaluate(matrix, suites)
        self.assertEqual(results[0]['status'], 'MISSING_EVIDENCE')
        self.assertFalse(gates['pilot']['eligible'])
        suites['backend']['summary'] = {'packages': {'internal/identity': ['TestPassword']}}
        self.assertTrue(evaluate(matrix, suites)[1]['integral']['eligible'])
        row['gap'] = 'Login HTTP ausente'
        self.assertEqual(evaluate(matrix, suites)[0][0]['status'], 'PARTIAL')
        self.assertFalse(evaluate(matrix, suites)[1]['integral']['eligible'])
        row['gap'] = ''
        del suites['contracts']
        self.assertFalse(evaluate(matrix, suites)[1]['pilot']['eligible'])

    def test_go_missing_dependencies_cannot_pass_as_skip(self):
        parent = {'Action': 'pass', 'Test': 'TestSQLiteCrashRecovery', 'Package': PREFIX + 'internal/platform/sqlite'}
        events = [parent, {'Action': 'skip', 'Test': 'TestSyncMobileE2E', 'Package': PREFIX + 'cmd/server'}]
        with self.assertRaises(AssertionError):
            go_summary('\n'.join(map(json.dumps, events)))
        events[1].update(Package=PREFIX + 'internal/platform/sqlite', Test='TestSQLiteCrashHelper')
        self.assertEqual(go_summary('\n'.join(map(json.dumps, events)))['passed'], 1)
        with self.assertRaises(AssertionError):
            go_summary(json.dumps(events[1]))

    def test_tap_zero_skip_cancel_or_failure_is_rejected(self):
        text = '# tests 2\n# pass 2\n# fail 0\n# cancelled 0\n# skipped 0\n# todo 0\n'
        self.assertEqual(tap_summary(text)['pass'], 2)
        for invalid in (text.replace('# skipped 0', '# skipped 1'), text.replace('# fail 0', '# fail 1'), text.replace('# pass 2', '# pass 0'), ''):
            with self.assertRaises(AssertionError):
                tap_summary(invalid)

    def test_vitest_pending_is_not_success(self):
        data = dict(success=True, numPassedTests=2, numTotalTests=2, numPendingTests=0)
        self.assertEqual(vitest_summary(data)['passed'], 2)
        data['numPendingTests'] = 1
        with self.assertRaises(AssertionError):
            vitest_summary(data)

    def test_both_browsers_must_pass_without_retries_or_skips(self):
        tests = [dict(projectName=p, status='expected', expectedStatus='passed', results=[dict(status='passed')]) for p in ('chromium', 'firefox')]
        data = dict(stats=dict(expected=2, unexpected=0, flaky=0, skipped=0), suites=[dict(specs=[dict(tests=tests)])])
        self.assertEqual(playwright_summary(data)['passed'], 2)
        for invalid in ('missing', 'flaky', 'skipped'):
            bad = copy.deepcopy(data)
            if invalid == 'missing':
                bad['suites'][0]['specs'][0]['tests'].pop()
            else:
                bad['stats'][invalid] = 1
            with self.assertRaises(AssertionError):
                playwright_summary(bad)

    def test_preflight_refuses_preview_database_before_any_write(self):
        env = dict(SALTCORN_PARITY_DISPOSABLE='1', SALTCORN_GO_TEST_DATABASE_URL='postgres://test:test@127.0.0.1:55573/saltcorn_preview')
        with self.assertRaisesRegex(AssertionError, 'go033_parity'):
            preflight(env)

if __name__ == '__main__':
    unittest.main()
