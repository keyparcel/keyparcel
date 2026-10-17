import hashlib
import json
from pathlib import Path
import tempfile
import unittest
import urllib.parse
from unittest.mock import patch

import automation


SHA = 'a' * 40


class API:
    def __init__(self, release=None, assets=None, upload_changes=None):
        self.release = release
        self.assets = assets or []
        self.calls = []
        self.upload_changes = upload_changes or {}

    def request(self, method, path, **kwargs):
        self.calls.append((method, path, kwargs))
        if path.startswith('/git/ref/tags/'):
            return dict(object=dict(type='commit', sha=SHA))
        if method == 'GET' and path.startswith('/releases?'):
            return [self.release] if self.release else []
        if method == 'GET' and '/assets?' in path:
            return self.assets
        if method == 'GET':
            return self.release
        if path == '/releases':
            self.release = dict(kwargs['body'], id=1, html_url='https://github.com/example/release',
                                upload_url='https://uploads.github.com/repos/example/repo/releases/1/assets{?name}')
            return self.release
        name = urllib.parse.parse_qs(urllib.parse.urlsplit(path).query)['name'][0]
        # GitHub normalizes the tilde in package filenames to a period.
        return dict(dict(name=name.replace('~', '.'), size=len(kwargs['data']), state='uploaded',
                         digest='sha256:' + hashlib.sha256(kwargs['data']).hexdigest()), **self.upload_changes)


def draft(**changes):
    return dict(dict(draft=True, tag_name='v1.2.3-rc.1', target_commitish=SHA, prerelease=True, id=1,
                     upload_url='https://uploads.github.com/repos/example/repo/releases/1/assets{?name}',
                     html_url='https://github.com/example/release'), **changes)


def release_assets(root, version='1.2.3-rc.1'):
    names = [f'keyparcel_{version}-1_amd64.deb', f'keyparcel-{version}-1.x86_64.rpm',
             f'keyparcel-{version}-ubuntu24.04-amd64.tar.gz', f'keyparcel-{version}-fedora44-x86_64.tar.gz',
             f'keyparcel-{version}-arm64.apk', 'android-signing-certificate.txt',
             'build-info-android-arm64.json', 'build-info-ubuntu24.04-amd64.json', 'build-info-fedora44-x86_64.json']
    for name in names:
        (root / name).write_bytes(name.encode())
    (root / 'SHA256SUMS').write_text(''.join(f'{automation.digest(root / name)}  {name}\n' for name in sorted(names)))


