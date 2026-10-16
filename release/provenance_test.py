from pathlib import Path
import re
import subprocess
import tempfile
import unittest
from unittest.mock import patch

import build


class Provenance(unittest.TestCase):
    def test_generated_logs_do_not_hide_source_changes(self):
        with tempfile.TemporaryDirectory() as temporary:
            repo = Path(temporary)
            (repo / '.gitignore').write_text((build.REPO / '.gitignore').read_text())
            tracked = repo / 'source.go'
            tracked.write_text('package source\n')
            def git(*args):
                return subprocess.check_output(['git', *args], cwd=repo, text=True, stderr=subprocess.DEVNULL).strip()
            git('init')
            git('add', '.')
            git('-c', 'user.name=Candidate test', '-c', 'user.email=test@example.invalid', 'commit', '-m', 'fixture')
            sha = git('rev-parse', 'HEAD')
            with patch.object(build, 'REPO', repo):
                self.assertEqual(build.revision(), sha)
                logs = repo / 'release/out'
                logs.mkdir(parents=True)
                for name in ['candidate-build.log', 'candidate-android.log', 'candidate-validation.log']:
                    (logs / name).write_text('generated log\n')
                self.assertEqual(build.revision(require_clean=True), sha, 'Generated CI logs taint clean provenance')
                untracked = repo / 'untracked.go'
                untracked.write_text('package source\n')
                self.assertEqual(build.revision(), sha + '-dirty', 'Untracked source change was hidden')
                with self.assertRaises(ValueError):
                    build.revision(require_clean=True)
                untracked.unlink()
                tracked.write_text('package changed\n')
                self.assertEqual(build.revision(), sha + '-dirty', 'Tracked source change was hidden')
                with self.assertRaises(ValueError):
                    build.revision(require_clean=True)

    def test_installed_doc_links_resolve_to_repository_files(self):
        guide = (build.REPO / 'release/INSTALLED.md').read_text()
        links = re.findall(r'\]\(([^)]+)\)', guide)
        self.assertTrue(links)
        prefix = 'https://github.com/keyparcel/keyparcel/blob/main/'
        for link in links:
            self.assertTrue(link.startswith(prefix), link)
            self.assertTrue((build.REPO / link.removeprefix(prefix)).is_file(), link)


if __name__ == '__main__':
    unittest.main()
