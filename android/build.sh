#!/usr/bin/env bash
set -euo pipefail
android_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
cd "$android_dir"
: "${ANDROID_HOME:?Set ANDROID_HOME to your Android SDK directory}"
: "${JAVA_HOME:?Set JAVA_HOME to your JDK directory}"
export PATH="$JAVA_HOME/bin:$PATH"
export ANDROID_NDK_HOME="$ANDROID_HOME/ndk/29.0.14206865"
tools="$ANDROID_HOME/build-tools/35.0.0"
android_jar="$ANDROID_HOME/platforms/android-35/android.jar"
build="$android_dir/build"
bash build-bridge.sh
rm -rf -- "$build/aar" "$build/classes" "$build/dex"
mkdir -p "$build/aar" "$build/classes" "$build/dex"
python3 - "$build" <<'PY'
from pathlib import Path
from zipfile import ZipFile
import sys
build = Path(sys.argv[1])
with ZipFile(build / 'keyparcel.aar') as aar:
    aar.extractall(build / 'aar')
PY
sources=(src/dev/keyparcel/verification/*.java src/dev/keyparcel/storage/*.java)
javac --release 8 -encoding UTF-8 -cp "$android_jar:$build/aar/classes.jar" -d "$build/classes" "${sources[@]}"
jar cf "$build/app.jar" -C "$build/classes" .
"$tools/d8" --min-api 26 --lib "$android_jar" --output "$build/dex" "$build/aar/classes.jar" "$build/app.jar"
"$tools/aapt2" link -o "$build/unsigned.apk" -I "$android_jar" --manifest AndroidManifest.xml
python3 - "$build" <<'PY'
from pathlib import Path
from zipfile import ZipFile, ZIP_STORED
import sys
build = Path(sys.argv[1])
with ZipFile(build / 'unsigned.apk', 'a') as apk:
    for name in ('LICENSE', 'NOTICE', 'THIRD_PARTY_NOTICES.txt'):
        apk.write(build.parent.parent / name, 'assets/licenses/' + name)
    for dex in sorted((build / 'dex').glob('*.dex')):
        apk.write(dex, dex.name)
    for library in sorted((build / 'aar/jni').glob('*/*.so')):
        apk.write(library, 'lib/' + str(library.relative_to(build / 'aar/jni')), compress_type=ZIP_STORED)
PY
if [[ ! -f "$build/debug.keystore" ]]; then
    keytool -genkeypair -keystore "$build/debug.keystore" -storepass android -keypass android \
        -alias androiddebugkey -keyalg RSA -keysize 2048 -validity 3650 -dname 'CN=keyparcel verification'
fi
"$tools/zipalign" -P 16 -f 4 "$build/unsigned.apk" "$build/aligned.apk"
"$tools/apksigner" sign --ks "$build/debug.keystore" --ks-pass pass:android --key-pass pass:android \
    --out "$build/verification.apk" "$build/aligned.apk"
"$tools/apksigner" verify "$build/verification.apk"
"${GO:-go}" version > "$build/versions.txt"
java -version >> "$build/versions.txt" 2>&1
"${GO:-go}" list -m all >> "$build/versions.txt"
for component in emulator platform-tools platforms/android-35 build-tools/35.0.0 ndk/29.0.14206865 system-images/android-35/default/x86_64; do
    cat "$ANDROID_HOME/$component/source.properties" >> "$build/versions.txt"
done
echo "Built $build/verification.apk"
