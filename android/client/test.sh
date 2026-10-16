#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/../.."
source android/env.sh
python3 tests/smoke/android_test.py
python3 android/ensure_emulator_test.py
bash android/ensure-emulator.sh test &
emulator_setup_pid=$!
cleanup() {
    if [[ -n $emulator_setup_pid ]]; then
        kill "$emulator_setup_pid" 2>/dev/null || true
        wait "$emulator_setup_pid" 2>/dev/null || true
    fi
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM
export ANDROID_SERIAL=emulator-5556
build_started=$SECONDS
bash android/client/build.sh assembleScenarioDebug assembleScenarioDebugAndroidTest testScenarioDebugUnitTest
echo "Android client build completed after $((SECONDS - build_started))s; waiting for emulator readiness"
emulator_status=0
wait "$emulator_setup_pid" || emulator_status=$?
emulator_setup_pid=
(( emulator_status == 0 )) || exit "$emulator_status"
echo "Android client build and emulator ready after $((SECONDS - build_started))s"
(cd android && "${GO:-go}" test ./bridge)
KEYPARCEL_GO="${GO:-go}" "${GO:-go}" test -tags=e2e,androidtest -count=1 -v -timeout=35m \
    ./tests/e2e -run '^TestAndroidTextClient$'
