#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")"
mkdir -p build
CGO_ENABLED=1 "${GO:-go}" build -p 1 -o build/keyparcel-gtk .
CGO_ENABLED=0 "${GO:-go}" build -p 1 -o build/keyparcel keyparcel/cmd/keyparcel
