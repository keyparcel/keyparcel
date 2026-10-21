#!/usr/bin/env python3
"""Privileged install checks ONLY on a disposable GitHub-hosted macOS runner."""
import argparse
import gzip
import json
import os
from pathlib import Path
import platform
import plistlib
import re
import subprocess
import tempfile
import xml.etree.ElementTree as ET

from build import REPO, revision
from macos import DOC, IDENTIFIER, INFO, PREFIX, digest, files


def run(*args, **kwargs):
    print('+', ' '.join(map(str, args)), flush=True)
    return subprocess.run(list(map(str, args)), check=True, cwd=REPO, **kwargs)


def output(*args, **kwargs):
    return subprocess.check_output(list(map(str, args)), cwd=REPO, **kwargs)


def receipt():
    result = subprocess.run(['/usr/sbin/pkgutil', '--pkg-info-plist', IDENTIFIER], capture_output=True)
    return plistlib.loads(result.stdout) if result.returncode == 0 else None


def uninstall():
    # Preflight rejects existing payload/receipt; only this job's declared files are ours.
    for name in files():
        path = PREFIX / name
        if os.path.lexists(path):
            run('sudo', '-n', '/bin/rm', '-f', path)
    if (PREFIX / DOC).exists():
        run('sudo', '-n', '/bin/rmdir', PREFIX / DOC)
    if receipt() is not None:
        run('sudo', '-n', '/usr/sbin/pkgutil', '--forget', IDENTIFIER)
    assert receipt() is None
    assert all(not os.path.lexists(PREFIX / name) for name in files())


