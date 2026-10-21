"""Targeted CI invocations must preserve full-build defaults and source checks."""
import json
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch

import build
import validate


class Selection(unittest.TestCase):
    def test_build_filter_keeps_default_targets_and_builds_selected_target_once(self):
        for selected in (None, *build.TARGETS):
            with self.subTest(target=selected), tempfile.TemporaryDirectory() as temporary:
                with patch.object(build, 'run') as run:
                    build.linux('1.2.3', Path(temporary), 'a' * 40, selected)
                commands = [call.args[0] for call in run.call_args_list]
                targets = list(build.TARGETS) if selected is None else [selected]
                self.assertEqual(len(commands), 2 * len(targets))
                for target, image_build, package in zip(targets, commands[::2], commands[1::2]):
                    self.assertIn('BASE=' + build.TARGETS[target], image_build)
                    self.assertEqual(package[-6:], ['python3', '/src/release/package.py', '1.2.3', 'a' * 40, target, '/out'])
                    self.assertTrue((Path(temporary) / target).is_dir())

    def test_distro_filter_selects_matching_runtime_without_changing_default_order(self):
        class ReachedRuntime(Exception):
            pass
        for distro, (target, base, _) in validate.DISTROS.items():
            with self.subTest(distro=distro), tempfile.TemporaryDirectory() as temporary:
                directory = Path(temporary) / target
                directory.mkdir()
                for client in ('cli', 'gtk'):
                    (directory / f'keyparcel-{client}.deb').touch()
                    (directory / f'keyparcel-{client}.rpm').touch()
                def run(command):
                    if 'release/Runtime.Dockerfile' in ' '.join(map(str, command)):
                        self.assertIn('BASE=' + base, command)
                        self.assertEqual(command[-1], str(directory))
                        raise ReachedRuntime
                with patch('sys.argv', ['validate.py', temporary, '--distro', distro]), \
                     patch.object(validate.os, 'getuid', return_value=1000), \
                     patch.object(validate, 'run', side_effect=run), self.assertRaises(ReachedRuntime):
                    validate.main()
        self.assertEqual(list(validate.DISTROS.values()), [
            ('ubuntu24.04-amd64', 'ubuntu:24.04', 'deb'),
            ('ubuntu24.04-amd64', 'debian:13', 'deb'),
            ('fedora44-x86_64', 'fedora:44', 'rpm')])

    def test_handoff_rejects_wrong_revision_version_or_target(self):
        with tempfile.TemporaryDirectory() as temporary:
            directory = Path(temporary) / '1.2.3' / 'ubuntu24.04-amd64'
            directory.mkdir(parents=True)
            info = dict(version='1.2.3', target=directory.name, revision='a' * 40)
            def write(info):
                (directory / 'build-info.json').write_text(json.dumps(info))
            write(info)
            validate.check_revision(directory, 'a' * 40)
            for field in info:
                with self.subTest(field=field), self.assertRaises(AssertionError):
                    write(dict(info, **{field: 'wrong'}))
                    validate.check_revision(directory, 'a' * 40)


if __name__ == '__main__':
    unittest.main()
