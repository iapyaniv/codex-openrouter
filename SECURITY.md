# Security

## Report a vulnerability

Use GitHub's private vulnerability reporting (**Security → Report a vulnerability**). Do not include real API keys in reports.

## How the launcher protects your key

Codex 0.155.1 passes its environment to the commands it runs, and it saves shell snapshots that can contain environment variables. Without changes, model-run commands could read `OPENROUTER_API_KEY`, and their output could go back to the model.

On each launch, `codex-openrouter` adds these Codex settings:

| Setting | Effect |
| --- | --- |
| `shell_environment_policy.set.OPENROUTER_API_KEY=""` | Commands see an empty key, even if your own config sets one. |
| `shell_environment_policy.ignore_default_excludes=false` | Codex removes other variables whose names contain `KEY`, `SECRET` or `TOKEN`. |
| `features.shell_snapshot=false` | Codex does not write new shell snapshots. |

Codex gets the real key from a helper command (`codex-openrouter --internal-auth`) in the installed release. The key is never written to a config file or put in command arguments. Your other `shell_environment_policy` settings are kept.

## Limits

- **The filter uses names only.** Variables such as `DATABASE_URL` or `PASSWORD` are not removed.
- **MCP servers, hooks and `notify` commands get the full Codex environment.** A trusted project's `.codex/config.toml` can pass `OPENROUTER_API_KEY` to an MCP server. Review that file in projects you trust, also after you change branches.
- **This is not a sandbox.** Shell profiles that commands load, and any process that runs as your user, can still read the key.
- **Old snapshots stay.** Snapshots from earlier runs are in `$CODEX_HOME/shell_snapshots`. If a key may have leaked, rotate it and delete those files.
- **OpenRouter and the model provider see your prompts.** Their logging and retention policies apply. Set your data policy in [OpenRouter privacy settings](https://openrouter.ai/settings/privacy). Use a separate key with a spending limit.

## Installation integrity

The installer downloads the Codex bundle over HTTPS from the pinned Codex GitHub release. It checks the size and SHA-256 against the manifest built into the launcher ([codex-artifacts.json](internal/distribution/codex-artifacts.json)). It refuses archives that contain links, path traversal, special files or unexpected files. It installs each release in a new directory and switches to it only after all checks pass. Codex's own update checks are turned off.

The launcher has only Go standard-library dependencies. The `codex-openrouter` binary is not signed or notarized. `SHA256SUMS` in a release finds damaged downloads, but it does not prove who built the file. If that matters to you, [build from source](README.md#build-from-source).

## To do: native dependency review

Codex 0.155.1 pins gix 0.81.0. That version matches four submodule advisories fixed in gix 0.83.0: [GHSA-f26g-jm89-4g65](https://github.com/advisories/GHSA-f26g-jm89-4g65), [GHSA-fr8x-3vfx-f45h](https://github.com/advisories/GHSA-fr8x-3vfx-f45h), [GHSA-p3hw-mv63-rf9w](https://github.com/advisories/GHSA-p3hw-mv63-rf9w) and [GHSA-pg4w-g64p-qwhj](https://github.com/advisories/GHSA-pg4w-g64p-qwhj). A source review found that Codex does not call the affected submodule APIs. The same lockfile also matches advisories for gix-pack 0.68.0, gix-packetline 0.21.2 and gix-fs 0.19.2, which are not reviewed yet.

These issues are in upstream Codex. They apply to every Codex 0.155.1 install, not only to this wrapper. To do: finish the review, or move the pin to a Codex release with patched gix crates.
