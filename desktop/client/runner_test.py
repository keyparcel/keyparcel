"""Host cleanup regressions require no GTK, display or Docker access."""
from pathlib import Path
import base64
import json
import os
import socket
import subprocess
import tempfile
import unittest
from unittest.mock import patch
import run
import fixture as fixtures


class HostCleanupTests(unittest.TestCase):
    def test_long_checkout_uses_short_private_runtime_and_cleans_it(self):
        for status in (0, 1):
            with self.subTest(status=status), tempfile.TemporaryDirectory() as temporary:
                directory = Path(temporary)
                session = directory / ("long-checkout-" * 10) / "build/session.regression"
                session.mkdir(parents=True)
                binary = directory / "dbus-run-session"
                record = directory / "runtime-path"
                binary.write_text('#!/bin/bash\nprintf "%s" "$XDG_RUNTIME_DIR" > "$RUNTIME_RECORD"\nexit "$SESSION_STATUS"\n')
                binary.chmod(0o700)
                result = subprocess.run(["bash", str(run.CLIENT / "test.sh"), str(session)],
                    env={**os.environ, "PATH": f"{directory}:{os.environ['PATH']}",
                        "KEYPARCEL_GTK_ATTACHED": str(session / "fixture"),
                        "RUNTIME_RECORD": str(record), "SESSION_STATUS": str(status)}, capture_output=True)
                self.assertEqual(result.returncode, status, result.stderr.decode())
                runtime = Path(record.read_text())
                self.assertFalse(runtime.exists(), "Desktop exit leaked its private runtime directory")
                # Prove the exact socket path fits the kernel's Unix socket limit.
                runtime.mkdir(mode=0o700)
                try:
                    with socket.socket(socket.AF_UNIX) as bus:
                        bus.bind(str(runtime / "a11y-bus"))
                    (runtime / "a11y-bus").unlink()
                finally:
                    runtime.rmdir()

    def test_log_capture_failure_still_removes_fixture(self):
        with tempfile.TemporaryDirectory() as temporary:
            with patch.object(run, "BUILD", Path(temporary)), patch.object(run, "prepare"), \
                 patch.object(run, "Fixture") as fixture, \
                 patch.object(run, "journey", side_effect=RuntimeError("failed UI check")), \
                 patch.object(run.subprocess, "run", side_effect=subprocess.TimeoutExpired("docker logs", 10)):
                fixture.return_value.container = "owned-server"
                with self.assertRaises(subprocess.TimeoutExpired):
                    run.test()
                fixture.return_value.down.assert_called_once()


