#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/../.."
mkdir -p test-artifacts/ci
case ${1:-} in
    fast)
        "${GO:-go}" test -race ./...
        "${GO:-go}" vet ./...
        (cd android && "${GO:-go}" test -race ./bridge && "${GO:-go}" vet ./bridge)
        python3 tests/smoke/smoke_test.py
        python3 tests/smoke/android_test.py
        python3 android/ensure_emulator_test.py
        (cd desktop/client && python3 -m unittest -v runner_test)
        python3 -m unittest discover -v -s tests/ci -p 'test_*.py'
        ;;
    cli)
        [[ ${2:-} =~ ^[1-3]$ ]] || { echo 'Select CLI shard 1, 2 or 3' >&2; exit 1; }
        python3 tests/ci/cli.py "$2" | tee "test-artifacts/ci/cli-$2.log"
        ;;
    gtk)
        make test-desktop-client | tee test-artifacts/ci/gtk.log
        ;;
    android)
        [[ ${2:-} == verification || ${2:-} == client ]] || { echo 'Select Android verification or client' >&2; exit 1; }
        bash tests/ci/android.sh "$2" | tee "test-artifacts/ci/android-$2.log"
        ;;
    *) echo 'Use fast, cli <1|2|3>, gtk, or android <verification|client>' >&2; exit 1 ;;
esac
