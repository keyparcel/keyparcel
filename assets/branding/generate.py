#!/usr/bin/env python3
"""Export the outlined SVG masters; only needed when changing the artwork."""
from copy import deepcopy
from pathlib import Path
import sys
import xml.etree.ElementTree as ET

HERE = Path(__file__).resolve().parent
REPO = HERE.parents[1]
SVG = 'http://www.w3.org/2000/svg'
ANDROID = 'http://schemas.android.com/apk/res/android'
ET.register_namespace('', SVG)
ET.register_namespace('android', ANDROID)
PETROL, PAPER = '#103F4D', '#F7F5F0'
assert sys.argv[1:] in ([], ['--check']), 'Usage: generate.py [--check]'
CHECK = bool(sys.argv[1:])
icon = ET.parse(HERE / 'icon.svg').getroot()
wordmark = ET.parse(HERE / 'wordmark.svg').getroot()
assert icon.get('viewBox') == '0 0 256 256'
assert len(icon.findall(f'{{{SVG}}}path')) == 5


def write(name, root, directory=HERE):
    ET.indent(root, space='  ')
    content = ET.tostring(root, encoding='unicode') + '\n'
    target = directory / name
    if CHECK:
        assert target.read_text() == content, f'Regenerate stale artwork: {target}'
    else:
        target.parent.mkdir(parents=True, exist_ok=True)
        target.write_text(content)


def symbol(monochrome=False, dark=False):
    paths = []
    for source in icon.findall(f'{{{SVG}}}path'):
        if monochrome and source.get('id') == 'keyhole':
            continue
        path = deepcopy(source)
        if monochrome:
            path.set('fill', '#000000')
        elif dark and path.get('fill') == PETROL:
            path.set('fill', PAPER)
        paths.append(path)
    return paths


mono = ET.Element(f'{{{SVG}}}svg', viewBox='0 0 256 256')
mono.extend(symbol(monochrome=True))
write('icon-monochrome.svg', mono)

tile = ET.Element(f'{{{SVG}}}svg', viewBox='0 0 320 320')
ET.SubElement(tile, f'{{{SVG}}}path', fill=PAPER,
              d='M64 0H256Q320 0 320 64V256Q320 320 256 320H64Q0 320 0 256V64Q0 0 64 0Z')
group = ET.SubElement(tile, f'{{{SVG}}}g', transform='translate(32 32)')
group.extend(symbol())
write('app-icon.svg', tile)

for dark in (False, True):
    logo = ET.Element(f'{{{SVG}}}svg', viewBox='0 0 640 160')
    ET.SubElement(logo, f'{{{SVG}}}title').text = 'keyparcel'
    group = ET.SubElement(logo, f'{{{SVG}}}g', transform='translate(4 4) scale(0.58)')
    group.extend(symbol(dark=dark))
    text = ET.SubElement(logo, f'{{{SVG}}}svg', x='172', y='42', width='460', height='92',
                         viewBox=wordmark.get('viewBox'))
    for source in wordmark.findall(f'{{{SVG}}}path'):
        path = deepcopy(source)
        path.set('fill', PAPER if dark else PETROL)
        text.append(path)
    write('logo-dark.svg' if dark else 'logo-light.svg', logo)

resources = REPO / 'android/client/app/src/main/res'
for name, size, offset, monochrome in [
    ('ic_keyparcel', '320', '32', False),
    ('ic_launcher_foreground', '432', '88', False),
    ('ic_launcher_monochrome', '432', '88', True),
]:
    vector = ET.Element('vector', {f'{{{ANDROID}}}width': '108dp', f'{{{ANDROID}}}height': '108dp',
                                 f'{{{ANDROID}}}viewportWidth': size, f'{{{ANDROID}}}viewportHeight': size})
    if name == 'ic_keyparcel':
        ET.SubElement(vector, 'path', {f'{{{ANDROID}}}fillColor': PAPER,
                                      f'{{{ANDROID}}}pathData': tile[0].get('d')})
    group = ET.SubElement(vector, 'group', {f'{{{ANDROID}}}translateX': offset, f'{{{ANDROID}}}translateY': offset})
    for source in symbol(monochrome=monochrome):
        ET.SubElement(group, 'path', {f'{{{ANDROID}}}fillColor': source.get('fill'),
                                     f'{{{ANDROID}}}fillType': source.get('fill-rule', 'nonzero').replace('evenodd', 'evenOdd').replace('nonzero', 'nonZero'),
                                     f'{{{ANDROID}}}pathData': source.get('d')})
    write(f'{name}.xml', vector, resources / 'drawable')

background = ET.Element('shape')
ET.SubElement(background, 'solid', {f'{{{ANDROID}}}color': PAPER})
write('ic_launcher_background.xml', background, resources / 'drawable')
launcher = ET.Element('adaptive-icon')
for layer, resource in [('background', 'ic_launcher_background'), ('foreground', 'ic_launcher_foreground'),
                        ('monochrome', 'ic_launcher_monochrome')]:
    ET.SubElement(launcher, layer, {f'{{{ANDROID}}}drawable': '@drawable/' + resource})
write('ic_launcher.xml', launcher, resources / 'mipmap-anydpi-v26')

print('Verified artwork exports' if CHECK else 'Updated SVG logos, application icon, and Android vector drawables')
