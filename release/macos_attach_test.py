import copy
import io
import json
from pathlib import Path
import subprocess
import tempfile
import unittest
import urllib.parse
import urllib.request
from unittest.mock import patch

import automation
import macos
import macos_attach as m

VERSION, TAG, SHA, OBJECT = '1.2.3-rc.1', 'v1.2.3-rc.1', 'a' * 40, 'b' * 40


class API:
    def __init__(self):
        self.release = dict(id=1, draft=True, tag_name=TAG, target_commitish=SHA, prerelease=True,
            html_url='https://github.com/example/draft', upload_url='https://uploads.github.com/repos/example/repo/releases/1/assets{?name}')
        self.assets, self.data, self.writes = {}, {}, []
        self.tag = dict(type='tag', sha=OBJECT)
        self.commit, self.fail_upload, self.fail_delete, self.fail_rename = SHA, None, False, False
        self.duplicate, self.concurrent, self.concurrent_asset = False, False, False

    def add(self, name, data):
        item = dict(id=len(self.data) + 1, name=name, state='uploaded', size=len(data), digest='sha256:' + m.sha(data))
        self.assets[name] = item
        self.data[item['id']] = data
        return copy.deepcopy(item)

    def download(self, asset_id):
        return self.data[asset_id]

    def request(self, method, path, **kwargs):
        if method == 'GET':
            if path.startswith('/git/ref/'):
                return dict(object=self.tag.copy())
            if path.startswith('/git/tags/'):
                return dict(object=dict(type='commit', sha=self.commit))
            if path.startswith('/releases?'):
                return [copy.deepcopy(self.release)] if self.release else []
            if '/assets?' in path:
                result = list(copy.deepcopy(self.assets).values())
                if self.duplicate:
                    result.append(result[0])
                if self.concurrent:
                    self.concurrent = False
                    self.release['draft'] = False
                if self.concurrent_asset:
                    self.concurrent_asset = False
                    self.add('concurrent.pkg', b'unexpected writer')
                return result
            return copy.deepcopy(self.release)
        self.writes.append((method, path, kwargs))
        if method == 'POST':
            name = urllib.parse.parse_qs(urllib.parse.urlsplit(path).query)['name'][0]
            if name in self.assets:
                raise AssertionError('No clobber allowed')
            result = self.add(name, kwargs['data'])
            if self.fail_upload == name:
                self.fail_upload = None
                raise OSError('Upload response lost after server accepted bytes')
            return result
        asset_id = int(path.rsplit('/', 1)[1])
        name = next(n for n, a in self.assets.items() if a['id'] == asset_id)
        if method == 'DELETE':
            del self.assets[name]
            if self.fail_delete:
                self.fail_delete = False
                raise OSError('Delete response lost after deletion')
            return None
        item = self.assets.pop(name)
        item['name'] = kwargs['body']['name']
        if item['name'] in self.assets:
            raise AssertionError('Rename cannot clobber')
        self.assets[item['name']] = item
        if self.fail_rename:
            self.fail_rename = False
            raise OSError('Rename response lost after completion')
        return copy.deepcopy(item)


def fixture(root):
    api = API()
    for target, names in automation.target_files(VERSION).items():
        info = dict(version=VERSION, revision=SHA, target=target)
        if target == 'android-arm64':
            info.update(versionCode=42, applicationId='io.github.keyparcel.keyparcel')
        api.add('build-info-' + target + '.json', json.dumps(info).encode())
        for name in names:
            api.add(name.replace('~', '-'), name.encode())
    api.add('SHA256SUMS', ''.join(f'{a["digest"][7:]}  {n}\n' for n, a in sorted(api.assets.items())).encode())
    for arch in ('arm64', 'amd64'):
        p = root / ('macos-cli-unsigned-' + arch)
        p.mkdir()
        name = f'keyparcel-cli-{VERSION}-macos-{arch}.pkg'
        data = b'xar!native fixture ' + arch.encode()
        (p / name).write_bytes(data)
        (p / 'build-info.json').write_text(json.dumps(dict(version=VERSION, revision=SHA,
            target='macos-' + arch, client='cli', identifier=macos.IDENTIFIER, package=name,
            tag=TAG, tag_object=OBJECT,
            package_sha256=m.sha(data), files={n: 'c' * 64 for n in macos.files() if n != macos.INFO})))
    return api


def attach(api, root):
    return m.attach(api, TAG, VERSION, SHA, 42, OBJECT, root)


