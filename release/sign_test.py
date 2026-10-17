"""Real tool smoke test, with a disposable key only; never production secrets."""
import base64
import os
from pathlib import Path
import shutil
import subprocess
import sys
import tempfile

import sign as signing
from sign import sign


def main():
    source = Path(sys.argv[1]).resolve()
    with tempfile.TemporaryDirectory(prefix='keyparcel-test-sign-') as temporary:
        root = Path(temporary)
        candidate = root / 'candidate'
        shutil.copytree(source, candidate, ignore=shutil.ignore_patterns('work', 'tools'))
        keystore = root / 'fixture.jks'
        env = dict(os.environ, FIXTURE_PASSWORD='disposable-only')
        subprocess.run(['keytool', '-genkeypair', '-noprompt', '-keystore', str(keystore),
                        '-storepass:env', 'FIXTURE_PASSWORD', '-keypass:env', 'FIXTURE_PASSWORD',
                        '-alias', 'fixture', '-keyalg', 'RSA', '-keysize', '2048',
                        '-dname', 'CN=Disposable signing test', '-validity', '1'],
                       env=env, check=True, stdout=subprocess.DEVNULL, stderr=subprocess.PIPE)
        os.environ['RUNNER_TEMP'] = str(root)
        os.environ.update(ANDROID_KEYSTORE_BASE64=base64.b64encode(keystore.read_bytes()).decode(),
                          ANDROID_KEYSTORE_PASSWORD='disposable-only', ANDROID_KEY_PASSWORD='disposable-only',
                          ANDROID_KEY_ALIAS='fixture')
        original_tool = signing.tool
        def checked_tool(args, **kwargs):
            for option in ('--ks', '-keystore'):
                if option in args:
                    path = Path(args[args.index(option) + 1])
                    assert path.stat().st_mode & 0o777 == 0o600, 'Keystore permissions are not private'
                    assert path.parent.stat().st_mode & 0o777 == 0o700, 'Signing directory is not private'
            return original_tool(args, **kwargs)
        signing.tool = checked_tool
        signed = sign(candidate)
        assert signed.is_file() and (candidate / 'android-signing-certificate.txt').is_file()
        assert not list(root.glob('keyparcel-sign-*')), 'Signing left a temporary keystore'
        signed.unlink()
        (candidate / 'android-signing-certificate.txt').unlink()
        os.environ['ANDROID_KEY_PASSWORD'] = 'incorrect-disposable-password'
        try:
            sign(candidate)
        except RuntimeError:
            pass
        else:
            raise AssertionError('Incorrect key password signed successfully')
        assert not signed.exists(), 'Failed signing left a distributable APK'
        assert not list(root.glob('keyparcel-sign-*')), 'Failed signing leaked temporary keystore'
        assert not list(candidate.glob('*.idsig')), 'Unexpected v4 signing sidecar'
        assert not list(source.glob('*-arm64.apk')), 'Test modified original unsigned candidate'
    print('Disposable-key signing, certificate/payload checks and failure cleanup passed')


if __name__ == '__main__':
    main()
