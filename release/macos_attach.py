"""Attach a verified native package pair to an existing, still unpublished draft."""
import argparse
import hashlib
import json
import os
from pathlib import Path
import re
import subprocess
import tempfile
import urllib.parse

from automation import GitHub, require_draft, require_tag, tagged_source, target_files, validate_assets
from build import REPO
from macos import IDENTIFIER, INFO, files

PENDING = 'SHA256SUMS.macos-pending'


def sha(data):
    return hashlib.sha256(data).hexdigest()


def local_tag(tag, event_sha):
    version, revision, code = tagged_source(tag, event_sha)
    ref = 'refs/tags/' + tag
    if subprocess.check_output(['git', 'cat-file', '-t', ref], cwd=REPO, text=True).strip() != 'tag':
        raise ValueError('Mac attachment requires an annotated release tag')
    obj = subprocess.check_output(['git', 'rev-parse', ref], cwd=REPO, text=True).strip()
    return version, revision, code, obj


def remote_tag(api, tag, revision, tag_object):
    obj = api.request('GET', '/git/ref/tags/' + tag)['object']
    if (obj.get('type'), obj.get('sha')) != ('tag', tag_object):
        raise ValueError('Remote annotated tag object differs from checkout')
    require_tag(api, tag, revision)


def record_tag(source, tag, version, revision, tag_object):
    path = source / 'build-info.json'
    info = json.loads(path.read_text())
    if (info.get('version'), info.get('revision')) != (version, revision):
        raise ValueError('Native build differs from the annotated release tag')
    info.update(tag=tag, tag_object=tag_object)
    path.write_text(json.dumps(info, indent=2) + '\n')


def pair(source, tag, version, revision, tag_object):
    if {p.name for p in source.iterdir()} != {'macos-cli-unsigned-arm64', 'macos-cli-unsigned-amd64'}:
        raise ValueError('Expected both native artifact directories and no extras')
    assets = {}
    for arch in ('arm64', 'amd64'):
        root = source / ('macos-cli-unsigned-' + arch)
        name = f'keyparcel-cli-{version}-macos-{arch}.pkg'
        if {p.name for p in root.iterdir()} != {name, 'build-info.json'}:
            raise ValueError('Expected exactly one package and build-info for ' + arch)
        raw = (root / 'build-info.json').read_bytes()
        info = json.loads(raw)
        expected = dict(version=version, revision=revision, target='macos-' + arch,
                        client='cli', identifier=IDENTIFIER, package=name,
                        tag=tag, tag_object=tag_object)
        if any(info.get(k) != v for k, v in expected.items()) or set(info.get('files', {})) != set(files()) - {INFO}:
            raise ValueError('Wrong native package provenance: ' + arch)
        if not all(isinstance(v, str) and re.fullmatch('[0-9a-f]{64}', v) for v in info['files'].values()):
            raise ValueError('Missing or invalid native payload hashes: ' + arch)
        data = (root / name).read_bytes()
        if not data.startswith(b'xar!') or info.get('package_sha256') != sha(data):
            raise ValueError('Native package hash/format differs: ' + arch)
        assets[name] = data
        assets['build-info-macos-' + arch + '.json'] = raw
    return assets


def inventory(api, release_id):
    result = {}
    page = 1
    while True:
        batch = api.request('GET', f'/releases/{release_id}/assets?per_page=100&page={page}')
        for item in batch:
            if item['name'] in result:
                raise ValueError('Duplicate draft asset name: ' + item['name'])
            result[item['name']] = item
        if len(batch) < 100:
            return result
        page += 1


def fingerprint(items):
    return {name: tuple(item.get(k) for k in ('id', 'state', 'size', 'digest')) for name, item in items.items()}


def checked(item, name, data):
    if (item.get('name'), item.get('state'), item.get('size'), item.get('digest')) != (
            name, 'uploaded', len(data), 'sha256:' + sha(data)):
        raise ValueError('Asset bytes/state/digest differ: ' + name)


def manifest(data, allowed, contents):
    result = {}
    for line in data.decode('ascii').splitlines():
        digest, name = line.split('  ')
        if name not in allowed or name in result or digest != sha(contents[name]):
            raise ValueError('Invalid, duplicate or mismatched checksum: ' + name)
        result[name] = digest
    if set(result) != allowed:
        raise ValueError('Missing release checksums')


