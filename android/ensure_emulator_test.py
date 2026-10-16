#!/usr/bin/env python3
"""Check test-emulator ownership and calibration without real SDK processes."""
from pathlib import Path
import json
import os
import shutil
import subprocess
import tempfile
import time
import unittest


class TestEmulatorSetup(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory(prefix="keyparcel-emulator-test-")
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        android = self.root / "android"
        android.mkdir()
        shutil.copyfile(Path(__file__).with_name("ensure-emulator.sh"), android / "ensure-emulator.sh")
        (android / "env.sh").write_text('export PATH="$FAKE_ROOT/bin:$PATH"\n')
        avds = self.root / "avds"
        baseline = avds / "keyparcel-software-trial.avd"
        baseline.mkdir(parents=True)
        (baseline / "config.ini").write_text("hw.cpu.ncore = 2\nhw.ramSize = 2560\nhw.keyboard = yes\nhw.lcd.width = 480\nhw.lcd.height = 800\nhw.lcd.density = 160\n")
        (baseline / "userdata-qemu.img").write_text("keep manual phone data")
        self.env = dict(os.environ, FAKE_ROOT=str(self.root), ANDROID_HOME=str(self.root / "sdk"),
                        ANDROID_AVD_HOME=str(avds))
        self.env.pop("ANDROID_SERIAL", None)
        self.env.pop("KEYPARCEL_ANDROID_ACCEL", None)
        self.env.pop("KEYPARCEL_ANDROID_TEST_AVD_DIR", None)
        self.executable("bin/avdmanager", """
from pathlib import Path
import os, sys
root = Path(os.environ['FAKE_ROOT'])
args = sys.argv[1:]
destination = Path(args[args.index('-p') + 1])
destination.mkdir(parents=True)
(destination / 'config.ini').write_text('hw.cpu.ncore=4\\nhw.ramSize=2G\\nhw.keyboard=no\\nhw.lcd.width=320\\nhw.lcd.height=640\\nAvdId=keyparcel-ui-tests\\n')
(root / 'avds/keyparcel-ui-tests.ini').write_text('path=' + str(destination) + '\\n')
""")
        self.executable("bin/adb", """
from pathlib import Path
import os, sys
root = Path(os.environ['FAKE_ROOT'])
args = sys.argv[1:]
online = (root / 'online').exists()
if args == ['devices']:
    print('List of devices attached\\nemulator-5554\\tdevice')
    if online: print('emulator-5556\\tdevice')
elif args[:2] != ['-s', 'emulator-5556']: raise AssertionError(args)
elif args[2:] == ['get-state']: sys.exit(0 if online and not os.environ.get('FAKE_OFFLINE') else 1)
elif args[2:] == ['emu', 'avd', 'name']: print(os.environ.get('FAKE_AVD', 'keyparcel-ui-tests') + '\\nOK')
elif args[2:] == ['shell', 'getprop', 'sys.boot_completed']: print('1' if online else '')
else: raise AssertionError(args)
""")
        self.executable("bin/pgrep", """
import os, sys
sys.exit(0 if os.environ.get('FAKE_WINDOW') else 1)
""")
        self.executable("sdk/emulator/emulator", """
from pathlib import Path
import json, os, sys, time
root = Path(os.environ['FAKE_ROOT'])
(root / 'launch.json').write_text(json.dumps(sys.argv[1:]))
(root / 'online').touch()
time.sleep(0.3)
""")

    def executable(self, name, source):
        path = self.root / name
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text("#!/usr/bin/env python3\n" + source)
        path.chmod(0o700)

    def run_setup(self):
        return subprocess.run(["bash", "android/ensure-emulator.sh", "test"], cwd=self.root,
                              env=self.env, capture_output=True, text=True, timeout=10)

    def existing_test_avd(self):
        destination = self.root / "android/build/ui-avd"
        destination.mkdir(parents=True)
        (destination / "userdata-qemu.img").write_text("keep test phone data")
        (self.root / "avds/keyparcel-ui-tests.ini").write_text("path=" + str(destination) + "\n")
        (self.root / "online").touch()

    def test_bootstrap_copies_calibration_not_phone_data(self):
        result = self.run_setup()
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        config = (self.root / "android/build/ui-avd/config.ini").read_text()
        for setting in ("hw.cpu.ncore=2\n", "hw.ramSize=2560\n", "hw.keyboard=yes\n",
                        "hw.lcd.width=480\n", "hw.lcd.height=800\n"):
            self.assertIn(setting, config)
        deadline = time.monotonic() + 2
        while not (self.root / "launch.json").exists() and time.monotonic() < deadline:
            time.sleep(0.01)
        args = json.loads((self.root / "launch.json").read_text())
        self.assertIn("-no-window", args)
        self.assertEqual(args[args.index("-port") + 1], "5556")
        self.assertEqual(args[args.index("-accel") + 1], "off")
        self.assertFalse((self.root / "android/build/ui-avd/userdata-qemu.img").exists(), "copied manual app data into tests")
        self.assertEqual((self.root / "avds/keyparcel-software-trial.avd/userdata-qemu.img").read_text(), "keep manual phone data")

    def test_reuse_retains_test_data(self):
        self.existing_test_avd()
        result = self.run_setup()
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertFalse((self.root / "launch.json").exists(), "restarted a running test emulator")
        self.assertEqual((self.root / "android/build/ui-avd/userdata-qemu.img").read_text(), "keep test phone data")

    def test_ci_acceleration_preserves_headless_test_isolation(self):
        self.env["KEYPARCEL_ANDROID_ACCEL"] = "on"
        result = self.run_setup()
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        args = json.loads((self.root / "launch.json").read_text())
        self.assertEqual(args[args.index("-accel") + 1], "on")
        self.assertIn("-no-window", args)
        self.assertEqual(args[args.index("-port") + 1], "5556")

    def test_invalid_acceleration_does_not_launch(self):
        self.env["KEYPARCEL_ANDROID_ACCEL"] = "invalid"
        result = self.run_setup()
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("acceleration", result.stderr)
        self.assertFalse((self.root / "launch.json").exists())

    def test_ci_avd_directory_preserves_development_test_data(self):
        development = self.root / "android/build/ui-avd"
        development.mkdir(parents=True)
        (development / "userdata-qemu.img").write_text("keep development test data")
        destination = self.root / "android/build/ci-avds/keyparcel-ui-tests.avd"
        self.env["KEYPARCEL_ANDROID_TEST_AVD_DIR"] = str(destination)
        result = self.run_setup()
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertTrue((destination / "config.ini").exists())
        self.assertEqual((development / "userdata-qemu.img").read_text(), "keep development test data")

    def test_refuses_manual_serial(self):
        self.env["ANDROID_SERIAL"] = "emulator-5554"
        result = self.run_setup()
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("isolated emulator-5556", result.stderr)
        self.assertFalse((self.root / "launch.json").exists())

    def test_refuses_foreign_or_windowed_test_emulator(self):
        for overrides, reason in (({"FAKE_AVD": "another-phone"}, "belongs to another"),
                                  ({"FAKE_WINDOW": "1"}, "must be headless"),
                                  ({"FAKE_WINDOW": "1", "FAKE_OFFLINE": "1"}, "must be headless")):
            with self.subTest(reason=reason):
                if not (self.root / "online").exists(): self.existing_test_avd()
                self.env.update(overrides)
                result = self.run_setup()
                self.assertNotEqual(result.returncode, 0)
                self.assertIn(reason, result.stderr)
                self.assertFalse((self.root / "launch.json").exists())
                for key in overrides: self.env.pop(key)


if __name__ == "__main__":
    unittest.main()
