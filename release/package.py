"""Run inside a distro builder: compile once and package independent clients."""
import gzip
import json
import os
from pathlib import Path
import shutil
import subprocess
import sys
import tarfile
import tempfile

from layout import CLIENTS, NOTICES
from version import package_version


def run(*args, **kwargs):
    return subprocess.check_output(args, text=True, **kwargs).strip()


def normalize(entry):
    entry.uid = entry.gid = 0
    entry.uname = entry.gname = 'root'
    return entry


def stage_client(stage, binaries, repo, info, client):
    name = 'keyparcel-' + client
    binary = 'keyparcel' if client == 'cli' else name
    sources = [(binaries / binary, 'usr/bin/' + binary),
               (repo / 'release/INSTALLED.md', f'usr/share/doc/{name}/README.md'),
               (repo / 'release/copyright', f'usr/share/doc/{name}/copyright'),
               *[(repo / leaf, f'usr/share/doc/{name}/' + leaf) for leaf in NOTICES if leaf != 'copyright']]
    if client == 'cli':
        sources.append((repo / 'completions/keyparcel.bash', 'usr/share/bash-completion/completions/keyparcel'))
    else:
        sources += [(repo / 'release/dev.keyparcel.desktop', 'usr/share/applications/dev.keyparcel.desktop'),
                    (repo / 'assets/branding/app-icon.svg', 'usr/share/icons/hicolor/scalable/apps/dev.keyparcel.svg')]
    for source, relative in sources:
        path = stage / relative
        path.parent.mkdir(parents=True, exist_ok=True)
        shutil.copyfile(source, path)
    if client == 'cli':
        manual = stage / 'usr/share/man/man1/keyparcel.1.gz'
        manual.parent.mkdir(parents=True, exist_ok=True)
        with manual.open('wb') as output:
            with gzip.GzipFile(filename='', mode='wb', fileobj=output, mtime=0) as compressed:
                compressed.write((repo / 'docs/keyparcel.1').read_bytes())
    (stage / f'usr/share/doc/{name}/build-info.json').write_text(json.dumps(dict(info, client=client), indent=2) + '\n')
    for path in stage.rglob('*'):
        path.chmod(0o755 if path.is_dir() or path.parent == stage / 'usr/bin' else 0o644)


def package(stage, name, version, target, output, root, gtk=False, legacy=False):
    pkg_version = package_version(version)
    conflicts = '' if legacy else 'Conflicts: keyparcel\n'
    if target == 'ubuntu24.04-amd64':
        deps = 'openssh-client, ca-certificates'
        if gtk:
            debian = root / 'debian'
            debian.mkdir(exist_ok=True)
            (debian / 'control').write_text(f'Source: {name}\n\nPackage: {name}\nArchitecture: amd64\nDescription: Encrypted text and file exchange over SSH\n')
            deps += ', ' + run('dpkg-shlibdeps', '-O', str(stage / 'usr/bin/keyparcel-gtk'), cwd=root).removeprefix('shlibs:Depends=')
            deps += ', adwaita-icon-theme, librsvg2-common'
        control = stage / 'DEBIAN'
        control.mkdir()
        (control / 'control').write_text(f'''Package: {name}
Version: {pkg_version}-1
Architecture: amd64
Maintainer: keyparcel contributors <packetwright@proton.me>
Depends: {deps}
{conflicts}Description: Encrypted text and file exchange over SSH
 {'GTK desktop client' if gtk else 'Command-line client'} for verified transfers through SSH/SFTP.
''')
        subprocess.run(['dpkg-deb', '--root-owner-group', '--build', str(stage), str(output)], check=True)
    else:
        deps = 'openssh-clients, ca-certificates' + (', adwaita-icon-theme, librsvg2' if gtk else '')
        spec = root / f'{name}.spec'
        paths = '\n'.join('/' + str(p.relative_to(stage)) for p in stage.rglob('*') if p.is_file())
        dirs = '\n'.join('%dir /' + str(p.relative_to(stage)) for p in (stage / 'usr/share/doc').iterdir())
        spec.write_text(f'''Name: {name}
Version: {pkg_version}
Release: 1
Summary: Encrypted text and file exchange over SSH
Packager: keyparcel contributors <packetwright@proton.me>
License: Apache-2.0 AND BSD-2-Clause AND BSD-3-Clause
Requires: {deps}
{conflicts}%description
{'GTK desktop client' if gtk else 'Command-line client'} for verified transfers through SSH/SFTP.
%install
mkdir -p %{{buildroot}}
cp -a {stage}/usr %{{buildroot}}/
%files
%defattr(-,root,root,-)
{dirs}
{paths}
''')
        top = root / ('rpm-' + name)
        subprocess.run(['rpmbuild', '-bb', '--define', f'_topdir {top}', '--define', '_build_id_links none', '--define', '__os_install_post %{nil}', str(spec)], check=True)
        packages = list((top / 'RPMS').rglob('*.rpm'))
        assert len(packages) == 1, packages
        shutil.copyfile(packages[0], output)


