import gzip
import json
import plistlib
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch

import macos


class MacPackageTest(unittest.TestCase):
    def test_payload_matches_sources_and_declared_permissions(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            binary = root / 'keyparcel'
            binary.write_bytes(b'compiled native executable\x00\xff')
            info = macos.stage(root / 'payload', binary,
                               dict(version='0.1.0-rc.5', revision='a' * 40, target='macos-arm64'))
            payload = root / 'payload'
            actual = {p.relative_to(payload).as_posix() for p in payload.rglob('*') if p.is_file()}
            self.assertEqual(actual, set(macos.files()))
            self.assertEqual((payload / 'bin/keyparcel').read_bytes(), binary.read_bytes())
            self.assertEqual(gzip.decompress((payload / 'share/man/man1/keyparcel.1.gz').read_bytes()),
                             (macos.REPO / 'docs/keyparcel.1').read_bytes())
            self.assertEqual(json.loads((payload / macos.INFO).read_text()), info)
            self.assertEqual(set(info['files']), actual - {macos.INFO})
            for name, mode in macos.files().items():
                self.assertEqual((payload / name).stat().st_mode & 0o7777, mode, name)
                if name != macos.INFO:
                    self.assertEqual(info['files'][name], macos.digest(payload / name), name)
            # Payload is restricted to /usr/local, never a home or service path.
            self.assertTrue(all(not Path(name).is_absolute() and '..' not in Path(name).parts for name in actual))
            self.assertEqual(macos.PREFIX, Path('/usr/local'))

    def test_rejects_invalid_version_before_build_or_filesystem_changes(self):
        for version in ['../escape', '1.2', '1.2.3;whoami', '1.2.3-unknown.1']:
            with self.subTest(version=version), patch.object(macos.subprocess, 'run') as run:
                with self.assertRaises(ValueError):
                    macos.build(Path('/must-not-write'), version, 'arm64')
                run.assert_not_called()

    def test_package_restricts_native_arch_without_signing_or_install_scripts(self):
        for arch, machine in [('arm64', 'arm64'), ('amd64', 'x86_64')]:
            with self.subTest(arch=arch):
                commands, requirements = [], []
                def capture(command, **kwargs):
                    commands.append(command)
                    if command[0] == 'productbuild':
                        requirements.append(plistlib.loads(Path(command[command.index('--product') + 1]).read_bytes()))
                with patch.object(macos.subprocess, 'run', side_effect=capture):
                    macos.package(Path('/private/payload'), Path('/private/output.pkg'), '1.2.3', arch)
                self.assertEqual(requirements, [dict(arch=[machine], os=['13.0'])])
                self.assertTrue(all('--sign' not in command and '--scripts' not in command for command in commands))

    def test_refuses_cross_architecture_package(self):
        with patch.object(macos, 'revision', return_value='a' * 40), \
             patch.object(macos.platform, 'system', return_value='Darwin'), \
             patch.object(macos.platform, 'machine', return_value='x86_64'), \
             patch.object(macos.subprocess, 'run') as run:
            with self.assertRaisesRegex(ValueError, 'matching native'):
                macos.build(Path('/must-not-write'), '1.2.3', 'arm64')
            run.assert_not_called()

    def test_build_records_hash_of_actual_package_bytes(self):
        with tempfile.TemporaryDirectory() as temporary:
            output = Path(temporary)
            def build(command, **kwargs):
                Path(command[command.index('-o') + 1]).write_bytes(b'native executable')
            def package(root, destination, version, arch):
                destination.write_bytes(b'xar!native package')
            with patch.object(macos, 'revision', return_value='a' * 40), \
                 patch.object(macos.platform, 'system', return_value='Darwin'), \
                 patch.object(macos.platform, 'machine', return_value='arm64'), \
                 patch.object(macos.subprocess, 'run', side_effect=build), \
                 patch.object(macos.subprocess, 'check_output', return_value='go version fixture'), \
                 patch.object(macos, 'package', side_effect=package):
                macos.build(output, '1.2.3', 'arm64')
            info = json.loads((output / 'build-info.json').read_text())
            self.assertEqual(info['package_sha256'], macos.digest(output / info['package']))
            self.assertEqual(info['revision'], 'a' * 40)


if __name__ == '__main__':
    unittest.main()
