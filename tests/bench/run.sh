#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/../.."
[[ ${1:-} == small || ${1:-} == bulk ]] || { echo 'Select small or bulk'; exit 1; }
mkdir -p test-artifacts/bench
export CGO_ENABLED=0
export KEYPARCEL_BENCH_SUITE=$1
export KEYPARCEL_BENCH_REVISION=$(git rev-parse HEAD)
if [[ -n $(git status --porcelain) ]]; then KEYPARCEL_BENCH_REVISION+=-dirty; fi
export KEYPARCEL_BENCH_BINARY="$PWD/test-artifacts/bench/worker"
go build -tags=e2e -o "$KEYPARCEL_BENCH_BINARY" ./tests/bench
go test -tags=e2e,performance -count=1 -timeout=5m -parallel=2 -v -run '^TestTransportPerformance$' ./tests/e2e | tee "test-artifacts/bench/$1.log"
