"""Run inside a distro builder: compile once and package that exact install tree."""
import json
import os
from pathlib import Path
import shutil
import subprocess
import sys
import tarfile
import tempfile

from version import package_version


def run(*args, **kwargs):
    return subprocess.check_output(args, text=True, **kwargs).strip()


def normalize(entry):
    entry.uid = entry.gid = 0
    entry.uname = entry.gname = 'root'
    return entry


version, revision, target, output = sys.argv[1:]
output = Path(output)
pkg_version = package_version(version)
flags = f'-X keyparcel/internal/buildinfo.Version={version} -X keyparcel/internal/buildinfo.Revision={revision}'
with tempfile.TemporaryDirectory() as temporary:
    root = Path(temporary)
    stage = root / 'stage'
    binary = stage / 'usr/bin'
    binary.mkdir(parents=True)
    env = dict(os.environ, CGO_ENABLED='0', GOCACHE=str(root / 'cache'), GOMODCACHE=str(root / 'mod'))
    subprocess.run(['go', 'build', '-trimpath', '-ldflags', flags, '-o', str(binary / 'keyparcel'), './cmd/keyparcel'], env=env, check=True)
    env['CGO_ENABLED'] = '1'
    subprocess.run(['go', 'build', '-trimpath', '-ldflags', flags, '-o', str(binary / 'keyparcel-gtk'), '.'], cwd='/src/desktop/client', env=env, check=True)
    for source, destination in [('completions/keyparcel.bash', 'usr/share/bash-completion/completions/keyparcel'),
                                ('release/dev.keyparcel.desktop', 'usr/share/applications/dev.keyparcel.desktop'),
                                ('assets/branding/app-icon.svg', 'usr/share/icons/hicolor/scalable/apps/dev.keyparcel.svg'),
                                ('release/INSTALLED.md', 'usr/share/doc/keyparcel/README.md'),
                                ('release/copyright', 'usr/share/doc/keyparcel/copyright'),
                                *[(name, 'usr/share/doc/keyparcel/' + name) for name in ('LICENSE', 'NOTICE', 'THIRD_PARTY_NOTICES.txt')]]:
        path = stage / destination
        path.parent.mkdir(parents=True, exist_ok=True)
        shutil.copyfile('/src/' + source, path)
    info = dict(version=version, revision=revision, target=target, go=run('go', 'version'))
    (stage / 'usr/share/doc/keyparcel/build-info.json').write_text(json.dumps(info, indent=2) + '\n')
    # Explicit modes; the host's umask must not make system-wide files private.
    for path in stage.rglob('*'):
        path.chmod(0o755 if path.is_dir() or path.parent == binary else 0o644)
    output.mkdir(parents=True, exist_ok=True)
    (output / 'build-info.json').write_text(json.dumps(info, indent=2) + '\n')
    with tarfile.open(output / f'keyparcel-{version}-{target}.tar.gz', 'w:gz') as archive:
        archive.add(stage / 'usr', arcname='usr', filter=lambda entry: normalize(entry))
    if target == 'ubuntu24.04-amd64':
        debian = root / 'debian'
        debian.mkdir()
        (debian / 'control').write_text('Source: keyparcel\n\nPackage: keyparcel\nArchitecture: amd64\nDescription: Encrypted text and file exchange over SSH\n')
        deps = run('dpkg-shlibdeps', '-O', str(binary / 'keyparcel-gtk'), cwd=root).removeprefix('shlibs:Depends=')
        control = stage / 'DEBIAN'
        control.mkdir()
        (control / 'control').write_text(f'''Package: keyparcel
Version: {pkg_version}-1
Architecture: amd64
Maintainer: keyparcel contributors <noreply@keyparcel.invalid>
Depends: {deps}, openssh-client, ca-certificates, adwaita-icon-theme, librsvg2-common
Description: Encrypted text and file exchange over SSH
 CLI and GTK client for asynchronous verified transfers through SSH/SFTP.
''')
        subprocess.run(['dpkg-deb', '--root-owner-group', '--build', str(stage), str(output / f'keyparcel_{pkg_version}-1_amd64.deb')], check=True)
        for earlier, later in [('1.0.0-alpha.1', '1.0.0-alpha.2'), ('1.0.0-alpha.2', '1.0.0-beta.1'),
                               ('1.0.0-beta.1', '1.0.0-rc.1'), ('1.0.0-rc.1', '1.0.0')]:
            run('dpkg', '--compare-versions', package_version(earlier), 'lt', package_version(later))
    else:
        for earlier, later in [('1.0.0-alpha.1', '1.0.0-alpha.2'), ('1.0.0-alpha.2', '1.0.0-beta.1'),
                               ('1.0.0-beta.1', '1.0.0-rc.1'), ('1.0.0-rc.1', '1.0.0')]:
            run('rpm', '--eval', '%{lua:assert(rpm.vercmp("' + package_version(earlier) + '","' + package_version(later) + '") < 0)}')
        spec = root / 'keyparcel.spec'
        spec.write_text(f'''Name: keyparcel
Version: {pkg_version}
Release: 1
Summary: Encrypted text and file exchange over SSH
License: Apache-2.0 AND BSD-2-Clause AND BSD-3-Clause
Requires: openssh-clients, ca-certificates, adwaita-icon-theme
%description
CLI and GTK client for asynchronous verified transfers through SSH/SFTP.
%install
mkdir -p %{{buildroot}}
cp -a {stage}/usr %{{buildroot}}/
%files
%defattr(-,root,root,-)
/usr/bin/keyparcel
/usr/bin/keyparcel-gtk
/usr/share/applications/dev.keyparcel.desktop
/usr/share/icons/hicolor/scalable/apps/dev.keyparcel.svg
/usr/share/bash-completion/completions/keyparcel
/usr/share/doc/keyparcel
''')
        subprocess.run(['rpmbuild', '-bb', '--define', f'_topdir {root}/rpm', '--define', '_build_id_links none', '--define', '__os_install_post %{nil}', str(spec)], check=True)
        packages = list((root / 'rpm/RPMS').rglob('*.rpm'))
        assert len(packages) == 1, packages
        shutil.copyfile(packages[0], output / packages[0].name)
