#!/usr/bin/env bash
set -euo pipefail
scope=${1:?Select an image cache scope}
shift
[[ $scope =~ ^[a-z0-9-]+$ ]] || { echo 'Invalid image cache scope' >&2; exit 1; }
read -r -a image_docker <<< "${KEYPARCEL_DOCKER:-docker}"
if [[ -z ${KEYPARCEL_DOCKER_CACHE:-} ]]; then
    exec "${image_docker[@]}" build "$@"
fi
mkdir -p "$KEYPARCEL_DOCKER_CACHE"
build_cache="$KEYPARCEL_DOCKER_CACHE/$scope"
build_cache_next=$(mktemp -d "$KEYPARCEL_DOCKER_CACHE/$scope.XXXXXX")
trap 'rm -rf -- "$build_cache_next"' EXIT
cache_args=()
[[ ! -f "$build_cache/index.json" ]] || cache_args=(--cache-from "type=local,src=$build_cache")
"${image_docker[@]}" buildx build --load "${cache_args[@]}" \
    --cache-to "type=local,dest=$build_cache_next,mode=max" "$@"
# Replace the exported cache instead of retaining old, unused build layers.
rm -rf -- "$build_cache"
mv -- "$build_cache_next" "$build_cache"
