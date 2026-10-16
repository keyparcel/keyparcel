"""Static ARM64 APK checks; this does not execute Android code."""
import json
from pathlib import Path
import struct
import sys
import zipfile


def check(apk):
    with zipfile.ZipFile(apk) as archive:
        libraries = [name for name in archive.namelist() if name.startswith('lib/') and name.endswith('.so')]
        assert libraries, 'APK has no native libraries'
        assert all(name.startswith('lib/arm64-v8a/') for name in libraries), libraries
        assert 'lib/arm64-v8a/libgojni.so' in libraries, 'Go bridge missing'
        for name in libraries:
            data = archive.read(name)
            assert data[:6] == b'\x7fELF\x02\x01', f'{name}: expected little-endian ELF64'
            assert struct.unpack_from('<H', data, 18)[0] == 183, f'{name}: expected AArch64'
            offset = struct.unpack_from('<Q', data, 32)[0]
            size, count = struct.unpack_from('<HH', data, 54)
            loads = 0
            for index in range(count):
                fields = struct.unpack_from('<IIQQQQQQ', data, offset + size * index)
                if fields[0] == 1:
                    loads += 1
                    assert fields[7] >= 16384, f'{name}: PT_LOAD alignment below 16 KiB'
                    assert fields[2] % 16384 == fields[3] % 16384, f'{name}: incongruent load offset'
            assert loads, f'{name}: no load segments'
            info = archive.getinfo(name)
            if info.compress_type == zipfile.ZIP_STORED:
                with apk.open('rb') as file:
                    file.seek(info.header_offset + 26)
                    length, extra = struct.unpack('<HH', file.read(4))
                assert (info.header_offset + 30 + length + extra) % 16384 == 0, f'{name}: APK alignment below 16 KiB'
        print('ARM64 native libraries and ELF/APK alignment verified:', ', '.join(libraries))


if __name__ == '__main__':
    directory = Path(sys.argv[1])
    info = json.loads((directory / 'build-info.json').read_text())
    badging = (directory / 'apk-badging.txt').read_text()
    assert f"name='{info['applicationId']}'" in badging, 'Wrong application ID'
    assert f"versionCode='{info['versionCode']}'" in badging, 'Wrong versionCode'
    assert f"versionName='{info['version']}'" in badging, 'Wrong versionName'
    assert "native-code: 'arm64-v8a'" in badging, 'Wrong ABI declaration'
    assert 'application-debuggable' not in badging, 'Release APK is debuggable'
    assert info['revision'] in (directory / 'apk-manifest.txt').read_text(), 'Source provenance missing'
    apk = directory / f'keyparcel-{info["version"]}-arm64-unsigned.apk'
    with zipfile.ZipFile(directory / 'build/keyparcel.aar') as aar, zipfile.ZipFile(apk) as archive:
        expected = {n.replace('jni/', 'lib/', 1) for n in aar.namelist() if n.startswith('jni/') and n.endswith('.so')}
        actual = {n for n in archive.namelist() if n.startswith('lib/') and n.endswith('.so')}
        assert expected and expected <= actual, f'AAR native libraries missing from APK: {expected - actual}'
        for name in ('LICENSE', 'NOTICE', 'THIRD_PARTY_NOTICES.txt'):
            assert archive.read('assets/licenses/' + name) == (directory / name).read_bytes(), f'License/notice missing or changed: {name}'
    check(directory / f'keyparcel-{info["version"]}-arm64-unsigned.apk')
