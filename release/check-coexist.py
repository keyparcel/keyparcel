"""Both independently owned packages can coexist without owning the same files."""
import json
import os
from pathlib import Path
import shutil
import subprocess

from layout import CLIENTS, files

assert os.getuid() != 0
info = json.loads(Path('/candidate/build-info.json').read_text())
owned = []
for client in CLIENTS:
    name = 'keyparcel-' + client
    for relative in files(client):
        path = Path('/') / relative
        assert path.is_file(), path
        owner = subprocess.check_output(['dpkg-query', '-S', str(path)] if shutil.which('dpkg-query')
                                        else ['rpm', '-qf', '--qf', '%{NAME}', str(path)], text=True).strip()
        assert owner == (name + ': ' + str(path) if shutil.which('dpkg-query') else name), (path, owner)
    binary = 'keyparcel' if client == 'cli' else 'keyparcel-gtk'
    assert subprocess.check_output(['/usr/bin/' + binary, '--version'], text=True).strip() == f'{binary} {info["version"]} ({info["revision"]})'
    owned.append(set(files(client)))
assert not owned[0] & owned[1], 'Client packages share owned files'
print('Both installed client packages work and have disjoint file ownership')
