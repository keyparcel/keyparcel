# Release builds

Build and validate candidates without tags, signing keys or publication rights.
Candidate builds stay unsigned and need no secrets. The tag workflow below adds
environment-scoped Android signing and an unpublished release draft.
Project code is [Apache-2.0](../LICENSE); linked Go components retain their BSD
licenses. Linux packages/archives and Android APK assets include the project
license, copyright notice and [third-party notices](../THIRD_PARTY_NOTICES.txt).
The RPM declares `Apache-2.0 AND BSD-2-Clause AND BSD-3-Clause` for each packaged client.

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
never reset it for a final version. The workflow reads this committed value; it does not infer a counter from a tag
or enforce a remote counter. Maintainers must check prior distributions before
assigning the next code.

CLI and GTK expose `--version`; each candidate also has `build-info.json` with
version, source revision and target (and Go toolchain for Linux). Dirty local
checkouts append `-dirty`; candidate CI checks out the exact PR head and uses
`--require-clean` to reject source changes. Ignored output/log files do not affect
that check. One revision value is shared by all targets in a build invocation. Android records
source revision in manifest metadata `dev.keyparcel.sourceRevision` and exposes
versionName/versionCode through package metadata. Default developer builds say
`development`; these values are not claims of reproducible bytes.

## Linux layout and supported baselines

Each Linux target produces independent `keyparcel-cli` and `keyparcel-gtk`
packages and matching, separate `.tar.gz` install trees. Neither depends on the
other. CLI contains the `keyparcel` binary, Bash/zsh completion and compressed
`keyparcel(1)` manual, with no GTK dependencies. GTK contains `keyparcel-gtk`, its
launcher/icon and desktop dependencies. Each owns its own notices, guide and
build provenance under `/usr/share/doc/keyparcel-cli` or `keyparcel-gtk`.
The bundled parcel-and-key SVG installs under `/usr/share/icons/hicolor/scalable/apps`.
The GTK application ID and icon name are `io.github.keyparcel.keyparcel`, matching the launcher
`io.github.keyparcel.keyparcel.desktop` on X11 and Wayland. See [shared artwork](../assets/branding/README.md).

- **DEB:** built on Ubuntu 24.04 x86_64, installed/tested on Ubuntu 24.04 and Debian
  13. `dpkg-shlibdeps` derives GTK/libc dependencies. Both clients require OpenSSH
  and CA certificates; Adwaita icons and the SVG loader are GTK-only dependencies
- **RPM:** built and installed/tested on Fedora 44 x86_64. RPM derives shared-library
  requirements; OpenSSH clients and CA certificates are common; Adwaita icons and
  librsvg2 are GTK-only dependencies
- **Archives:** contain the exact package binaries and install files. The GTK
  binary still needs the matching baseline's shared libraries; it is not a
  universally portable bundle. The CLI is built without cgo; OpenSSH is required
  for its transport. Other distros/baselines are not validated

Install the selected client with `sudo apt install ./keyparcel-cli_*.deb` or
`sudo apt install ./keyparcel-gtk_*.deb`; use the corresponding `keyparcel-cli-*.rpm`
or `keyparcel-gtk-*.rpm` with `sudo dnf install` on Fedora. Remove the old combined
`keyparcel` package first. Both new packages explicitly conflict with that name;
no transitional package or cross-client dependency is used. Package managers may
propose removing the conflict; inspect that transaction. Uninstall removes system files, not user
configuration, identities or settings. Candidate packages do not own home paths.
The included [installed-user guide](INSTALLED.md) covers first setup and provides
concrete archive installation/removal commands under `/usr/local`, separate from
package-owned `/usr` files. Its usage/enrollment/security links point online;
source/developer guides are not bundled as broken relative links.

## Validation and limits

Candidate CI builds DEBs and RPMs on separate runners, then validates Ubuntu,
Debian and Fedora in parallel. Ubuntu and Debian download the same DEB build.
`Linux installed packages` succeeds only when both builders and all three
validators succeed, including their artifact handoffs. The internal one-day
`linux-validation-*` build artifacts include the old-name migration fixture;
public candidate artifacts and the release asset allowlist exclude it.

