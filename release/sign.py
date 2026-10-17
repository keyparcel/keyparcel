"""Sign a statically checked production APK with an ephemeral keystore file."""
import argparse
import base64
import hashlib
import json
import os
from pathlib import Path
import re
import runpy
import subprocess
import tempfile
import zipfile

from build import REPO


def tool(args, **kwargs):
    # Captured diagnostics/arguments may contain secret aliases: never echo them.
    try:
        return subprocess.check_output(list(map(str, args)), stderr=subprocess.PIPE, **kwargs)
    except subprocess.CalledProcessError:
        raise RuntimeError('Android signing/verification tool failed') from None


def sign(directory):
    tools = Path(os.environ['ANDROID_HOME']) / 'build-tools/35.0.0'
    for name in ('ANDROID_KEYSTORE_BASE64', 'ANDROID_KEYSTORE_PASSWORD', 'ANDROID_KEY_ALIAS', 'ANDROID_KEY_PASSWORD'):
        if not os.environ.get(name):
            raise ValueError('Missing signing secret: ' + name)
    info = json.loads((directory / 'build-info.json').read_text())
    unsigned = directory / f'keyparcel-{info["version"]}-arm64-unsigned.apk'
    signed = directory / f'keyparcel-{info["version"]}-arm64.apk'
    certificate = directory / 'android-signing-certificate.txt'
    # Reuse all unsigned production checks, including AAR completeness and notices.
    tool(['python3', REPO / 'release/check-apk.py', directory])
    if subprocess.run([str(tools / 'apksigner'), 'verify', str(unsigned)],
                      stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL).returncode == 0:
        raise ValueError('Signing input must be unsigned')
    for name in ('LICENSE', 'NOTICE', 'THIRD_PARTY_NOTICES.txt'):
        if (directory / name).read_bytes() != (REPO / name).read_bytes():
            raise ValueError('Candidate notices do not match tagged source')
    if signed.exists() or certificate.exists():
        raise ValueError('Signing outputs already exist')
    try:
        with tempfile.TemporaryDirectory(prefix='keyparcel-sign-', dir=os.environ.get('RUNNER_TEMP')) as temporary:
            # TemporaryDirectory is 0700; keystore is created 0600 regardless of umask.
            keystore = Path(temporary) / 'release.jks'
            with keystore.open('xb') as file:
                keystore.chmod(0o600)
                file.write(base64.b64decode(''.join(os.environ['ANDROID_KEYSTORE_BASE64'].split()), validate=True))
            cert = tool(['keytool', '-exportcert', '-keystore', keystore, '-alias', os.environ['ANDROID_KEY_ALIAS'],
                         '-storepass:env', 'ANDROID_KEYSTORE_PASSWORD'])
            fingerprint = hashlib.sha256(cert).hexdigest()
            tool([tools / 'apksigner', 'sign', '--ks', keystore,
                  '--ks-key-alias', os.environ['ANDROID_KEY_ALIAS'],
                  '--ks-pass', 'env:ANDROID_KEYSTORE_PASSWORD', '--key-pass', 'env:ANDROID_KEY_PASSWORD',
                  '--v1-signing-enabled', 'false', '--v2-signing-enabled', 'true',
                  '--v3-signing-enabled', 'true', '--v4-signing-enabled', 'false', '--out', signed, unsigned])
        verification = tool([tools / 'apksigner', 'verify', '--verbose', '--print-certs', signed]).decode()
        digests = re.findall(r'^Signer #\d+ certificate SHA-256 digest: ([0-9a-fA-F]+)$', verification, re.M)
        if digests != [fingerprint]:
            raise ValueError('APK signer does not match the selected keystore certificate')
        tool([tools / 'zipalign', '-c', '-P', '16', '4', signed])
        # Verify real signed metadata, not only the cached unsigned reports.
        for arguments, report in [(['badging'], 'apk-badging.txt'),
                                  (['xmltree'], 'apk-manifest.txt')]:
            command = [tools / 'aapt2', 'dump', *arguments, signed]
            if arguments == ['xmltree']:
                command += ['--file', 'AndroidManifest.xml']
            if tool(command) != (directory / report).read_bytes():
                raise ValueError('Signed APK metadata differs from verified unsigned input')
        with zipfile.ZipFile(unsigned) as before, zipfile.ZipFile(signed) as after:
            # minSdk 26 uses v2/v3; preserve every unsigned entry exactly.
            if set(before.namelist()) != set(after.namelist()):
                raise ValueError('Signing changed APK entries')
            for name in before.namelist():
                if before.read(name) != after.read(name):
                    raise ValueError('Signing changed APK payload: ' + name)
        runpy.run_path(str(REPO / 'release/check-apk.py'))['check'](signed)
        certificate.write_text('Android signing certificate SHA-256: ' + fingerprint + '\n')
        print('Signed APK verified; certificate SHA-256:', fingerprint)
    except BaseException:
        signed.unlink(missing_ok=True)
        certificate.unlink(missing_ok=True)
        raise
    return signed


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('directory', type=Path)
    sign(parser.parse_args().directory.resolve())
