# keyparcel CLI macOS package testing

These unsigned CLI packages target native macOS 26 on Apple Silicon
or Intel. Choose the package matching your processor; the installer declares
that architecture and requires macOS 26 or later. They are not signed or
notarized. Native test runs retain packages as workflow artifacts; a verified
pair can also be attached to an existing, unpublished release draft.

A package installs `keyparcel` into `/usr/local/bin`, its manual into
`/usr/local/share/man/man1`, and Bash and zsh completions into the corresponding
`/usr/local/share` directories. Notices and build provenance are in
`/usr/local/share/doc/keyparcel-cli`. Check `keyparcel --version` and
`man keyparcel` after installation.

For zsh, enable its completion system in your own shell configuration if you
have not already done so: `autoload -Uz compinit && compinit`. The package does
not edit startup files. For Bash, source
`/usr/local/share/bash-completion/completions/keyparcel` from your own startup
configuration. Existing clipboard commands remain available on macOS.

Automatic Linux CI cross-compiles the CLI for both Darwin architectures. This
verifies compilation only: it neither runs those binaries nor publishes release
assets. Native runtime and package checks are on demand, so a build may have no
native verification when macOS runners are unavailable.

Run the **macOS CLI** workflow manually for the desired ref, choosing `arm64`,
`amd64` (Intel), or `both` (the default). Branch and PR-head runs only build/test
and retain artifacts; they never write release assets. Single-architecture tag
runs also remain test-only.

For a release tag containing this workflow, one manual dispatch defaults to both
architectures and `attach_to_draft=true`:

```sh
gh workflow run macos.yml --ref v<version>
```

Both native builds and installed tests must succeed before attachment. The tag
must be annotated and resolve to the exact built source/version. Attachment
looks up only the existing draft with that exact tag, never the latest release;
a missing, published or mismatched release fails without writes. No draft is
created or published. Set `attach_to_draft=false` for a tag test without writes.
The ordinary Linux/Android release workflow does not wait for Mac runners.

The attachment adds two `.pkg` files and distinct `build-info-macos-arm64.json`
and `build-info-macos-amd64.json` assets, then extends `SHA256SUMS`. Linux/Android
asset IDs and bytes stay unchanged. Existing Mac assets must match exactly;
reruns resume matching uploads and never overwrite a package. Re-running the
base Linux/Android draft uploader after Mac attachment refuses the expanded
asset set rather than overwriting it.

GitHub has no atomic multi-asset upload. The workflow serializes with the base
release workflow and validates the draft/tag/inventory before each write, but
manual edits or publication must not overlap attachment. Interrupted uploads
can leave an incomplete **unpublished** draft. Checksum replacement first uploads
and verifies `SHA256SUMS.macos-pending`, then deletes the old checksum and renames
the pending asset. If interrupted between deletion and rename, rerun only the
failed attachment job in that same workflow run to reuse its verified artifacts;
do not publish or manually remove the pending asset. Artifacts expire after seven
days. Rebuilt PKGs can have different bytes at the same commit, so a new build
may correctly refuse existing packages rather than overwrite them. Differing packages, pending checksums or concurrent edits stop
the run for inspection. Only the checksum asset is intentionally replaced.

Native CI uses the command-line installer on a disposable hosted runner. That
checks installed files, metadata, real transfers and clipboard behavior. It does
**not** establish how a quarantined download behaves in Gatekeeper or the
Installer GUI. Download/open/install must still be reviewed manually on a Mac;
no command to disable security checks is supplied here. ARM runtime results may
also be unavailable while hosted ARM runners lack capacity.

The package does not contain identity keys, user configuration, service changes,
or install scripts. Installing or upgrading must preserve your existing keys,
configuration and saved downloads. There is no older published macOS package;
CI's previous-package fixture tests receipt upgrade and file replacement only.

macOS package receipts do not provide an uninstaller. Forgetting a receipt alone
does not remove files. To remove this test installation, remove only these files
with administrator privileges, then forget `io.github.keyparcel.keyparcel.cli`:

- `/usr/local/bin/keyparcel`
- `/usr/local/share/man/man1/keyparcel.1.gz`
- `/usr/local/share/bash-completion/completions/keyparcel`
- `/usr/local/share/zsh/site-functions/_keyparcel`
- The files in `/usr/local/share/doc/keyparcel-cli`

Keep your home directory, keys and configuration. Do not remove shared parent
directories such as `/usr/local/bin` or `/usr/local/share`.
