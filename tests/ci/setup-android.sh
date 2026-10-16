#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/../.."
source android/env.sh
export PATH="$ANDROID_HOME/cmdline-tools/latest/bin:$PATH"
mkdir -p test-artifacts/ci
sdkmanager --licenses < <(yes) > test-artifacts/ci/sdk-licenses.log 2>&1
sdkmanager 'platform-tools' 'emulator' 'platforms;android-35' 'build-tools;35.0.0' \
    'ndk;29.0.14206865' 'system-images;android-35;default;x86_64' \
    > test-artifacts/ci/sdk-install.log 2>&1
