#!/usr/bin/env python3
"""Candidate builds only: no tags, signing, checksums or publication."""
import argparse
import json
import os
from pathlib import Path
import shlex
import shutil
import subprocess
import sys

from version import android_code, package_version

REPO = Path(__file__).resolve().parents[1]
DOCKER = shlex.split(os.environ.get('KEYPARCEL_DOCKER', 'docker'))
TARGETS = {'ubuntu24.04-amd64': 'ubuntu:24.04', 'fedora44-x86_64': 'fedora:44'}


def run(args, **kwargs):
    print('+', shlex.join(map(str, args)), flush=True)
    return subprocess.run(list(map(str, args)), check=True, cwd=REPO, **kwargs)


def revision(require_clean=False):
    sha = subprocess.check_output(['git', 'rev-parse', 'HEAD'], cwd=REPO, text=True).strip()
    dirty = subprocess.check_output(['git', 'status', '--porcelain'], cwd=REPO, text=True).strip()
    if require_clean and dirty:
        raise ValueError('Candidate CI requires a clean source checkout')
    return sha + ('-dirty' if dirty else '')


def linux(version, output, source, target=None):
    targets = TARGETS if target is None else {target: TARGETS[target]}
    for target, base in targets.items():
        destination = output / target
        destination.mkdir(parents=True, exist_ok=True)
        image = 'keyparcel-release-builder:' + target
        run(DOCKER + ['build', '--platform', 'linux/amd64', '--build-arg', 'BASE=' + base,
                      '-t', image, '-f', 'release/Builder.Dockerfile', 'release'])
        run(DOCKER + ['run', '--rm', '--platform', 'linux/amd64', '--user', f'{os.getuid()}:{os.getgid()}',
                      '--mount', f'type=bind,src={REPO},dst=/src,readonly',
                      '--mount', f'type=bind,src={destination},dst=/out', image,
                      'python3', '/src/release/package.py', version, source, target, '/out'])


def android(version, code, output, source):
    # A private project copy keeps Gradle and gomobile output out of emulator builds.
    destination = output / 'android-arm64'
    destination.mkdir(parents=True, exist_ok=True)
    work = destination / 'work'
    if work.exists():
        shutil.rmtree(work)
    shutil.copytree(REPO / 'android/client', work / 'client',
                    ignore=shutil.ignore_patterns('build', '.gradle', '.kotlin', 'local.properties'))
    shutil.copytree(REPO / 'android/src', work / 'src')
    for name in ('LICENSE', 'NOTICE', 'THIRD_PARTY_NOTICES.txt'):
        shutil.copyfile(REPO / name, destination / name)
    env = dict(os.environ, KEYPARCEL_RELEASE_VERSION=version, KEYPARCEL_RELEASE_CODE=str(code),
               KEYPARCEL_RELEASE_REVISION=source, KEYPARCEL_RELEASE_ANDROID_DIR=str(destination))
    run(['bash', 'release/android.sh'], env=env)
    (destination / 'build-info.json').write_text(json.dumps(dict(version=version, versionCode=code,
             revision=source, target='android-arm64', applicationId='io.github.keyparcel.keyparcel'), indent=2) + '\n')
    run([sys.executable, 'release/check-apk.py', destination])


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('target', choices=['linux', 'android', 'all'])
    parser.add_argument('--version', required=True)
    parser.add_argument('--linux-target', choices=TARGETS, help='Build only this Linux target (default: both)')
    parser.add_argument('--android-code', type=android_code)
    parser.add_argument('--require-clean', action='store_true', help='Reject uncommitted source changes (CI)')
    parser.add_argument('--output', type=Path, default=REPO / 'release/out')
    args = parser.parse_args()
    package_version(args.version)
    if args.linux_target and args.target == 'android':
        parser.error('--linux-target requires linux or all')
    if args.target != 'linux' and args.android_code is None:
        parser.error('--android-code is required for Android')
    source = revision(require_clean=args.require_clean)
    output = args.output.resolve() / args.version
    if args.target in ('linux', 'all'):
        linux(args.version, output, source, args.linux_target)
    if args.target in ('android', 'all'):
        android(args.version, args.android_code, output, source)


if __name__ == '__main__':
    main()
