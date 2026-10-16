"""Exercise reset ordering with fake ADB/Docker, preserving manual smoke data."""
import json
import os
from pathlib import Path
import shutil
import subprocess
import tempfile
import unittest


class AndroidSmokeResetTest(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory(prefix="keyparcel-smoke-reset-")
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        repo = Path(__file__).resolve().parents[2]
        (self.root / "tests/smoke").mkdir(parents=True)
        (self.root / "android").mkdir()
        shutil.copy(repo / "tests/smoke/android.sh", self.root / "tests/smoke/android.sh")
        shutil.copy(repo / "android/env.sh", self.root / "android/env.sh")
        self.fixture = self.root / ".smoke-android"
        self.fixture.mkdir()
        (self.fixture / "name").write_text("keyparcel-android-smoke-100-200\n")
        (self.fixture / "serial").write_text("emulator-5554\n")
        self.settings = self.root / "saved-smoke-settings"
        self.settings.write_text("old pinned host and identity")
        self.other = self.root / "other-app-data"
        self.other.write_text("keep this")
        self.state = self.root / "fake-state.json"
        self.set_state(online=False, fail_clear="", clears=[], removed=[])
        self.executable("sdk/platform-tools/adb", """#!/usr/bin/env python3
import json, os, sys
from pathlib import Path
root = Path(os.environ['RESET_TEST_ROOT'])
file = root / 'fake-state.json'
s = json.loads(file.read_text())
args = sys.argv[3:]
if args == ['get-state']:
    if not s['online']: sys.exit(1)
    print('device')
elif args == ['emu', 'avd', 'name']:
    assert s['online']
    print('keyparcel-software-trial\\nOK')
elif args[:5] == ['shell', 'pm', 'list', 'packages', '-u']:
    assert s['online']
    if s.get('installed', True):
        print('package:dev.keyparcel.app.smoke\\npackage:dev.keyparcel.app.smoke.test')
elif args[:3] == ['shell', 'pm', 'clear']:
    assert s['online']
    app = args[3]
    assert app in ['dev.keyparcel.app.smoke', 'dev.keyparcel.app.smoke.test']
    if app == s['fail_clear']:
        print('Failed')  # Some pm failures have a successful shell exit code.
    else:
        s['clears'].append(app)
        if app == 'dev.keyparcel.app.smoke':
            (root / 'saved-smoke-settings').unlink(missing_ok=True)
        print('Success')
elif args != ['reverse', '--list']:
    if args[:2] == ['install', '-r']:
        print('Success')
    elif args[:1] == ['reverse']:
        pass
    elif args[:3] == ['shell', 'am', 'force-stop']:
        pass
    elif args[:3] == ['shell', 'am', 'start']:
        settings = root / 'saved-smoke-settings'
        if not settings.exists():
            settings.write_text('normal app-created identity; no host or approvals')
            s['generations'] = s.get('generations', 0) + 1
    else:
        raise AssertionError(args)
file.write_text(json.dumps(s))
""")
        self.executable("bin/docker", """#!/usr/bin/env python3
import json, os, sys
from pathlib import Path
root = Path(os.environ['RESET_TEST_ROOT'])
file = root / 'fake-state.json'
s = json.loads(file.read_text())
args = sys.argv[1:]
if args[:2] in [['container', 'inspect'], ['network', 'inspect']]:
    sys.exit(0 if args[2] not in s['removed'] else 1)
elif args[:2] == ['rm', '-f'] or args[:2] == ['network', 'rm']:
    if s.get('installed', True):
        assert s['clears'][-2:] == ['dev.keyparcel.app.smoke', 'dev.keyparcel.app.smoke.test']
    s['removed'].append(args[2])
elif args[:2] == ['inspect', '--format']:
    print('true')
elif args[:1] == ['port']:
    print('127.0.0.1:30000')
elif args[:1] == ['exec'] and 'ssh-keygen' in args:
    print('256 SHA256:fixture-key alice (ED25519)')
else:
    raise AssertionError(args)
file.write_text(json.dumps(s))
""")
        self.executable("android/ensure-emulator.sh", """#!/usr/bin/env python3
import json, os
from pathlib import Path
file = Path(os.environ['RESET_TEST_ROOT']) / 'fake-state.json'
s = json.loads(file.read_text())
s['online'] = True
file.write_text(json.dumps(s))
""")
        self.executable("tests/build-images.sh", """#!/usr/bin/env python3
import json, os, sys
from pathlib import Path
root = Path(os.environ['RESET_TEST_ROOT'])
s = json.loads((root / 'fake-state.json').read_text())
assert not (root / 'saved-smoke-settings').exists(), 'old host pin survived relaunch'
assert not (root / '.smoke-android').exists(), 'old ownership state survived reset'
assert len(s['removed']) == 3, 'old fixture survived reset'
(root / 'relaunch-ready').touch()
sys.exit(77)  # Stop before rebuilding real images or creating a new fixture.
""")
        self.env = dict(os.environ, RESET_TEST_ROOT=str(self.root),
                        ANDROID_HOME=str(self.root / "sdk"), JAVA_HOME=str(self.root / "jdk"),
                        ANDROID_SERIAL="emulator-5554", KEYPARCEL_DOCKER="docker",
                        PATH=str(self.root / "bin") + os.pathsep + os.environ["PATH"])

    def executable(self, name, text):
        path = self.root / name
        path.parent.mkdir(parents=True, exist_ok=True)
        if name.endswith(".sh"):
            text = "#!/usr/bin/env bash\nexec python3 - <<'PY'\n" + text.split("\n", 1)[1] + "\nPY\n"
        path.write_text(text)
        path.chmod(0o755)

    def set_state(self, **changes):
        state = json.loads(self.state.read_text()) if self.state.exists() else {}
        state.update(changes)
        self.state.write_text(json.dumps(state))

    def run_smoke(self, action):
        return subprocess.run(["bash", "tests/smoke/android.sh", action], cwd=self.root,
                              env=self.env, text=True, capture_output=True, timeout=10)

    def assert_pending(self):
        self.assertTrue((self.fixture / "reset-pending").exists(), "reset intent was discarded")
        self.assertTrue((self.fixture / "name").exists(), "fixture ownership was discarded")
        self.assertEqual(json.loads(self.state.read_text())["removed"], [], "fixture removed before app clearing")
        self.assertEqual(self.other.read_text(), "keep this", "another app's data changed")

    def assert_relaunch(self):
        result = self.run_smoke("up")
        self.assertEqual(result.returncode, 77, result.stdout + result.stderr)
        self.assertTrue((self.root / "relaunch-ready").exists(), "relaunch did not finish pending reset before setup")
        self.assertFalse(self.settings.exists(), "old pinned settings survived reset")
        self.assertEqual(self.other.read_text(), "keep this", "another app's data changed")

    def test_closed_emulator_reset_finishes_before_relaunch(self):
        result = self.run_smoke("down")
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn("reset is pending", result.stdout)
        self.assert_pending()
        self.assertTrue(self.settings.exists(), "offline reset claimed to clear unavailable app data")
        self.assert_relaunch()

    def test_failed_app_clear_preserves_pending_reset(self):
        for app in ["dev.keyparcel.app.smoke", "dev.keyparcel.app.smoke.test"]:
            with self.subTest(app=app):
                self.set_state(online=True, fail_clear=app)
                result = self.run_smoke("down")
                self.assertNotEqual(result.returncode, 0, "failed pm clear reported successful reset")
                self.assertIn("reset remains pending", result.stderr)
                self.assert_pending()
                result = self.run_smoke("up")
                self.assertNotEqual(result.returncode, 0, "relaunch ignored failed app clearing")
                self.assert_pending()
        self.set_state(fail_clear="")
        self.assert_relaunch()

    def test_online_reset_clears_apps_before_removing_fixture(self):
        self.set_state(online=True)
        result = self.run_smoke("down")
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertIn("Removed Android smoke fixture and its app data", result.stdout)
        self.assertFalse(self.fixture.exists(), "completed reset retained stale ownership")
        self.assertFalse(self.settings.exists(), "completed reset retained old host pin")
        self.assertEqual(self.other.read_text(), "keep this", "another app's data changed")

    def test_reset_handles_apps_not_installed_yet(self):
        self.set_state(online=True, installed=False)
        self.settings.unlink()
        result = self.run_smoke("down")
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertFalse(self.fixture.exists(), "unfinished installation could not be reset")
        self.assertEqual(json.loads(self.state.read_text())["clears"], [], "tried to clear uninstalled apps")

    def fresh_launch_ready(self):
        (self.fixture / "linux-configured").touch()
        (self.fixture / "host.pub").write_text("ssh-ed25519 fixture-server\n")
        self.executable("android/client/build.sh", "#!/usr/bin/env python3\npass\n")
        self.executable("bin/xdotool", "#!/usr/bin/env python3\npass\n")

    def test_fresh_launch_and_reopen_keep_manual_setup(self):
        self.fresh_launch_ready()
        result = self.run_smoke("fresh")
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertIn("phone enrollment, and approval are manual", result.stdout)
        self.assertIn("fixture-server", result.stdout)
        self.assertEqual(self.settings.read_text(), "normal app-created identity; no host or approvals")
        self.assertEqual((self.fixture / "mode").read_text().strip(), "fresh")
        self.assertFalse((self.fixture / "fresh-reset-pending").exists())
        self.assertEqual(json.loads(self.state.read_text())["removed"], [], "fresh mode removed the Linux fixture")
        self.settings.write_text("same identity; manually pinned host; approved peer")
        self.set_state(online=False)
        result = self.run_smoke("up")
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertEqual(self.settings.read_text(), "same identity; manually pinned host; approved peer")
        self.assertEqual(json.loads(self.state.read_text())["generations"], 1, "ordinary reopening replaced the identity")
        self.assertEqual(self.other.read_text(), "keep this")

    def test_failed_fresh_reset_retries_before_launch(self):
        self.fresh_launch_ready()
        self.set_state(fail_clear="dev.keyparcel.app.smoke")
        result = self.run_smoke("fresh")
        self.assertNotEqual(result.returncode, 0, "fresh mode ignored failed app clearing")
        self.assertTrue((self.fixture / "fresh-reset-pending").exists())
        self.assertEqual(self.settings.read_text(), "old pinned host and identity")
        self.assertEqual(json.loads(self.state.read_text())["removed"], [])
        self.set_state(fail_clear="")
        result = self.run_smoke("up")
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertFalse((self.fixture / "fresh-reset-pending").exists())
        self.assertEqual(self.settings.read_text(), "normal app-created identity; no host or approvals")


if __name__ == "__main__":
    unittest.main()
