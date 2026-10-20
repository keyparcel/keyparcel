# GitHub Actions checks

[CI](../../.github/workflows/ci.yml) runs on pull requests, pushes to `main` and
manual dispatch. Eight independent standard `ubuntu-24.04` jobs start together:

| Job | Checks |
| --- | --- |
| Fast | Root Go race tests/vet, Android bridge race tests/vet, Python smoke/emulator/runner/CI helper regressions |
| CLI × 3 | Automatically discovered scenarios, both supported SSH transports, two independent scenarios at a time per shard |
| GTK | Containerized client unit/lifecycle checks, real AT-SPI transfer journey and smoke lifecycle |
| Android verification | Go mobile AAR, headless verification APK and real Android/Linux exchange |
| Android client × 2 | Independent network/viewer and removal/file-transfer journeys; each builds Compose APK/JVM tests and enrolls its own device |

The separate [candidate workflow](../../.github/workflows/candidates.yml) builds
Linux packages and an unsigned ARM64 Android APK, with installed-package and
static APK checks. Neither workflow publishes releases or deploys anything.
The eight broad jobs do not depend on fast checks succeeding. Matrix failures leave sibling jobs running. New commits cancel
superseded runs for the same branch/PR; job limits are 15 minutes for fast checks,
25 minutes for CLI/GTK and 45 minutes for Android. Actions are pinned to upstream
release commits; Go comes from `go.mod` (also pinned in the GTK image), JDK is 21,
and SDK/NDK components match the Android build scripts.

## Local entry points

From the repository root:

```sh
bash tests/ci/run.sh fast
bash tests/ci/run.sh cli 1   # Also run shards 2 and 3; each has its own fixtures
bash tests/ci/run.sh gtk
# Hardware-accelerated host, configured JDK/SDK and free emulator ports required:
bash tests/ci/setup-android.sh
KEYPARCEL_ANDROID_ACCEL=on bash tests/ci/run.sh android verification
KEYPARCEL_ANDROID_ACCEL=on bash tests/ci/run.sh android client
```

Fast checks need Go, a C compiler for the race detector, OpenSSH and Python 3.
CLI/GTK additionally need a local Linux Docker daemon; GTK dependencies are inside
its image. The CLI driver compiles its test binary once, lists tests without Docker
startup, and partitions sorted top-level scenario names into three disjoint shards.
A missing/empty shard is an error. New scenarios are assigned automatically;
transport subtests and shared-state assertions stay together. Ordinary
`make test-e2e` also uses bounded parallel scenarios; `E2E_PARALLEL=1` selects serial
execution, and `SCENARIO=...` still filters normally.

Android CI requires writable `/dev/kvm`, JDK 21, and the components in
[Android build prerequisites](../../android/DEVELOPMENT.md#build-prerequisites). The workflow grants the runner user
access to its ephemeral VM's KVM device. CPU acceleration is explicitly `on`;
software GPU rendering and the tested 480×800 layout remain. Verification uses
5554 and the UI client uses the dedicated headless 5556 emulator, on separate VMs.
Local CI commands refuse an occupied port. CI AVD files are confined to
`android/build/ci-avds/`; development phone/test AVD data is not reused. The existing
AVD-name, path, serial and headless checks still apply. An exit trap captures logcat
and stops only the expected emulator. The Android client starts emulator setup in
parallel with its build, then waits for both before running tests. Build/readiness
timings are printed in the job log. The development launcher's acceleration
stays `off` unless `KEYPARCEL_ANDROID_ACCEL` is explicitly set (`on`, `off`, `auto`).
Use normal Android targets on a development host without KVM.

## Android client shards

CI runs `network` and `files` on separate VMs, each with its own emulator,
SSH fixture, real enrollment, host/device trust checks and text exchange.
The `network` shard keeps timeout/recovery, viewer/clipboard boundaries and
multi-recipient retries together. The `files` shard keeps failed removal,
restart, inbox deletion and file transfers together, including their shared
provider-saved file and independent CLI byte/count checks. Both use the software
keyboard and real touch assertions.

Both jobs retain build/emulator-boot overlap and build their own APKs; no app or
fixture state crosses shards. The required `Android client` aggregate passes
only after both shards and the separate verification suite succeed. Failure
artifacts and screenshots are named by shard. This uses an extra client runner
and duplicates setup/build work in exchange for a shorter critical path.

An unset `KEYPARCEL_ANDROID_CLIENT_SHARD` keeps the complete sequential journey
for `make test-android-client`. Set it to `network` or `files` to select one
independent journey; other values fail. Run journeys sequentially on a local
test emulator. Hosted CI measures the shard wall time and runner cost.

## Caches and diagnostics

Go and Gradle actions cache dependencies/build outputs. SDK caching covers the
selected platform, build tools, NDK and system image. Docker images use a shared
build helper: ordinary local builds retain their existing Docker cache behavior;
CI sets `KEYPARCEL_DOCKER_CACHE` and uses Buildx to import/export per-image local
layer caches restored through Actions cache. Export replaces old cache contents,
so obsolete layers do not accumulate indefinitely. GTK compiler/module caches are
separate. No fixture homes, keys, `known_hosts`, AVD userdata or app state are cached.

Failed/cancelled jobs upload distinctly named diagnostics for seven days: command
logs, CLI failure captures, GTK screenshots/semantic trees, Android logcat,
verification reports, UI screenshots and JVM reports. Binaries, private fixture
identity files and large generated build/cache directories are excluded. Normal
console output is available on successful runs.

Local workflow validation uses `actionlint .github/workflows/ci.yml`, plus the fast
helper regressions. Accelerated Android execution requires writable `/dev/kvm`; use the documented
software-emulation development targets on hosts without it.

Two additional independent, non-required [transport benchmark jobs](../bench/README.md)
measure production embedded/OpenSSH latency and bulk transfers on isolated shaped
links. They do not feed the Android aggregate or other required checks. Correctness
and setup failures remain visible; timing changes alone do not fail a job.
