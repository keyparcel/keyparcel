#!/usr/bin/env bash
set -euo pipefail
repo_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd)
cd "$repo_dir"
source android/env.sh
state_dir="$repo_dir/.smoke-android"
serial=${ANDROID_SERIAL:-emulator-5554}
app=dev.keyparcel.app.smoke
read -r -a docker_cmd <<< "${KEYPARCEL_DOCKER:-docker}"
fail() { echo "$*" >&2; exit 1; }
load() {
    [[ -f "$state_dir/name" ]] || fail 'Run make smoke-android first.'
    name=$(cat "$state_dir/name")
    [[ "$name" =~ ^keyparcel-android-smoke-[0-9]+-[0-9]+$ ]] || fail 'Invalid Android smoke resource name.'
    [[ $(cat "$state_dir/serial") == "$serial" ]] || fail 'Select the emulator originally used for this smoke fixture.'
}
client() { "${docker_cmd[@]}" exec -i "$name-alice" "$@"; }
clear_smoke_apps() {
    [[ $(adb -s "$serial" emu avd name | head -n1 | tr -d '\r') == keyparcel-software-trial ]] || fail 'Refusing to reset apps on an unexpected emulator.'
    packages=$(adb -s "$serial" shell pm list packages -u "$app" | tr -d '\r')
    for target in "$app" "$app.test"; do
        if printf '%s\n' "$packages" | grep -Fxq "package:$target"; then
            result=$(adb -s "$serial" shell pm clear "$target" | tr -d '\r')
            [[ "$result" == Success ]] || fail "Could not clear $target; reset remains pending."
        fi
    done
}
reset_fixture() {
    # Retain ownership until both app clearing and resource removal succeed.
    clear_smoke_apps
    if adb -s "$serial" reverse --list | awk '$2 == "tcp:2333" {found=1} END {exit !found}'; then
        adb -s "$serial" reverse --remove tcp:2333
    fi
    for actor in alice server; do
        if "${docker_cmd[@]}" container inspect "$name-$actor" >/dev/null 2>&1; then
            "${docker_cmd[@]}" rm -f "$name-$actor" >/dev/null
        fi
    done
    if "${docker_cmd[@]}" network inspect "$name" >/dev/null 2>&1; then
        "${docker_cmd[@]}" network rm "$name" >/dev/null
    fi
    rm -rf -- "$state_dir"
    echo 'Removed Android smoke fixture and its app data; emulator retained.'
}
action=${1:-up}
case "$action" in
up|fresh)
    trap 'echo "Smoke setup failed; state retained. Use make smoke-android-down to reset." >&2' ERR
    if [[ -f "$state_dir/reset-pending" ]]; then
        load
        echo 'Finishing the pending Android smoke reset…'
        bash android/ensure-emulator.sh window
        reset_fixture
    fi
    if [[ -e "$state_dir" ]]; then
        load
        for actor in server alice; do
            [[ $("${docker_cmd[@]}" inspect --format '{{.State.Running}}' "$name-$actor") == true ]] || fail 'Smoke fixture is incomplete; run make smoke-android-down, then retry.'
        done
    else
        echo 'Preparing disposable SSH server and Linux peer…'
        KEYPARCEL_GO="${GO:-go}" bash tests/build-images.sh
        umask 077
        mkdir -p "$state_dir/alice/.ssh"
        name="keyparcel-android-smoke-$(date +%s)-$$"
        printf '%s\n' "$name" > "$state_dir/name"
        printf '%s\n' "$serial" > "$state_dir/serial"
        "${docker_cmd[@]}" network create "$name" >/dev/null
        "${docker_cmd[@]}" run -d --name "$name-server" --network "$name" --network-alias inbox \
            -p 127.0.0.1::22 keyparcel-test-sshd:local >/dev/null
        client_home=$("${docker_cmd[@]}" run --rm keyparcel-test-client:local getent passwd "$(id -u)" | cut -d: -f6)
        [[ -n "$client_home" ]] || fail 'Missing Linux peer home.'
        "${docker_cmd[@]}" run -d --init --hostname alice --name "$name-alice" --network "$name" \
            --user "$(id -u):$(id -g)" --env "HOME=$client_home" --workdir "$client_home" \
            --mount "type=bind,src=$state_dir/alice,dst=$client_home" keyparcel-test-client:local >/dev/null
        client sh -c 'ssh-keygen -q -t ed25519 -N "" -C alice -f "$HOME/.ssh/id_ed25519"'
        cat > "$state_dir/alice/.ssh/config" <<CONFIG