def verify(info):
    installed = receipt()
    assert installed is not None and installed['pkg-version'] == info['version'], installed
    assert installed['volume'] == '/' and installed['install-location'].strip('/') == 'usr/local', installed
    owned = {name.removeprefix('./').rstrip('/') for name in output('/usr/sbin/pkgutil', '--files', IDENTIFIER, text=True).splitlines()}
    allowed = set(files()) | {str(parent) for name in files() for parent in Path(name).parents}
    assert owned <= allowed, f'Unexpected receipt paths: {owned - allowed}'
    actual = {name for name in owned if (PREFIX / name).is_file()}
    assert actual == set(files()), f'Unexpected receipt files: {actual ^ set(files())}'
    for name, mode in files().items():
        path = PREFIX / name
        assert path.is_file() and not path.is_symlink(), path
        stat = path.stat()
        assert stat.st_uid == stat.st_gid == 0 and stat.st_mode & 0o7777 == mode, path
        if name != INFO:
            assert digest(path) == info['files'][name], f'Installed bytes differ: {name}'
    installed_info = json.loads((PREFIX / INFO).read_text())
    assert installed_info == {key: value for key, value in info.items()
                              if key not in ('package', 'package_sha256', 'tag', 'tag_object')}
    expected_arch = {'macos-arm64': 'arm64', 'macos-amd64': 'x86_64'}[info['target']]
    assert output('/usr/bin/lipo', '-archs', PREFIX / 'bin/keyparcel', text=True).strip() == expected_arch
    assert output(PREFIX / 'bin/keyparcel', '--version', text=True).strip() == f'keyparcel {info["version"]} ({info["revision"]})'
    provenance = output('go', 'version', '-m', PREFIX / 'bin/keyparcel', text=True)
    assert f'vcs.revision={info["revision"]}' in provenance and 'vcs.modified=false' in provenance, provenance
    assert 'GOOS=darwin' in provenance and f'GOARCH={info["target"].removeprefix("macos-")}' in provenance
    assert gzip.decompress((PREFIX / 'share/man/man1/keyparcel.1.gz').read_bytes()) == (REPO / 'docs/keyparcel.1').read_bytes()


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('candidate', type=Path)
    parser.add_argument('scenarios', help='The same native journey selection used before installation')
    args = parser.parse_args()
    assert platform.system() == 'Darwin' and output('sw_vers', '-productVersion', text=True).startswith('26.')
    assert os.environ.get('GITHUB_ACTIONS') == 'true' and os.environ.get('RUNNER_ENVIRONMENT') == 'github-hosted'
    assert os.getuid() != 0, 'CLI and SSH fixtures must run as an ordinary user'
    candidate = args.candidate.resolve()
    info = json.loads((candidate / 'build-info.json').read_text())
    assert info['revision'] == revision(require_clean=True)
    assert info['version'] == (REPO / 'release/VERSION').read_text().strip()
    assert info['identifier'] == IDENTIFIER and info['target'] == 'macos-' + os.environ['EXPECTED_GOARCH']
    assert set(info['files']) == set(files()) - {INFO}
    package = candidate / info['package']
    assert package.parent == candidate and package.name.startswith('keyparcel-cli-') and package.suffix == '.pkg'
    assert digest(package) == info['package_sha256'], 'Package bytes differ from build provenance'
    signature = subprocess.run(['/usr/sbin/pkgutil', '--check-signature', str(package)], text=True, capture_output=True)
    assert signature.returncode != 0 and 'no signature' in (signature.stdout + signature.stderr).lower(), signature
    with tempfile.TemporaryDirectory() as temporary:
        expanded = Path(temporary) / 'expanded'
        run('/usr/sbin/pkgutil', '--expand-full', package, expanded)
        distribution = ET.parse(expanded / 'Distribution').getroot()
        expected_arch = {'macos-arm64': 'arm64', 'macos-amd64': 'x86_64'}[info['target']]
        assert distribution.find('options').get('hostArchitectures') == expected_arch
        assert not any(path.name == 'Scripts' and (path.is_file() or any(path.iterdir())) for path in expanded.rglob('*')), 'Package must not run install scripts'
    assert receipt() is None, 'Refuse to replace a pre-existing installation'
    assert not (PREFIX / DOC).exists(), 'Refuse to replace pre-existing documentation'
    assert all(not os.path.lexists(PREFIX / name) for name in files()), 'Refuse to replace unrelated files'
    neighbor = PREFIX / 'bin/keyparcel-package-test-neighbor'
    assert not os.path.lexists(neighbor)
    before_receipts = set(output('/usr/sbin/pkgutil', '--pkgs', text=True).splitlines())
    root = Path(os.environ['KEYPARCEL_E2E_LOCAL_ROOT'])
    with tempfile.TemporaryDirectory(prefix='preserved-', dir=root) as temporary:
        home = Path(temporary)
        env = dict(os.environ, HOME=str(home), ZDOTDIR=str(home), MANPATH=str(PREFIX / 'share/man') + ':', MANPAGER='cat', PAGER='cat', TERM='dumb')
        env.pop('FPATH', None)
        identity = home / '.ssh/id_ed25519'
        identity.parent.mkdir(mode=0o700)
        run('ssh-keygen', '-q', '-t', 'ed25519', '-N', '', '-f', identity, env=env)
        config = home / '.config/keyparcel/config.json'
        config.parent.mkdir(parents=True)
        config.write_text('{"default_host":"not-contacted","hosts":{},"peers":{}}\n')
        config.chmod(0o600)
        saved = home / 'saved.bin'
        saved.write_bytes(b'preserved download\x00\xff')
        preserved = {path: (digest(path), path.stat().st_mode & 0o7777) for path in (identity, identity.with_suffix('.pub'), config, saved)}
        sentinel = home / 'neighbor'
        sentinel.write_text('Unrelated neighboring file\n')
        touched = False
        neighbor_created = False
        try:
            run('sudo', '-n', '/usr/bin/install', '-m', '644', sentinel, neighbor)
            neighbor_created = True
            touched = True
            run('sudo', '-n', '/usr/sbin/installer', '-pkg', package, '-target', '/')
            verify(info)
            assert output('/usr/bin/man', '-w', 'keyparcel', env=env, text=True).strip() == str(PREFIX / 'share/man/man1/keyparcel.1.gz')
            rendered = output('/usr/bin/man', 'keyparcel', env=env, text=True)
            assert 'EXIT STATUS' in re.sub('.\x08', '', rendered)
            run('/bin/bash', '--noprofile', '--norc', '-c', f'source {PREFIX}/share/bash-completion/completions/keyparcel; complete -p keyparcel', env=env)
            run('/bin/zsh', '-i', '-c', '''print -r -- "Before explicit initialization: compdef=$+functions[compdef]";
print -rl -- $fpath;
(( ${fpath[(Ie)/usr/local/share/zsh/site-functions]} )) || exit 1;
autoload -Uz compinit; compinit -D -i </dev/null;
[[ $_comps[keyparcel] == _keyparcel ]]''', env=env)
            # Compiles the harness, never the CLI: TestMain requires this installed path.
            run('go', 'test', '-race', '-tags=e2e', '-count=1', '-timeout=12m', '-v', './tests/e2e',
                '-run', args.scenarios,
                env=dict(os.environ, KEYPARCEL_E2E_INSTALLED_CLI=str(PREFIX / 'bin/keyparcel')))
            uninstall()
            run('sudo', '-n', '/usr/sbin/installer', '-pkg', candidate / 'upgrade.fixture.pkg', '-target', '/')
            assert receipt()['pkg-version'] == '0'
            assert (PREFIX / DOC / 'README.md').read_text() == 'Test-only previous-package guide\n'
            run('sudo', '-n', '/usr/sbin/installer', '-pkg', package, '-target', '/')
            verify(info)
        finally:
            if touched:
                uninstall()
            if neighbor_created:
                assert neighbor.read_bytes() == sentinel.read_bytes(), 'Unrelated file changed'
                run('sudo', '-n', '/bin/rm', neighbor)
            for path, stamp in preserved.items():
                assert (digest(path), path.stat().st_mode & 0o7777) == stamp, f'User file changed: {path}'
            assert set(output('/usr/sbin/pkgutil', '--pkgs', text=True).splitlines()) == before_receipts
    print('Installed CLI, native transfers/clipboard, receipt upgrade, cleanup and user-data preservation passed')


if __name__ == '__main__':
    main()
