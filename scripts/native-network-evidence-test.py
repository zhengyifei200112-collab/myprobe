import copy
import importlib.util
from pathlib import Path
import unittest
import sys

sys.dont_write_bytecode = True

spec = importlib.util.spec_from_file_location('native_proof', Path(__file__).with_name('native-network-evidence.py'))
proof = importlib.util.module_from_spec(spec)
spec.loader.exec_module(proof)

def fixture():
    rows = [{'Package': proof.PACKAGE, 'Test': name, 'Action': 'pass'} for name in sorted(proof.REQUIRED)]
    for family in ('ipv4', 'ipv6'):
        rows.append({'Package': proof.PACKAGE, 'Test': 'TestNetworkProbeNativeICMPEvidence/' + family, 'Action': 'output',
                     'Output': f'native=darwin family={family} sent=2 received=2 rtt_ms=0.024000'})
    return rows + [{'Package': proof.PACKAGE, 'Action': 'pass'}]

class EvidenceBoundaryTests(unittest.TestCase):
    def test_complete_synthetic_contract(self):
        self.assertEqual(len(proof.validate_events(fixture(), 'darwin')['passed_tests']), 9)

    def test_incomplete_or_forged_claims_rejected(self):
        for case in ('missing', 'skip', 'failure', 'no_packet_output', 'wrong_os', 'wrong_count', 'wrong_identity', 'duplicate', 'no_package_pass'):
            with self.subTest(case=case):
                rows = copy.deepcopy(fixture())
                if case == 'missing': del rows[0]
                if case == 'skip': rows[0]['Action'] = 'skip'
                if case == 'failure': rows.append({'Package': proof.PACKAGE, 'Action': 'fail'})
                if case == 'no_packet_output': rows = [r for r in rows if r['Action'] != 'output']
                if case == 'wrong_os': rows[-3]['Output'] = rows[-3]['Output'].replace('darwin', 'linux')
                if case == 'wrong_count': rows[-3]['Output'] = rows[-3]['Output'].replace('sent=2', 'sent=5')
                if case == 'wrong_identity': rows[-3]['Test'] = 'unrelated'
                if case == 'duplicate': rows.append(copy.deepcopy(rows[-3]))
                if case == 'no_package_pass': rows.pop()
                with self.assertRaises(ValueError): proof.validate_events(rows, 'darwin')

if __name__ == '__main__':
    unittest.main()