class FixtureRegressionTests(unittest.TestCase):
    def test_installed_gui_cli_uses_only_owned_external_driver(self):
        with tempfile.TemporaryDirectory() as temporary:
            fixture = fixtures.Fixture(Path(temporary))
            fixture.client, fixture.driver = 'gui-with-no-cli', 'owned-cli-driver'
            fixture.binary = Path('/usr/bin/keyparcel')
            payload = '  exact 世界\n'.encode()
            with patch.object(fixtures, 'command', return_value=b'output') as command:
                self.assertEqual(fixture.cli('peer', 'send', '--stdin', input=payload), b'output')
                args = command.call_args.args[0]
                self.assertEqual(args[:len(fixtures.DOCKER)+4], fixtures.DOCKER + ['exec', '-i', 'owned-cli-driver', '/usr/bin/keyparcel'])
                self.assertEqual(command.call_args.kwargs['input'], payload)
            encoded = base64.b64encode(payload).decode()
            line = fixtures.CONTROL + json.dumps(['cli', ['peer', 'send', encoded, '--stdin']])
            with patch.object(fixture, 'cli', return_value=b'output') as cli:
                self.assertEqual(run.handle_control(fixture, line), b'output')
                cli.assert_called_once_with('peer', 'send', '--stdin', input=payload)
            fixture.driver = None
            with self.assertRaises(ValueError):
                run.handle_control(fixture, line)

    def test_creation_interruption_removes_every_created_resource(self):
        cases = [(kind, False) for kind in ("network", "server", "client", "driver")] + [("network", True)]
        for interrupted_kind, retry_cleanup in cases:
            with self.subTest(creation=interrupted_kind, retry=retry_cleanup), tempfile.TemporaryDirectory() as temporary:
                fixture = fixtures.Fixture(Path(temporary) / "owned")
                resources = {}

                def docker_command(args, **kwargs):
                    args = args[len(fixtures.DOCKER):]
                    if args[:2] == ["network", "create"] or args[0] == "run":
                        kind = "network" if args[0] == "network" else "container"
                        name = args[-1] if kind == "network" else args[args.index("--name") + 1]
                        planned = json.loads(fixture.state.read_text())
                        self.assertTrue(all(planned.values()), "All names must be persisted before creation")
                        self.assertIn(name, planned.values(), "Created resource absent from persisted plan")
                        owner = args[args.index("--label") + 1].split("=", 1)[1]
                        resources[kind, name] = owner
                        stage = "network" if kind == "network" else name.rsplit("-", 1)[1]
                        if stage == interrupted_kind:
                            raise KeyboardInterrupt("Created resource; Docker reply interrupted")
                        return b"docker-resource-id\n"
                    if args[0] == "rm":
                        del resources["container", args[-1]]
                    elif args[:2] == ["network", "rm"]:
                        self.assertFalse(any(kind == "container" for kind, _ in resources), "Live client prevents network removal")
                        del resources["network", args[-1]]
                    else:
                        self.fail(f"Unexpected Docker command: {args}")
                    return b""

                def inspect(args, **kwargs):
                    args = args[len(fixtures.DOCKER):]
                    self.assertEqual(args[1], "inspect")
                    owner = resources.get((args[0], args[-1]))
                    return subprocess.CompletedProcess(args, 0 if owner else 1,
                        stdout=(owner + "\n").encode() if owner else b"",
                        stderr=b"" if owner else b"No such resource")

                with patch.object(fixtures, "command", side_effect=docker_command), \
                     patch.object(fixtures.subprocess, "run", side_effect=inspect), \
                     patch.object(fixtures, "client_args", return_value=[]), \
                     patch.dict(os.environ, {"KEYPARCEL_GTK_DRIVER_IMAGE": "cli-image" if interrupted_kind == "driver" else ""}):
                    if retry_cleanup:
                        with patch.object(fixture, "down", side_effect=RuntimeError("Docker unavailable during cleanup")):
                            with self.assertRaises(RuntimeError):
                                fixture.start()
                        self.assertTrue(fixture.state.exists(), "Failed cleanup discarded the resource plan")
                        run.check_smoke_idle(fixture, "down")
                        fixture.down()
                    else:
                        with self.assertRaises(KeyboardInterrupt):
                            fixture.start()
                self.assertFalse(resources, "Interrupted startup leaked a created resource")
                self.assertFalse(fixture.directory.exists(), "Successful cleanup retained ownership state")

    def test_fresh_reset_does_not_follow_trust_file_symlinks(self):
        for case in ("existing_target", "dangling_target", "replacement_race"):
            with self.subTest(case=case), tempfile.TemporaryDirectory() as temporary:
                fixture = fixtures.Fixture(Path(temporary) / "owned")
                fixture.directory.mkdir()
                fixture.network, fixture.container, fixture.client = "network", "server", "client"
                fixture.save()
                for actor in ("desktop", "peer"):
                    directory = fixture.directory / actor
                    directory.mkdir()
                    (directory / "id_ed25519").write_text("fixture identity")
                    (directory / "id_ed25519.pub").write_text("ssh-ed25519 fixture-public-key")
                    (directory / "config.json").write_text('{"peers": {}}')
                    (directory / "known_hosts").write_text("fixture trust\n")
                target = Path(temporary) / "other-profile-known_hosts"
                if case != "dangling_target":
                    target.write_bytes(b"other profile's approved server\n")
                known = fixture.directory / "desktop/known_hosts"
                if case != "replacement_race":
                    known.unlink()
                    known.symlink_to(target)
                original_touch = Path.touch

                def touch(path, *args, **kwargs):
                    if path == known and case == "replacement_race":
                        path.symlink_to(target)
                    return original_touch(path, *args, **kwargs)

                with patch.object(fixture, "check_owner"), \
                     patch.object(fixtures, "command", return_value=b"fixture-host-key\n"), \
                     patch.object(fixtures.subprocess, "run"), patch.object(Path, "touch", touch):
                    if case == "replacement_race":
                        with self.assertRaises(FileExistsError):
                            fixture.start(fresh=True)
                    else:
                        fixture.start(fresh=True)
                        self.assertFalse(known.is_symlink(), "Fresh reset left a trust-file symlink")
                        self.assertEqual(known.read_bytes(), b"")
                        self.assertEqual(known.stat().st_mode & 0o777, 0o600)
                if case == "dangling_target":
                    self.assertFalse(target.exists(), "Fresh startup created the symlink's external target")
                else:
                    self.assertEqual(target.read_bytes(), b"other profile's approved server\n", "Reset modified another profile's trust")


if __name__ == "__main__":
    unittest.main()
