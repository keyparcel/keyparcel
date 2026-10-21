import hashlib
import json
import os
from pathlib import Path
import subprocess
import sys

from layout import files

assert os.getuid() != 0
for name, digest in json.loads((Path.home() / 'preserved.json').read_text()).items():
    assert hashlib.sha256(Path(name).read_bytes()).hexdigest() == digest, f'Uninstall changed user file: {name}'
info = json.loads(Path('/candidate/build-info.json').read_text())
for client in sys.argv[1:]:
    for name in files(client, info['target']):
        assert not (Path('/') / name).exists(), f'Package file left after uninstall: {name}'
for client in {'cli', 'gtk'} - set(sys.argv[1:]):
    binary = 'keyparcel' if client == 'cli' else 'keyparcel-gtk'
    if (Path('/usr/bin') / binary).exists():
        subprocess.run(['/usr/bin/' + binary, '--version'], check=True)
print('Removed client files are absent; remaining client works; user identity/config/saved bytes preserved')
