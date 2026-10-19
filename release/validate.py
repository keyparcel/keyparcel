#!/usr/bin/env python3
"""Install independent candidates into fresh distros, reusing each client journey."""
import argparse
import os
from pathlib import Path
import subprocess
import uuid

from build import DOCKER, REPO, run
from layout import CLIENTS


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('candidates', type=Path, help='Version directory from build.py')
    args = parser.parse_args()
    if os.getuid() == 0:
        parser.error('Run as an ordinary user (Docker access required)')
    candidates = args.candidates.resolve()
    run(['python3', 'release/version_test.py'])
    run(['bash', 'tests/build-image.sh', 'sshd', '-t', 'keyparcel-test-sshd:local', 'tests/e2e'])
    for target, base, package in [('ubuntu24.04-amd64', 'ubuntu:24.04', 'deb'),
                                  ('ubuntu24.04-amd64', 'debian:13', 'deb'),
                                  ('fedora44-x86_64', 'fedora:44', 'rpm')]:
        directory = candidates / target
        packages = {client: list(directory.glob('keyparcel-' + client + '*.' + package)) for client in CLIENTS}
        assert all(len(paths) == 1 for paths in packages.values()), f'Expected one package per client: {directory}'
        images = {}
        for client in CLIENTS:
            image = 'keyparcel-installed:' + base.replace(':', '-') + '-' + client
            images[client] = image
            run(DOCKER + ['build', '--platform', 'linux/amd64', '--build-arg', 'BASE=' + base,
                          '--build-arg', 'CLIENT=' + client, '--build-arg', f'CLIENT_UID={os.getuid()}',
                          '--build-arg', f'CLIENT_GID={os.getgid()}', '-t', image,
                          '-f', str(REPO / 'release/Runtime.Dockerfile'), str(directory)])
            name = 'keyparcel-package-check-' + uuid.uuid4().hex
            user = f'{os.getuid()}:{os.getgid()}'
            def root(*command):
                return run(DOCKER + ['exec', name, *command])
            def check(script, *arguments):
                run(DOCKER + ['exec', '--user', user, name, 'python3', '/tmp/' + script, *arguments])
            def install(paths):
                root(*(['apt-get', 'install', '-y', '--no-install-recommends'] if package == 'deb'
                       else ['dnf', '--setopt=tsflags=', '--setopt=install_weak_deps=False', 'install', '-y']), *paths)
            def remove(*names):
                root(*(['apt-get', 'remove', '-y'] if package == 'deb' else ['dnf', 'remove', '-y']), *names)
            try:
                run(DOCKER + ['run', '-d', '--name', name, '--env', 'HOME=/tmp/package-user', image])
                root('bash', '-c', f'mkdir /tmp/package-user && chown {user} /tmp/package-user')
                for script in ('layout.py', 'check-installed.py', 'check-uninstalled.py', 'check-coexist.py'):
                    run(DOCKER + ['cp', 'release/' + script, name + ':/tmp/' + script])
                check('check-installed.py', client)
                env = dict(os.environ, KEYPARCEL_INSTALLED_IMAGE=image)
                if client == 'cli':
                    run(['go', 'test', '-tags=e2e', '-count=1', '-v', '-timeout=6m', './tests/e2e',
                         '-run', '^(TestReceiveTextDeletesPackage|TestFileBytesAndKeep|TestTamperingBeyondMetadataAndTruncation)$'], env=env)
                    remove('keyparcel-cli')
                    check('check-uninstalled.py', 'cli')
                    continue
                env['KEYPARCEL_GTK_DRIVER_IMAGE'] = images['cli']
                print('GTK-only installed journey with external CLI driver:', images['cli'], flush=True)
                run(['python3', 'desktop/client/run.py', 'installed'], env=env)
                paths = {c: '/candidate/' + packages[c][0].name for c in CLIENTS}
                install([paths['cli']])
                check('check-coexist.py')
                remove('keyparcel-gtk')
                check('check-uninstalled.py', 'gtk')
                install([paths['gtk']])
                remove('keyparcel-cli')
                check('check-uninstalled.py', 'cli')
                remove('keyparcel-gtk')
                check('check-uninstalled.py', 'cli', 'gtk')
                # Test an old-name package with real overlapping binaries, not just metadata.
                legacy = '/tmp/keyparcel-legacy.' + package
                root('cp', '/candidate/legacy-package.fixture', legacy)
                install([legacy])
                before = subprocess.check_output(DOCKER + ['exec', name, 'sha256sum', '/usr/bin/keyparcel', '/usr/bin/keyparcel-gtk'])
                for path in paths.values():
                    result = subprocess.run(DOCKER + ['exec', name, *(['dpkg', '-i'] if package == 'deb' else ['rpm', '-i']), path], capture_output=True)
                    assert result.returncode != 0 and b'conflict' in (result.stdout + result.stderr).lower(), 'Old package did not cause an explicit conflict'
                    after = subprocess.check_output(DOCKER + ['exec', name, 'sha256sum', '/usr/bin/keyparcel', '/usr/bin/keyparcel-gtk'])
                    assert before == after, 'Rejected installation overwrote old package binaries'
                remove('keyparcel')
                install(list(paths.values()))
                check('check-coexist.py')
                remove('keyparcel-cli', 'keyparcel-gtk')
                check('check-uninstalled.py', 'cli', 'gtk')
                print('Removal-first migration, coexistence and independent uninstall verified:', base, flush=True)
            finally:
                subprocess.run(DOCKER + ['rm', '-f', name], check=False)


if __name__ == '__main__':
    main()