Host inbox
 HostName inbox
 User parcel
 IdentityFile $client_home/.ssh/id_ed25519
 IdentityAgent none
 StrictHostKeyChecking yes
 GlobalKnownHostsFile /dev/null
CONFIG
        for ((attempt=0; attempt<100; attempt++)); do
            if "${docker_cmd[@]}" exec "$name-server" cat /etc/ssh/ssh_host_ed25519_key.pub > "$state_dir/host.pub" 2>/dev/null; then break; fi
            sleep 0.1
        done
        [[ -s "$state_dir/host.pub" ]] || fail 'SSH server did not generate its key.'
        read -r key_type key_data comment < "$state_dir/host.pub"
        printf 'inbox %s %s\n' "$key_type" "$key_data" > "$state_dir/alice/.ssh/known_hosts"
        "${docker_cmd[@]}" exec -i "$name-server" sh -c 'cat > /home/parcel/.ssh/authorized_keys; chown parcel:parcel /home/parcel/.ssh/authorized_keys; chmod 600 /home/parcel/.ssh/authorized_keys' < "$state_dir/alice/.ssh/id_ed25519.pub"
    fi
    if [[ "$action" == fresh ]]; then
        printf 'fresh\n' > "$state_dir/mode"
        touch "$state_dir/fresh-reset-pending"
    fi
    mode=preconfigured
    [[ ! -f "$state_dir/mode" ]] || mode=$(cat "$state_dir/mode")
    [[ "$mode" == preconfigured || "$mode" == fresh ]] || fail 'Invalid Android smoke mode.'
    bash android/ensure-emulator.sh window
    if [[ -f "$state_dir/fresh-reset-pending" ]]; then
        clear_smoke_apps
        rm "$state_dir/fresh-reset-pending"
        echo 'Cleared smoke identity, host settings, and approvals; manual setup mode is retained.'
    fi
    echo 'Building and installing the smoke client…'
    bash android/client/build.sh assembleSmokeDebug assembleSmokeDebugAndroidTest
    adb -s "$serial" install -r android/client/app/build/outputs/apk/smoke/debug/app-smoke-debug.apk
    adb -s "$serial" install -r android/client/app/build/outputs/apk/androidTest/smoke/debug/app-smoke-debug-androidTest.apk
    if [[ "$mode" == preconfigured ]]; then
        host_key=$(cat "$state_dir/host.pub")
        # Fixture-owned server key: quote it for adb shell's remote command joining.
        host_key_quoted="'${host_key//\'/\'\"\'\"\'}'"
        adb -s "$serial" shell am instrument -w -e class dev.keyparcel.app.SmokeSetup \
            -e host_key "$host_key_quoted" "$app.test/androidx.test.runner.AndroidJUnitRunner" | tee "$state_dir/setup.log"
        python3 - "$state_dir" <<'PY'
from pathlib import Path
import re, sys
state = Path(sys.argv[1])
log = (state / 'setup.log').read_text()
if not re.search(r'^OK \(1 test\)\s*$', log, re.MULTILINE):
    raise SystemExit('Smoke app setup failed; see .smoke-android/setup.log')
match = re.search(r'INSTRUMENTATION_STATUS: phone_public=(ssh-ed25519 [A-Za-z0-9+/=]+)', log)
if not match: raise SystemExit('Missing public device identity')
public = match[1] + '\n'
file = state / 'phone.pub'
if file.exists() and file.read_text() != public:
    raise SystemExit('Smoke device identity changed; reset the fixture explicitly before re-enrolling')
