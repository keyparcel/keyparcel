"""Tag validation, checked release assets and draft-only GitHub upload."""
import argparse
import hashlib
import json
import os
from pathlib import Path
import re
import shutil
import subprocess
import urllib.parse
import urllib.request

from build import REPO
from version import android_code, package_version


def tag_version(tag):
    if not tag.startswith('v'):
        raise ValueError('Release tag must start with v')
    package_version(tag[1:])
    return tag[1:]


def tagged_source(tag, sha):
    version = tag_version(tag)
    if not re.fullmatch(r'[0-9a-f]{40}', sha):
        raise ValueError('Expected a full source SHA')
    def git(ref):
        return subprocess.check_output(['git', 'rev-parse', ref + '^{commit}'], cwd=REPO, text=True).strip()
    revision = git('HEAD')
    if revision != git('refs/tags/' + tag) or revision != git(sha):
        raise ValueError('Checkout, event SHA and tag must identify the same commit')
    if (REPO / 'release/VERSION').read_text().strip() != version:
        raise ValueError('Tag must match release/VERSION at the tagged commit')
    code = android_code((REPO / 'release/ANDROID_VERSION_CODE').read_text().strip())
    return version, revision, code


def digest(path):
    with path.open('rb') as file:
        return hashlib.file_digest(file, 'sha256').hexdigest()


def target_files(version):
    pkg = package_version(version)
    return {
        'ubuntu24.04-amd64': [name for client in ('cli', 'gtk') for name in
                              (f'keyparcel-{client}_{pkg}-1_amd64.deb', f'keyparcel-{client}-{version}-ubuntu24.04-amd64.tar.gz')],
        'fedora44-x86_64': [name for client in ('cli', 'gtk') for name in
                            (f'keyparcel-{client}-{pkg}-1.x86_64.rpm', f'keyparcel-{client}-{version}-fedora44-x86_64.tar.gz')],
        'android-arm64': [f'keyparcel-{version}-arm64.apk', 'android-signing-certificate.txt'],
    }


def validate_assets(assets, version):
    targets = target_files(version)
    expected = {f'build-info-{target}.json' for target in targets}
    expected.update(name.replace('~', '-') for names in targets.values() for name in names)
    if {path.name for path in assets.iterdir()} != expected | {'SHA256SUMS'}:
        raise ValueError('Release assets do not match the intended final filenames')
    checked = set()
    for line in (assets / 'SHA256SUMS').read_text().splitlines():
        checksum, name = line.split('  ', 1)
        if name not in expected or name in checked or checksum != digest(assets / name):
            raise ValueError('Invalid or duplicate release checksum: ' + name)
        checked.add(name)
    if checked != expected:
        raise ValueError('Checksums must cover every intended release asset exactly once')


def assemble(source, output, version, revision, code):
    """Only known final assets, never work directories, logs or unsigned APKs."""
    if output.exists():
        raise ValueError('Asset destination must not already exist')
    output.mkdir(parents=True)
    for target, names in target_files(version).items():
        directory = source / target
        info = json.loads((directory / 'build-info.json').read_text())
        if any(info.get(key) != value for key, value in dict(version=version, revision=revision, target=target).items()):
            raise ValueError(f'Wrong tagged provenance: {target}')
        if target == 'android-arm64' and (info.get('versionCode') != code or info.get('applicationId') != 'io.github.keyparcel.keyparcel'):
            raise ValueError('Wrong Android versionCode/application ID')
        shutil.copyfile(directory / 'build-info.json', output / f'build-info-{target}.json')
        for name in names:
            # GitHub normalizes '~' to '.'; use the product's hyphen externally.
            shutil.copyfile(directory / name, output / name.replace('~', '-'))
    lines = [f'{digest(path)}  {path.name}\n' for path in sorted(output.iterdir())]
    (output / 'SHA256SUMS').write_text(''.join(lines))
    validate_assets(output, version)


class GitHub:
    def __init__(self, repo, token):
        if not re.fullmatch(r'[\w.-]+/[\w.-]+', repo):
            raise ValueError('Invalid GitHub repository')
        self.base = f'https://api.github.com/repos/{repo}'
        self.token = token

    def request(self, method, path, body=None, data=None, content_type='application/json'):
        url = path if path.startswith('https://') else self.base + path
        if body is not None:
            data = json.dumps(body).encode()
        request = urllib.request.Request(url, data=data, method=method, headers={
            'Authorization': 'Bearer ' + self.token, 'Accept': 'application/vnd.github+json',
            'X-GitHub-Api-Version': '2022-11-28', 'Content-Type': content_type,
        })
        with urllib.request.urlopen(request, timeout=120) as response:
            if response.status == 204:
                return None
            return json.load(response)

    def download(self, asset_id):
        request = urllib.request.Request(self.base + f'/releases/assets/{asset_id}',
                                         headers={'Accept': 'application/octet-stream'})
        # Signed asset redirects do not need our API token.
        request.add_unredirected_header('Authorization', 'Bearer ' + self.token)
        with urllib.request.urlopen(request, timeout=120) as response:
            return response.read()


