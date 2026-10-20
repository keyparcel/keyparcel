# keyparcel branding

The parcel and key represent private delivery between your devices. Write the
product name **keyparcel** in lowercase, including headings and sentence starts.

| Asset | Use |
| --- | --- |
| [icon.svg](icon.svg) | Transparent symbol; editable master |
| [wordmark.svg](wordmark.svg) | Lowercase lettering; outlined master |
| [logo-light.svg](logo-light.svg) / [logo-dark.svg](logo-dark.svg) | Horizontal logo on light / dark backgrounds |
| [app-icon.svg](app-icon.svg) | Padded application icon on a warm neutral tile |
| [icon-monochrome.svg](icon-monochrome.svg) | Single-colour symbol with transparent gaps |

The palette is petrol **#103F4D**, amber **#DDA348**, and paper **#F7F5F0**.
Keep the symbol's proportions and clear space; omit lettering at icon sizes.
Use the dark logo on dark backgrounds. Application icons use the neutral tile
so their colours remain readable in either desktop theme.

## Editing

Edit the two SVG masters, then run from the repository root:

```sh
python3 assets/branding/generate.py
python3 assets/branding/generate.py --check
```

Exports are committed, so ordinary builds need no artwork tools or fonts. The
Python standard library generates the SVG variants and Android vector drawables
from the same paths. Android launcher layers leave room for adaptive masks and
include a monochrome layer for themed icons. The GTK client embeds the application
icon for its header; Linux packages also install it in the hicolor icon theme.
The GTK application ID, icon name and desktop launcher basename are `io.github.keyparcel.keyparcel`.

The wordmark uses Noto Sans Bold, converted to paths; no font file is bundled.
Noto Sans is an [SIL Open Font License](https://github.com/notofonts/latin-greek-cyrillic)
font. The [OFL FAQ](https://openfontlicense.org/ofl-faq/#faq1_1) permits logos and
does not impose the font's licence on the resulting artwork.

## Artwork licence

Copyright 2026 keyparcel contributors. The project artwork is provided under the
repository's [Apache-2.0 licence](../../LICENSE). No third-party logos are included.
