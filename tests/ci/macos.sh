#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/../.."

[[ $(uname -s) == Darwin && $(uname -m) == "$EXPECTED_MACHINE" ]]
[[ $(go env GOARCH) == "$EXPECTED_GOARCH" ]]
[[ ${GITHUB_ACTIONS:-} == true && $(id -u) != 0 ]]
sw_vers
ssh -V
go version
command -v zsh

# StrictModes needs safe ancestors; Darwin's default temp path is too long for mux sockets.
fixture_root=$(mktemp -d "$HOME/keyparcel-macos.XXXXXX")
socket_root=$(mktemp -d /tmp/kp-mac.XXXXXX)
chmod 700 "$fixture_root" "$socket_root"
kill_tree() {
  local pid=$1 child
  for child in $(pgrep -P "$pid" || true); do kill_tree "$child"; done
  kill -KILL "$pid" 2>/dev/null || true
}
cleanup() {
  local file pid cmd
  # Backstop for test-process timeout; normal T.Cleanup already reaps each world.
  while IFS= read -r file; do
    pid=$(cat "$file")
    [[ $pid =~ ^[0-9]+$ && $pid -gt 1 ]] || continue
    cmd=$(ps -p "$pid" -o command= || true)
    # Check ownership again before using a stale pidfile.
    if [[ $cmd == *"$fixture_root/"* ]]; then kill_tree "$pid"; fi
  done < <(find "$fixture_root" -type f -name '*.pid')
  chmod -R u+rwX "$fixture_root"
  rm -rf "$fixture_root" "$socket_root"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM
export TMPDIR="$socket_root"
go build -o "$RUNNER_TEMP/keyparcel" ./cmd/keyparcel
# These execute GNU/BusyBox *server* scripts, not portable macOS client code.
# Linux CI still runs both unchanged; no GNU tools are injected into the native fixture.
go test -race -count=1 ./... \
  -skip '^(TestCommandParcelScriptRejectsUnsafeFilesBeforeReading|TestInboxBatchToolsAndLocalVerification)$'
go vet ./...
export KEYPARCEL_E2E_FIXTURE=local KEYPARCEL_E2E_LOCAL_ROOT="$fixture_root"
go test -race -tags=e2e -count=1 -timeout=12m -v ./tests/e2e \
  -run '^(TestReceiveTextDeletesPackage|TestFileBytesAndKeep|TestUnapprovedSenderAndExplicitDelete|TestTamperingBeyondMetadataAndTruncation|TestIdentityChangedAndRemoteCommentDoesNotApproveReplacement|TestCopyUsesNativeMacClipboard|TestLocalWatchCancellation|TestLocalSelectedIdentity)$'