def build(version, revision, target, output):
    output.mkdir(parents=True, exist_ok=True)
    flags = f'-X keyparcel/internal/buildinfo.Version={version} -X keyparcel/internal/buildinfo.Revision={revision}'
    with tempfile.TemporaryDirectory() as temporary:
        root = Path(temporary)
        binaries = root / 'bin'
        binaries.mkdir()
        env = dict(os.environ, CGO_ENABLED='0', GOCACHE=str(root / 'cache'), GOMODCACHE=str(root / 'mod'))
        subprocess.run(['go', 'build', '-trimpath', '-ldflags', flags, '-o', str(binaries / 'keyparcel'), './cmd/keyparcel'], env=env, check=True)
        env['CGO_ENABLED'] = '1'
        subprocess.run(['go', 'build', '-trimpath', '-ldflags', flags, '-o', str(binaries / 'keyparcel-gtk'), '.'], cwd='/src/desktop/client', env=env, check=True)
        info = dict(version=version, revision=revision, target=target, go=run('go', 'version'))
        (output / 'build-info.json').write_text(json.dumps(info, indent=2) + '\n')
        for client in CLIENTS:
            stage = root / client
            stage_client(stage, binaries, Path('/src'), info, client)
            with tarfile.open(output / f'keyparcel-{client}-{version}-{target}.tar.gz', 'w:gz') as archive:
                archive.add(stage / 'usr', arcname='usr', filter=normalize)
            filename = (f'keyparcel-{client}_{package_version(version)}-1_amd64.deb' if target == 'ubuntu24.04-amd64'
                        else f'keyparcel-{client}-{package_version(version)}-1.x86_64.rpm')
            package(stage, 'keyparcel-' + client, version, target, output / filename, root, gtk=client == 'gtk')
        # A real, test-only old-name package proves conflicts and removal-first migration.
        # The non-package suffix keeps this fixture out of all distributed asset globs.
        legacy = root / 'legacy'
        (legacy / 'usr/bin').mkdir(parents=True)
        (legacy / 'usr/share/doc/keyparcel').mkdir(parents=True)
        for binary in ('keyparcel', 'keyparcel-gtk'):
            shutil.copyfile(binaries / binary, legacy / 'usr/bin' / binary)
            (legacy / 'usr/bin' / binary).chmod(0o755)
        (legacy / 'usr/share/doc/keyparcel/README').write_text('Old-name migration fixture; never distribute\n')
        package(legacy, 'keyparcel', version, target, output / 'legacy-package.fixture', root, gtk=True, legacy=True)
        for earlier, later in [('1.0.0-alpha.1', '1.0.0-alpha.2'), ('1.0.0-alpha.2', '1.0.0-beta.1'),
                               ('1.0.0-beta.1', '1.0.0-rc.1'), ('1.0.0-rc.1', '1.0.0')]:
            if target == 'ubuntu24.04-amd64':
                run('dpkg', '--compare-versions', package_version(earlier), 'lt', package_version(later))
            else:
                run('rpm', '--eval', '%{lua:assert(rpm.vercmp("' + package_version(earlier) + '","' + package_version(later) + '") < 0)}')


if __name__ == '__main__':
    build(*sys.argv[1:4], Path(sys.argv[4]))
