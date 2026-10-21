#!/usr/bin/env python3
"""Build unsigned native CLI test packages; never sign or publish them."""
import argparse
import gzip
import hashlib
import json
import os
from pathlib import Path
import platform
import plistlib
import shutil
import subprocess
import tempfile

from build import REPO, revision
from layout import NOTICES
from version import package_version

PREFIX = Path('/usr/local')
IDENTIFIER = 'io.github.keyparcel.keyparcel.cli'
MACHINES = {'arm64': 'arm64', 'amd64': 'x86_64'}
DOC = 'share/doc/keyparcel-cli/'
INFO = DOC + 'build-info.json'


def files():
    return {'bin/keyparcel': 0o755,
            'share/man/man1/keyparcel.1.gz': 0o644,
            'share/bash-completion/completions/keyparcel': 0o644,
            'share/zsh/site-functions/_keyparcel': 0o644,
            **{DOC + name: 0o644 for name in (*NOTICES, 'README.md', 'build-info.json')}}


def digest(path):
    return hashlib.sha256(path.read_bytes()).hexdigest()


def stage(root, binary, info, repo=REPO):
    sources = {binary: 'bin/keyparcel',
               repo / 'completions/keyparcel.bash': 'share/bash-completion/completions/keyparcel',
               repo / 'completions/_keyparcel': 'share/zsh/site-functions/_keyparcel',
               repo / 'release/MACOS.md': DOC + 'README.md',
               repo / 'release/copyright': DOC + 'copyright',
               **{repo / name: DOC + name for name in NOTICES if name != 'copyright'}}
    for source, relative in sources.items():
        destination = root / relative
        destination.parent.mkdir(parents=True, exist_ok=True)
        shutil.copyfile(source, destination)
    manual = root / 'share/man/man1/keyparcel.1.gz'
    manual.parent.mkdir(parents=True, exist_ok=True)
    with manual.open('wb') as output:
        with gzip.GzipFile(filename='', mode='wb', fileobj=output, mtime=0) as zipped:
            zipped.write((repo / 'docs/keyparcel.1').read_bytes())
    info = dict(info, files={name: digest(root / name) for name in files() if name != INFO})
    (root / INFO).write_text(json.dumps(info, indent=2) + '\n')
    for path in root.rglob('*'):
        path.chmod(0o755 if path.is_dir() else files()[path.relative_to(root).as_posix()])
    return info


def package(root, output, version, arch):
    with tempfile.TemporaryDirectory() as temporary:
        component = Path(temporary) / 'component.pkg'
        requirements = Path(temporary) / 'requirements.plist'
        requirements.write_bytes(plistlib.dumps(dict(arch=[MACHINES[arch]], os=['26.0'])))
        subprocess.run(['pkgbuild', '--root', str(root), '--identifier', IDENTIFIER,
                        '--version', version, '--install-location', str(PREFIX),
                        '--ownership', 'recommended', str(component)], check=True)
        # Declare the architecture instead of productbuild's default universal list.
        subprocess.run(['productbuild', '--package', str(component), '--product', str(requirements), str(output)], check=True)


def build(output, version, arch, require_clean=False, upgrade_fixture=False):
    package_version(version)  # Share the existing explicit-version validation.
    source = revision(require_clean=require_clean)
    expected = MACHINES[arch]
    if platform.system() != 'Darwin' or platform.machine() != expected:
        raise ValueError('Build each package on its matching native macOS architecture')
    output.mkdir(parents=True, exist_ok=True)
    with tempfile.TemporaryDirectory() as temporary:
        root = Path(temporary)
        binary = root / 'keyparcel'
        flags = f'-X keyparcel/internal/buildinfo.Version={version} -X keyparcel/internal/buildinfo.Revision={source}'
        subprocess.run(['go', 'build', '-buildvcs=true', '-trimpath', '-ldflags', flags,
                        '-o', str(binary), './cmd/keyparcel'], cwd=REPO,
                       env=dict(os.environ, CGO_ENABLED='0', GOOS='darwin', GOARCH=arch), check=True)
        payload = root / 'payload'
        info = stage(payload, binary, dict(version=version, revision=source,
                     target='macos-' + arch, client='cli', identifier=IDENTIFIER,
                     go=subprocess.check_output(['go', 'version'], text=True).strip()))
        filename = f'keyparcel-cli-{version}-macos-{arch}.pkg'
        package(payload, output / filename, version, arch)
        (output / 'build-info.json').write_text(json.dumps(dict(info, package=filename,
            package_sha256=digest(output / filename)), indent=2) + '\n')
        if upgrade_fixture:
            # No previous Mac release exists: exercise real receipt upgrade/replacement,
            # without claiming compatibility with a historical released binary.
            (payload / DOC / 'README.md').write_text('Test-only previous-package guide\n')
            package(payload, output / 'upgrade.fixture.pkg', '0', arch)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--version', default=(REPO / 'release/VERSION').read_text().strip())
    parser.add_argument('--arch', required=True, choices=['arm64', 'amd64'])
    parser.add_argument('--output', type=Path, required=True)
    parser.add_argument('--require-clean', action='store_true')
    parser.add_argument('--upgrade-fixture', action='store_true')
    args = parser.parse_args()
    build(args.output.resolve(), args.version, args.arch, args.require_clean, args.upgrade_fixture)


if __name__ == '__main__':
    main()
