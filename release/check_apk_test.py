import runpy
from pathlib import Path
import struct
import tempfile
import unittest
import zipfile

check = runpy.run_path(str(Path(__file__).with_name('check-apk.py')))['check']


class APK(unittest.TestCase):
    def fixture(self, path, abi='arm64-v8a', alignment=16384, machine=183):
        data = bytearray(120)
        data[:6] = b'\x7fELF\x02\x01'
        struct.pack_into('<H', data, 18, machine)
        struct.pack_into('<Q', data, 32, 64)
        struct.pack_into('<HH', data, 54, 56, 1)
        struct.pack_into('<IIQQQQQQ', data, 64, 1, 5, 0, 0, 0, 120, 120, alignment)
        with zipfile.ZipFile(path, 'w', compression=zipfile.ZIP_DEFLATED) as archive:
            archive.writestr(f'lib/{abi}/libgojni.so', data)

    def test_reject_wrong_abi_machine_and_alignment(self):
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / 'candidate.apk'
            self.fixture(path)
            check(path)
            for options in [dict(abi='x86_64'), dict(alignment=4096), dict(machine=62)]:
                self.fixture(path, **options)
                with self.assertRaises(AssertionError):
                    check(path)


if __name__ == '__main__':
    unittest.main()
