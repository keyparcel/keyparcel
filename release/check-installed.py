"""Run as an ordinary user against one independently installed client."""
import gzip
import hashlib
import json
import os
import re
import shutil
from pathlib import Path
import subprocess
import sys
import tarfile

from layout import CLIENTS, files

assert os.getuid() != 0, 'Installed binaries must run as an ordinary user'
assert shutil.which('go') is None and shutil.which('gcc') is None, 'Runtime image contains a build toolchain'
assert not Path('/usr/local/bin/keyparcel').exists(), 'Development CLI must not be available'
client = sys.argv[1]
assert client in CLIENTS
name = 'keyparcel-' + client
info = json.loads(Path('/candidate/build-info.json').read_text())
if shutil.which('dpkg-query'):
    contact = subprocess.check_output(['dpkg-query', '-W', '-f=${Maintainer}', name], text=True)
    owned = subprocess.check_output(['dpkg-query', '-L', name], text=True).splitlines()
    deps = subprocess.check_output(['dpkg-query', '-W', '-f=${Depends}', name], text=True)
    conflict = subprocess.check_output(['dpkg-query', '-W', '-f=${Conflicts}', name], text=True)
else:
    contact = subprocess.check_output(['rpm', '-q', '--qf', '%{PACKAGER}', name], text=True)
    owned = subprocess.check_output(['rpm', '-ql', name], text=True).splitlines()
    deps = subprocess.check_output(['rpm', '-q', '--requires', name], text=True)
    conflict = subprocess.check_output(['rpm', '-q', '--conflicts', name], text=True)
assert contact == 'keyparcel contributors <packetwright@proton.me>', 'Wrong package contact'
assert conflict.strip() == 'keyparcel', 'Missing old-package conflict'
assert 'keyparcel-cli' not in deps and 'keyparcel-gtk' not in deps, 'Artificial cross-client dependency'
if client == 'cli':
    assert not re.search(r'gtk|adwaita|librsvg', deps, re.I), 'CLI declares desktop dependencies'
    assert not Path('/usr/bin/keyparcel-gtk').exists(), 'CLI runtime contains GUI binary'
    assert not any(list(Path(directory).rglob('libgtk*')) for directory in ('/usr/lib', '/usr/lib64')), 'Headless runtime contains GTK'
else:
    assert 'libgtk' in deps, 'GTK runtime dependencies missing'
    assert not Path('/usr/bin/keyparcel').exists() and shutil.which('keyparcel') is None, 'GTK-only runtime has a CLI fallback'
paths = files(client)
owned_files = {p.lstrip('/') for p in owned if Path(p).is_file()}
assert owned_files == set(paths), f'Package ownership mismatch: missing {set(paths) - owned_files}, extra {owned_files - set(paths)}'
archive, = Path('/candidate').glob(name + '-*.tar.gz')
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
binary = 'keyparcel' if client == 'cli' else 'keyparcel-gtk'
result = subprocess.check_output(['/usr/bin/' + binary, '--version'], text=True).strip()
assert result == f'{binary} {info["version"]} ({info["revision"]})', result
doc = Path('/usr/share/doc') / name
assert json.loads((doc / 'build-info.json').read_text()) == dict(info, client=client)
assert 'Copyright 2026 keyparcel contributors' in (doc / 'NOTICE').read_text()
assert 'filippo.io/age' in (doc / 'THIRD_PARTY_NOTICES.txt').read_text()
readme = (doc / 'README.md').read_text()
links = re.findall(r'\]\(([^)]+)\)', readme)
assert links and all(link.startswith('https://github.com/keyparcel/keyparcel/blob/main/') for link in links), 'Installed guide has unusable links'
assert 'sudo install -Dm755' in readme and 'sudo rm -- /usr/local/bin/keyparcel' in readme, 'Archive install/removal commands missing'
if client == 'gtk':
    import gi
    gi.require_version('GdkPixbuf', '2.0')
    from gi.repository import GdkPixbuf, Gio
    subprocess.run(['desktop-file-validate', '/usr/share/applications/dev.keyparcel.desktop'], check=True)
    launcher = Path('/usr/share/applications/dev.keyparcel.desktop').read_text()
    assert 'Exec=keyparcel-gtk\n' in launcher and 'Icon=dev.keyparcel\n' in launcher
    assert Gio.DesktopAppInfo.new('dev.keyparcel.desktop').get_icon().to_string() == 'dev.keyparcel'
    icon = GdkPixbuf.Pixbuf.new_from_file('/usr/share/icons/hicolor/scalable/apps/dev.keyparcel.svg')
    assert (icon.get_width(), icon.get_height()) == (320, 320)
else:
    subprocess.run(['bash', '-c', 'source /usr/share/bash-completion/completions/keyparcel; complete -p keyparcel'], check=True)
    manual = '/usr/share/man/man1/keyparcel.1.gz'
    # Ubuntu minimal images divert man to a notice; use the installed real tool.
    man = shutil.which('man.REAL') or shutil.which('man')
    assert subprocess.check_output([man, '-w', 'keyparcel'], text=True).strip() == manual
    source = gzip.decompress(Path(manual).read_bytes())
    assert b'.TH KEYPARCEL 1' in source and b'SH EXIT STATUS' in source
    rendered = subprocess.check_output(['groff', '-Tutf8', '-man', '-ww'], input=source, stderr=subprocess.PIPE)
    assert b'RECEIVE OUTPUT AND RETENTION' in rendered and b'EXIT STATUS' in rendered
    print('Installed keyparcel(1) man path and roff render verified')
# Real identity, config and saved bytes must survive every removal/migration step.
home = Path.home()
config, identity, saved = home / '.config/keyparcel/config.json', home / '.ssh/id_ed25519', home / 'Downloads/saved.bin'
for path in (config, identity, saved):
    path.parent.mkdir(parents=True, exist_ok=True)
subprocess.run(['ssh-keygen', '-q', '-t', 'ed25519', '-N', '', '-f', str(identity)], check=True)
fingerprint = subprocess.check_output(['ssh-keygen', '-lf', str(identity) + '.pub'], text=True).split()[1]
config.write_text(json.dumps(dict(default_host='not-contacted', hosts={'not-contacted': dict(
    identity=str(identity), fingerprint=fingerprint, root='.local/share/keyparcel', discovery='.ssh/authorized_keys')}, peers={})))
config.chmod(0o600)
saved.write_bytes(b'preserved saved parcel\x00\xff')
user_files = [config, identity, identity.with_suffix('.pub'), saved]
(home / 'preserved.json').write_text(json.dumps({str(p): hashlib.sha256(p.read_bytes()).hexdigest() for p in user_files}))
print(f'{name}: independent installed/archive ownership, dependencies, metadata and startup verified as uid {os.getuid()}')
