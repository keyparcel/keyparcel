# Repository guidance

## Branding

- Write the product name as `keyparcel`, including headings and sentence starts
- Use the shared artwork in `assets/branding`; regenerate exports after changing
  its SVG masters, as described in that directory's README

## Pull requests

- Prefer a few concise bullets describing the core changes and motivation
- Omit routine validation details unless unusual risks, limitations or special
  testing matter to the reviewer

## Commits

- Commit only when explicitly requested.
- Use Conventional Commits: `<type>[optional scope]: <imperative summary>`.
- Start with `feat`, `fix`, `test`, `docs`, or `chore`. Use a scope when it clarifies the affected area.
- Keep the title short. Add body bullets when a change covers several meaningful behaviors.
- Aim for a title around 50 characters and body lines no longer than 72 characters. Prefer clarity over a strict count. Keep each bullet to one concise point; wrap longer bullets onto indented continuation lines.
- Omit trailing periods in commit titles and body bullets.
- Describe the resulting behavior. Omit internal spec references and validation details from commit messages.
- Keep supporting code, tests, and documentation together in one coherent commit.

Example:

```text
feat: improve save paths and plain-text clipboard copying

- Support exact output filenames for file and text transfers
- Reject invalid local inputs before contacting SSH
- Allow copying UTF-8 file contents with a 16 MiB clipboard cap
- Preserve symlink path semantics and reject named-pipe inputs
```

## Tests

- Use unit tests for local validation, path decisions, and filesystem edge cases.
- Group related transfer behaviors into readable end-to-end scenarios that share one fixture.
- Create separate environments when state changes, such as permissions or connectivity failures, would interfere with other checks.
- Give checks specific failure messages so a shared scenario still makes failures easy to diagnose.

## GUI feedback

- Follow the shared [feedback channels](docs/development.md#gui-feedback-channels)
  when adding progress, errors or confirmations
