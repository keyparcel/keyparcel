# GTK client development

[User guide](README.md) · [Repository development guide](../../docs/development.md)

Native builds require Linux, the Go version in `go.mod`, a C compiler, pkg-config,
GTK 4 development headers/runtime, an SVG image loader (`librsvg2-common` on
Debian/Ubuntu), and OpenSSH. From the repository root:

```sh
make desktop-build
desktop/client/build/keyparcel-gtk
```

The client uses application-specific GTK/CGo bindings and the shared Go core.
Network and filesystem work runs on workers. Cancellation closes owned SSH process
groups; non-interruptible filesystem calls are bounded by two worker slots.
Settings are reloaded before operations, with the CLI's last-writer behavior for
concurrent edits. No generated Go UI bindings are needed.

## Automated tests

The host requires a local Linux Docker daemon, Python 3 and Make, running as a
normal user. GTK, Go, C compilation and all desktop automation tools live in the shared
[`Dockerfile`](Dockerfile) image. No host GTK/Python GI/Xvfb installation is needed
for tests or smoke. The native build command above remains available separately.

```sh
make test-desktop-client
# Build the container binaries without running the journey:
python3 desktop/client/run.py build
```

The host runner builds/reuses `keyparcel-test-gtk:local` and the existing SSH server
image, then creates an internal Docker network with separate client/server
containers. No SSH ports are published. The client runs as the host user's UID/GID,
with a matching passwd entry for OpenSSH. Source is mounted read-only; only the
ignored build/fixture directory is writable. Go caches persist in
`build/container-cache/`; rebuilding images reuses Docker's cached layers.

The client container owns a private X11 display, clipboard, D-Bus/accessibility
buses, window manager and XDG directories. Focused tests and the existing AT-SPI
journey run there. Only owned-server fault injection (exec/pause/unpause) crosses
the runner's stdin/stdout; Docker and its socket are absent from the client.
An independent CLI executable in the container checks filenames and exact bytes.
Related transfers continue to share one SSH fixture.

The [AT-SPI journey](journey.py) checks real dialogs, server/device trust, timed
refresh and reconnects, drafts, text/files, clipboard boundaries, failure retention,
cancellation and shutdown. [Lifecycle checks](smoke_test.py) exercise restart,
reset and ownership guards. An independent CLI checks filenames and exact bytes.
Tests use semantic controls and real X11 input rather than screenshot baselines.

Logs, screenshots and semantic-tree failure dumps remain in `build/session.*`.
The [product gallery](docs/gallery/README.md) shows current screens captured
from this journey.
Image/compiler logs are `build/images.log`, `build/build.log` and
`build/test-build.log`. The host removes owned test containers/networks on exit,
including interrupted runs. Automated tests never mount the operator's display or
home and do not touch the desktop clipboard, real CLI settings, Android or other
smoke environments. This workflow is intended for Linux GitHub Actions runners;
hosted GitHub Actions also runs this suite.

## Interactive smoke

```sh
make smoke-gtk          # Build/open the persistent GUI/server fixture
make smoke-gtk-peer     # Open the configured independent CLI peer shell
make smoke-gtk-fresh    # Close GUI first; clear desktop setup/server trust
make smoke-gtk-down     # Close GUI first; remove owned fixture/settings
```

An IDE task opens the same window. Smoke uses the same image as tests, with the
local X11 socket mounted read-only (XWayland may work but is unverified), the normal
user UID and a copied Xauthority cookie when present. It never relaxes X server
access with `xhost +`. The host needs a local `DISPLAY` and existing permission to
use that display. A fresh private D-Bus session runs inside the client; personal
SSH/configuration directories and the host session bus are not mounted.
Interactive smoke intentionally shares your desktop display and clipboard.

Fixture state lives in `build/smoke-container/`. The printed `files/` directory is
shared at the same absolute path on host and container: put send files there or
save received files there to retrieve them on the host. The picker sees the
container filesystem, not your full home. The peer shell runs inside the same
client container and starts in this folder; it can stay open alongside the GUI.

Rebuilding/reopening preserves fixture containers, keys, settings, trust, approvals,
files and transfers. The client/server can stop/restart without changing identity
or endpoint (`inbox:22` on the private network). Dependency-image changes take
full effect after reset/recreation; rebuilding source updates the shared binaries.
Fresh mode retains enrolled identities/server/transfers: enter the printed identity
path and target `inbox`, Save settings, then Connect to exercise graphical approval.
The peer stays configured. Close the window **and peer shell** before fresh/reset;
reset verifies ownership and refuses active sessions. `smoke-gtk-down` deletes
these containers, their network and all owned smoke files; copy saved files out
first. Test artifacts and Go caches remain.

## GTK compatibility

The supported, deprecated in-process `GtkFileChooserDialog` avoids portal
cancellation issues. Product tests cancel immediately and close
with a picker pending; the crashing case is not skipped. Dialog/saved-host combo
APIs need migration before GTK 5. File selection currently supports local paths.

The container image uses Ubuntu 24.04, GTK 4.14.5 and Go 1.27.2, with X11/software
rendering. Candidate installs are validated on Ubuntu 24.04, Debian 13 and Fedora 44;
see the [release guide](../../release/README.md). Native Wayland and other
distributions remain unverified. Background transfers and notifications are deferred.

The preconfigured smoke Inbox is seeded once with six representative parcels:
small text, a UTF-8 text file, small binary, bounded-preview text, oversized text
and oversized binary, newest first. Approve the smoke peer to view them; reopening
retains the fixture and does not resend the samples. Fresh setup retains its
manual server/device approval steps.