def attach(api, tag, version, revision, code, tag_object, source):
    additions = pair(source, tag, version, revision, tag_object)  # BOTH before any API writes.
    remote_tag(api, tag, revision, tag_object)
    matches = []
    page = 1
    while True:
        batch = api.request('GET', f'/releases?per_page=100&page={page}')
        matches.extend(r for r in batch if r['tag_name'] == tag)
        if len(batch) < 100:
            break
        page += 1
    if len(matches) != 1:
        raise ValueError('Expected exactly one existing draft for this tag')
    release = matches[0]
    require_draft(release, tag, revision, '-' in version)
    rid = release['id']
    current = inventory(api, rid)
    base = {name.replace('~', '-') for names in target_files(version).values() for name in names}
    base.update('build-info-' + target + '.json' for target in target_files(version))
    full = base | set(additions)
    if not base <= set(current) or set(current) - (full | {'SHA256SUMS', PENDING}):
        raise ValueError('Missing base assets or unexpected draft assets')
    contents = {}
    for name, item in current.items():
        data = api.download(item['id'])
        checked(item, name, data)
        contents[name] = data
    for name in set(additions) & set(current):
        if contents[name] != additions[name]:
            raise ValueError('Existing Mac asset differs; refusing overwrite: ' + name)
    contents.update(additions)
    for target in target_files(version):
        info = json.loads(contents['build-info-' + target + '.json'])
        expected = dict(version=version, revision=revision, target=target)
        if target == 'android-arm64':
            expected.update(versionCode=code, applicationId='io.github.keyparcel.keyparcel')
        if any(info.get(k) != v for k, v in expected.items()):
            raise ValueError('Existing draft has wrong base provenance: ' + target)
    # Reuse the base release allowlist/checksum validator without widening its semantics.
    with tempfile.TemporaryDirectory() as temporary:
        root = Path(temporary)
        for name in base:
            (root / name).write_bytes(contents[name])
        (root / 'SHA256SUMS').write_text(''.join(f'{sha(contents[n])}  {n}\n' for n in sorted(base)))
        validate_assets(root, version)
    merged = ''.join(f'{sha(contents[n])}  {n}\n' for n in sorted(full)).encode()
    if 'SHA256SUMS' in current:
        entries = [line.split('  ') for line in contents['SHA256SUMS'].decode('ascii').splitlines()]
        if any(len(entry) != 2 for entry in entries):
            raise ValueError('Malformed existing checksum file')
        names = {name for _, name in entries}
        if names not in (base, full):
            raise ValueError('Checksums must cover the complete base or complete expanded release')
        manifest(contents['SHA256SUMS'], names, contents)
        if names == full and not set(additions) <= set(current):
            raise ValueError('Checksums reference missing Mac assets')
    elif PENDING not in current:
        raise ValueError('Missing canonical and recoverable pending checksums')
    if PENDING in current and contents[PENDING] != merged:
        raise ValueError('Pending checksum transaction differs; inspect draft')
    url = release['upload_url'].split('{', 1)[0]
    if not url.startswith('https://uploads.github.com/repos/'):
        raise ValueError('Unexpected upload host')
    original_base = fingerprint({n: current[n] for n in base})

    def guard():
        remote_tag(api, tag, revision, tag_object)
        require_draft(api.request('GET', f'/releases/{rid}'), tag, revision, '-' in version)
        if fingerprint(inventory(api, rid)) != fingerprint(current):
            raise ValueError('Draft assets changed concurrently; stop before writing')

    def upload(name, data):
        guard()
        item = api.request('POST', url + '?' + urllib.parse.urlencode({'name': name}),
                           data=data, content_type='application/octet-stream')
        checked(item, name, data)
        current[name] = item

    # No package deletion/replacement: interrupted uploads are verified and resumed.
    for name, data in additions.items():
        if name not in current:
            upload(name, data)
    if contents.get('SHA256SUMS') != merged:
        if PENDING not in current:
            upload(PENDING, merged)
        if 'SHA256SUMS' in current:
            guard()
            api.request('DELETE', f'/releases/assets/{current["SHA256SUMS"]["id"]}')
            del current['SHA256SUMS']
        guard()
        item = api.request('PATCH', f'/releases/assets/{current[PENDING]["id"]}', body={'name': 'SHA256SUMS'})
        checked(item, 'SHA256SUMS', merged)
        del current[PENDING]
        current['SHA256SUMS'] = item
    elif PENDING in current:
        guard()
        api.request('DELETE', f'/releases/assets/{current[PENDING]["id"]}')
        del current[PENDING]
    guard()
    if set(current) != full | {'SHA256SUMS'} or fingerprint({n: current[n] for n in base}) != original_base:
        raise ValueError('Final draft inventory differs')
    if api.download(current['SHA256SUMS']['id']) != merged:
        raise ValueError('Final checksum bytes differ')
    print('Verified Mac package pair attached; release remains unpublished:', release['html_url'])


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('operation', choices=('tag', 'record', 'attach'))
    parser.add_argument('--source', type=Path, default=REPO / 'release/out/macos-attach')
    args = parser.parse_args()
    if os.environ.get('GITHUB_REF_TYPE') != 'tag':
        parser.error('Attachment requires a release tag ref')
    tag = os.environ['GITHUB_REF_NAME']
    version, revision, code, obj = local_tag(tag, os.environ['GITHUB_SHA'])
    if args.operation == 'record':
        record_tag(args.source, tag, version, revision, obj)
    elif args.operation == 'attach':
        attach(GitHub(os.environ['GITHUB_REPOSITORY'], os.environ['GH_TOKEN']),
               tag, version, revision, code, obj, args.source)


if __name__ == '__main__':
    main()
