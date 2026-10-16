#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/../.."
source android/env.sh
[[ ${1:-} == verification || ${1:-} == client ]] || { echo 'Select verification or client' >&2; exit 1; }
[[ ${KEYPARCEL_ANDROID_ACCEL:-} == on ]] || { echo 'CI requires KEYPARCEL_ANDROID_ACCEL=on' >&2; exit 1; }
[[ -r /dev/kvm && -w /dev/kvm ]] || { echo 'Hardware-accelerated Android requires access to /dev/kvm' >&2; exit 1; }
export PATH="$ANDROID_HOME/cmdline-tools/latest/bin:$ANDROID_HOME/emulator:$PATH"
export ANDROID_AVD_HOME="$PWD/android/build/ci-avds"
export KEYPARCEL_ANDROID_TEST_AVD_DIR="$ANDROID_AVD_HOME/keyparcel-ui-tests.avd"
export ANDROID_SERIAL=emulator-5554
[[ "$1" != client ]] || export ANDROID_SERIAL=emulator-5556
if adb -s "$ANDROID_SERIAL" get-state >/dev/null 2>&1; then
    echo 'The CI emulator port is occupied; stop that device before running CI checks' >&2
    exit 1
fi
mkdir -p "$ANDROID_AVD_HOME"
avd=keyparcel-software-trial
[[ "$ANDROID_SERIAL" != emulator-5556 ]] || avd=keyparcel-ui-tests
cleanup() {
    adb -s "$ANDROID_SERIAL" logcat -d > "android/build/ci-$1-logcat.txt" 2>&1 || true
    # Kill only the expected CI emulator; leave a foreign device alone.
    if [[ $(adb -s "$ANDROID_SERIAL" emu avd name 2>/dev/null | head -n1 | tr -d '\r') == "$avd" ]]; then
        adb -s "$ANDROID_SERIAL" emu kill >/dev/null 2>&1 || true
    fi
}
trap 'cleanup "$1"' EXIT
trap 'exit 130' INT
trap 'exit 143' TERM
if [[ ! -f "$ANDROID_AVD_HOME/keyparcel-software-trial.ini" ]]; then
    printf 'no\n' | avdmanager create avd -n keyparcel-software-trial \
        -p "$ANDROID_AVD_HOME/keyparcel-software-trial.avd" -k 'system-images;android-35;default;x86_64'
    python3 - "$ANDROID_AVD_HOME/keyparcel-software-trial.avd/config.ini" <<'PYTHON'
from pathlib import Path
import sys
path = Path(sys.argv[1])
settings = dict((key.strip(), value.strip()) for line in path.read_text().splitlines() if '=' in line for key, value in [line.split('=', 1)])
settings.update({'hw.cpu.ncore': '2', 'hw.ramSize': '2560', 'hw.lcd.width': '480',
                 'hw.lcd.height': '800', 'hw.lcd.density': '160', 'hw.keyboard': 'yes'})
path.write_text(''.join(f'{key}={value}\n' for key, value in settings.items()))
PYTHON
fi
if [[ "$1" == client ]]; then
    make test-android-client
else
    bash android/ensure-emulator.sh headless
    make test-android
fi
