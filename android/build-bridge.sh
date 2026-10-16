#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")"
source env.sh
export ANDROID_NDK_HOME="$ANDROID_HOME/ndk/29.0.14206865"
mkdir -p build/tools
for tool in gomobile gobind; do
    "${GO:-go}" build -o "build/tools/$tool" "golang.org/x/mobile/cmd/$tool"
done
export PATH="$PWD/build/tools:$PATH"
gomobile bind -target=android/amd64 -androidapi=26 -javapkg=dev.keyparcel -o build/keyparcel.aar ./bridge
