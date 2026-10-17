#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/../.."
source android/env.sh
export PATH="$ANDROID_HOME/cmdline-tools/latest/bin:$PATH"
mkdir -p test-artifacts/ci
sdkmanager --sdk_root="$ANDROID_HOME" --licenses < <(yes) > test-artifacts/ci/sdk-licenses.log 2>&1
sdkmanager --sdk_root="$ANDROID_HOME" 'platform-tools' 'emulator' 'platforms;android-35' 'build-tools;35.0.0' \
    'ndk;29.0.14206865' 'system-images;android-35;default;x86_64' \
    > test-artifacts/ci/sdk-install.log 2>&1
properties=(platform-tools emulator platforms/android-35 build-tools/35.0.0 ndk/29.0.14206865 system-images/android-35/default/x86_64)
revision=$(
    for package in "${properties[@]}"; do
        printf '%s\n' "$package"
        cat "$ANDROID_HOME/$package/source.properties"
    done | sha256sum | cut -d ' ' -f 1
)
for package in "${properties[@]}"; do
    stat -c 'SDK layout: %A %U:%G %N' "$ANDROID_HOME/$package"
    printf 'SDK package: %s ' "$package"
    grep '^Pkg.Revision' "$ANDROID_HOME/$package/source.properties"
done
if [[ -n ${GITHUB_OUTPUT:-} ]]; then
    printf 'revision=%s\n' "$revision" >> "$GITHUB_OUTPUT"
fi