For a focused local run, build with `--linux-target ubuntu24.04-amd64` or
`--linux-target fedora44-x86_64`, then validate with `--distro ubuntu24.04`,
`--distro debian13` or `--distro fedora44`. Without these options, the scripts
still build both targets and validate all three distros. CI additionally uses
`--revision` to reject a handoff whose source, version or target differs from
its checkout. The tag workflow keeps the full serial build/validation path.

`validate.py` installs the actual packages through apt/dnf in fresh matching
runtime images (retaining candidate docs despite minimal-image doc filters).
It installs each client separately and checks startup/linking **before** adding
test-driver tools. The CLI image has no GTK libraries/themes; GTK has no CLI
package or binary. It then checks archive/package byte equality, paths, modes,
ownership, provenance, launcher, icon, completion and the manual as the ordinary
fixture user. Runtime
images contain neither Go nor development binaries; selected CLI invocations
use `/usr/bin/keyparcel` and GTK uses `/usr/bin/keyparcel-gtk` explicitly.

The CLI text-deletion, file/keep/overwrite and tampered/truncated receive
scenarios run through the narrow installed-image hook, with their existing SSH
fixture and assertions. They exercise the production OpenSSH CLI; the ordinary
source suite still tests both OpenSSH and embedded connectors. A short branch of
the existing GTK journey uses a separate owned CLI-only driver container and
reuses enrollment/server and peer approval, verified
Unicode receive/copy/retention, explicit removal, and GUI-to-CLI peer delivery.
The full GTK journey, widget checks and smoke lifecycle suite remain the source
workflow's responsibility. Package checks do not repeat the full suite per format.
Coexistence and removing either client must leave the other working and generated
user identities, configuration and saved files byte-identical. A test-only old-name
package with overlapping real binaries must cause explicit package-manager conflicts;
removing it first permits migration to both clients. This internal fixture has a
non-package suffix and is never a release asset. Each run cleans only its owned fixtures.

Android's stable production application ID is `io.github.keyparcel.keyparcel`; smoke/scenario
retain their suffixes and x86_64 workflow. Candidate production APKs are unsigned and
non-debuggable, with ARM64-only native libraries. Static checks inspect package
version/ID/provenance, AAR-to-APK library completeness, ELF machine/load alignment,
APK 16 KiB library alignment, and absence of a valid signature. These checks do
**not** establish ARM runtime or upgrade behavior. Each candidate still needs
manual device inspection before publication; see [current platform status](../README.md#clients).
There is no device farm, package repository, macOS GUI or auto-update service.
Optional unsigned macOS CLI packages can be built, tested and attached to an
existing draft [on demand](MACOS.md); Linux/Android releases do not wait for them.
Signing and release drafts are described below.

Candidate CI preserves the broad source suites, including both Android client
shards, and adds two candidate jobs. Its Android job also signs a private copy
with a disposable test key and checks failure cleanup; the uploaded candidate
remains unsigned. Artifacts and
logs are retained for seven days. Local logs go to stdout; GTK/test failure
artifacts use the existing fixture locations. Do not distribute failure artifacts
as releases; they can contain disposable fixture keys and plaintext.

## Tagged release drafts

The `Draft release` workflow runs only on pushed `v*` tags. It accepts exactly
`vMAJOR.MINOR.PATCH`, optionally `-alpha.N`, `-beta.N` or `-rc.N`, with the same
no-leading-zero grammar as the builders. The tag must match `release/VERSION`,
and checkout, event SHA and tag must resolve to the same commit. Linux amd64
packages and archives are built and installed/tested on the baselines above;
Android ARM64 uses the unsigned production build and static checks.
Neither the broad scenario suites nor an ARM emulator are added to this workflow.
Run ordinary PR CI before tagging.

1. Commit the intended `release/VERSION` and `release/ANDROID_VERSION_CODE` through
   normal review. **Increase versionCode for every distributed Android build**,
   including prereleases; a stable release must also have a higher code than its
   prereleases. Never reuse a code for different APK contents or reset it with a
   new version. Check any previously distributed APKs/drafts as well as published
   releases. Re-executing the same tag does not allocate a new code.
2. From the reviewed clean main commit, create and push the matching tag, for example:

   ```sh
   version=$(cat release/VERSION)
   git tag -a "v$version" -m "keyparcel $version"
   git push origin "refs/tags/v$version"
   ```

   Check that the tag and release name are unused first. Never move a distributed
   version tag. Unsupported tags fail validation.
3. After both build/test jobs pass, the `release` environment job automatically
   signs Android and prepares the draft from the same-run artifacts. The current
   environment allows `v*` tags and has no required reviewer or approval wait.
   PR candidates stay unsigned. The workflow does not change settings or secrets.
4. Inspect the resulting **unpublished draft** and its assets. There is no automatic
   publish action. Tags with alpha/beta/rc remain marked prerelease; stable drafts
   are not prereleases. Creation explicitly does not select a latest release;
   choose the appropriate prerelease/latest status when publishing manually.

Only the environment job has `contents: write`. Its signing step receives the four
environment secrets `ANDROID_KEYSTORE_BASE64`, `ANDROID_KEYSTORE_PASSWORD`,
`ANDROID_KEY_ALIAS` and `ANDROID_KEY_PASSWORD`; the upload step receives the job's
GitHub token. Signing uses SDK Build Tools 35.0.0 and JDK 21. The keystore is decoded
into a 0600 file in a private temporary directory, removed even on tool failure.
Passwords are passed through environment variables rather than command arguments;
secret tool diagnostics are not printed. No keystore or password is an artifact.

Signing uses v2/v3 APK signatures (Android 8 minimum), with no legacy v1 or v4
sidecar. After signing, the workflow verifies the APK signature against the selected
keystore certificate, ZIP alignment, production ID/version/provenance and unchanged
manifest, every original payload entry, notices and ARM64 libraries. The public
certificate SHA-256 fingerprint is an asset for comparison with the offline backup.
Unsigned APKs, AARs, logs and fixture output are not release assets.

The base draft contains 14 assets: two DEBs, two RPMs, four matching CLI/GTK archives,
one signed APK, three per-target build-info files, the Android certificate
fingerprint and `SHA256SUMS`. The checksums cover the other 13 assets. Optional on-demand macOS attachment
adds two unsigned packages and two build-info files, updating checksums to cover
all 17 non-checksum assets; it never publishes the draft.
Release asset filenames use the product's hyphen prerelease (for example,
`keyparcel-cli_0.1.0-rc.1-1_amd64.deb`); internal DEB/RPM versions still use `~` for
package-manager ordering. This avoids GitHub's normalization of `~` in uploaded
filenames. Before any upload, checksums must cover exactly the intended final
asset names and bytes; upload responses must preserve names, sizes and digests.
Checksums cover every other asset. They detect changed bytes; they do not replace
trust in GitHub or provide Linux package/repository signatures.

