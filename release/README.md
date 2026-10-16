# Release candidates

Build and validate candidates without tags, signing keys or publication rights.
Checksums, signing and publication are separate from candidate builds.
Project code is [Apache-2.0](../LICENSE); linked Go components retain their BSD
licenses. Linux packages/archives and Android APK assets include the project
license, copyright notice and [third-party notices](../THIRD_PARTY_NOTICES.txt).
The RPM declares `Apache-2.0 AND BSD-2-Clause AND BSD-3-Clause` for the combined work.

## Build

Run from the checkout as an ordinary user with Docker access, Python 3 and the
Go version in `go.mod`. Linux build tools stay inside distro builder images.
Android additionally needs JDK 21 and the SDK/NDK listed in
[Android development](../android/DEVELOPMENT.md); no emulator is used here.

```sh
python3 release/build.py linux --version "$(cat release/VERSION)"
python3 release/build.py android --version "$(cat release/VERSION)" \
  --android-code "$(cat release/ANDROID_VERSION_CODE)"
python3 release/validate.py "release/out/$(cat release/VERSION)"
```

`all` builds both targets. `--output PATH` selects another output parent. Linux
builds require an x86_64 Docker host; there is no Linux ARM build. Versioned output
has separate `ubuntu24.04-amd64`, `fedora44-x86_64` and `android-arm64` directories.
Android copies the Gradle project and builds its own AAR/tools there; emulator,
smoke and scenario outputs and identities are untouched.

`release/VERSION` is the explicit product version for CI; local commands require
it explicitly. Use `MAJOR.MINOR.PATCH`, optionally followed by `-alpha.N`, `-beta.N` or `-rc.N`,
for example `0.1.0-rc.1` (no leading zeroes). This deliberately limited version
grammar preserves ordering in both package managers; arbitrary SemVer suffixes
and build metadata are rejected. DEB/RPM map the prerelease separator to `~`,
sorting alpha, beta, rc, then the corresponding final version;
packages have revision/release `1`. The Android versionName keeps the product
version. `ANDROID_VERSION_CODE` is an independently assigned integer, 1 through
2,100,000,000: increase it for every published Android build, including prereleases;
never reset it for a final version. Publication and enforcing a remote counter
are outside this build-only stage.

CLI and GTK expose `--version`; each candidate also has `build-info.json` with
version, source revision and target (and Go toolchain for Linux). Dirty local
checkouts append `-dirty`; CI checks out the exact PR head and uses
`--require-clean` to reject source changes. Ignored output/log files do not affect
that check. One revision value is shared by all targets in a build invocation. Android records
source revision in manifest metadata `dev.keyparcel.sourceRevision` and exposes
versionName/versionCode through package metadata. Default developer builds say
`development`; these values are not claims of reproducible bytes.

## Linux layout and supported baselines

Each Linux target produces one combined CLI/GTK package and a matching `.tar.gz`
install tree. Keeping them together avoids another package dependency/version
relationship. Files install under `/usr/bin`, `/usr/share/applications`,
`/usr/share/bash-completion/completions`, and `/usr/share/doc/keyparcel`.
The bundled parcel-and-key SVG installs under `/usr/share/icons/hicolor/scalable/apps`.
The GTK application ID and icon name are `dev.keyparcel`, matching the launcher
`dev.keyparcel.desktop` on X11 and Wayland. See [shared artwork](../assets/branding/README.md).

- **DEB:** built on Ubuntu 24.04 x86_64, installed/tested on Ubuntu 24.04 and Debian
  13. `dpkg-shlibdeps` derives GTK/libc dependencies; OpenSSH client, CA certificates
  Adwaita icons and the SVG loader are explicit dependencies
- **RPM:** built and installed/tested on Fedora 44 x86_64. RPM derives shared-library
  requirements; OpenSSH clients, CA certificates and Adwaita icons are explicit
- **Archives:** contain the exact package binaries and install files. The GTK
  binary still needs the matching baseline's shared libraries; it is not a
  universally portable bundle. The CLI is built without cgo; OpenSSH is required
  for its transport. Other distros/baselines are not validated

Install a local candidate with `sudo apt install ./keyparcel_*.deb` or
`sudo dnf install ./keyparcel-*.rpm`. Uninstall removes system files, not user
configuration, identities or settings. Candidate packages do not own home paths.
The included [installed-user guide](INSTALLED.md) covers first setup and provides
concrete archive installation/removal commands under `/usr/local`, separate from
package-owned `/usr` files. Its usage/enrollment/security links point online;
source/developer guides are not bundled as broken relative links.

## Validation and limits

`validate.py` installs the actual packages through apt/dnf in fresh matching
runtime images (retaining candidate docs despite minimal-image doc filters).
It checks startup/linking **before** adding test-driver tools,
then checks archive/package byte equality, paths/modes/ownership, provenance,
launcher, application icon and completion as the ordinary fixture user. Runtime
images contain neither Go nor development binaries; selected CLI invocations
use `/usr/bin/keyparcel` and GTK uses `/usr/bin/keyparcel-gtk` explicitly.

The unchanged CLI text-deletion, file/keep/overwrite and tampered/truncated receive
scenarios run through the narrow installed-image hook, with their existing SSH
fixture and assertions. They exercise the production OpenSSH CLI; the ordinary
source suite still tests both OpenSSH and embedded connectors. A short branch of
the existing GTK journey reuses enrollment/server and peer approval, verified
Unicode receive/copy/retention, explicit removal, and GUI-to-CLI peer delivery.
The full GTK journey, widget checks and smoke lifecycle suite remain the source
workflow's responsibility. Package checks do not repeat the full suite per format.
Finally, removal through apt/dnf must leave a generated user identity and config
byte-identical. Each run owns and cleans its own fixtures.

Android's stable production application ID is `dev.keyparcel.app`; smoke/scenario
retain their suffixes and x86_64 workflow. The production release is unsigned and
non-debuggable, with ARM64-only native libraries. Static checks inspect package
version/ID/provenance, AAR-to-APK library completeness, ELF machine/load alignment,
APK 16 KiB library alignment, and absence of a valid signature. These checks do
**not** establish ARM runtime behavior; first physical-device execution remains
deferred. No device farm, signing, release creation, upgrade matrix, package
repository, macOS packaging or auto-update implementation is included.

CI keeps the existing seven checks and adds two candidate jobs. Artifacts and
logs are retained for seven days. Local logs go to stdout; GTK/test failure
artifacts use the existing fixture locations. Do not distribute failure artifacts
as releases; they can contain disposable fixture keys and plaintext.
