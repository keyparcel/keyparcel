#!/usr/bin/env bash
set -euo pipefail
# Reuse hosted packages without restoring archives over their protected files.
hosted_sdk=${ANDROID_HOME:?}
ci_sdk=${RUNNER_TEMP:?}/keyparcel-android-sdk
mkdir -p "$ci_sdk"
# avdmanager locates its SDK from its real tool path, ignoring a linked SDK root.
cp -R "$hosted_sdk/cmdline-tools" "$ci_sdk/cmdline-tools"
for package in platforms/android-35 build-tools/35.0.0 ndk/29.0.14206865; do
    if [[ -d "$hosted_sdk/$package" ]]; then
        stat -c 'Hosted SDK: %A %U:%G %n' "$hosted_sdk/$package"
        mkdir -p "$(dirname "$ci_sdk/$package")"
        ln -s "$hosted_sdk/$package" "$ci_sdk/$package"
    fi
done
printf 'ANDROID_HOME=%s\nANDROID_SDK_ROOT=%s\n' "$ci_sdk" "$ci_sdk" >> "${GITHUB_ENV:?}"
printf 'image=%s\n' "${ImageVersion:?}" >> "${GITHUB_OUTPUT:?}"