def require_draft(release, tag, revision, prerelease):
    if not release['draft']:
        raise ValueError('Refusing to modify a published release')
    if (release['tag_name'], release['target_commitish'], release['prerelease']) != (tag, revision, prerelease):
        raise ValueError('Existing draft has different tag/provenance/prerelease state')


def require_tag(api, tag, revision):
    obj = api.request('GET', '/git/ref/tags/' + tag)['object']
    for _ in range(16):
        if obj['type'] == 'commit':
            if obj['sha'] != revision:
                raise ValueError('Remote tag no longer points to the built commit')
            return
        if obj['type'] != 'tag':
            break
        obj = api.request('GET', '/git/tags/' + obj['sha'])['object']
    raise ValueError('Release tag must resolve to a commit')


def publish(api, tag, revision, assets):
    version = tag_version(tag)
    validate_assets(assets, version)
    prerelease = '-' in version
    require_tag(api, tag, revision)
    # List includes drafts; the by-tag endpoint does not reliably find unpublished drafts.
    release = None
    page = 1
    while True:
        batch = api.request('GET', f'/releases?per_page=100&page={page}')
        release = next((item for item in batch if item['tag_name'] == tag), None)
        if release or len(batch) < 100:
            break
        page += 1
    if release is None:
        body = f'''Linux amd64: independent keyparcel-cli and keyparcel-gtk packages and archives. DEB for Ubuntu 24.04 / Debian 13; RPM for Fedora 44. CLI is headless; GTK does not require the CLI package. GTK archives need the matching baseline libraries.
Android ARM64 APK: Android 8 or newer; first physical ARM installation still requires manual validation.

Remove an older combined `keyparcel` package first. Install your chosen client with `sudo apt install ./keyparcel-cli_*.deb` or `./keyparcel-gtk_*.deb`, `sudo dnf install ./keyparcel-cli-*.rpm` or `./keyparcel-gtk-*.rpm`, or open the APK on Android. Archive installation and enrollment: https://github.com/keyparcel/keyparcel/blob/{tag}/release/INSTALLED.md
Verify downloaded files with `sha256sum -c SHA256SUMS`. The Android certificate fingerprint is included.

Source: `{revision}`. Draft only: inspect artifacts and test a physical ARM device before manually publishing. No ARM runtime or additional distro support is implied.'''
        release = api.request('POST', '/releases', body=dict(tag_name=tag, target_commitish=revision,
            name='keyparcel ' + version, body=body, draft=True, prerelease=prerelease, make_latest='false'))
    require_draft(release, tag, revision, prerelease)
    release_id = release['id']
    existing = {}
    page = 1
    while True:
        batch = api.request('GET', f'/releases/{release_id}/assets?per_page=100&page={page}')
        for item in batch:
            existing[item['name']] = item
        if len(batch) < 100:
            break
        page += 1
    paths = sorted(assets.iterdir())
    if set(existing) - {path.name for path in paths}:
        raise ValueError('Draft contains unexpected assets; inspect it manually')
    # Validate all existing bytes before uploading anything; never delete or clobber.
    for path in paths:
        item = existing.get(path.name)
        if item and (item.get('state') != 'uploaded' or item.get('size') != path.stat().st_size or
                     item.get('digest') != 'sha256:' + digest(path)):
            raise ValueError(f'Existing asset differs or has no verifiable digest: {path.name}')
    for path in paths:
        if path.name in existing:
            continue
        require_draft(api.request('GET', f'/releases/{release_id}'), tag, revision, prerelease)
        # This is a GitHub-provided upload URL, restricted before attaching the token.
        url = release['upload_url'].split('{', 1)[0]
        if not url.startswith('https://uploads.github.com/repos/'):
            raise ValueError('Unexpected upload host')
        result = api.request('POST', url + '?' + urllib.parse.urlencode({'name': path.name}),
                             data=path.read_bytes(), content_type='application/octet-stream')
        if (result.get('name') != path.name or result.get('size') != path.stat().st_size or
                result.get('state') != 'uploaded' or result.get('digest') != 'sha256:' + digest(path)):
            raise ValueError('Uploaded asset name, size, state or checksum mismatch: ' + path.name)
    require_draft(api.request('GET', f'/releases/{release_id}'), tag, revision, prerelease)
    require_tag(api, tag, revision)
    print('Unpublished draft ready:', release['html_url'])


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('operation', choices=['tag', 'assets', 'draft'])
    args = parser.parse_args()
    if os.environ.get('GITHUB_REF_TYPE') != 'tag':
        parser.error('Release automation requires a tag event')
    tag = os.environ['GITHUB_REF_NAME']
    version, revision, code = tagged_source(tag, os.environ['GITHUB_SHA'])
    if args.operation == 'tag':
        with open(os.environ['GITHUB_OUTPUT'], 'a') as output:
            output.write(f'version={version}\nrevision={revision}\ncode={code}\n')
    elif args.operation == 'assets':
        assemble(REPO / 'release/out' / version, REPO / 'release/out/assets', version, revision, code)
    else:
        publish(GitHub(os.environ['GITHUB_REPOSITORY'], os.environ['GH_TOKEN']), tag, revision, REPO / 'release/out/assets')


if __name__ == '__main__':
    main()
