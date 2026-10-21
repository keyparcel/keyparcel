"""The preserved required context must fail closed across both job matrices."""
import itertools
import os
from pathlib import Path
import subprocess
import unittest


class InstalledPackagesGate(unittest.TestCase):
    def test_required_aggregate_rejects_failed_cancelled_and_skipped_dependencies(self):
        workflow = (Path(__file__).resolve().parents[2] / '.github/workflows/candidates.yml').read_text()
        aggregate = workflow.split('  linux:\n', 1)[1].split('  android:\n', 1)[0]
        self.assertIn('name: Linux installed packages\n', aggregate)
        self.assertIn('if: always()\n', aggregate)
        self.assertIn('needs: [linux-build, linux-validate]\n', aggregate)
        command = aggregate.split('        run: ', 1)[1].strip()
        for build, validate in itertools.product(('success', 'failure', 'cancelled', 'skipped'), repeat=2):
            with self.subTest(build=build, validate=validate):
                result = subprocess.run(['bash', '-c', command], env=dict(os.environ,
                    BUILD_RESULT=build, VALIDATE_RESULT=validate))
                self.assertEqual(result.returncode == 0, build == validate == 'success')


if __name__ == '__main__':
    unittest.main()
