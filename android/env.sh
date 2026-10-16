#!/usr/bin/env bash
# Optional local toolchain configuration; explicit environment settings take precedence.
if [[ -z ${ANDROID_HOME:-}${JAVA_HOME:-} ]]; then
    if [[ -f "$HOME/.local/share/keyparcel-android/env.sh" ]]; then
        source "$HOME/.local/share/keyparcel-android/env.sh"
    fi
fi
: "${ANDROID_HOME:?Set ANDROID_HOME to your Android SDK directory}"
: "${JAVA_HOME:?Set JAVA_HOME to your JDK directory}"
export PATH="$JAVA_HOME/bin:$ANDROID_HOME/platform-tools:$PATH"