class Automation(unittest.TestCase):
    def test_tags_match_exact_checked_out_version_and_source(self):
        for tag in ('1.2.3', 'v01.2.3', 'v1.2.3-rc', 'v1.2.3+build', 'v1.2.3\n', 'v1.2.3;echo'):
            with self.assertRaises(ValueError, msg=tag):
                automation.tag_version(tag)
        self.assertEqual(automation.tag_version('v1.2.3-rc.1'), '1.2.3-rc.1')
        with tempfile.TemporaryDirectory() as temporary:
            repo = Path(temporary)
            (repo / 'release').mkdir()
            (repo / 'release/VERSION').write_text('1.2.3-rc.1\n')
            (repo / 'release/ANDROID_VERSION_CODE').write_text('42\n')
            with patch.object(automation, 'REPO', repo), patch.object(automation.subprocess, 'check_output', return_value=SHA + '\n'):
                self.assertEqual(automation.tagged_source('v1.2.3-rc.1', SHA), ('1.2.3-rc.1', SHA, 42))
                with self.assertRaises(ValueError):
                    automation.tagged_source('v1.2.4', SHA)
            with patch.object(automation, 'REPO', repo), patch.object(automation.subprocess, 'check_output', side_effect=[SHA+'\n', 'b'*40+'\n']):
                with self.assertRaises(ValueError):
                    automation.tagged_source('v1.2.3-rc.1', SHA)

    def test_remote_annotated_tag_must_still_match(self):
        api = API()
        with patch.object(api, 'request', side_effect=[dict(object=dict(type='tag', sha='c'*40)),
                                                      dict(object=dict(type='commit', sha=SHA))]):
            automation.require_tag(api, 'v1.2.3', SHA)
        with patch.object(api, 'request', return_value=dict(object=dict(type='commit', sha='b'*40))):
            with self.assertRaises(ValueError):
                automation.require_tag(api, 'v1.2.3', SHA)

    def test_asset_allowlist_checksums_and_provenance(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            for target, names in {
                'ubuntu24.04-amd64': ['keyparcel_1.2.3~rc.1-1_amd64.deb', 'keyparcel-1.2.3-rc.1-ubuntu24.04-amd64.tar.gz'],
                'fedora44-x86_64': ['keyparcel-1.2.3~rc.1-1.x86_64.rpm', 'keyparcel-1.2.3-rc.1-fedora44-x86_64.tar.gz'],
                'android-arm64': ['keyparcel-1.2.3-rc.1-arm64.apk', 'android-signing-certificate.txt'],
            }.items():
                directory = root / target
                directory.mkdir()
                info = dict(version='1.2.3-rc.1', revision=SHA, target=target)
                if target == 'android-arm64':
                    info.update(versionCode=42, applicationId='dev.keyparcel.app')
                (directory / 'build-info.json').write_text(json.dumps(info))
                for name in names + ['diagnostic.log', 'unsigned.apk']:
                    (directory / name).write_text(name)
            output = root / 'assets'
            automation.assemble(root, output, '1.2.3-rc.1', SHA, 42)
            self.assertEqual(len(list(output.iterdir())), 10)
            lines = (output / 'SHA256SUMS').read_text().splitlines()
            self.assertEqual(len(lines), 9)
            for original, final, target in [
                ('keyparcel_1.2.3~rc.1-1_amd64.deb', 'keyparcel_1.2.3-rc.1-1_amd64.deb', 'ubuntu24.04-amd64'),
                ('keyparcel-1.2.3~rc.1-1.x86_64.rpm', 'keyparcel-1.2.3-rc.1-1.x86_64.rpm', 'fedora44-x86_64'),
            ]:
                self.assertEqual((output / final).read_bytes(), (root / target / original).read_bytes())
                self.assertFalse((output / original).exists(), 'Unsafe package filename staged for GitHub')
            self.assertTrue(all('~' not in path.name for path in output.iterdir()))
            for line in lines:
                digest, name = line.split('  ')
                self.assertEqual(digest, automation.digest(output / name))
                self.assertNotIn(name, ('diagnostic.log', 'unsigned.apk'))
            automation.publish(API(), 'v1.2.3-rc.1', SHA, output)
            with self.assertRaises(ValueError):
                automation.assemble(root, root / 'wrong-sha', '1.2.3-rc.1', 'b'*40, 42)
            with self.assertRaises(ValueError):
                automation.assemble(root, root / 'wrong-code', '1.2.3-rc.1', SHA, 43)

    def test_draft_only_prerelease_and_safe_reruns(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            release_assets(root)
            api = API()
            automation.publish(api, 'v1.2.3-rc.1', SHA, root)
            self.assertTrue(api.release['draft'])
            self.assertTrue(api.release['prerelease'])
            self.assertEqual(api.release['make_latest'], 'false')
            stable = API()
            with tempfile.TemporaryDirectory() as temporary_stable:
                stable_assets = Path(temporary_stable)
                release_assets(stable_assets, '1.2.3')
                automation.publish(stable, 'v1.2.3', SHA, stable_assets)
            self.assertFalse(stable.release['prerelease'])
            self.assertEqual(stable.release['make_latest'], 'false')
            uploaded = [dict(name=asset.name, size=asset.stat().st_size, state='uploaded',
                             digest='sha256:' + automation.digest(asset)) for asset in root.iterdir()]
            same = API(draft(), uploaded)
            automation.publish(same, 'v1.2.3-rc.1', SHA, root)
            self.assertTrue(all(method == 'GET' for method, _, _ in same.calls), 'Rerun replaced an identical asset')
            for release, existing in [
                (draft(draft=False), []), (draft(target_commitish='b'*40), []), (draft(prerelease=False), []),
                (draft(), [dict(uploaded[0], digest='sha256:wrong')]),
                (draft(), [dict(uploaded[0], state='starter')]),
                (draft(), [dict(uploaded[0], name='unrecognized.log')]),
            ]:
                api = API(release, existing)
                with self.assertRaises(ValueError):
                    automation.publish(api, 'v1.2.3-rc.1', SHA, root)
                self.assertTrue(all(method == 'GET' for method, _, _ in api.calls), 'Unsafe rerun mutated release')

    def test_upload_metadata_must_match_staged_assets(self):
        for change in [dict(name='renamed-by-server.txt'), dict(size=0),
                       dict(digest='sha256:wrong'), dict(state='starter')]:
            with self.subTest(change=change), tempfile.TemporaryDirectory() as temporary:
                root = Path(temporary)
                release_assets(root)
                with self.assertRaises(ValueError):
                    automation.publish(API(upload_changes=change), 'v1.2.3-rc.1', SHA, root)

    def test_invalid_checksum_set_rejected_before_any_api_calls(self):
        for defect in ('missing', 'extra', 'duplicate', 'wrong-digest', 'missing-asset', 'extra-asset', 'unsafe-name'):
            with self.subTest(defect=defect), tempfile.TemporaryDirectory() as temporary:
                root = Path(temporary)
                release_assets(root)
                manifest = root / 'SHA256SUMS'
                lines = manifest.read_text().splitlines(keepends=True)
                if defect == 'missing':
                    lines.pop()
                elif defect == 'extra':
                    lines.append('0'*64 + '  unexpected.log\n')
                elif defect == 'duplicate':
                    lines.append(lines[0])
                elif defect == 'wrong-digest':
                    lines[0] = '0'*64 + lines[0][64:]
                elif defect == 'missing-asset':
                    (root / 'android-signing-certificate.txt').unlink()
                elif defect == 'extra-asset':
                    (root / 'diagnostic.log').write_text('must not upload')
                elif defect == 'unsafe-name':
                    original = root / 'keyparcel_1.2.3-rc.1-1_amd64.deb'
                    original.rename(root / original.name.replace('-rc', '~rc'))
                    lines = [line.replace('1.2.3-rc.1-1_amd64.deb', '1.2.3~rc.1-1_amd64.deb') for line in lines]
                manifest.write_text(''.join(lines))
                api = API()
                with self.assertRaises(ValueError):
                    automation.publish(api, 'v1.2.3-rc.1', SHA, root)
                self.assertEqual(api.calls, [], 'Invalid checksum/assets set reached GitHub')


if __name__ == '__main__':
    unittest.main()
