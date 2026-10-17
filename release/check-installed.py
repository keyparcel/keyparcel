"""Run as an ordinary user in the runtime image, before and after uninstall."""
import hashlib
import json
import os
import shutil
import re
from pathlib import Path
import subprocess
import sys
import tarfile
import gi

gi.require_version('GdkPixbuf', '2.0')
from gi.repository import GdkPixbuf, Gio

assert os.getuid() != 0, 'Installed binaries must run as an ordinary user'
assert shutil.which('go') is None and shutil.which('gcc') is None, 'Runtime image contains a build toolchain'
assert not Path('/usr/local/bin/keyparcel').exists(), 'Development CLI must not be available'
info = json.loads(Path('/candidate/build-info.json').read_text())
contact = (subprocess.check_output(['dpkg-query', '-W', '-f=${Maintainer}', 'keyparcel'], text=True)
           if shutil.which('dpkg-query') else
           subprocess.check_output(['rpm', '-q', '--qf', '%{PACKAGER}', 'keyparcel'], text=True))
assert contact == 'keyparcel contributors <packetwright@proton.me>', 'Wrong package contact'
paths = {'usr/bin/keyparcel': 0o755, 'usr/bin/keyparcel-gtk': 0o755,
         'usr/share/applications/dev.keyparcel.desktop': 0o644,
         'usr/share/icons/hicolor/scalable/apps/dev.keyparcel.svg': 0o644,
         'usr/share/bash-completion/completions/keyparcel': 0o644,
         'usr/share/doc/keyparcel/build-info.json': 0o644,
         'usr/share/doc/keyparcel/README.md': 0o644,
         **{'usr/share/doc/keyparcel/' + name: 0o644 for name in ('LICENSE', 'NOTICE', 'THIRD_PARTY_NOTICES.txt', 'copyright')}}
archive, = Path('/candidate').glob('*.tar.gz')
with tarfile.open(archive) as tar:
    members = {m.name: m for m in tar.getmembers()}
    assert all(n == 'usr' or n.startswith('usr/') for n in members), 'Archive must contain only usr/'
    assert all(not m.issym() and not m.islnk() and '..' not in Path(m.name).parts for m in members.values()), 'Unsafe archive path/link'
    assert {m.name for m in members.values() if m.isfile()} == set(paths), 'Unexpected archive file layout'
    for relative, mode in paths.items():
        path = Path('/') / relative
        assert path.is_file() and not path.is_symlink(), path
        assert path.stat().st_mode & 0o7777 == mode, path
        assert path.stat().st_uid == path.stat().st_gid == 0, path
        member = members[relative]
        assert member.mode == mode and member.uid == member.gid == 0, relative
        assert tar.extractfile(member).read() == path.read_bytes(), f'Archive/package mismatch: {relative}'
for name in ['keyparcel', 'keyparcel-gtk']:
    result = subprocess.check_output(['/usr/bin/' + name, '--version'], text=True).strip()
    assert result == f'{name} {info["version"]} ({info["revision"]})', result
assert json.loads(Path('/usr/share/doc/keyparcel/build-info.json').read_text()) == info
assert 'Copyright 2026 keyparcel contributors' in Path('/usr/share/doc/keyparcel/NOTICE').read_text()
assert 'filippo.io/age' in Path('/usr/share/doc/keyparcel/THIRD_PARTY_NOTICES.txt').read_text()
readme = Path('/usr/share/doc/keyparcel/README.md').read_text()
links = re.findall(r'\]\(([^)]+)\)', readme)
assert links and all(link.startswith('https://github.com/keyparcel/keyparcel/blob/main/') for link in links), 'Installed guide has unusable relative links'
assert 'sudo install -Dm755' in readme and 'sudo rm -- /usr/local/bin/keyparcel' in readme, 'Archive install/removal commands missing'
subprocess.run(['desktop-file-validate', '/usr/share/applications/dev.keyparcel.desktop'], check=True)
launcher = Path('/usr/share/applications/dev.keyparcel.desktop').read_text()
assert 'Exec=keyparcel-gtk\n' in launcher and 'Icon=dev.keyparcel\n' in launcher
assert Gio.DesktopAppInfo.new('dev.keyparcel.desktop').get_icon().to_string() == 'dev.keyparcel', 'Launcher icon does not match the GTK application ID'
icon = GdkPixbuf.Pixbuf.new_from_file('/usr/share/icons/hicolor/scalable/apps/dev.keyparcel.svg')
assert (icon.get_width(), icon.get_height()) == (320, 320), 'Application SVG icon did not decode at its expected size'
subprocess.run(['bash', '-c', 'source /usr/share/bash-completion/completions/keyparcel; complete -p keyparcel'], check=True)
# Real identity and settings in the normal home location survive package removal.
home = Path.home()
config = home / '.config/keyparcel/config.json'
identity = home / '.ssh/id_ed25519'
config.parent.mkdir(parents=True, exist_ok=True)
identity.parent.mkdir(parents=True, exist_ok=True)
subprocess.run(['ssh-keygen', '-q', '-t', 'ed25519', '-N', '', '-f', str(identity)], check=True)
subprocess.run(['/usr/bin/keyparcel', 'setup', '--host', 'not-contacted', '--identity', str(identity), '--default'], check=True)
files = [config, identity, identity.with_suffix('.pub')]
(home / 'preserved.json').write_text(json.dumps({str(p): hashlib.sha256(p.read_bytes()).hexdigest() for p in files}))
print(f'Installed/archive paths, permissions, version, launcher, application icon and completion verified as uid {os.getuid()}')
