# Security review and decisions

**Go migration checks: 2026-09-30.** Scope: local codex-openrouter 0.2.0 candidates, Go 1.27.1 and the separately downloaded full Codex 0.155.1 native bundle. Installer, packaging, process, credential/environment and terminal checks are described below. No real API keys or paid inference were used.

**Historical source/provider review: 2026-09-22T19:50:06Z (UTC).** The Codex source, MCP/hooks, Linux sandbox and hosted-provider policy observations retain that date; the migration did not re-audit all of them. These are version-specific observations, not a certification of Codex, the Go toolchain or hosted providers.

## Confirmed exposures and mitigations

### Codex tool environments and shell snapshots

**Status: mitigated by launcher defaults; runtime isolation has limits.** Codex 0.155.1 defaults to inheriting the environment with its default secret-name filtering disabled. Its non-inheritable list does not include `OPENROUTER_API_KEY`. Passing the key to Codex therefore also exposed it to ordinary model-executed commands. Command output can subsequently become model input. Evidence: [environment defaults](https://github.com/openai/codex/blob/rust-v0.155.1/codex-rs/config/src/shell_environment_policy.rs) and [tool environment construction](https://github.com/openai/codex/blob/rust-v0.155.1/codex-rs/protocol/src/shell_environment.rs).

The same version enables legacy shell snapshots. Without an active credential broker, its login-shell capture can save inherited environment declarations under `$CODEX_HOME/shell_snapshots`. Filtering the later tool environment alone does not fix that earlier capture. Evidence: [feature defaults](https://github.com/openai/codex/blob/rust-v0.155.1/codex-rs/features/src/lib.rs) and [snapshot implementation](https://github.com/openai/codex/blob/rust-v0.155.1/codex-rs/core/src/shell_snapshot.rs).

The launcher supplies `shell_environment_policy.ignore_default_excludes=false`, `shell_environment_policy.set.OPENROUTER_API_KEY=""`, and `features.shell_snapshot=false` on each run. This enables Codex's case-insensitive `*KEY*`, `*SECRET*`, and `*TOKEN*` name filters for other ambient credentials, preserves existing exclusion/inclusion rules, and keeps the OpenRouter key empty even if a user-level `set` would restore it. Authentication still receives the original key.

These are name heuristics, not general secret detection: names such as `DATABASE_URL` or `PASSWORD` are not covered, while some nonsecret names also match. Trusted `set` entries, shell profiles, and explicit caller policy/feature overrides can reintroduce credentials. The launcher preserves other user-defined additions; review them before sharing a session with untrusted code.

On 2026-09-30, the installed Go command's real-Codex loopback test verified authenticated requests, absent/empty synthetic credentials in the shell tool, preserved user environment policy, and no synthetic credentials in active snapshots. It covers legacy exclusion/inclusion arrays and canonical filters, persisted/session model precedence, and release-local command auth through paths containing spaces, Unicode, quotes, backslash and DEL. Removing the injected default-exclusion setting in a disposable source copy failed both environment-policy variants. This verifies the observed mitigation, not every possible way to access a credential.

Disabling future snapshots does not delete old snapshots, transcripts, or backups. If an earlier run exposed a key there or in tool output, revoke/rotate it and remove affected local copies.

### Dependency integrity and managed updates

**Status: embedded native inventory and transactional installation.** Shipped code and ordinary developer tools use only the Go standard library. Node/npm are no longer installation or runtime dependencies. The reviewed [artifact manifest](internal/distribution/codex-artifacts.json) is embedded in the launcher; no production flag or environment variable can replace its native URL, hash, inventory or executable path.

The installer uses verified HTTPS, exact approved redirect hosts and default port 443. It requires HTTP 200, bounds download length/time, and verifies compressed length/SHA-256 before extracting. A private staging directory receives only allowlisted regular files/directories; links, traversal, special files, duplicate paths, unexpected inventory entries and unsafe modes are refused. Full decompressed-stream limits include tar metadata, and gzip footer/trailing-member checks prevent silently accepting a partial or appended archive. Every native file's length/hash is verified before activation.

Bounded native `--version`/`--help` probes use a private home and no API credentials. The installer retains notices, writes its completion record last, publishes a complete immutable release with a rename, then replaces the public command. Reuse verifies all file hashes and the candidate helper. An OS-held install lock coordinates cooperating installers. Failed preactivation work preserves the old public command and settings; a failure after activation reports that the new command may already be installed. Cleanup errors remain visible. Previous releases and the old npm tree are retained for sessions and recovery. [Recovery instructions](README.md#update-and-recovery) cover retry, explicit move-aside repair and rollback.

Ordinary launch verifies release structure, identity, audit/notices and helper digest without rehashing the entire native tree. Private installed files are therefore a local trust boundary. The completion record does not choose native paths or hashes; it binds the copied helper and embedded identity. Launch does not download dependencies or check for new releases.

Candidates build only from an exact Git source archive with controlled Go environment/flags. Their identity binds actual source archive bytes, commit, target, compiler, recipe and full embedded manifest digest. Two builds with separate source paths and caches must match byte-for-byte. Archives carry notices and provenance, with final hashes in `SHA256SUMS`. This is unsigned local provenance: checksums from the same publisher are not independent authentication, and neither hashes nor probes establish that upstream code is safe. The downloaded launcher is the initial trust anchor. See [packaging and maintenance](docs/releasing.md).

The historical Codex 0.155.1 source review found its npm updater can target an unrelated installation and bypass wrapper dependency selection. Startup update checks remain disabled and command-position `codex-openrouter update` directs users to run a new trusted downloaded launcher's `--install`. Evidence: [updater command](https://github.com/openai/codex/blob/rust-v0.155.1/codex-rs/tui/src/update_action.rs) and [startup update gate](https://github.com/openai/codex/blob/rust-v0.155.1/codex-rs/tui/src/updates.rs). Maintainers must keep pins current; disabling automatic updates is not a reason to defer security updates.

### Installer and configuration boundaries

**Status: hardened.** These checks address local write and injection risks:

| Boundary | Decision |
| --- | --- |
| Shared command directory | Use a dedicated `~/.codex-openrouter` prefix, separate from regular Codex. |
| Model/config input | Parse JSON as data; accept only model/reasoning fields, validate their values, and encode TOML strings. Never source or evaluate config. |
| Child processes | Execute the fixed native entrypoint directly with argument arrays. On Unix replace the launcher process, preserving PID, cwd, streams, exit and signal behavior. Authentication uses a verified immutable release-local helper rather than PATH lookup. |
| Redirected writes | Reject links in managed root/descendants and linked, non-regular or multiply linked files. Check Unix ownership and group/other-write bits; write config through an exclusively created temporary file and rename. The trusted installation parent may be a symlink. |
| Concurrent settings | Re-read under an OS-held writer lock so separate model/reasoning updates merge; report lock contention for caller retry. |
| Failed installs | Validate unsupported/unstamped/minimum-OS conditions before prefix creation. Preserve saved settings, validate existing public-entry identity, and activate only a complete verified release. |

Invalid settings/argument diagnostics name the failing rule without echoing rejected values. The bounded settings JSON accepts exactly model/reasoning strings; duplicate keys, invalid UTF-8, unknown fields and trailing data are refused. Generated TOML encodes strings as data, including the helper path; the API key never enters it.

## Upstream advisories and remaining boundaries

### Native gix dependency advisories

**Targeted source review: 2026-09-30; full native audit incomplete.** Codex `rust-v0.155.1` pins gix `0.81.0` in its [Cargo.toml](https://raw.githubusercontent.com/openai/codex/rust-v0.155.1/codex-rs/Cargo.toml) and [Cargo.lock](https://raw.githubusercontent.com/openai/codex/rust-v0.155.1/codex-rs/Cargo.lock). That version matches all four submodule advisories below; each lists gix `0.83.0` as the fix.

| Advisory | Issue | Affected gix versions |
| --- | --- | --- |
| [GHSA-f26g-jm89-4g65](https://github.com/advisories/GHSA-f26g-jm89-4g65) | Command-strategy injection through `.gitmodules` | `>= 0.31.0, < 0.83.0` |
| [GHSA-fr8x-3vfx-f45h](https://github.com/advisories/GHSA-fr8x-3vfx-f45h) | Submodule path traversal and repository confusion | `< 0.83.0` |
| [GHSA-p3hw-mv63-rf9w](https://github.com/advisories/GHSA-p3hw-mv63-rf9w) | Submodule validation/trust flaws exposing repository credentials | `< 0.83.0` |
| [GHSA-pg4w-g64p-qwhj](https://github.com/advisories/GHSA-pg4w-g64p-qwhj) | Reading symlinked `.gitmodules` outside the repository | `< 0.83.0` |

The advisories require submodule operations, including `Repository::submodules` or `Submodule::{open,state,update}`, rather than plain `gix::open`. A full exact-tag source search found production gix use in [git-utils/src/baseline.rs](https://raw.githubusercontent.com/openai/codex/rust-v0.155.1/codex-rs/git-utils/src/baseline.rs), for internal baseline/memories repositories and object/tree operations; no vulnerable submodule calls were found. The workspace disables gix default features and requests only `sha1`. Inspection of the checksum-verified gix crate found its [submodule API requires `attributes`](https://docs.rs/gix/0.81.0/gix/struct.Repository.html#method.submodules); the optional `gix-submodule?/sha1` feature edge does not activate that dependency. This is source-level applicability analysis, not a binary feature/exploit audit or a claim of universal non-reachability.

The same lockfile also has these known version matches:

| Dependency / pinned version | Advisory | Affected versions / fix |
| --- | --- | --- |
| gix-pack `0.68.0` | [GHSA-x494-mj8g-cj27: pack-data denial of service](https://github.com/advisories/GHSA-x494-mj8g-cj27) | `<= 0.68.0` / `0.69.0` |
| gix-packetline `0.21.2` | [GHSA-2vh6-hw4j-32ww: empty side-band packet panic](https://github.com/advisories/GHSA-2vh6-hw4j-32ww) | `<= 0.21.4` / `0.21.5` |
| gix-fs `0.19.2` | [GHSA-f89h-2fjh-2r9q: checkout symlink escape](https://github.com/advisories/GHSA-f89h-2fjh-2r9q) | `<= 0.21.0` / `0.21.1` |

Their feature/call applicability needs further review. In particular, baseline `find_tree`/`find_blob` calls read existing internal repositories; the gix-pack exposure question cannot be dismissed using the submodule feature analysis. No Codex exploit was reproduced. The exact native feature graph and exploit exposure remain unverified.

Upstream [.cargo/audit.toml](https://raw.githubusercontent.com/openai/codex/rust-v0.155.1/codex-rs/.cargo/audit.toml) and [deny.toml](https://raw.githubusercontent.com/openai/codex/rust-v0.155.1/codex-rs/deny.toml) ignore 11 advisory IDs, including maintenance issues, hickory-proto and quick-xml findings. Those upstream exceptions are not independently accepted dispositions for this wrapper. **Public releases are blocked until the exact native audit and exception review are resolved**, with patches or substantiated applicability/risk dispositions for known matches and exceptions.

This local behavior-compatible migration retains Codex `0.155.1`: all 42 managed macOS native files (317,010,263 bytes) match the earlier npm installation in length and SHA-256. It does not fix these upstream findings or certify the native bundle's safety. Re-evaluate when pins, features or call sites change and follow [the maintenance procedure](docs/releasing.md#pin-updates) before any public release.

### Codex sandboxing

**Historical advisory; pinned version is outside the affected range.** [CVE-2025-59532 / GHSA-w5fx-fh39-j5rw](https://github.com/openai/codex/security/advisories/GHSA-w5fx-fh39-j5rw), published 2025-09-19, describes a sandbox writable-root bug affecting CLI 0.2.0 through 0.38.0, patched in 0.39.0. The selected 0.155.1 version is not in that range. This is not evidence that the newer sandbox has no other bugs.

The public Codex advisory list was rechecked on 2026-09-30 and still listed this historical advisory. That listing is not an exhaustive security audit of the selected native bundle.

**Host-process inspection is a conditional risk, not a confirmed normal-sandbox bypass.** In the pinned source, restricted Linux commands normally use bubblewrap with a separate PID namespace and fresh `/proc`; the tool environment is cleared and rebuilt. Thus simply reading a parent environment does not establish access to the host Codex process in that mode. Full-access execution, explicit legacy Landlock, and the fallback when fresh `/proc` cannot be mounted have different boundaries. Do not rely on environment masking as protection from arbitrary same-account process inspection. This assessment is source-based; Linux execution was not reproduced locally. Evidence: [bubblewrap setup](https://github.com/openai/codex/blob/rust-v0.155.1/codex-rs/linux-sandbox/src/bwrap.rs#L290), [sandbox selection and proc fallback](https://github.com/openai/codex/blob/rust-v0.155.1/codex-rs/linux-sandbox/src/linux_run_main.rs#L259), and [child environment replacement](https://github.com/openai/codex/blob/rust-v0.155.1/codex-rs/core/src/spawn.rs#L76).

The launcher preserves existing sandbox/approval settings and forwards explicit Codex options. Model output and repository content can be hostile. A readable secret can still be copied into tool output and sent as model context even when a tool has no network access. Trusted MCP servers, plugins, hooks, shell profiles, and provider overrides need their own review. This wrapper is not a sandbox.

### Trusted MCP servers, hooks, and notifications

**Status: remaining trust boundary with environment-based authentication.** A previously trusted project's `.codex/config.toml` can configure a stdio MCP server with `env_vars = ["OPENROUTER_API_KEY"]`. Codex builds that server's environment independently of the shell policy and copies the named variable from its own process. A synthetic-key probe against 0.155.1 reproduced this on server startup, before a successful model request. Evidence: [MCP environment construction](https://github.com/openai/codex/blob/rust-v0.155.1/codex-rs/rmcp-client/src/utils.rs), [server launch](https://github.com/openai/codex/blob/rust-v0.155.1/codex-rs/rmcp-client/src/stdio_server_launcher.rs), and [project trust/config loading](https://github.com/openai/codex/blob/rust-v0.155.1/codex-rs/config/src/loader/mod.rs).

Trust applies to the project path, so later branch/checkout changes can alter its MCP configuration. Review those changes before starting a session. This is not a demonstrated untrusted-project bypass: project-local configuration is gated on trust.

Trusted/enabled command hooks and configured legacy `notify` also receive the Codex process environment independently of shell filtering. This was verified from [hook environment capture](https://github.com/openai/codex/blob/rust-v0.155.1/codex-rs/hooks/src/registry.rs), [command execution](https://github.com/openai/codex/blob/rust-v0.155.1/codex-rs/hooks/src/engine/command_runner.rs), and [notification handling](https://github.com/openai/codex/blob/rust-v0.155.1/codex-rs/hooks/src/legacy_notify.rs); no hook-trust bypass was established. Shell masking and disabling snapshots do not protect these separate paths. A future saved-key implementation should retrieve credentials directly in the auth helper, avoiding ambient inheritance into these processes; credential-store access would still need its own trust boundary.

### OpenRouter and upstream model providers

**Historical documented service boundaries (2026-09-22), not newly discovered vulnerabilities.** The hosted services were not penetration-tested or internally audited; these policy pages were not rechecked as part of the Go migration.

- Requests pass through OpenRouter and the selected model provider. OpenRouter documents opt-in prompt/completion logging and product-improvement use, request metadata retention, and sampled anonymous prompt categorization. A no-content-logging setting is not a promise of no processing or metadata. See [data collection](https://openrouter.ai/docs/guides/privacy/data-collection).
- Upstream providers have their own training and retention policies. Opting out of training is distinct from requiring zero data retention (ZDR). OpenRouter documents account/key guardrails and per-request ZDR routing; these restrictions can reduce model availability. ZDR does not cover enabled plugins/tools, and OpenRouter permits implicit in-memory prompt caching under its ZDR definition. See [provider policies](https://openrouter.ai/docs/guides/privacy/provider-logging), [ZDR scope](https://openrouter.ai/docs/guides/features/zdr), and [guardrails](https://openrouter.ai/docs/guides/features/guardrails/overview).
- This wrapper does not enforce ZDR, no-training, data residency, or a particular upstream provider, and does not configure or verify account privacy settings. Choose appropriate restrictions in [OpenRouter settings](https://openrouter.ai/settings/privacy), including the applicable model group for DeepSeek. Use a dedicated inference key with a spending limit and, where available, model/provider allowlists. Server-enforced limits reduce the impact of a stolen key; local filtering cannot enforce them. See [key and budget controls](https://openrouter.ai/docs/guides/features/guardrails/overview).

Public policy pages can change independently of this repository. Recheck them when changing models, providers, account settings, or the dependency pin. The model default sets reasoning to `high` explicitly; it does not assume a provider default.

## Credential storage and local trust

- Currently, `OPENROUTER_API_KEY` remains in the wrapper and Codex process environments for authentication. The helper returns it on stdout for Codex to capture; the wrapper does not insert it into arguments, config JSON, or generated launchers. Persistent OS credential storage is not implemented yet.
- Command-backed auth is retained because [OpenRouter uses it for model-catalog discovery](https://openrouter.ai/docs/cookbook/coding-agents/codex-cli). Codex's [built-in credential-store setting](https://learn.chatgpt.com/docs/auth#credential-storage) is not automatically a store for this custom helper's environment key.
- An OS credential store would improve protection at rest and avoid repeated exports. It would not by itself isolate the key from commands running as the same user: [Windows credential reads use the caller's logon session](https://learn.microsoft.com/en-us/windows/win32/api/wincred/nf-wincred-credreadw), [Linux Secret Service operates within the user session](https://specifications.freedesktop.org/secret-service/latest-single/), and [macOS access depends on Keychain permissions](https://support.apple.com/guide/mac-help/allow-apps-to-access-your-keychain-kychn002/mac). Do not describe an unlocked store or a helper that returns a key as a sandbox boundary.
- New managed Unix directories use `0700` and non-executable files use `0600`. Ownership and write checks permit existing entries with group/other read permission, including retained npm files. Windows ACL/privacy and running-executable replacement have no runtime validation, so Windows installation is withheld. Use a private local directory, especially with `CODEX_OPENROUTER_HOME`.
- The downloaded launcher/publisher, Go runtime, installation parent/ancestors, private installed files and existing Codex configuration are trusted. Checks do not defend against an administrator, compromised account, malicious runtime or concurrent changes by another process with the same rights. Do not install into shared/attacker-controlled locations or use `sudo`.

## Verification and maintenance

On 2026-09-30, local Go formatting, vet, offline ordinary/race tests and six application/test cross-compiles passed with Go 1.27.1 and toolchain/module downloads disabled. Regression checks cover settings persistence/concurrency, malformed/secret-bearing inputs, path/TOML boundaries, credential stdout, exact Unix exec/PID/cwd/streams/exit/signal behavior, archive/download bounds, failed publication/activation, cleanup reporting, previous identities, legacy recognition and repair.

The real installed command passed HTTPS installation, both authenticated loopback environment-policy variants, version/help without credentials and separate terminal Ctrl-C/SIGTERM checks. An independent inventory check covered all 42 native files, eight retained notices and helper/audit/completion identity. Bundled ripgrep performed a search and bundled zsh ran with `-f`; voice and code-mode-host functionality were not exercised. The controlled environment probe uses `danger-full-access` to run only that probe, so this is not OS-sandbox or sandbox-escape evidence.

Only darwin/arm64 has native evidence, observed on macOS 26.5.1. The declared macOS 15 minimum is artifact-derived, not a test on macOS 15. All other platforms remain withheld. The bundled Codex executable passed strict code-signature checks outside the local test sandbox. The Go launcher has no publisher signing/notarization credentials, and downloaded-software policy acceptance has not been established.

`govulncheck@v1.8.0` found no vulnerabilities in the locally scanned Go source and candidate binary on 2026-09-30. This separately pinned scanner has its own third-party build dependencies; the application module contains only `codex-openrouter`. Its result includes the Go standard library and does not audit upstream Rust/C/native resource code. Repeat source/binary scans on the final artifact after any source change; no older scan certifies a later build.

[CI](.github/workflows/test.yml) and [candidate packaging](.github/workflows/release.yml) use pinned Go/actions with read-only permissions. Linux/macOS race tests and explicit Mac ARM64 native gates are configured; hosted CI has not run. Before uploading a test candidate, the Mac job checksums and extracts its archive, verifies the supplied executable's BUILD identity/hash, and installs/exercises those exact bytes through the existing loopback tests. Packaging also refuses root-license drift from the retained notice. No release publishing is configured. Publication requires passing hosted/native checks and an exact-artifact manual TUI/termination report as specified in [the release procedure](docs/releasing.md); future publishing automation must enforce that record. [Dependabot](.github/dependabot.yml) requests weekly GitHub Actions updates when enabled; it does not update the custom native JSON manifest or patch already installed Go executables.

Follow the [maintenance procedure](docs/releasing.md) for Go security rebuilds, native inventory/notices/advisories, scanner/action dependencies and auth/environment/resource/terminal checks. Keep historical source and provider-policy dates separate from new measurements. Installed copies do not receive automatic updates or notifications.

Report suspected vulnerabilities privately through the hosting platform's security advisory feature if enabled. Do not include real API keys in reports.