class MacAttachment(unittest.TestCase):
    def test_pair_attachment_preserves_base_and_repeat_is_read_only(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp); api = fixture(root)
            before = {n: (a.copy(), api.download(a['id'])) for n, a in api.assets.items() if n != 'SHA256SUMS'}
            old_sum = api.assets['SHA256SUMS']['id']
            attach(api, root)
            self.assertTrue(api.release['draft'])
            for n, (item, data) in before.items():
                self.assertEqual(api.assets[n], item)
                self.assertEqual(api.download(item['id']), data)
            self.assertNotEqual(api.assets['SHA256SUMS']['id'], old_sum)
            self.assertEqual(len(api.assets), 18)
            self.assertNotIn(m.PENDING, api.assets)
            contents = {n: api.download(a['id']) for n, a in api.assets.items()}
            m.manifest(contents['SHA256SUMS'], set(contents) - {'SHA256SUMS'}, contents)
            api.writes.clear(); attach(api, root); self.assertEqual(api.writes, [])

    def test_all_invalid_inputs_rejected_before_any_write(self):
        changes = ['missing-release', 'published', 'wrong-target', 'wrong-version', 'wrong-tag-object',
                   'wrong-peeled-source', 'duplicate', 'missing-base', 'bad-base-hash', 'bad-checksum',
                   'bad-base-provenance', 'bad-package', 'missing-arch', 'bad-mac-source', 'existing-different',
                   'unexpected-asset', 'concurrent-publication', 'concurrent-asset',
                   'duplicate-checksum', 'bad-pending', 'wrong-mac-version', 'wrong-arch', 'extra-package',
                   'retagged-artifact']
        for change in changes:
            with self.subTest(change=change), tempfile.TemporaryDirectory() as tmp:
                root = Path(tmp); api = fixture(root)
                if change == 'missing-release': api.release = None
                elif change == 'published': api.release['draft'] = False
                elif change == 'wrong-target': api.release['target_commitish'] = 'c' * 40
                elif change == 'wrong-version': api.release['tag_name'] = 'v1.2.4'
                elif change == 'wrong-tag-object': api.tag['sha'] = 'c' * 40
                elif change == 'wrong-peeled-source': api.commit = 'c' * 40
                elif change == 'duplicate': api.duplicate = True
                elif change == 'missing-base': del api.assets['android-signing-certificate.txt']
                elif change == 'bad-base-hash': api.assets['android-signing-certificate.txt']['digest'] = 'sha256:' + '0' * 64
                elif change == 'bad-checksum': api.add('SHA256SUMS', b'0' * 64 + b'  android-signing-certificate.txt\n')
                elif change == 'bad-base-provenance': api.add('build-info-android-arm64.json', b'{}')
                elif change == 'bad-package': next((root / 'macos-cli-unsigned-arm64').glob('*.pkg')).write_bytes(b'xar!changed')
                elif change == 'missing-arch': (root / 'macos-cli-unsigned-amd64/build-info.json').unlink()
                elif change == 'bad-mac-source':
                    p = root / 'macos-cli-unsigned-arm64/build-info.json'
                    d = json.loads(p.read_text()); d['revision'] = 'c' * 40; p.write_text(json.dumps(d))
                elif change == 'existing-different': api.add(f'keyparcel-cli-{VERSION}-macos-arm64.pkg', b'different')
                elif change == 'unexpected-asset': api.add('other.pkg', b'unknown')
                elif change == 'concurrent-publication': api.concurrent = True
                elif change == 'concurrent-asset': api.concurrent_asset = True
                elif change == 'duplicate-checksum':
                    data = api.download(api.assets['SHA256SUMS']['id'])
                    api.add('SHA256SUMS', data + data.splitlines(keepends=True)[0])
                elif change == 'bad-pending': api.add(m.PENDING, b'unexpected transaction')
                elif change == 'wrong-mac-version':
                    p = root / 'macos-cli-unsigned-amd64/build-info.json'
                    data = json.loads(p.read_text()); data['version'] = '1.2.4'; p.write_text(json.dumps(data))
                elif change == 'wrong-arch':
                    p = root / 'macos-cli-unsigned-amd64/build-info.json'
                    data = json.loads(p.read_text()); data['target'] = 'macos-arm64'; p.write_text(json.dumps(data))
                elif change == 'extra-package': (root / 'macos-cli-unsigned-arm64/duplicate.pkg').write_bytes(b'xar!extra')
                elif change == 'retagged-artifact':
                    p = root / 'macos-cli-unsigned-amd64/build-info.json'
                    data = json.loads(p.read_text()); data['tag_object'] = 'c' * 40; p.write_text(json.dumps(data))
                with self.assertRaises((ValueError, FileNotFoundError)):
                    attach(api, root)
                self.assertEqual(api.writes, [], 'Invalid input must not mutate the draft')

    def test_response_loss_and_checksum_swap_recover_without_clobber(self):
        for failure in ('package-upload', 'pending-upload', 'checksum-delete', 'checksum-rename'):
            with self.subTest(failure=failure), tempfile.TemporaryDirectory() as tmp:
                root = Path(tmp); api = fixture(root)
                old = {n: a.copy() for n, a in api.assets.items() if n != 'SHA256SUMS'}
                if failure == 'package-upload': api.fail_upload = f'keyparcel-cli-{VERSION}-macos-arm64.pkg'
                elif failure == 'pending-upload': api.fail_upload = m.PENDING
                elif failure == 'checksum-delete': api.fail_delete = True
                else: api.fail_rename = True
                with self.assertRaises(OSError): attach(api, root)
                uploaded = {n: a.copy() for n, a in api.assets.items() if n.endswith('.pkg')}
                attach(api, root)
                for n, a in {**old, **uploaded}.items(): self.assertEqual(api.assets[n], a)
                self.assertEqual(len(api.assets), 18)
                self.assertNotIn(m.PENDING, api.assets)

    def test_local_tag_requires_annotated_exact_source_and_version(self):
        with tempfile.TemporaryDirectory() as tmp:
            repo = Path(tmp); (repo / 'release').mkdir()
            (repo / 'release/VERSION').write_text(VERSION)
            (repo / 'release/ANDROID_VERSION_CODE').write_text('42')
            def git(*args):
                return subprocess.check_output(['git', '-c', 'user.name=Fixture', '-c', 'user.email=fixture@example.invalid', *args], cwd=repo, stderr=subprocess.DEVNULL, text=True).strip()
            git('init'); git('add', '.'); git('commit', '-m', 'fixture'); source = git('rev-parse', 'HEAD')
            git('tag', TAG)
            with patch.object(m, 'REPO', repo), patch.object(automation, 'REPO', repo):
                with self.assertRaisesRegex(ValueError, 'annotated'): m.local_tag(TAG, source)
                git('tag', '-d', TAG); git('tag', '-a', TAG, '-m', 'fixture')
                self.assertEqual(m.local_tag(TAG, source), (VERSION, source, 42, git('rev-parse', TAG)))
                with self.assertRaises((ValueError, subprocess.CalledProcessError)): m.local_tag(TAG, 'c' * 40)

    def test_record_binds_native_source_to_tag_without_rewriting_package(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp); fixture(root)
            native = root / 'macos-cli-unsigned-arm64'
            package = next(native.glob('*.pkg')); before = package.read_bytes()
            m.record_tag(native, TAG, VERSION, SHA, OBJECT)
            info = json.loads((native / 'build-info.json').read_text())
            self.assertEqual((info['tag'], info['tag_object']), (TAG, OBJECT))
            self.assertEqual(package.read_bytes(), before)
            with self.assertRaises(ValueError): m.record_tag(native, TAG, VERSION, 'c' * 40, OBJECT)

    def test_attachment_workflow_is_tag_both_optin_only_with_narrow_write_scope(self):
        text = (macos.REPO / '.github/workflows/macos.yml').read_text()
        attach_job = text.split('  attach:\n')[1]
        self.assertIn("if: github.ref_type == 'tag' && inputs.architecture == 'both' && inputs.attach_to_draft", attach_job)
        self.assertIn('needs: native\n', attach_job)
        self.assertIn('permissions:\n      contents: write', attach_job)
        self.assertNotIn('contents: write', text.split('  attach:\n')[0])
        self.assertIn('group: release-${{ github.ref }}', attach_job)
        self.assertNotIn('cancel-in-progress: true', text)

    def test_download_does_not_forward_api_token_to_asset_redirect(self):
        api = automation.GitHub('example/repo', 'private-token')
        def receive(request, **kwargs):
            self.assertEqual(request.get_header('Authorization'), 'Bearer private-token')
            redirected = urllib.request.HTTPRedirectHandler().redirect_request(
                request, None, 302, '', {}, 'https://release-assets.githubusercontent.com/signed')
            self.assertIsNone(redirected.get_header('Authorization'))
            return io.BytesIO(b'asset bytes')
        with patch.object(automation.urllib.request, 'urlopen', side_effect=receive):
            self.assertEqual(api.download(12), b'asset bytes')

    def test_checksum_delete_accepts_empty_api_response(self):
        response = io.BytesIO()
        response.status = 204
        with patch.object(automation.urllib.request, 'urlopen', return_value=response):
            self.assertIsNone(automation.GitHub('example/repo', 'token').request('DELETE', '/releases/assets/12'))


if __name__ == '__main__':
    unittest.main()
