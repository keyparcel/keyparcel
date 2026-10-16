#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")"
source env.sh
mode=${1:-headless}
[[ "$mode" == headless || "$mode" == window || "$mode" == test ]] || { echo 'Choose headless, window, or test mode' >&2; exit 1; }
accel=${KEYPARCEL_ANDROID_ACCEL:-off}
[[ "$accel" == off || "$accel" == on || "$accel" == auto ]] || { echo 'Choose Android acceleration off, on or auto' >&2; exit 1; }
serial=${ANDROID_SERIAL:-emulator-5554}
avd=keyparcel-software-trial
emulator_log=build/emulator.log
check_test_emulator() {
    [[ $(adb -s "$serial" emu avd name | head -n1 | tr -d '\r') == "$avd" ]] || { echo 'Port 5556 belongs to another emulator.' >&2; exit 1; }
    if pgrep -f "$ANDROID_HOME/emulator/qemu/linux-x86_64/qemu-system-x86_64 -avd $avd.*-port 5556" >/dev/null; then
        echo 'The test emulator must be headless to isolate desktop clipboard sharing; close its window first.' >&2
        exit 1
    fi
}
if [[ "$mode" == test ]]; then
    serial=${ANDROID_SERIAL:-emulator-5556}
    [[ "$serial" == emulator-5556 ]] || { echo 'UI tests use the isolated emulator-5556; unset ANDROID_SERIAL.' >&2; exit 1; }
    avd=keyparcel-ui-tests
    emulator_log=build/ui-emulator.log
    avd_home=${ANDROID_AVD_HOME:-${ANDROID_EMULATOR_HOME:-${ANDROID_USER_HOME:-$HOME/.android}}/avd}
    test_avd_dir=${KEYPARCEL_ANDROID_TEST_AVD_DIR:-"$PWD/build/ui-avd"}
    if [[ ! -f "$avd_home/$avd.ini" ]]; then
        template="$avd_home/keyparcel-software-trial.avd/config.ini"
        [[ -f "$template" ]] || { echo 'Create the baseline keyparcel-software-trial AVD first; see android/README.md.' >&2; exit 1; }
        mkdir -p build
        printf 'no\n' | avdmanager create avd -n "$avd" -p "$test_avd_dir" -k 'system-images;android-35;default;x86_64'
        python3 - "$template" "$test_avd_dir/config.ini" <<'PY'
from pathlib import Path
import sys
def read(path):
    return dict((key.strip(), value.strip()) for line in Path(path).read_text().splitlines() if '=' in line for key, value in [line.split('=', 1)])
template, destination = read(sys.argv[1]), read(sys.argv[2])
for key, value in template.items():
    if key in ('hw.cpu.ncore', 'hw.ramSize', 'hw.keyboard', 'disk.dataPartition.size') or key.startswith('hw.lcd.'):
        destination[key] = value
Path(sys.argv[2]).write_text(''.join(f'{key}={value}\n' for key, value in destination.items()))
PY
    fi
    grep -Fxq "path=$test_avd_dir" "$avd_home/$avd.ini" || { echo 'The test AVD points elsewhere; refusing to use unrelated emulator data.' >&2; exit 1; }
    if adb -s "$serial" get-state >/dev/null 2>&1; then
        check_test_emulator
    fi
fi
if [[ "$mode" == window ]]; then
    [[ "$serial" == emulator-5554 ]] || { echo 'Smoke window startup uses the local emulator-5554 AVD' >&2; exit 1; }
    [[ -n ${DISPLAY:-}${WAYLAND_DISPLAY:-} ]] || { echo 'A desktop display is required for the phone window' >&2; exit 1; }
    if pgrep -f "$ANDROID_HOME/emulator/qemu/linux-x86_64/qemu-system-x86_64-headless.*-avd keyparcel-software-trial.*-port 5554" >/dev/null; then
        [[ "$serial" == emulator-5554 ]] || { echo 'Use the local smoke emulator for window mode' >&2; exit 1; }
        [[ $(adb -s "$serial" emu avd name | head -n1 | tr -d '\r') == keyparcel-software-trial ]] || { echo 'Unexpected emulator; refusing to restart it' >&2; exit 1; }
        echo 'Switching the owned emulator to a desktop window; app data is preserved…'
        adb -s "$serial" emu kill
        for ((attempt=0; attempt<30; attempt++)); do
            if ! adb devices | awk '{print $1}' | grep -Fxq "$serial"; then break; fi
            sleep 1
        done
    fi
fi
if ! adb devices | awk '{print $1}' | grep -Fxq "$serial"; then
    [[ "$serial" == emulator-5554 || "$mode" == test ]] || { echo 'Start the selected emulator first' >&2; exit 1; }
    echo "Starting Android with acceleration $accel; cold boot may take several minutes…"
    mkdir -p build
    window_flags=()
    [[ "$mode" == window ]] || window_flags=(-no-window)
    # Keep the phone alive when its terminal or IDE task finishes.
    ANDROID_I_WANT_MY_TCG=yes nohup setsid "$ANDROID_HOME/emulator/emulator" \
        -avd "$avd" -accel "$accel" -gpu swangle "${window_flags[@]}" \
        -no-audio -no-boot-anim -no-metrics -no-snapshot -feature -Vulkan -port "${serial#emulator-}" \
        > "$emulator_log" 2>&1 < /dev/null &
    emulator_pid=$!
fi
for ((attempt=0; attempt<300; attempt++)); do
    if [[ $(adb -s "$serial" shell getprop sys.boot_completed 2>/dev/null | tr -d '\r') == 1 ]]; then
        [[ "$mode" != test ]] || check_test_emulator
        if [[ "$mode" == window ]]; then
            [[ $(adb -s "$serial" emu avd name | head -n1 | tr -d '\r') == keyparcel-software-trial ]] || { echo 'Window mode requires the owned smoke AVD' >&2; exit 1; }
        fi
        echo "Android ready: $serial"
        exit 0
    fi
    if [[ -n ${emulator_pid:-} ]] && ! kill -0 "$emulator_pid" 2>/dev/null; then
        echo "Emulator exited; inspect android/$emulator_log" >&2
        exit 1
    fi
    sleep 2
done
echo "Android did not finish booting; inspect android/$emulator_log" >&2
exit 1
