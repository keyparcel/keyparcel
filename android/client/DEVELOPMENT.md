# Android client development

[User guide](README.md) · [Toolchain and emulator setup](../DEVELOPMENT.md) ·
[Repository development guide](../../docs/development.md)

Run `make` commands from the repository root. `make android-client-build` builds
the Go bridge, debug APKs and instrumentation APKs; the scripts use the pinned
Gradle wrapper. Debug builds are for development, not signed release artifacts.

## Try the interactive phone

With the Android prerequisites from [Android toolchain setup](../DEVELOPMENT.md) installed:

```sh
make smoke-android
```

The command prepares/reuses a disposable Docker SSH server and Alice CLI client,
builds and installs the app, enrolls its public key in that fixture, and opens
the emulator's own desktop phone window. It can load the optional SDK/JDK
environment file described in [toolchain setup](../DEVELOPMENT.md). The VS Code
task **keyparcel: Android smoke** runs the same command.

The fixture pins its own known server key. In the app, open **Devices**,
press **Refresh**, compare Alice's fingerprint with the one printed in the smoke
terminal, then approve it with a name such as `alice`. Return to Inbox and refresh
to see the six sample parcels described below. For a real server, enter its
address, port and username in Settings and press **Connect**. Remote paths are under **Advanced
settings**. The app discovers its key and
shows the endpoint, key type, and fingerprint. Compare that fingerprint through
a trusted source before **Approve & connect**. Manually enroll this device's
public key on the server; it is never uploaded automatically.
The setup convenience lives in the separate instrumentation APK, not an exported
setup interface in the client.

Use mouse clicks as touches and the keyboard for text entry. The AVD must have
`hw.keyboard=yes` (included in the Android setup instructions); changing it
requires restarting the emulator. Open Alice's shell:

```sh
make smoke-android-alice
```

Inside that shell:

```sh
printf 'Hello from Linux!\n' | keyparcel send --to phone --stdin
keyparcel send --to phone --file /etc/os-release
keyparcel inbox
keyparcel receive <text-id> --stdout
keyparcel receive <file-id> --save-to ./received-file
```

On the phone, **Send → Text**, select the approved Alice name, enter text, and
press **Send**. For a file, choose **Send → File → Choose file**, select a document
in Android's picker, choose a recipient, and press **Send**. Refresh the Linux inbox
to receive it. **This device** sends to your own inbox without approving yourself; the server must still have your
public key enrolled.

Repeated smoke launches reinstall updated app code while preserving its key,
server settings, approvals, and remote transfers. The Docker containers remain
running. Closing the native emulator window stops Android and preserves app
data; reopening may therefore incur a cold boot. A running windowed emulator is
reused. Switching an already running headless emulator to window mode requires
one restart. Software cold boot can take several minutes. If startup services interrupt setup,
let Android settle, dismiss stale system-service dialogs with **Wait**, and rerun
the same command. Partial setup resumes without resetting app data.

Reset only the Android smoke fixture and smoke app data with:

```sh
make smoke-android-down
```

This deletes its disposable keys, transfers, and app configuration. It leaves the
shared emulator running and does not touch the ordinary Alice/Bob smoke fixture,
scenario app, or headless verification app. State lives in ignored
`.smoke-android/`. Reset/recreate to update the Linux peer's container image;
ordinary reruns reuse its running container.

If Android is stopped, teardown reports a **pending reset** and retains the
fixture until `make smoke-android` starts Android again. That launch confirms
the smoke apps were cleared before removing the old fixture and creating a new
one. Failed app clearing retains the pending reset and ownership state for retry.

## Try first-time setup

```sh
make smoke-android-fresh
```

The IDE task **keyparcel: Android fresh-user smoke** runs the same command. Each
explicit fresh launch clears only smoke app data, including its identity, host
settings, and approvals. The app generates its replacement identity normally.
The emulator, other app variants, Docker SSH server, Linux peer, and server-side
transfers remain intact. Discarding a key requires re-enrollment and prevents
reading transfers addressed to the old identity.

The terminal prints the server address (`127.0.0.1`), forwarded port (`2333`),
username (`parcel`), public key/fingerprint, remote paths, Alice's fingerprint,
and manual enrollment commands. In Settings, copy the public enrollment key.
From the project terminal, replace the placeholder before running:

```sh
PHONE_PUBLIC_KEY='paste the public enrollment key from Settings here'
smoke_name=$(cat .smoke-android/name)
printf '%s\n' "$PHONE_PUBLIC_KEY" | docker exec -i "${smoke_name}-server" \
  sh -c 'cat >> /home/parcel/.ssh/authorized_keys'
```

