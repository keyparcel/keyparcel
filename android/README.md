# Android

The [Android client](client/README.md) is the Compose application for sending and
receiving text and files through an SSH/SFTP server. It uses the shared Go core
and an Android Keystore-encrypted device identity.

- [Client usage](client/README.md)
- [SDK/emulator setup and core verification](DEVELOPMENT.md)
- [Client builds, interactive smoke and UI tests](client/DEVELOPMENT.md)

`bridge/` exposes the Go operations to Android; `verification/` is the separate
headless test APK, not a user-facing app. Automated runtime coverage uses x86_64
emulators. [Candidate builds](../release/README.md) produce unsigned ARM64 APKs
with static checks. The [tag workflow](../release/README.md#tagged-release-drafts)
signs production APKs and creates unpublished drafts. Automated runtime coverage
remains x86_64; see the [current platform status](../README.md#clients).
