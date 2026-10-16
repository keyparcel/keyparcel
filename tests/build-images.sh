#!/usr/bin/env bash
set -euo pipefail
repo_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)
cd "$repo_dir"
read -r -a docker_cmd <<< "${KEYPARCEL_DOCKER:-docker}"
read -r -a go_cmd <<< "${KEYPARCEL_GO:-go}"
"${docker_cmd[@]}" info --format '{{.ServerVersion}}' >/dev/null
build_dir=$(mktemp -d -t keyparcel-images-XXXXXXXX)
trap 'rm -rf -- "$build_dir"' EXIT
export CGO_ENABLED=0
"${go_cmd[@]}" build -tags=e2e -o "$build_dir/keyparcel" ./cmd/keyparcel
"${go_cmd[@]}" build -tags=e2e -o "$build_dir/sftp-probe" ./tests/e2e/probe
age_version=$("${go_cmd[@]}" list -m -f '{{.Version}}' filippo.io/age)
GOBIN="$build_dir" "${go_cmd[@]}" install "filippo.io/age/cmd/age@$age_version"
cp completions/keyparcel.bash "$build_dir/"
cp tests/e2e/Dockerfile.client "$build_dir/"
bash tests/build-image.sh sshd -t keyparcel-test-sshd:local tests/e2e
bash tests/build-image.sh client --target client --build-arg "CLIENT_UID=$(id -u)" --build-arg "CLIENT_GID=$(id -g)" -f "$build_dir/Dockerfile.client" -t keyparcel-test-client:local "$build_dir"
bash tests/build-image.sh clipboard --target clipboard --build-arg "CLIENT_UID=$(id -u)" --build-arg "CLIENT_GID=$(id -g)" -f "$build_dir/Dockerfile.client" -t keyparcel-test-clipboard:local "$build_dir"
