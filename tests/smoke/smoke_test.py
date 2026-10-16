#!/usr/bin/env python3
"""Smoke-mode state checks without touching the user's Docker fixture."""
from pathlib import Path
import json
import os
import shutil
import subprocess
import tempfile
import unittest


class SmokeModeTest(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory(prefix="keyparcel-smoke-test-")
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        scripts = self.root / "tests/smoke"
        scripts.mkdir(parents=True)
        shutil.copyfile(Path(__file__).with_name("smoke.sh"), scripts / "smoke.sh")
        self.fixture = self.root / ".smoke"
        self.fixture.mkdir()
        (self.fixture / "name").write_text("keyparcel-smoke-123-456\n")
        (self.fixture / "host.pub").write_text("ssh-ed25519 server\n")
        for actor in ("alice", "bob"):
            home = self.fixture / actor
            (home / ".ssh").mkdir(parents=True)
            (home / ".ssh/known_hosts").write_text("approved server\n")
            (home / ".ssh/id_ed25519").write_text("retained identity")
            (home / "config.json").write_text("retained approvals")
            (home / "saved.txt").write_text("retained download")
        self.state = self.root / "calls.json"
        self.state.write_text("[]")
        self.executable("docker", """
import json, os, sys
from pathlib import Path
args = sys.argv[1:]
file = Path(os.environ['FAKE_CALLS'])
calls = json.loads(file.read_text())
calls.append(args)
file.write_text(json.dumps(calls))
if args[0] == 'info': print('fixture-docker')
elif args[:2] == ['inspect', '--format']: print('true')
elif args[0] == 'cp': assert Path(args[1]).read_text() == 'updated binary'
elif args[:3] == ['exec', '--user', '0']: assert args[4] == 'mv'
elif args[0] == 'exec' and 'ssh-keygen' in args: print('256 SHA256:server-fixture (ED25519)')
else: raise AssertionError(args)
""")
        self.executable("go", """
import os, sys
from pathlib import Path
if os.environ.get('FAKE_BUILD_FAIL'): sys.exit(1)
args = sys.argv[1:]
Path(args[args.index('-o') + 1]).write_text('updated binary')
""")
        self.env = dict(os.environ, KEYPARCEL_DOCKER=str(self.root / "docker"),
                        KEYPARCEL_GO=str(self.root / "go"), FAKE_CALLS=str(self.state))

    def executable(self, name, source):
        path = self.root / name
        path.write_text("#!/usr/bin/env python3\n" + source)
        path.chmod(0o700)

    def launch(self, action):
        return subprocess.run(["bash", "tests/smoke/smoke.sh", action], cwd=self.root,
                              env=self.env, capture_output=True, text=True, timeout=10)

    def assert_other_data_retained(self):
        self.assertEqual((self.fixture / "bob/.ssh/known_hosts").read_text(), "approved server\n")
        for actor in ("alice", "bob"):
            self.assertEqual((self.fixture / actor / ".ssh/id_ed25519").read_text(), "retained identity")
            self.assertEqual((self.fixture / actor / "config.json").read_text(), "retained approvals")
            self.assertEqual((self.fixture / actor / "saved.txt").read_text(), "retained download")

    def test_fresh_clears_only_alice_trust_and_up_keeps_approval(self):
        result = self.launch("fresh")
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertFalse((self.fixture / "alice/.ssh/known_hosts").exists(), "fresh Alice still has server trust")
        self.assertTrue((self.fixture / "fresh").exists())
        self.assertIn("SHA256:server-fixture", result.stdout)
        self.assert_other_data_retained()
        (self.fixture / "alice/.ssh/known_hosts").write_text("manually approved\n")
        result = self.launch("up")
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertEqual((self.fixture / "alice/.ssh/known_hosts").read_text(), "manually approved\n")
        self.assert_other_data_retained()
        calls = json.loads(self.state.read_text())
        self.assertEqual(sum(c[0] == "cp" for c in calls), 4, "launches did not update both client binaries")
        self.assertFalse(any(c[0] in ("run", "rm", "network", "build") for c in calls), "reopening rebuilt or replaced fixture resources")

    def test_ordinary_up_keeps_preconfigured_trust(self):
        result = self.launch("up")
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertEqual((self.fixture / "alice/.ssh/known_hosts").read_text(), "approved server\n")
        self.assertFalse((self.fixture / "fresh").exists())
        self.assert_other_data_retained()

    def test_failed_build_preserves_trust(self):
        self.env["FAKE_BUILD_FAIL"] = "1"
        result = self.launch("fresh")
        self.assertNotEqual(result.returncode, 0, "fresh launch ignored build failure")
        self.assertEqual((self.fixture / "alice/.ssh/known_hosts").read_text(), "approved server\n")
        self.assertFalse((self.fixture / "fresh").exists())
        self.assert_other_data_retained()

    def test_fresh_does_not_follow_known_hosts_symlink(self):
        external = self.root / "external-known-hosts"
        external.write_text("keep unrelated host trust")
        known = self.fixture / "alice/.ssh/known_hosts"
        known.unlink()
        known.symlink_to(external)
        result = self.launch("fresh")
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertFalse(known.is_symlink(), "fresh retained an external trust link")
        self.assertEqual(external.read_text(), "keep unrelated host trust")
        self.assert_other_data_retained()


if __name__ == "__main__":
    unittest.main()
