"""Explicit candidate versions; package prereleases sort before the final release."""
import re


def package_version(version):
    if not re.fullmatch(r'(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(?:-(?:alpha|beta|rc)\.(0|[1-9][0-9]*))?', version):
        raise ValueError('Use MAJOR.MINOR.PATCH, optionally followed by -alpha.N, -beta.N or -rc.N')
    return version.replace('-', '~', 1)



def android_code(value):
    code = int(value)
    if not 1 <= code <= 2100000000:
        raise ValueError('Android versionCode must be 1..2100000000; increase it for every published build')
    return code