Enter the printed server settings in the app and press **Connect**; no server
public-key paste is needed. Compare the displayed fingerprint with the printed
fixture fingerprint, then choose **Approve & connect**. If you approve before
enrolling the phone, authentication fails but your explicit server approval is
retained; enroll the public key and press Connect again. Refresh Devices, compare
Alice's fingerprint, and approve her. In
`make smoke-android-alice`, verify the phone fingerprint and run
`keyparcel trust <phone-fingerprint> --name phone` to approve the phone for Linux
receiving. Fresh mode does not configure the phone, enroll it, approve peers,
or send an automatic greeting.

Use ordinary `make smoke-android` to reopen or rebuild: it remembers fresh mode
and preserves manually completed setup. Another explicit fresh launch resets
the phone again. Clearing failures retain a pending fresh reset for retry after
Android restarts. To return to the original preconfigured fixture, run
`make smoke-android-down`, then `make smoke-android`; this discards the entire
owned fixture and its transfers.

## Emulator clipboard

The desktop emulator's clipboard sharing has a separate
[32 KiB limit](https://android.googlesource.com/platform/external/qemu/+/refs/heads/emu-main-dev/android/emu/hardware/src/android/emulation/ClipboardPipe.cpp).
This host-sharing limit is separate from the app clipboard. If large copies
interrupt sharing, close the emulator window and reopen it with
`make smoke-android`; app data is preserved. Public keys and fingerprints
fit within the sharing limit; paste them with **Ctrl+V** in a host editor or
**Ctrl+Shift+V** in a host terminal.

## Automated checks

```sh
make test-android-client
```

The target runs the complete sequential journey by default. Set
`KEYPARCEL_ANDROID_CLIENT_SHARD=network` or `files` for one independent shard,
as described in [CI](../../tests/ci/README.md#android-client-shards). Each shard
builds its own APKs and performs real enrollment against its own SSH fixture.

The target builds the bridge/app/test APKs and uses a separate headless AVD,
`keyparcel-ui-tests` on `emulator-5556`. It creates/reuses test state under
`android/build/ui-avd/` from the installed image and baseline calibration. Manual
smoke uses the windowed `keyparcel-software-trial` AVD on 5554. Tests refuse an
occupied/wrong/windowed emulator or another `ANDROID_SERIAL`; unset that variable
for the UI target. Stop just the test emulator with `adb -s emulator-5556 emu kill`.

The scenario app (`io.github.keyparcel.keyparcel.scenario`) and smoke app
(`io.github.keyparcel.keyparcel.smoke`) have separate data and forwarding ports.
Tests reset only scenario data; manual smoke can stay open. Run journeys sequentially on each
emulator because they share its foreground screen. Clipboard limits are exercised
inside the isolated test Android system, not the interactive phone's sharing link.

The [real UI journey](app/src/androidTest/java/dev/keyparcel/app/ClientJourneyTest.kt)
and [Linux peer fixture](../../tests/e2e/android_client_test.go) cover enrollment,
server/device trust, automatic refresh/lifecycle, drafts, text/files, system pickers,
clipboard limits, cancellation, timeouts and failure retention. They compare actual
bytes and filenames against the independent CLI; screenshots are diagnostics,
not pixel baselines. The software keyboard remains enabled. Screenshots and
instrumentation logs are saved under `android/client/build/`; host fixture failures
also use `test-artifacts/`. [CI](../../tests/ci/README.md) has hosted execution details.

Reset and emulator ownership checks can run without a device or Docker:

```sh
python3 tests/smoke/android_test.py
python3 android/ensure_emulator_test.py
```

These also run before the UI journey and never clear manual app data. The
headless core verification target can separately use `ANDROID_SERIAL=emulator-5556`
when that device is already running; it resets only its own package.

For fast UI edits after building the bridge once, from `android/client/`:

```sh
source ../env.sh
./gradlew --console=plain assembleSmokeDebug
./gradlew --console=plain testScenarioDebugUnitTest  # No emulator needed
```

The pinned Gradle wrapper and project build files specify SDK 35, AGP, Kotlin and
Compose versions. See [toolchain setup](../DEVELOPMENT.md) before building.

The preconfigured smoke Inbox is seeded once with six representative parcels:
small text, a UTF-8 text file, small binary, bounded-preview text, oversized text
and oversized binary, newest first. Approve the smoke peer to view them; reopening
retains the fixture and does not resend the samples. Fresh Android setup remains
manual and does not seed parcels before enrollment.
