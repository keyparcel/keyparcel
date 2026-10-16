#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")"
source ../env.sh
bash ../build-bridge.sh
if [[ $# == 0 ]]; then
    set -- assembleSmokeDebug assembleSmokeDebugAndroidTest assembleScenarioDebug assembleScenarioDebugAndroidTest
fi
./gradlew --console=plain "$@"