file.write_text(public)
PY
        cat "$state_dir/alice/.ssh/id_ed25519.pub" "$state_dir/phone.pub" > "$state_dir/authorized_keys"
        "${docker_cmd[@]}" exec -i "$name-server" sh -c 'cat > /home/parcel/.ssh/authorized_keys; chown parcel:parcel /home/parcel/.ssh/authorized_keys; chmod 600 /home/parcel/.ssh/authorized_keys' < "$state_dir/authorized_keys"
    fi
    if [[ ! -f "$state_dir/linux-configured" ]]; then
        client keyparcel setup --host inbox --default
        touch "$state_dir/linux-configured"
    fi
    if [[ "$mode" == preconfigured ]]; then
        phone_fingerprint=$(client ssh-keygen -lf - < "$state_dir/phone.pub" | awk '{print $2}')
        client keyparcel devices --json > "$state_dir/linux-devices.json"
        if ! python3 - "$state_dir" <<'PY'
from pathlib import Path
import json, sys
state = Path(sys.argv[1])
public = (state / 'phone.pub').read_text().strip()
devices = json.loads((state / 'linux-devices.json').read_text())
raise SystemExit(0 if any(d['public_key'] == public and d['trusted'] and d.get('name') == 'phone' for d in devices) else 1)
PY
        then
            client keyparcel trust "$phone_fingerprint" --name phone
        fi
        port=$("${docker_cmd[@]}" port "$name-server" 22/tcp)
        adb -s "$serial" reverse tcp:2333 "tcp:${port##*:}"
        if [[ ! -f "$state_dir/greeting-sent" ]]; then
            printf 'Hello from Alice!\n' | client keyparcel send --to phone --stdin
            touch "$state_dir/greeting-sent"
        fi
        echo 'Ready. In Settings → Devices, refresh and approve Alice after comparing this fingerprint:'
        client ssh-keygen -lf .ssh/id_ed25519.pub
    else
        port=$("${docker_cmd[@]}" port "$name-server" 22/tcp)
        adb -s "$serial" reverse tcp:2333 "tcp:${port##*:}"
        echo 'Fresh-user mode: identity is created by the app; host configuration, phone enrollment, and approval are manual.'
        echo 'Server settings: address 127.0.0.1; port 2333; SSH username parcel'
        echo 'Storage path: .local/share/keyparcel; discovery path: .ssh/authorized_keys'
        echo 'Server public key:'
        cat "$state_dir/host.pub"
        echo 'In Settings, enter the connection details and press Connect; approve only after comparing this server fingerprint:'
        client ssh-keygen -lf - < "$state_dir/host.pub"
        echo 'Verify Alice in Settings → Devices using this fingerprint:'
        client ssh-keygen -lf .ssh/id_ed25519.pub
        cat <<'INSTRUCTIONS'
Enroll this phone from the VM terminal in the project directory:
  PHONE_PUBLIC_KEY='paste the public enrollment key from Settings here'
  smoke_name=$(cat .smoke-android/name)
  printf '%s\n' "$PHONE_PUBLIC_KEY" | docker exec -i "$smoke_name-server" sh -c 'cat >> /home/parcel/.ssh/authorized_keys'
Then open make smoke-android-alice, compare the phone fingerprint, and run:
  keyparcel devices
  keyparcel trust <phone-fingerprint-from-Settings> --name phone
Reopen/rebuild with make smoke-android to retain manual setup. Only another
explicit fresh launch or smoke teardown discards this identity. Old addressed
transfers cannot be read with the replacement key; re-enrollment is required.
INSTRUCTIONS
    fi
    echo 'Linux shell: make smoke-android-alice'
    echo 'Closing the phone window stops Android but preserves app data and the fixture.'
    adb -s "$serial" shell am force-stop "$app"
    adb -s "$serial" shell am start -W -n "$app/dev.keyparcel.app.MainActivity"
    if command -v xdotool >/dev/null 2>&1; then
        xdotool search --name 'Android Emulator.*keyparcel-software-trial' windowraise windowfocus || true
    fi
    echo 'Phone ready. This task may now finish; the emulator window remains open.'
    ;;
alice)
    load
    "${docker_cmd[@]}" exec -it "$name-alice" bash
    ;;
down)
    [[ -e "$state_dir" ]] || { echo 'No Android smoke fixture to remove.'; exit 0; }
    load
    touch "$state_dir/reset-pending"
    if adb -s "$serial" get-state >/dev/null 2>&1; then
        reset_fixture
    else
        echo 'Android is stopped; reset is pending. Fixture and app data are retained until the next make smoke-android launch.'
    fi
    ;;
*) fail 'Usage: android.sh up|fresh|alice|down' ;;
esac
