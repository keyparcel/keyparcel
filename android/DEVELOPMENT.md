# Android development

[Android overview](README.md) · [Client development](client/DEVELOPMENT.md) ·
[Repository development guide](../docs/development.md)

## Build prerequisites

Use the Go version in `go.mod`, JDK 21, Python 3, and the Android SDK command-line
tools. Set `JAVA_HOME` and `ANDROID_HOME` to your installed toolchains and put the
JDK, SDK command-line tools and `platform-tools` on `PATH`. The scripts can also
load an optional `~/.local/share/keyparcel-android/env.sh`; explicit settings take
precedence.

Install the SDK components and accept their licenses:

```sh
sdkmanager --licenses
sdkmanager 'platform-tools' 'emulator' 'platforms;android-35' \
  'build-tools;35.0.0' 'ndk;29.0.14206865' \
  'system-images;android-35;default;x86_64'
```

The SDK/NDK components above match the build scripts. Go mobile is pinned in the
Android Go module; builds record actual tool versions in `build/versions.txt`.
Ordinary Go and Linux CLI tests need no Android tooling.

## Emulator and test workflow

Create `keyparcel-software-trial` once if it does not exist:

```sh
echo no | avdmanager create avd -n keyparcel-software-trial \
  -k 'system-images;android-35;default;x86_64'
```

For the software-emulation baseline, set these entries in its `config.ini`
under `ANDROID_AVD_HOME` (normally `~/.android/avd`):

```ini
hw.cpu.ncore=2
hw.ramSize=2560
hw.lcd.width=480
hw.lcd.height=800
hw.lcd.density=160
hw.keyboard=yes
```

From the repository root, start the emulator in one terminal:

```sh
make android-emulator
```

This uses software CPU emulation and SwANGLE with snapshots disabled. Leave the
emulator running across development iterations. Hosted CI uses KVM separately.

Once `adb -s emulator-5554 shell getprop sys.boot_completed` prints `1`, run in
another terminal:

```sh
make test-android
```

The target builds the AAR and APK, runs bridge unit tests, and runs the Android
scenario. It reuses the existing Docker SSH server and Linux client fixture,
publishes SSH only on host loopback, and uses ADB reverse forwarding to reach it
from the app. No personal keys or real servers are involved. Related checks
share one emulator and one disposable server fixture. Each invocation resets
only `dev.keyparcel.verification` app data, so its generated identity is
disposable. Do not put real identities in this test app.

The scenario generates and encrypts an Ed25519 identity, enrolls its public key,
checks exact Unicode text and binary transfers in both directions, inbox
listing, incorrect host-key rejection, and rejection/retention of unapproved
and corrupted incoming transfers. Java delivery errors must also retain the
incoming copy. It stops and restarts the app process,
checks the same identity, and receives another Linux transfer without
re-enrollment. Identity ciphertext tampering is also checked and restored
within the same fixture.

Instrumentation logs, the successful verification report, and tool versions
are saved under `android/build/`. Failed server scenarios also use the ordinary
`test-artifacts/` capture. Run Android scenarios sequentially on a given emulator;
`ANDROID_SERIAL` can select another already running emulator.

Stop the emulator when finished:

```sh
adb -s emulator-5554 emu kill
```

## Hardware-accelerated CI

GitHub Actions uses the [repository CI runner](../tests/ci/README.md) with
`KEYPARCEL_ANDROID_ACCEL=on` and isolated headless AVDs. The regular development
launcher retains software CPU emulation by default. Android CI uses its own AVD
registry and UI data directory; existing headless/serial/ownership guards remain.
Hosted CI exercises KVM; software emulation remains available on hosts without it.

## Identity and platform boundaries

`bridge/` adapts only the required core operations to Go mobile's binding types.
Receive invokes an app callback with the signed metadata and verified staging
path. The app consumes that file during the callback; the core removes staging
afterward, and callback errors retain the remote transfer. This avoids the CLI's
hard-link publication helper: [Android's app security policy](https://android.googlesource.com/platform/system/sepolicy/+/refs/heads/android15-release/private/app_neverallows.te)
forbids apps from creating hard links. The Compose client handles document pickers
and destination writes.

The generated private SSH key crosses JNI only into the internal identity
store. The store encrypts it with AES-256-GCM using an Android Keystore key and
writes ciphertext with `AtomicFile` under `getNoBackupFilesDir()`. That directory
is excluded from backup and device transfer; the app also disables backup in its
manifest. No plaintext private-key file is required. Java private-key byte arrays
are cleared after use; Go/JNI copies remain subject to runtime garbage collection.

Private-key material is decrypted for the operation/session, because SSHSIG and
age need the software identity. A non-exportable signing-only Keystore key is
not a replacement. If the ciphertext or Keystore key disappears, the app reports
the loss rather than silently replacing an existing identity.

Reinstalling or losing the device without recovery of the old private key
requires re-enrollment. Transfers encrypted to the old identity cannot be
received by a newly generated one. Identity recovery is not implemented.

Development/test builds target the x86_64 emulator. The separate
[candidate build](../release/README.md) produces an unsigned ARM64 APK with static
checks. Physical ARM64 execution and signing/publication remain deferred.
The verification APK is a test harness, not the user-facing Compose application.
