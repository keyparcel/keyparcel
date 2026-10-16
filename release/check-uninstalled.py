import hashlib
import json
import os
from pathlib import Path

assert os.getuid() != 0
for name, digest in json.loads((Path.home() / 'preserved.json').read_text()).items():
    assert hashlib.sha256(Path(name).read_bytes()).hexdigest() == digest, f'Uninstall changed user file: {name}'
for name in ['/usr/bin/keyparcel', '/usr/bin/keyparcel-gtk', '/usr/share/applications/dev.keyparcel.desktop',
             '/usr/share/icons/hicolor/scalable/apps/dev.keyparcel.svg',
             '/usr/share/bash-completion/completions/keyparcel', '/usr/share/doc/keyparcel/build-info.json',
             *['/usr/share/doc/keyparcel/' + name for name in ('README.md', 'LICENSE', 'NOTICE', 'THIRD_PARTY_NOTICES.txt', 'copyright')]]:
    assert not Path(name).exists(), f'Package file left after uninstall: {name}'
print('Uninstall removed package files and preserved user identity/settings')
