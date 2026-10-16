import unittest
from version import android_code, package_version


class Versions(unittest.TestCase):
    def test_versions(self):
        for source, expected in [('1.2.3', '1.2.3'), ('1.2.3-rc.2', '1.2.3~rc.2'),
                                 ('1.2.3-beta.1', '1.2.3~beta.1')]:
            self.assertEqual(package_version(source), expected)
        for invalid in ['v1.2.3', '1.2', '01.2.3', '1.2.3;id', '1.2.3-', '../1.2.3', '1.2.3-rc.01', '1.2.3-foo', '1.2.3+build.1']:
            with self.assertRaises(ValueError):
                package_version(invalid)

    def test_android_codes(self):
        self.assertEqual(android_code('42'), 42)
        for invalid in ['0', '-1', '2100000001', 'abc']:
            with self.assertRaises(ValueError):
                android_code(invalid)


if __name__ == '__main__':
    unittest.main()
