#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."
source android/env.sh
export ANDROID_NDK_HOME="$ANDROID_HOME/ndk/29.0.14206865"
release_android_dir=${KEYPARCEL_RELEASE_ANDROID_DIR:?}
mkdir -p "$release_android_dir/tools" "$release_android_dir/build"
for tool in gomobile gobind; do
    (cd android && "${GO:-go}" build -o "$release_android_dir/tools/$tool" "golang.org/x/mobile/cmd/$tool")
done
export PATH="$release_android_dir/tools:$PATH"
(cd android && gomobile bind -target=android/arm64 -androidapi=26 -javapkg=dev.keyparcel -o "$release_android_dir/build/keyparcel.aar" ./bridge)
export ANDROID_HOME JAVA_HOME
(cd "$release_android_dir/work/client" && ./gradlew --console=plain assembleProductionRelease \
    "-PkeyparcelAar=$release_android_dir/build/keyparcel.aar" \
    "-PreleaseVersion=$KEYPARCEL_RELEASE_VERSION" "-PreleaseVersionCode=$KEYPARCEL_RELEASE_CODE" \
    "-PsourceRevision=$KEYPARCEL_RELEASE_REVISION")
cp "$release_android_dir/work/client/app/build/outputs/apk/production/release/app-production-release-unsigned.apk" "$release_android_dir/keyparcel-$KEYPARCEL_RELEASE_VERSION-arm64-unsigned.apk"
"$ANDROID_HOME/build-tools/35.0.0/zipalign" -c -P 16 4 "$release_android_dir/keyparcel-$KEYPARCEL_RELEASE_VERSION-arm64-unsigned.apk"
if "$ANDROID_HOME/build-tools/35.0.0/apksigner" verify "$release_android_dir/keyparcel-$KEYPARCEL_RELEASE_VERSION-arm64-unsigned.apk"; then
    echo 'Candidate must be unsigned' >&2
    exit 1
fi
"$ANDROID_HOME/build-tools/35.0.0/aapt2" dump badging "$release_android_dir/keyparcel-$KEYPARCEL_RELEASE_VERSION-arm64-unsigned.apk" > "$release_android_dir/apk-badging.txt"
"$ANDROID_HOME/build-tools/35.0.0/aapt2" dump xmltree "$release_android_dir/keyparcel-$KEYPARCEL_RELEASE_VERSION-arm64-unsigned.apk" --file AndroidManifest.xml > "$release_android_dir/apk-manifest.txt"
