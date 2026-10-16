#!/usr/bin/env bash
set -euo pipefail
client_dir=$(cd "$(dirname "$0")" && pwd)
cd "$client_dir"
if [[ ${1:-} != --session ]]; then
    [[ ${KEYPARCEL_GTK_ATTACHED:-} == "${1:-}/fixture" ]] || { echo "Use make test-desktop-client" >&2; exit 1; }
    test_session=$1
    mkdir -p "$test_session"/{config,data,cache}
    # Unix socket paths must fit even when the checkout path is long.
    test_runtime=$(mktemp -d /tmp/keyparcel-gtk-runtime.XXXXXX)
    trap 'rm -rf "$test_runtime"' EXIT
    env -u WAYLAND_DISPLAY -u DBUS_SESSION_BUS_ADDRESS -u AT_SPI_BUS_ADDRESS \
        -u SESSION_MANAGER -u DBUS_STARTER_ADDRESS -u DBUS_STARTER_BUS_TYPE \
        XDG_CONFIG_HOME="$test_session/config" XDG_DATA_HOME="$test_session/data" \
        XDG_CACHE_HOME="$test_session/cache" XDG_RUNTIME_DIR="$test_runtime" \
        KEYPARCEL_DESKTOP_TEST_SESSION="$test_session" \
        GDK_BACKEND=x11 GDK_DEBUG= GDK_DISABLE= GTK_A11Y=atspi GSK_RENDERER=cairo GSETTINGS_BACKEND=memory \
        GTK_USE_PORTAL=0 GIO_USE_VFS=local NO_AT_BRIDGE=0 LC_ALL=C.UTF-8 \
        dbus-run-session -- xvfb-run -a -s '-screen 0 1280x1024x24 -nolisten tcp' \
        timeout 600 bash "$client_dir/test.sh" --session "$test_session"
    exit
fi
[[ ${KEYPARCEL_DESKTOP_TEST_SESSION:-} == "${2:-}" && ${2:-} == "$PWD/build/session."* \
    && ${XDG_RUNTIME_DIR:-} == /tmp/keyparcel-gtk-runtime.* \
    && -d $XDG_RUNTIME_DIR && -O $XDG_RUNTIME_DIR ]] || { echo 'Use test.sh to create an isolated desktop' >&2; exit 1; }
export AT_SPI_BUS_ADDRESS="unix:path=$XDG_RUNTIME_DIR/a11y-bus"
dbus-update-activation-environment DISPLAY XAUTHORITY XDG_RUNTIME_DIR XDG_CONFIG_HOME XDG_DATA_HOME XDG_CACHE_HOME AT_SPI_BUS_ADDRESS GSETTINGS_BACKEND GTK_USE_PORTAL GIO_USE_VFS
# Fedora and Debian/Ubuntu install these at different paths.
a11y_config=/usr/share/defaults/at-spi2/accessibility.conf
[[ -f $a11y_config ]] || a11y_config=/usr/share/at-spi2/accessibility.conf
registry=/usr/libexec/at-spi2-registryd
[[ -x $registry ]] || registry=/usr/lib/at-spi2-core/at-spi2-registryd
dbus-daemon --config-file="$a11y_config" --address="$AT_SPI_BUS_ADDRESS" --nofork > "$2/accessibility-bus.log" 2>&1 &
test_a11y=$!
for attempt in {1..100}; do
    gdbus call --address "$AT_SPI_BUS_ADDRESS" --dest org.freedesktop.DBus --object-path /org/freedesktop/DBus --method org.freedesktop.DBus.ListNames >/dev/null 2>&1 && break
    sleep .05
done
"$registry" > "$2/accessibility-registry.log" 2>&1 &
test_registry=$!
xfwm4 --compositor=off > "$2/window-manager.log" 2>&1 &
test_wm=$!
cleanup() {
    kill -KILL "$test_wm" "$test_registry" "$test_a11y" 2>/dev/null || true
    wait "$test_wm" "$test_registry" "$test_a11y" 2>/dev/null || true
}
trap 'cleanup --session "$2"' EXIT
trap 'exit 143' TERM
trap 'exit 130' INT
for attempt in {1..100}; do
    owner=$(gdbus call --address "$AT_SPI_BUS_ADDRESS" --dest org.freedesktop.DBus --object-path /org/freedesktop/DBus --method org.freedesktop.DBus.NameHasOwner org.a11y.atspi.Registry 2>/dev/null || true)
    [[ $owner == '(true,)' ]] && break
    sleep .05
done
[[ $owner == '(true,)' ]] || { echo 'Private accessibility registry failed' >&2; exit 1; }
if [[ -z ${KEYPARCEL_INSTALLED_IMAGE:-} ]]; then
    ./build/widgets.test -test.v -test.timeout=30s | tee "$2/widgets.log"
fi
python3 -u journey.py "$2" | tee "$2/journey.log"
