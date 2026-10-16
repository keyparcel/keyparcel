#!/usr/bin/env python3
"""Install exact candidates into fresh distro images, then reuse existing journeys."""
import argparse
import os
from pathlib import Path
import subprocess
import uuid

from build import DOCKER, REPO, run


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
        packages = list(directory.glob('*.' + package))
        assert len(packages) == 1, f'Expected exactly one {package}: {directory}'
        image = 'keyparcel-installed:' + base.replace(':', '-')
        run(DOCKER + ['build', '--platform', 'linux/amd64', '--build-arg', 'BASE=' + base,
                      '--build-arg', f'CLIENT_UID={os.getuid()}', '--build-arg', f'CLIENT_GID={os.getgid()}',
                      '-t', image, '-f', str(REPO / 'release/Runtime.Dockerfile'), str(directory)])
        name = 'keyparcel-package-check-' + uuid.uuid4().hex
        try:
            run(DOCKER + ['run', '-d', '--name', name, '--env', 'HOME=/tmp/package-user', image])
            run(DOCKER + ['exec', name, 'bash', '-c', f'mkdir /tmp/package-user && chown {os.getuid()}:{os.getgid()} /tmp/package-user'])
            run(DOCKER + ['cp', 'release/check-installed.py', name + ':/tmp/check-installed.py'])
            run(DOCKER + ['exec', '--user', f'{os.getuid()}:{os.getgid()}', name, 'python3', '/tmp/check-installed.py'])
            # Selected unchanged CLI assertions and a short path through the GTK journey.
            env = dict(os.environ, KEYPARCEL_INSTALLED_IMAGE=image)
            run(['go', 'test', '-tags=e2e', '-count=1', '-v', '-timeout=6m', './tests/e2e',
                 '-run', '^(TestReceiveTextDeletesPackage|TestFileBytesAndKeep|TestTamperingBeyondMetadataAndTruncation)$'], env=env)
            run(['python3', 'desktop/client/run.py', 'installed'], env=env)
            remove = 'apt-get remove -y keyparcel' if package == 'deb' else 'dnf remove -y keyparcel'
            run(DOCKER + ['exec', name, 'bash', '-c', remove])
            run(DOCKER + ['cp', 'release/check-uninstalled.py', name + ':/tmp/check-uninstalled.py'])
            run(DOCKER + ['exec', '--user', f'{os.getuid()}:{os.getgid()}', name, 'python3', '/tmp/check-uninstalled.py'])
        finally:
            subprocess.run(DOCKER + ['rm', '-f', name], check=False)


if __name__ == '__main__':
    main()
