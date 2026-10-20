# Contributing to keyparcel

Use the [development guide](docs/development.md) for builds, tests and isolated
smoke environments. Start with a focused change; include the relevant tests and
update user documentation when behavior changes.

- Use Conventional Commits, such as `fix: preserve a cancelled transfer`
- Reuse existing fixtures and journey assertions; test local validation with unit tests
- Include the client version from Settings or `keyparcel --version` in bug reports
- Keep real keys, personal settings, logs and diagnostic artifacts out of commits
- Preserve third-party headers and update [notices](THIRD_PARTY_NOTICES.txt) when dependencies change

Project contributions use [Apache-2.0](LICENSE). See [NOTICE](NOTICE) for copyright
attribution and [SECURITY.md](SECURITY.md) for the security model and identity-loss
limits. Additional repository conventions are in [AGENTS.md](AGENTS.md).
