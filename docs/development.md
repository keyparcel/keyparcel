# Development

Run commands from the repository root unless noted otherwise. Use the Go version
in `go.mod`, Make and Python 3. End-to-end tests need a local Linux Docker daemon;
Android and native GTK builds have their own toolchain requirements.

## Build and check

```sh
make build
make test                         # Go unit tests; no Docker
make test-e2e                     # Real CLI and SSH/SFTP scenarios
make test-e2e SCENARIO=TestReceiveTextDeletesPackage
make check                        # Unit tests, vet and CLI end-to-end tests
bash tests/ci/run.sh fast          # Race/vet and Python helper checks
make test-desktop-client          # GTK lifecycle and accessibility journey
make test-android-client          # Android UI and file-transfer journey
```

Set `DOCKER='sudo docker'` if required by local Docker permissions. CLI/GTK fixtures
use bind mounts, so a remote Docker daemon is not supported. First builds download
modules and image dependencies; subsequent builds reuse caches. CLI builds disable
CGO. `SCENARIO='TestReceiveTextDeletesPackage/embedded'` selects one transport;
`E2E_PARALLEL=1` runs CLI scenarios serially.

Read [CLI scenarios](../tests/e2e/scenarios_test.go) and the
[harness](../tests/e2e/harness_test.go) for exact command and byte assertions.
Related checks share a disposable SSH fixture; transport subtests get separate
fixtures. Clients mount only their own temporary homes, never personal SSH keys,
agent sockets or desktop sockets. The clipboard scenario uses its own Xvfb/X11
clipboard. Local Docker timings do not predict real-server latency.

Failures save transcripts, logs and fixture data under `test-artifacts/`, which
can include disposable private keys. `KEYPARCEL_KEEP_FAILED=1` also retains failed
containers, networks and fixture directories and prints inspection commands.
Remove those owned resources when finished; do not publish their private keys.

- [CI entry points, coverage and diagnostics](../tests/ci/README.md)
- [GTK build, tests and interactive smoke](../desktop/client/DEVELOPMENT.md)
- [Android SDK/emulator setup and core verification](../android/DEVELOPMENT.md)
- [Android client builds, smoke and UI tests](../android/client/DEVELOPMENT.md)

## CLI smoke environment

```sh
make smoke-up DOCKER='sudo docker'
make smoke-alice DOCKER='sudo docker'   # terminal one
make smoke-bob DOCKER='sudo docker'     # terminal two
```

Inside Alice's shell, run `keyparcel devices`, compare Bob's listed fingerprint with
`ssh-keygen -lf ~/.ssh/id_ed25519.pub` in Bob's shell, and approve it:

```sh
keyparcel trust 'SHA256:…' --name bob
keyparcel send --to bob --text 'hello'
```

Inside Bob's shell, verify Alice's fingerprint against the same `ssh-keygen` command
in Alice's shell, approve it as `alice`, then receive:

```sh
keyparcel trust 'SHA256:…' --name alice
keyparcel inbox
keyparcel receive <id> --stdout
```

Normal commands need no extra configuration flags. Setup generates disposable keys,
enrolls public keys, pins the generated server host key, and configures the clients.
Peer approval remains manual. The generated server key is trusted by this local
fixture setup; your real server still needs independent host-key verification.

To try first-connection server approval, use:

```sh
make smoke-fresh
make smoke-alice
```

The IDE task **keyparcel: CLI fresh host-trust smoke** runs the fresh launch.
Alice starts without the server key in her fixture `known_hosts`; Bob already
trusts it. Both authentication keys remain enrolled on the server. In Alice's
shell, run `keyparcel devices`, compare OpenSSH's fingerprint with the fixture
fingerprint printed by the launch command, and type `yes` to approve. Declining
leaves it untrusted. `make smoke-bob` opens the already trusted peer.

An explicit `smoke-fresh` launch clears only Alice's saved server-key trust in
the owned fixture. Identities, peer approvals, inboxes, and saved files remain.
Ordinary `smoke-up` launches preserve manually approved trust and fresh mode;
they do not silently preconfigure Alice again. Reset/recreate with `smoke-down`
then `smoke-up` to return to the original preconfigured environment.

Client homes are available at `.smoke/alice/` and `.smoke/bob/`. Put a sample file in
Alice's directory and run `keyparcel send --to bob --file ./sample.bin`. In Bob's
shell, receive it with `--save-to ./Downloads/`. Bob also has an isolated X11
clipboard: use `receive <id> --copy`, then `xclip -selection clipboard -out` to
inspect it. Your desktop clipboard is untouched.

`smoke-up` reuses a running environment and updates the CLI binaries in place,
preserving host trust, peer approvals, identities, and inbox contents. Rebuilds
do not replace Docker containers or reinstall their packages. Reset/recreate to
update container dependencies. **`smoke-down` deletes the smoke containers,
network, and all `.smoke/` data**, including saved downloads. Copy anything you
want to keep outside that directory first. The directory is ignored by Git and
contains disposable private keys. Omit `DOCKER='sudo docker'` if your account can
run Docker directly.

## Code layout and core API

- `core/`: configuration, identities, package verification and transfers
- `internal/cli/` and `cmd/keyparcel/`: CLI and shell completion
- `desktop/client/`: Linux GTK application and its own Go module
- `android/`: Go mobile bridge, shared identity store, Compose client and core verification APK
- `tests/e2e/`: shared real-server scenarios; `tests/smoke/`: interactive fixtures

The core does not prompt or print. `core.ParseIdentity` accepts unencrypted key
bytes; `core.GenerateIdentity` creates an Ed25519 identity for storage by its
caller. OpenSSH and embedded connectors return the same `Session` API. Embedded
SSH takes explicit connection settings and a trusted server key; it does not use
OpenSSH configuration, agents, jump hosts or external commands. Cancel the context
or close the session to stop work. Reconnection policy belongs to the caller.

Android consumes verified temporary files through a receive callback rather than
the CLI's hard-link publication helper. The callback must finish consuming the
file before returning; failure retains the remote transfer. See the
[package format](../PROTOCOL.md) and [security model](../SECURITY.md).

Candidate builds and clean-install package tests: [release guide](../release/README.md).

## GUI feedback channels

GTK and Android share four small channels. Prefer no message when the resulting
screen already makes an outcome clear.

- The fixed top-bar connection symbol observes the existing SSH/SFTP owner:
  green connected/authenticated, yellow connecting, red failed/lost awaiting a
  retry, neutral unconfigured or intentionally suspended. Its accessible state
  and symbol accompany color. It is not inbox freshness or a reachability probe.
- Adjacent progress is present only during active work, with a short label and
  Cancel where supported. Idle has no status text.
- Action errors stay in the current screen until dismissed, retried or its
  context changes. Background polling never replaces them. Send feedback,
  preview limitations and save results remain in their dedicated controls.
- A single bottom confirmation lasts about three seconds without moving layout
  or focus. A new confirmation replaces the old one; do not stack them. Use it
  for subtle outcomes such as copying or deleting, not refresh completion or a
  filename already visible in the form.
