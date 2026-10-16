"""Local checks for automatic scenario assignment."""
import unittest
import json
import os
from pathlib import Path
import subprocess
import shutil
import tempfile
import sys
from cli import shard


class ShardTests(unittest.TestCase):
    def test_every_scenario_including_new_names_is_assigned_once(self):
        names = ["TestReceive", "TestJumpHost", "TestFiles", "TestNewScenario", "TestClipboard"]
        groups = [shard(names, i) for i in (1, 2, 3)]
        flattened = [name for group in groups for name in group]
        self.assertCountEqual(flattened, names)
        self.assertEqual(len(flattened), len(set(flattened)))
        self.assertEqual(groups, [shard(list(reversed(names)), i) for i in (1, 2, 3)])

    def test_invalid_or_empty_selection_fails(self):
        for names, index in (([], 1), (["TestOne"], 3), (["TestOne"], 0), (["TestOne"], 4), (["TestOne", "TestOne"], 1)):
            with self.subTest(names=names, index=index), self.assertRaises(ValueError):
                shard(names, index)


class AndroidProfileTests(unittest.TestCase):
    def test_ci_calibration_replaces_settings_with_real_ini_lines(self):
        # Exercise the actual bootstrap snippet without requiring SDK/KVM processes.
        script = Path(__file__).with_name("android.sh").read_text()
        configure = script.split("<<'PYTHON'\n", 1)[1].split("\nPYTHON", 1)[0]
        with tempfile.TemporaryDirectory() as temporary:
            config = Path(temporary) / "config.ini"
            config.write_text("hw.cpu.ncore = 4\nhw.lcd.width = 320\ncustom.value = keep\n")
            subprocess.run([sys.executable, "-c", configure, str(config)], check=True)
            lines = config.read_text().splitlines()
            self.assertIn("hw.cpu.ncore=2", lines)
            self.assertIn("hw.lcd.width=480", lines)
            self.assertIn("hw.lcd.height=800", lines)
            self.assertIn("hw.ramSize=2560", lines)
            self.assertIn("custom.value=keep", lines)
            self.assertEqual(sum(line.startswith("hw.cpu.ncore=") for line in lines), 1)


class AndroidOverlapTests(unittest.TestCase):
    def test_build_and_boot_overlap_and_gate_tests(self):
        for boot_first in (False, True):
            with self.subTest(boot_first=boot_first):
                self.run_client(boot_first=boot_first)

    def test_failed_boot_prevents_tests(self):
        self.run_client(boot_status=42)

    def test_failed_build_cancels_pending_boot_and_prevents_tests(self):
        self.run_client(build_status=43)

    def run_client(self, boot_first=False, boot_status=0, build_status=0):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            client = root / "android/client"
            client.mkdir(parents=True)
            shutil.copyfile(Path(__file__).resolve().parents[2] / "android/client/test.sh", client / "test.sh")
            (root / "android/env.sh").write_text(":\n")
            smoke = root / "tests/smoke"
            smoke.mkdir(parents=True)
            (smoke / "android_test.py").write_text("")
            (root / "android/ensure_emulator_test.py").write_text("")
            (root / "android/ensure-emulator.sh").write_text('''#!/bin/bash
set -eu
trap 'touch "$FAKE_ROOT/boot-cancelled"; exit 143' TERM
touch "$FAKE_ROOT/boot-started"
while [[ ! -f "$FAKE_ROOT/build-started" ]]; do sleep .01; done
if (( BOOT_STATUS != 0 )); then exit "$BOOT_STATUS"; fi
if [[ $BOOT_FIRST != 1 ]]; then
    while [[ ! -f "$FAKE_ROOT/build-finished" ]]; do sleep .01; done
fi
touch "$FAKE_ROOT/boot-ready"
''')
            (client / "build.sh").write_text('''#!/bin/bash
set -eu
touch "$FAKE_ROOT/build-started"
while [[ ! -f "$FAKE_ROOT/boot-started" ]]; do sleep .01; done
if (( BUILD_STATUS != 0 )); then exit "$BUILD_STATUS"; fi
if [[ $BOOT_FIRST == 1 ]]; then
    while [[ ! -f "$FAKE_ROOT/boot-ready" ]]; do sleep .01; done
fi
touch "$FAKE_ROOT/build-finished"
''')
            go = root / "go"
            go.write_text('''#!/bin/bash
set -eu
[[ -f "$FAKE_ROOT/build-finished" && -f "$FAKE_ROOT/boot-ready" ]]
[[ $ANDROID_SERIAL == emulator-5556 ]]
touch "$FAKE_ROOT/tests-started"
''')
            go.chmod(0o700)
            result = subprocess.run(["bash", str(client / "test.sh")], capture_output=True, text=True,
                env={**os.environ, "FAKE_ROOT": str(root), "GO": str(go),
                     "BOOT_FIRST": str(int(boot_first)), "BOOT_STATUS": str(boot_status),
                     "BUILD_STATUS": str(build_status)}, timeout=10)
            self.assertEqual(result.returncode, build_status or boot_status, result.stdout + result.stderr)
            self.assertEqual((root / "tests-started").exists(), not (build_status or boot_status),
                             "Tests ran without a successful build and ready emulator")
            if build_status:
                self.assertTrue((root / "boot-cancelled").exists(), "Failed build left emulator setup running")


