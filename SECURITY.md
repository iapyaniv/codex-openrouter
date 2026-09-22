# Security review and decisions

**Last checked: 2026-09-22T19:50:06Z (UTC).** Scope: codex-openrouter 0.1.0, the pinned `@openai/codex` 0.155.1 package and tagged source, and the linked public OpenRouter documentation. Findings below were checked on this date unless an earlier publication date is given. These are version-specific observations, not a certification of Node.js, npm, Codex, or hosted providers.

## Confirmed exposures and mitigations

### Codex tool environments and shell snapshots

**Status: mitigated by launcher defaults; runtime isolation has limits.** Codex 0.155.1 defaults to inheriting the environment with its default secret-name filtering disabled. Its non-inheritable list does not include `OPENROUTER_API_KEY`. Passing the key to Codex therefore also exposed it to ordinary model-executed commands. Command output can subsequently become model input. Evidence: [environment defaults](https://github.com/openai/codex/blob/rust-v0.155.1/codex-rs/config/src/shell_environment_policy.rs) and [tool environment construction](https://github.com/openai/codex/blob/rust-v0.155.1/codex-rs/protocol/src/shell_environment.rs).

The same version enables legacy shell snapshots. Without an active credential broker, its login-shell capture can save inherited environment declarations under `$CODEX_HOME/shell_snapshots`. Filtering the later tool environment alone does not fix that earlier capture. Evidence: [feature defaults](https://github.com/openai/codex/blob/rust-v0.155.1/codex-rs/features/src/lib.rs) and [snapshot implementation](https://github.com/openai/codex/blob/rust-v0.155.1/codex-rs/core/src/shell_snapshot.rs).

The launcher supplies `shell_environment_policy.ignore_default_excludes=false`, `shell_environment_policy.set.OPENROUTER_API_KEY=""`, and `features.shell_snapshot=false` on each run. This enables Codex's case-insensitive `*KEY*`, `*SECRET*`, and `*TOKEN*` name filters for other ambient credentials, preserves existing exclusion/inclusion rules, and keeps the OpenRouter key empty even if a user-level `set` would restore it. Authentication still receives the original key.

These are name heuristics, not general secret detection: names such as `DATABASE_URL` or `PASSWORD` are not covered, while some nonsecret names also match. Trusted `set` entries, shell profiles, and explicit caller policy/feature overrides can reintroduce credentials. The launcher preserves other user-defined additions; review them before sharing a session with untrusted code.

The real Codex smoke test verifies authenticated requests, absent/empty synthetic credentials in the shell tool, preserved user environment policy, and no synthetic credentials in active snapshots. It covers legacy exclusion/inclusion arrays and canonical filters. Removing the protections in a disposable copy caused the probe to detect credential exposure. This verifies the observed leak and mitigation, not every possible way to access a credential.

Disabling future snapshots does not delete old snapshots, transcripts, or backups. If an earlier run exposed a key there or in tool output, revoke/rotate it and remove affected local copies.

### Dependency integrity and managed updates

**Status: corrected with a locked staging install.** The original installer downloaded `@openai/codex@latest` with lifecycle scripts enabled. Pinning the version and shipping a shrinkwrap was also insufficient: npm 11.5.1 accepted a local-package global install after the embedded dependency hash was deliberately corrupted. An invalid dependency URL was ignored too. npm's local-tarball manifest path did not mark the embedded shrinkwrap for loading. A packaged lock's presence and a successful `--version` did not prove enforcement. npm 12 also explicitly stops honoring published shrinkwraps; see [npm 12.0.2's documentation](https://github.com/npm/cli/blob/v12.0.2/docs/lib/content/configuring-npm/package-lock-json.md#npm-shrinkwrapjson).

The installer now uses [npm ci](https://docs.npmjs.com/cli/v11/commands/npm-ci) in private staging with the root `package-lock.json`, exact dependency URLs, and SHA-512 integrity values. It verifies the staged Codex binary (npm can tolerate a failed optional native dependency), bundles that verified dependency tree, and installs the local archive **offline with a new empty cache**. Missing bundle contents therefore fail instead of resolving another unverified dependency. Lifecycle scripts are disabled throughout. npm still generates the platform launchers, including Windows shims.

The installed `dependency-lock.json` is an audit copy; enforcement happens during staging. The smoke test deliberately corrupts a dependency hash and checks that installation fails while the existing command and saved defaults survive. Hashes verify the selected upstream artifacts; they do not establish that upstream code is safe. The root lock strategy supports npm 10+ without relying on published-shrinkwrap behavior.

Codex 0.155.1's npm updater runs a global install without this wrapper's dedicated prefix. It can fail or target an unrelated Codex installation and bypass the wrapper's dependency selection. We disable startup update checks and intercept command-position `codex-openrouter update`, directing users to rerun `node install.mjs` from an updated checkout. Evidence: [updater command](https://github.com/openai/codex/blob/rust-v0.155.1/codex-rs/tui/src/update_action.rs) and [startup update gate](https://github.com/openai/codex/blob/rust-v0.155.1/codex-rs/tui/src/updates.rs). Maintainers must keep the pin current; disabling automatic updates is not a reason to defer security updates.

### Installer and configuration boundaries

**Status: hardened.** These checks address local write and injection risks:

| Boundary | Decision |
| --- | --- |
| Shared command directory | Use a dedicated `~/.codex-openrouter` prefix, separate from regular Codex. |
| Model/config input | Parse JSON as data; accept only model/reasoning fields, validate their values, and encode TOML strings. Never source or evaluate config. |
| Child processes | Invoke npm, Codex, and the fixed credential helper through Node with argument arrays and `shell: false`. Remove OpenRouter/OpenAI keys from npm's environment. |
| Redirected writes | Reject symlinks/junctions for the managed root and links/non-regular config files. Check Unix ownership/writable permissions; write config atomically through an exclusively created temporary file. |
| Failed installs | Clean staging files in `finally`; verify that the installed native binary runs before saving defaults. |

The original script contained no embedded key, deliberate exfiltration, or approval/sandbox bypass. Its quoted shell helper did not evaluate the API key as code; the Node replacement removes a shell dependency and improves portability.

## Upstream advisories and remaining boundaries

### Codex sandboxing

**Historical advisory; pinned version is outside the affected range.** [CVE-2025-59532 / GHSA-w5fx-fh39-j5rw](https://github.com/openai/codex/security/advisories/GHSA-w5fx-fh39-j5rw), published 2025-09-19, describes a sandbox writable-root bug affecting CLI 0.2.0 through 0.38.0, patched in 0.39.0. The selected 0.155.1 version is not in that range. This is not evidence that the newer sandbox has no other bugs.

**Host-process inspection is a conditional risk, not a confirmed normal-sandbox bypass.** In the pinned source, restricted Linux commands normally use bubblewrap with a separate PID namespace and fresh `/proc`; the tool environment is cleared and rebuilt. Thus simply reading a parent environment does not establish access to the host Codex process in that mode. Full-access execution, explicit legacy Landlock, and the fallback when fresh `/proc` cannot be mounted have different boundaries. Do not rely on environment masking as protection from arbitrary same-account process inspection. This assessment is source-based; Linux execution was not reproduced locally. Evidence: [bubblewrap setup](https://github.com/openai/codex/blob/rust-v0.155.1/codex-rs/linux-sandbox/src/bwrap.rs#L290), [sandbox selection and proc fallback](https://github.com/openai/codex/blob/rust-v0.155.1/codex-rs/linux-sandbox/src/linux_run_main.rs#L259), and [child environment replacement](https://github.com/openai/codex/blob/rust-v0.155.1/codex-rs/core/src/spawn.rs#L76).

The launcher preserves existing sandbox/approval settings and forwards explicit Codex options. Model output and repository content can be hostile. A readable secret can still be copied into tool output and sent as model context even when a tool has no network access. Trusted MCP servers, plugins, hooks, shell profiles, and provider overrides need their own review. This wrapper is not a sandbox.

### Trusted MCP servers, hooks, and notifications

**Status: remaining trust boundary with environment-based authentication.** A previously trusted project's `.codex/config.toml` can configure a stdio MCP server with `env_vars = ["OPENROUTER_API_KEY"]`. Codex builds that server's environment independently of the shell policy and copies the named variable from its own process. A synthetic-key probe against 0.155.1 reproduced this on server startup, before a successful model request. Evidence: [MCP environment construction](https://github.com/openai/codex/blob/rust-v0.155.1/codex-rs/rmcp-client/src/utils.rs), [server launch](https://github.com/openai/codex/blob/rust-v0.155.1/codex-rs/rmcp-client/src/stdio_server_launcher.rs), and [project trust/config loading](https://github.com/openai/codex/blob/rust-v0.155.1/codex-rs/config/src/loader/mod.rs).

Trust applies to the project path, so later branch/checkout changes can alter its MCP configuration. Review those changes before starting a session. This is not a demonstrated untrusted-project bypass: project-local configuration is gated on trust.

Trusted/enabled command hooks and configured legacy `notify` also receive the Codex process environment independently of shell filtering. This was verified from [hook environment capture](https://github.com/openai/codex/blob/rust-v0.155.1/codex-rs/hooks/src/registry.rs), [command execution](https://github.com/openai/codex/blob/rust-v0.155.1/codex-rs/hooks/src/engine/command_runner.rs), and [notification handling](https://github.com/openai/codex/blob/rust-v0.155.1/codex-rs/hooks/src/legacy_notify.rs); no hook-trust bypass was established. Shell masking and disabling snapshots do not protect these separate paths. A future saved-key implementation should retrieve credentials directly in the auth helper, avoiding ambient inheritance into these processes; credential-store access would still need its own trust boundary.

### OpenRouter and upstream model providers

**Documented service boundaries, not newly discovered vulnerabilities.** The hosted services were not penetration-tested or internally audited.

- Requests pass through OpenRouter and the selected model provider. OpenRouter documents opt-in prompt/completion logging and product-improvement use, request metadata retention, and sampled anonymous prompt categorization. A no-content-logging setting is not a promise of no processing or metadata. See [data collection](https://openrouter.ai/docs/guides/privacy/data-collection).
- Upstream providers have their own training and retention policies. Opting out of training is distinct from requiring zero data retention (ZDR). OpenRouter documents account/key guardrails and per-request ZDR routing; these restrictions can reduce model availability. ZDR does not cover enabled plugins/tools, and OpenRouter permits implicit in-memory prompt caching under its ZDR definition. See [provider policies](https://openrouter.ai/docs/guides/privacy/provider-logging), [ZDR scope](https://openrouter.ai/docs/guides/features/zdr), and [guardrails](https://openrouter.ai/docs/guides/features/guardrails/overview).
- This wrapper does not enforce ZDR, no-training, data residency, or a particular upstream provider, and does not configure or verify account privacy settings. Choose appropriate restrictions in [OpenRouter settings](https://openrouter.ai/settings/privacy), including the applicable model group for DeepSeek. Use a dedicated inference key with a spending limit and, where available, model/provider allowlists. Server-enforced limits reduce the impact of a stolen key; local filtering cannot enforce them. See [key and budget controls](https://openrouter.ai/docs/guides/features/guardrails/overview).

Public policy pages can change independently of this repository. Recheck them when changing models, providers, account settings, or the dependency pin. The model default sets reasoning to `high` explicitly; it does not assume a provider default.

## Credential storage and local trust

- Currently, `OPENROUTER_API_KEY` remains in the wrapper and Codex process environments for authentication. The helper returns it on stdout for Codex to capture; the wrapper does not insert it into arguments, config JSON, or generated launchers. Persistent OS credential storage is not implemented yet.
- Command-backed auth is retained because [OpenRouter uses it for model-catalog discovery](https://openrouter.ai/docs/cookbook/coding-agents/codex-cli). Codex's [built-in credential-store setting](https://learn.chatgpt.com/docs/auth#credential-storage) is not automatically a store for this custom helper's environment key.
- An OS credential store would improve protection at rest and avoid repeated exports. It would not by itself isolate the key from commands running as the same user: [Windows credential reads use the caller's logon session](https://learn.microsoft.com/en-us/windows/win32/api/wincred/nf-wincred-credreadw), [Linux Secret Service operates within the user session](https://specifications.freedesktop.org/secret-service/latest-single/), and [macOS access depends on Keychain permissions](https://support.apple.com/guide/mac-help/allow-apps-to-access-your-keychain-kychn002/mac). Do not describe an unlocked store or a helper that returns a key as a sandbox boundary.
- Unix installation directories use `0700` and config writes use `0600`. Windows uses inherited user-profile ACLs; POSIX modes do not enforce Windows access control. Use a private local directory, especially with `CODEX_OPENROUTER_HOME`.
- Node/npm, PATH entries, the installation parent and its ancestors, installed packages, and existing Codex configuration are trusted. Checks do not defend against an administrator, compromised account, malicious runtime, or concurrent changes by another process with the same rights. Do not install into shared/attacker-controlled locations or use `sudo`.

## Verification and maintenance

Offline tests cover persisted defaults, malformed/secret-bearing config, injection attempts, linked files, Unix permission boundaries, literal argument forwarding, managed updates, auth output, exit codes, and signals. The installer smoke test uses the real pinned CLI with a loopback mock provider and a generated environment probe; no real key or paid inference is used. Its controlled probe runs without the OS sandbox, so it is not a sandbox-escape test.

Local checks passed on macOS ARM64 with Node 24/npm 11. The CI matrix targets macOS, Linux, and Windows with Node 22/npm 10, Node 24/npm 11, and Node 24/npm 12; hosted runs have not yet been verified. Linux runtime validation is planned; Windows support is intended, with runtime testing and fixes welcome from contributors. These platform targets are not claims of completed validation. `npm audit --omit=dev` reported no known advisories for the locked packages on 2026-09-22. That result does not audit the bundled native Rust code. Support is limited to Codex's supplied OS/CPU binaries, excluding BSD, 32-bit systems, and mobile OSes.

When updating Codex or authentication, rerun the installed-artifact and synthetic-credential checks, check upstream advisories, and revisit environment construction, snapshots, updater behavior, and sandbox assumptions. Update the UTC timestamp, affected versions, evidence, mitigation status, and remaining limits here; keep historical advisory dates distinct from review dates.

[Dependabot configuration](.github/dependabot.yml) requests weekly npm and GitHub Actions update PRs once published on GitHub with Dependabot enabled. Updates require review; installed copies do not contact an update service or receive automatic notifications. Users must follow releases/advisories, update their checkout, and rerun the installer. On other hosts maintainers need an equivalent review cadence. See [GitHub's version-update configuration](https://docs.github.com/en/code-security/how-tos/secure-your-supply-chain/secure-your-dependencies/configure-version-updates).

Report suspected vulnerabilities privately through the hosting platform's security advisory feature if enabled. Do not include real API keys in reports.
