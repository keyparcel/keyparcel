"""SDK layout/cache checks using a fake package manager, never Android runtime."""
import os
from pathlib import Path
import shutil
import subprocess
import tempfile
import unittest


PACKAGES = ('platform-tools', 'emulator', 'platforms/android-35',
            'build-tools/35.0.0', 'ndk/29.0.14206865',
            'system-images/android-35/default/x86_64')
REPO = Path(__file__).resolve().parents[2]


class AndroidSDKTests(unittest.TestCase):
    def test_cold_install_warm_archive_and_tool_revision_change(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            repo = root / 'repo'
            (repo / 'tests/ci').mkdir(parents=True)
            (repo / 'android').mkdir()
            for name in ('prepare-android-sdk.sh', 'setup-android.sh'):
                shutil.copyfile(REPO / 'tests/ci' / name, repo / 'tests/ci' / name)
            shutil.copyfile(REPO / 'android/env.sh', repo / 'android/env.sh')
            hosted = root / 'hosted'
            tools = hosted / 'cmdline-tools/latest/bin'
            tools.mkdir(parents=True)
            manager = tools / 'sdkmanager'
            manager.write_text('''#!/usr/bin/env python3
import os, sys
from pathlib import Path
sdk = Path(sys.argv[1].split('=', 1)[1])
assert sdk == Path(os.environ['ANDROID_HOME'])
if '--licenses' not in sys.argv:
    for package in sys.argv[2:]:
        destination = sdk / package.replace(';', '/')
        properties = destination / 'source.properties'
        if not properties.exists() or package == 'emulator':
            assert not destination.is_symlink(), 'installer wrote to hosted SDK'
            destination.mkdir(parents=True, exist_ok=True)
            properties.write_text('Pkg.Revision = ' + os.environ.get('EMULATOR_REVISION', '1') + '\\n')
''')
            manager.chmod(0o755)
            # Omit the pinned NDK to exercise the fallback for a missing hosted package.
            for package in ('platforms/android-35', 'build-tools/35.0.0'):
                directory = hosted / package
                directory.mkdir(parents=True)
                (directory / 'source.properties').write_text('Pkg.Revision = 1\n')
            original = {str(p): p.read_bytes() for p in hosted.rglob('source.properties')}
            env = dict(os.environ, ANDROID_HOME=str(hosted), JAVA_HOME=str(root),
                       RUNNER_TEMP=str(root / 'runner'), GITHUB_ENV=str(root / 'env'),
                       GITHUB_OUTPUT=str(root / 'output'), ImageVersion='test-image')
            subprocess.run(['bash', str(repo / 'tests/ci/prepare-android-sdk.sh')],
                           env=env, check=True, capture_output=True)
            sdk = root / 'runner/keyparcel-android-sdk'
            env.update(ANDROID_HOME=str(sdk), ANDROID_SDK_ROOT=str(sdk))

            def install():
                (root / 'output').write_text('')
                subprocess.run(['bash', str(repo / 'tests/ci/setup-android.sh')],
                               env=env, check=True, capture_output=True)
                return (root / 'output').read_text()

            cold = install()
            self.assertFalse((sdk / 'cmdline-tools').is_symlink(),
                             'avdmanager would locate the hosted SDK instead of the owned image')
            self.assertEqual((sdk / 'cmdline-tools/latest/bin/sdkmanager').read_bytes(), manager.read_bytes())
            self.assertTrue((sdk / 'platforms/android-35').is_symlink())
            self.assertFalse((sdk / 'ndk/29.0.14206865').is_symlink())
            self.assertTrue(all((sdk / package / 'source.properties').is_file() for package in PACKAGES))
            # GNU tar preserves links rather than archiving the protected target tree.
            archive = root / 'sdk.tar'
            subprocess.run(['tar', '-cf', str(archive), '-C', str(sdk.parent), sdk.name], check=True)
            self.assertNotIn('keyparcel-android-sdk/platforms/android-35/source.properties',
                             subprocess.check_output(['tar', '-tf', str(archive)], text=True).splitlines())
            shutil.rmtree(sdk)
            subprocess.run(['tar', '-xf', str(archive), '-C', str(sdk.parent)], check=True)
            self.assertEqual(install(), cold, 'Warm SDK changed its revision cache key')
            env['EMULATOR_REVISION'] = '2'
            self.assertNotEqual(install(), cold, 'Updated emulator retained the old cache key')
            self.assertEqual({str(p): p.read_bytes() for p in hosted.rglob('source.properties')}, original,
                             'SDK setup modified a hosted package')