class ImageCacheTests(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory()
        self.addCleanup(self.temporary.cleanup)
        self.root = Path(self.temporary.name)
        docker = self.root / "docker"
        docker.write_text('''#!/usr/bin/env python3
import json, os
from pathlib import Path
import sys
args = sys.argv[1:]
Path(os.environ['FAKE_DOCKER_LOG']).write_text(json.dumps({'args': args, 'stdin': sys.stdin.read()}))
if os.environ.get('FAKE_BUILD_FAILURE'): sys.exit(42)
if '--cache-to' in args:
    destination = args[args.index('--cache-to') + 1].split('dest=', 1)[1].split(',', 1)[0]
    Path(destination, 'index.json').write_text('new build cache')
''')
        docker.chmod(0o700)
        self.env = dict(os.environ, KEYPARCEL_DOCKER=str(docker), FAKE_DOCKER_LOG=str(self.root / "docker.json"))
        self.env.pop("KEYPARCEL_DOCKER_CACHE", None)
        self.script = Path(__file__).resolve().parents[1] / "build-image.sh"

    def build(self, scope="gtk"):
        return subprocess.run(["bash", str(self.script), scope, "-t", "test-image", "-"],
                              env=self.env, input="FROM scratch\n", capture_output=True, text=True)

    def test_normal_build_preserves_arguments_and_stdin(self):
        result = self.build()
        self.assertEqual(result.returncode, 0, result.stderr)
        called = json.loads((self.root / "docker.json").read_text())
        self.assertEqual(called, {"args": ["build", "-t", "test-image", "-"], "stdin": "FROM scratch\n"})

    def test_cached_build_imports_exports_and_loads_image(self):
        cache = self.root / "cache"
        old = cache / "gtk"
        old.mkdir(parents=True)
        (old / "index.json").write_text("previous build cache")
        (old / "unused-blob").touch()
        self.env["KEYPARCEL_DOCKER_CACHE"] = str(cache)
        result = self.build()
        self.assertEqual(result.returncode, 0, result.stderr)
        args = json.loads((self.root / "docker.json").read_text())["args"]
        self.assertEqual(args[:3], ["buildx", "build", "--load"])
        self.assertEqual(args[args.index("--cache-from") + 1], "type=local,src=" + str(old))
        self.assertEqual((old / "index.json").read_text(), "new build cache")
        self.assertFalse((old / "unused-blob").exists(), "Cache grows with obsolete layers")
        self.assertEqual(list(cache.iterdir()), [old], "Export left temporary cache directories")

    def test_failed_build_preserves_existing_cache(self):
        old = self.root / "cache/gtk"
        old.mkdir(parents=True)
        (old / "index.json").write_text("previous build cache")
        self.env.update(KEYPARCEL_DOCKER_CACHE=str(old.parent), FAKE_BUILD_FAILURE="1")
        result = self.build()
        self.assertEqual(result.returncode, 42)
        self.assertEqual((old / "index.json").read_text(), "previous build cache")
        self.assertEqual(list(old.parent.iterdir()), [old])

    def test_invalid_cache_scope_does_not_invoke_docker(self):
        self.assertNotEqual(self.build("../other-state").returncode, 0)
        self.assertFalse((self.root / "docker.json").exists())
