import gzip
import json
from pathlib import Path
import shutil
import subprocess
import tempfile
import unittest

import build
import layout
import package


class ClientLayout(unittest.TestCase):
    def test_independent_client_trees_and_deterministic_manual(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            binaries = root / 'bin'
            binaries.mkdir()
            for name in ('keyparcel', 'keyparcel-gtk'):
                (binaries / name).write_bytes(name.encode())
            info = dict(version='1.2.3-rc.1', revision='a'*40, target='test', go='test')
            stages = {}
            for client in layout.CLIENTS:
                stage = root / client
                package.stage_client(stage, binaries, build.REPO, info, client)
                stages[client] = stage
                actual = {str(p.relative_to(stage)): p.stat().st_mode & 0o7777 for p in stage.rglob('*') if p.is_file()}
                self.assertEqual(actual, layout.files(client, info['target']))
                doc = stage / f'usr/share/doc/keyparcel-{client}'
                self.assertEqual(json.loads((doc / 'build-info.json').read_text()), dict(info, client=client))
                for name in ('LICENSE', 'NOTICE', 'THIRD_PARTY_NOTICES.txt'):
                    self.assertEqual((doc / name).read_bytes(), (build.REPO / name).read_bytes())
            self.assertFalse(set(layout.files('cli', info['target'])) & set(layout.files('gtk', info['target'])), 'Clients share owned files')
            self.assertNotIn('usr/bin/keyparcel', layout.files('gtk', info['target']))
            self.assertNotIn('usr/bin/keyparcel-gtk', layout.files('cli', info['target']))
            manual = stages['cli'] / 'usr/share/man/man1/keyparcel.1.gz'
            source = (build.REPO / 'docs/keyparcel.1').read_bytes()
            self.assertEqual(gzip.decompress(manual.read_bytes()), source)
            self.assertEqual(manual.read_bytes()[4:8], b'\0'*4, 'gzip timestamp is not deterministic')
            repeat = root / 'repeat'
            package.stage_client(repeat, binaries, build.REPO, info, 'cli')
            self.assertEqual((repeat / 'usr/share/man/man1/keyparcel.1.gz').read_bytes(), manual.read_bytes())
            self.assertNotIn('usr/share/man/man1/keyparcel.1.gz', layout.files('gtk', info['target']))

    def test_zsh_completion_uses_each_distros_autoload_directory(self):
        for target, directory in [('ubuntu24.04-amd64', 'vendor-completions'),
                                  ('fedora44-amd64', 'site-functions')]:
            with self.subTest(target=target), tempfile.TemporaryDirectory() as temporary:
                root = Path(temporary)
                binary = root / 'keyparcel'
                binary.write_bytes(b'fixture')
                stage = root / 'cli'
                package.stage_client(stage, root, build.REPO, dict(target=target), 'cli')
                relative = f'usr/share/zsh/{directory}/_keyparcel'
                self.assertEqual((stage / relative).read_bytes(), (build.REPO / 'completions/_keyparcel').read_bytes())
                self.assertIn(relative, layout.files('cli', target))
                self.assertNotIn(relative, layout.files('gtk', target))

    @unittest.skipUnless(shutil.which('groff'), 'groff not installed; installed-package checks also render the manual')
    def test_manual_renders_without_warnings(self):
        result = subprocess.run(['groff', '-Tutf8', '-man', '-ww', build.REPO / 'docs/keyparcel.1'], capture_output=True)
        self.assertEqual(result.returncode, 0, result.stderr.decode())
        self.assertEqual(result.stderr, b'')
        self.assertTrue(result.stdout)


if __name__ == '__main__':
    unittest.main()