### Inspection, first device test and publication

Download the draft assets and verify `sha256sum -c SHA256SUMS`; compare all three
build-info revisions with the tag's commit and Android versionCode with the
committed value. Check the APK certificate fingerprint against the backed-up
signing key (SDK `apksigner verify --verbose --print-certs APK` reports it).
Before publishing a candidate, install its signed production APK on a physical
ARM64 Android device and try enrollment, peer approval and verified text/file
send, view, save and deletion. This is a manual gate: CI provides static
ARM64 packaging evidence, **not ARM runtime or upgrade assurance**. Retain the
explicit Linux distro limits when editing release notes. Then publish manually
in GitHub after reviewing the draft, prerelease/latest choices and device results.

Keep the original keystore, alias and passwords in separate secure offline
backups, and record its public certificate fingerprint. Test that backups can be
read before relying on the key for updates. Never commit them, place them in
workflow artifacts or rotate/delete the only copy; Android updates require the
same signing identity. This workflow introduces no key rotation or recovery service.

### Reruns and interrupted uploads

Tag runs are serialized. Rerun an interrupted draft job within the seven-day
retention of its same-run build artifacts. It can resume an existing draft only
when its tag, exact source SHA and prerelease flag match. Already-uploaded assets
must have identical sizes and GitHub SHA-256 digests; missing assets can then be uploaded.
Published releases are refused, and assets are never deleted or overwritten.
Do not publish/edit a draft while a run is active. If rebuilding produces different
bytes (builds are not claimed byte-reproducible), the rerun deliberately fails.
Inspect the discrepancy before taking action. A maintainer may explicitly remove
a wholly undistributed incomplete draft, but never reuse a versionCode for different
APK contents or move an existing tag. Otherwise use a new version and versionCode.
The workflow never performs that destructive cleanup for you.
