# Go launcher migration: specification and delivery plan

Status: the local Go migration, documentation and candidate workflow are implemented for the initial darwin-arm64 scope. Dated local evidence covers HTTPS installation, real-Codex loopback sessions through the installed public command, terminal checks and checksummed packaging. Bootstrap and verify each final artifact from its exact trusted commit before installation; earlier candidate evidence does not certify changed source. Public releases are blocked by the [incomplete native dependency/exception review](../SECURITY.md#native-gix-dependency-advisories). Hosted CI and all other native targets remain unverified.

Baseline: repository commit `d14e315`, Codex `0.155.1`, and the working tree reviewed on 2026-09-22. Existing user README content, including the Zsh key instructions and credential-store TODO, is retained.

## 1. Decision, value, and scope

Use Go for the launcher, credential helper, and managed installer. Use only the Go standard library in shipped code. Continue to run Codex as a separate native program; do not embed Codex source, reimplement its protocol, or add a local proxy.

The reasons are an installation that does not require Node/npm, a small executable users can download, and the maintainer's preference for Go. This is not an emergency vulnerability remediation: the historical JavaScript wrapper had no third-party npm libraries beyond Codex. Go still brings a compiler, runtime, standard library, and release process that require security maintenance.

Official OpenAI documentation currently offers standalone Codex installation on macOS/Linux and Windows; npm is an alternative, not a requirement. See the [CLI installation guide](https://learn.chatgpt.com/docs/cli) and [standalone installer variables](https://learn.chatgpt.com/docs/config-file/environment-variables). These pages establish that a Node-free route exists; they do not establish the complete contents or compatibility of the exact pinned artifacts. Milestone M0 must establish that separately.

Deliver in two stages:

1. **Runnable port:** demonstrate the Go launcher and credential helper against the exact pinned native Codex distribution on the development host, with existing behavior protected by focused regression checks. Keep the current release intact.
2. **Replacement release:** finish verified native installation, migration, platform validation, release artifacts, and documentation before removing the Node implementation.

A working first stage in one or two hours of focused agent work is plausible, provided the pinned distribution is usable and the toolchain is ready. It is an estimate, not an acceptance criterion or a promise of a finished cross-platform release. Packaging defects, Windows behavior, unavailable test machines, or upstream resource requirements can exceed that window. At the end of that window, report completed gates and actual blockers rather than weakening the gates.

### In scope

- The existing `codex-openrouter` command and saved model/reasoning defaults.
- Environment-based OpenRouter authentication and existing credential protections.
- Direct execution of a pinned native Codex distribution with its required resources.
- Installation into the existing private per-user prefix without administrator access.
- A documented transition from the current npm installation.
- Downloadable launcher binaries, checksums, platform evidence, and maintenance instructions.

### Deferred

- OS credential stores, new authentication methods, stronger credential isolation, or changes to provider privacy policy.
- Background update checks, self-update daemons, a general package manager, automatic garbage collection, or an automatic rollback command.
- Importing the user's arbitrary `codex` from PATH, selecting arbitrary Codex versions, or falling back to npm.
- New configuration fields, model discovery UI, plugins, telemetry, or a proxy server.
- A shell/PowerShell script that downloads and executes the launcher automatically. Initial distribution uses explicitly downloaded release files.
- New platforms beyond those supported by the pinned upstream distribution.

## 2. Baseline behavior and compatibility rules

The historical implementation is recorded at baseline commit `d14e315523590e651862ec712a60758f2faae473`. With the original repository's Git history, retrieve its sources without relying on removed working-tree files:

```sh
git show d14e315523590e651862ec712a60758f2faae473:codex-openrouter.mjs
git show d14e315523590e651862ec712a60758f2faae473:lib/config.mjs
git show d14e315523590e651862ec712a60758f2faae473:auth.mjs
git show d14e315523590e651862ec712a60758f2faae473:install.mjs
git show d14e315523590e651862ec712a60758f2faae473:test/launcher.test.mjs
git show d14e315523590e651862ec712a60758f2faae473:scripts/smoke.mjs
```

The current implementation is in `cmd/codex-openrouter` and `internal/`; its installed-artifact integration is `internal/integration/TestNativeLoopback`. Dated historical observations in [SECURITY.md](../SECURITY.md) remain relevant.

Preserve these contracts:

| Area | Required behavior |
| --- | --- |
| Command | `codex-openrouter` launches the managed Codex distribution. |
| Default model | `deepseek/deepseek-v4.1-flash`; do not change the model during this migration. |
| Default reasoning | `high`, supplied explicitly. |
| Settings file | Existing `config.json`, containing only `model` and `reasoning`. |
| User prefix | `CODEX_OPENROUTER_HOME`, otherwise the user's home directory plus `.codex-openrouter`. |
| Ordinary Codex state | Respect the existing `CODEX_HOME` and Codex configuration. Do not rewrite, relocate, or delete them. |
| Overrides | Wrapper defaults precede caller arguments; explicit caller Codex options retain precedence. |
| Credentials | `OPENROUTER_API_KEY` from the environment, returned to Codex by a command-backed helper. |
| Network at launch | Only Codex handles provider traffic. The Go launcher does not check releases or download dependencies during launch. |
| Working directory | Preserve the caller's working directory. Resolve internal paths absolutely. |
| Standard streams | Preserve interactive terminal behavior, piped input, redirected output, and native Codex output. |
| Updates | Intercept Codex's command-position `update` and print the managed-update instructions. Do not run Codex's updater. |
| Ordinary command failures | Preserve Codex's exit status; wrapper failures return nonzero without dumping secrets. |

Intentional interface changes must be documented in the release notes:

- The new installer is `./codex-openrouter --install` or `.\codex-openrouter.exe --install`, run from a downloaded release executable. It replaces `node install.mjs` and `sh install.sh`.
- Installation does **not** edit settings. Select a different default afterward using `--set-default`. The old install-time positional model, `--model`, and `--reasoning` options are not accepted by `--install`.
- On Windows the native command is `codex-openrouter.exe`, also callable as `codex-openrouter`. Explicit calls to the old `codex-openrouter.cmd` must be updated. Do not add a batch shim that would reintroduce shell argument interpretation.
- Reject duplicate JSON keys, invalid UTF-8, and incorrectly cased settings keys rather than inheriting Go's permissive struct decoding behavior. Normal existing settings files remain compatible.
- Wrapper-only modes reject unexpected extra arguments instead of accidentally forwarding malformed management commands to Codex.

## 3. Code and dependency boundaries

Start with this structure; add packages only when there is a concrete separation of responsibility:

```text
cmd/codex-openrouter/main.go
internal/launcher/
    cli.go
    config.go
    auth.go
    launch.go
    process_unix.go
    process_windows.go
    *_test.go
internal/install/
    install.go
    download.go
    extract.go
    manifest.go
    files_unix.go
    files_windows.go
    *_test.go
internal/platform/          # only shared file/lock primitives actually needed by both packages
internal/integration/      # installed-artifact and real-Codex behavioral tests
go.mod
.go-version
internal/distribution/codex-artifacts.json
docs/go-launcher-spec.md
.github/workflows/test.yml
.github/workflows/release.yml
```

`main` chooses a mode and returns an exit status. It must not contain installation or configuration logic. Keep exported symbols minimal; do not create a public Go SDK, generic plugin system, dependency-injection framework, or reusable installer library.

Production and ordinary tests use standard-library packages only. No Cobra, Viper, third-party HTTP client, TOML parser, or test framework is needed. Generate the few required TOML strings; do not parse or rewrite the user's TOML.

Choose a currently supported patched Go toolchain in M0, record its exact version, and use it in CI and releases. Do not choose an old version merely because it happens to be installed locally. Use `CGO_ENABLED=0` for release builds. Test that ordinary building and testing do not download modules or silently download a different toolchain. A separately pinned CI vulnerability scanner is a build tool, not a runtime dependency; disclose that distinction.

## 4. CLI contract

Only the first argument selects a wrapper-specific mode. Otherwise, forward the original argument vector after the injected Codex defaults. Do not parse all arguments through Go's `flag` package or normalize arbitrary Codex options.

| Invocation | Result | Key required? | Managed Codex required? |
| --- | --- | --- | --- |
| No arguments, prompt text, `exec`, or another Codex command | Launch Codex with defaults and unchanged caller arguments. | Yes | Yes |
| Exactly `--help`, `-h`, `--version`, or `-V` | Forward to Codex; `--version` continues to mean Codex's version. | No | Yes |
| `--set-default MODEL [--reasoning LEVEL]` | Validate and atomically update the saved defaults. | No | No |
| `--set-default --model MODEL [--reasoning LEVEL]` | Equivalent explicit model option. | No | No |
| `--set-default --reasoning LEVEL` | Preserve the current model and change reasoning only. | No | No |
| Exactly `--show-config` | Print the absolute settings path, then the validated JSON object. | No | No |
| Exactly `--launcher-version` | Print launcher version, target, build ID, and pinned Codex version. | No | No |
| Exactly `--launcher-help` | Print wrapper modes and installation/update instructions. | No | No |
| Exactly `--install` | Run the managed installer described below. | No | No |
| Exactly `--install --help` | Print installer usage; do not inspect or mutate the installation. | No | No |
| Exactly `--internal-auth` | Print the trimmed environment key, without a newline, for Codex to capture. | Yes | No |
| Recognized command-position `update` | Print instructions to download a new launcher release and run its `--install`; exit zero. | No | No |

Additional parsing requirements:

- `--launcher-version`, `--launcher-help`, and `--internal-auth` do not read settings, initialize the prefix, inspect Codex, or access the network.
- `--set-default` accepts one model source: positional or `--model`, never both, in both `--model VALUE` and `--model=VALUE` forms (same for `--reasoning`), with `--` ending option parsing. Reject missing option values, unknown options, repeated settings options, and multiple positional models. Re-supplying the current values explicitly succeeds as an idempotent rewrite, matching the legacy wrapper; only an invocation with no model or reasoning argument at all is rejected as changing nothing.
- `--set-default --help` prints mode usage with status zero and no writes. Invalid mode syntax returns status 1.
- Model/reasoning values are data. Spaces, quotation marks, `%`, `$()`, backticks, Unicode, and empty strings in forwarded Codex arguments must retain their received boundaries. OS-level NUL limitations are not a feature to emulate.
- An argument after `--` is forwarded even if it resembles a wrapper flag or the word `update`.
- Use the existing `isUpdateCommand` behavior as the compatibility baseline, including `--profile update`, attached `-c` values, and separated versus attached `--image` values. An unknown option makes interception conservative: forward it and let Codex decide. Do not intercept prompt text containing “update,” `exec update`, `plugin update`, or `help update`.
- Review this small command-position recognizer whenever the Codex pin changes; do not independently reimplement Codex's complete parser.

Wrapper diagnostics go to stderr and begin with `codex-openrouter:`. Successful config/help/version output goes to stdout. Installation progress goes to stderr. Ordinary Codex invocation gets no wrapper banner, progress output, or debug argument dump.

## 5. Configuration, paths, and file safety

### Settings semantics

- An absent file means the compiled defaults. An existing file must provide both fields with string values; null, arrays, numbers, partial objects, unknown fields, duplicate keys, multiple JSON documents, and trailing non-whitespace are errors.
- Read at most 4,097 bytes and reject files exceeding 4,096 bytes. Do not rely solely on a pre-read `stat` size.
- Accept model IDs matching the existing ASCII grammar: `^~?[a-zA-Z0-9][a-zA-Z0-9._-]*/[a-zA-Z0-9][a-zA-Z0-9._:~/-]*$`, at most 200 characters.
- Accept reasoning values `none`, `minimal`, `low`, `medium`, `high`, `xhigh`, and `max`. Codex/provider validation of actual model support remains upstream behavior.
- Malformed settings produce a generic error identifying the path, without echoing file contents, offending field names, values, or raw parser errors.
- Updating one setting preserves the other. Session overrides never persist.
- Format writes as two-space-indented JSON, model before reasoning, ending in a newline. Successful `--set-default` prints `Default: MODEL (REASONING)`.
- `--show-config` prints only the two validated fields. Installation must leave an existing settings file byte-for-byte unchanged.

Go's JSON decoding has compatibility behaviors, including case-insensitive struct field matching and duplicate-key handling. Implement the exact key contract deliberately, rather than assuming `DisallowUnknownFields` alone supplies it. See [encoding/json](https://pkg.go.dev/encoding/json).

### Prefix and permissions

- If `CODEX_OPENROUTER_HOME` is set, an empty value is an error. Require an absolute non-root path; do not expand a literal `~`, shell variables, or command substitutions.
- For the default, use the OS user's home directory, failing clearly when unavailable. Do not use the repository directory as a fallback.
- Windows drive-relative paths such as `C:folder`, bare drive roots, device paths, and UNC/network locations are outside the first release's supported installation contract. WSL uses the Linux build in the Linux filesystem.
- Spaces and valid Unicode in supported local paths must work. A path that cannot be represented correctly in the generated provider configuration must fail before launching Codex, not be silently rewritten.
- Require the installation parent to exist. Reject a managed root that is a symlink, Windows junction/reparse point, or non-directory. Reject links/reparse points in newly managed descendants as well.
- On Unix, retain the existing ownership and group/other-write checks for the root, immediate parent, and settings file. Use `0700` for private managed directories and `0600` for settings and transaction data. Do not silently repair another user's files.
- For settings, reject symlinks, hardlinks with link count greater than one, and non-regular files. Check the opened file's identity/type as well as the path; do not claim to prevent all hostile same-account races.
- On Windows, query link count and reparse information using OS facilities; POSIX permission bits are not an ACL. Retain the requirement for a private user-controlled directory and document the ACL limitation. Do not claim a full ACL audit unless it is implemented and tested.
- Installation ancestors and the account remain trusted. This wrapper is not a sandbox against an administrator or another process with the same user rights.

### Writes and concurrent settings changes

- Serialize cooperating settings writers with an OS lock in the private prefix. Acquire the lock before re-reading settings and calculating the new values, so simultaneous model-only and reasoning-only updates do not lose each other's changes.
- Use a lock held by an OS handle, not an existence-only sentinel requiring users to delete “stale” locks. A process crash must release the lock. Fail promptly with a “settings busy; retry” message if acquisition would block.
- Create a unique temporary file exclusively in the destination directory, write it completely, sync/close it, and replace the destination using the tested platform-specific operation. Readers must get either complete old or complete new JSON.
- Never implement replacement as “delete the destination, then rename.” On failure retain the previous file and remove only the temporary file created by this operation.
- Do not promise power-loss durability beyond what the tested OS/filesystem provides. Test ordinary errors and process interruption. Go explicitly does not promise atomic `os.Rename` on every platform; Windows replacement needs its own implementation and evidence. See [os.Rename](https://pkg.go.dev/os#Rename).

## 6. Native launch and authentication

The Go executable's embedded release identity selects an immutable installed release directory. It must launch that release's native Codex through an absolute path. Never search PATH for Codex, run a command string through a shell, or fall back to a different installed version.

Preserve these injected configuration values, in this order before caller arguments:

```text
model_provider="codex_openrouter"
model_providers.codex_openrouter={name="OpenRouter", base_url="https://openrouter.ai/api/v1", wire_api="responses", auth=...}
model=<saved model>
model_reasoning_effort=<saved reasoning>
check_for_update_on_startup=false
shell_environment_policy.ignore_default_excludes=false
shell_environment_policy.set.OPENROUTER_API_KEY=""
features.shell_snapshot=false
```

Each line is the value of a separate `-c` argument. Use a correct TOML basic-string encoder for dynamic values. JSON string encoding can be used for this restricted purpose; Go-specific quoted escapes such as `\xNN` must not appear. Verify paths containing backslashes, quotes, spaces, and Unicode with real Codex.

`auth` must invoke the **immutable release-local copy of this launcher** with the sole argument `--internal-auth`. Do not point it at a mutable PATH entry or the executable that a later installation will replace. This allows a running Codex session to keep its original helper after an upgrade.

The helper reads only `OPENROUTER_API_KEY` and applies the previous JavaScript helper's surrounding-whitespace rule: trim U+0009–U+000D, U+0020, U+00A0, U+1680, U+2000–U+200A, U+2028, U+2029, U+202F, U+205F, U+3000, and U+FEFF from both ends. Do not use a different Unicode whitespace definition accidentally. Write the remaining key with no newline. Missing/blank keys return status 1 with a generic stderr message and no stdout. No key goes into arguments, JSON, TOML, release metadata, logs, crash diagnostics, or installation state. Internal punctuation is data, not a command.

The key remains in the environment of Codex for this migration. Do not advertise the language change as fixing credential inheritance into trusted MCP servers, hooks, shell profiles, or same-account process inspection. Preserve the existing shell filtering and disabled snapshots; caller overrides remain possible and must be documented.

Before an ordinary launch, verify that the expected managed release is structurally complete and its manifest matches the launcher's embedded release identity. Missing/corrupt installations fail with reinstall instructions; do not download automatically. Full file hashes are enforced at installation, not re-read over the whole upstream distribution on every launch. Installed private files remain a local trust boundary.

Do not copy the npm wrapper's package-manager environment wholesale. In M0, identify the native distribution's actual resource discovery requirements. Remove inherited `CODEX_MANAGED_BY_NPM`, `CODEX_MANAGED_BY_BUN`, `CODEX_MANAGED_BY_PNPM`, and `CODEX_MANAGED_BY_VITE_PLUS` from the Codex child environment. Set or clear `CODEX_MANAGED_PACKAGE_ROOT` according to verified native-bundle semantics. Do not invent a value to make `--version` pass while breaking tools or resources.

### Process and terminal behavior

- On Unix, prefer replacing the launcher process with native Codex using `exec` after preparation. Preserve PID, working directory, environment, streams, and normal signal semantics. This avoids introducing another long-lived signal-forwarding process.
- On Windows, start the native executable directly, inherit its streams and console, wait, and return the native exit status. Do not use `cmd /c`, PowerShell, `Start-Process`, or a detached process to run Codex.
- Ctrl-C must reach Codex exactly as intended by console behavior, without double delivery caused by both OS delivery and manual forwarding. Do not assume Unix signal APIs implement Windows console control events. See [os/signal](https://pkg.go.dev/os/signal).
- A missing/non-executable native binary or failed process creation returns status 1 with a sanitized diagnostic. A normally exiting child preserves its status. Unix signal termination preserves normal shell-observed signal status.
- Verify interactive startup, Ctrl-C, redirected stdin/stdout, and programmatic termination separately. Cross-compilation alone does not establish terminal correctness.
- Do not add a PTY library or a background supervisor. Do not make new claims about cleaning up all descendants after SIGKILL or forced Windows process termination.

## 7. Upstream distribution and immutable release layout

M0 must inventory the complete pinned distribution. The locally inspected macOS ARM64 npm package contains a native Codex executable, code-mode helper, ripgrep, shell resources, voice resources/libraries, and package metadata. This is evidence that copying one `codex` file is insufficient; it is not proof that every standalone archive has the same layout.

Keep the first migration pinned to Codex `0.155.1`. If an advisory or missing compatible artifact requires a different pin, make that a separately explained dependency change and rerun the version-specific security checks. Do not silently substitute “latest.”

Commit `internal/distribution/codex-artifacts.json` and embed its contents in the launcher. For every published platform it must specify:

- Schema version, exact upstream version/tag, OS/architecture, and supported OS minimum.
- Exact HTTPS artifact URLs, approved redirect hosts, archive format, compressed byte length, and SHA-256.
- The expected native entrypoint and companion/resource layout.
- An inventory or installation map of accepted output paths, file types, lengths, hashes, and executable flags, plus total extracted-size/file-count bounds.
- Provenance of the selected upstream artifacts and the license/notice files to retain.

These must be actual reviewed values, not placeholder hashes or values downloaded and trusted at install time. The embedded manifest is the authority; an installed JSON copy is for audit only. Do not allow an environment variable or local file to override production download URLs or expected hashes. Tests may inject a transport and manifest through internal test seams.

Require artifacts that can be handled with Go's standard archive facilities, normally ZIP or tar/gzip. If the required upstream distribution is only available in another format, M0 must resolve that explicitly before the installer is implemented. Do not silently add a decompression library or external extraction executable.

Use this installed layout, with paths relative to the managed prefix:

```text
config.json                         existing user settings; installer never rewrites it
bin/codex-openrouter                 Unix public executable
codex-openrouter.exe                 Windows public executable
releases/<release-id>/
    codex-openrouter[.exe]           immutable copy used for authentication
    codex/                          complete reviewed native distribution
    codex-artifacts.json             audit copy
    installation.json                generated inventory and launcher checksum
    LICENSES/                       required wrapper/upstream notices
.install.lock                       OS lock file; no secret contents
.config.lock                        OS lock file; no secret contents
.staging-<random>/                   current transaction only
.legacy-shims/<transaction-id>/      recognized retired npm shims, when applicable
```

`release-id` is embedded at build time, restricted to safe ASCII filename characters, and identifies the launcher version/commit, target, toolchain/build recipe, and artifact-lock digest. A release ID is immutable: publishing different bytes under the same identity is an error. No mutable “current” pointer is needed; the public executable selects its own release ID.

Keep previous release directories during this migration. Do not delete files a running Codex session or credential helper may still need. Disk reclamation is a later feature. The ordinary Codex installation and the old npm package tree remain separate.

## 8. Installer contract

### Entry and trust

The user downloads the appropriate launcher archive and verifies its published checksum before extracting and running the executable. The first release need not implement a bootstrap downloader. The Go installer downloads only the Codex artifacts named by its embedded manifest.

The downloaded launcher/release publisher is the initial trust anchor. Hash checking detects substitution relative to the trusted manifest; it does not prove that the launcher publisher or upstream code is safe. Release checksums fetched from the same compromised publisher are not independent proof of authenticity. Publish build provenance where the hosting platform supports it and document its verification.

`--install` accepts no model, reasoning, mirror, insecure-TLS, arbitrary-version, or hash-bypass options. It installs the version compiled into the executable. An older trusted executable may deliberately reinstall its older pin; this is a user-chosen downgrade, not automatic rollback.

Do not run installation from the public executable being replaced. Detect that case using file identity, not just matching path strings, and instruct the user to run the newly downloaded executable. Never request administrator elevation or uninstall Node/npm on the user's behalf.

### Ordered transaction

1. Validate the supported target, embedded manifest, prefix, destination directories, and existing settings. A malformed existing settings file is reported without modifying it. If no settings file exists, leave it absent and use compiled defaults later.
2. Acquire a nonblocking exclusive OS lock on `.install.lock`. A second installer fails promptly with “installation busy; retry.” A crashed installer must not leave a stale lock that requires manual removal. Hold the lock through activation and cleanup.
3. Inspect the existing public entry and potential legacy shims. Refuse to overwrite an unrelated executable or script. Recognize a previous Go entry by its matching immutable release inventory. Recognize a legacy npm installation only by its expected local package identity and exact managed entry targets. Check ownership/type before accepting either.
4. Create private unpredictable staging under the managed prefix. Download, verify, extract, and inventory the exact pinned artifacts as specified below. No downloaded code executes before verification completes.
5. Run the staged native Codex with `--version` and `--help` using a private temporary `CODEX_HOME` and a minimal OS-appropriate environment without API credentials or package-manager markers. Require the exact expected version and successful exit. Apply a 10-second timeout and a 16-KiB output cap per probe; do not echo arbitrary probe output on failure. These checks supplement integrity and platform smoke tests; they do not prove every resource works.
6. Copy the running launcher's bytes into the staged immutable release, verify the copy's digest, include notices and audit metadata, and write the completion inventory only after all required files are present. Sync/close files before publication.
7. Publish the complete release directory with a same-filesystem rename to `releases/<release-id>`. If that directory already exists, reuse it only after checking the complete inventory and file hashes. Never merge into or silently overwrite a mismatched or incomplete existing release directory. Provide explicit repair instructions instead.
8. Prepare the public executable as an exclusively created temporary regular file in the public entry's destination directory. Verify its digest and executable permissions. Preserve the old recognized entry as a private backup before attempting activation; back up an allowed legacy symlink as a link, not by following it.
9. **Activation point:** replace the public entry using the tested OS operation. On Unix use atomic same-directory replacement. On Windows use the supported replacement primitive; never fall back to truncating or deleting the live executable. A sharing violation from a running launcher is a pre-activation failure: ask the user to close those sessions and retry.
10. After activation, retire only recognized legacy Windows shims that would shadow the new executable, and finish transaction cleanup. Report success only after command selection is unambiguous. Print the installed launcher/Codex versions, executable path, settings path, and the appropriate user PATH directory. Do not modify shell profiles, the Windows registry, or PATH automatically.

The installer does not write `config.json`, so settings are not part of a multi-file installation transaction. A concurrent `--set-default` can finish independently; its values must not be overwritten by installation.

### Failure, interruption, and repair

| When failure occurs | Required result |
| --- | --- |
| Validation, download, integrity, extraction, or probe failure | Existing public entry and settings remain unchanged. No failed artifact executes. |
| Complete release published but activation not attempted/successful | Existing entry remains usable. A complete unused release may remain and be verified/reused on retry. |
| Windows destination is in use | Fail before replacement; do not kill processes or schedule a reboot-time replacement. |
| Activation succeeded but legacy-shim retirement or cleanup failed | Return status 1 and explicitly say the native version was installed but migration/cleanup is incomplete. Print the direct native executable path and retry instructions. Do not falsely claim that nothing changed. |
| Process dies before activation | The previous entry is still selected. Private staging may remain; a later install creates its own staging and does not blindly delete unknown leftovers. |
| Process dies after activation | The new public executable points to a complete immutable release. Retry completes recognized legacy cleanup. Existing settings and previous releases remain available. |
| Replacement API returns an ambiguous result | Compare the entry against the known old/new digest without executing unknown contents. Report the observed state; do not guess or delete the destination. |
| A completed release directory is later damaged | Fail safely and document repair: close affected sessions, move aside that specifically identified managed release directory, then rerun the trusted downloaded installer. Do not recursively delete arbitrary paths. |

For initial installation, failure may leave a private root, lock files, or a verified unused release; it must not leave a public command pointing into partial staging. Cleanup removes only paths created by the current transaction. Cleanup errors must not mask the primary error or be presented as a clean installation.

Process-interruption recovery and an intact old-or-new executable are required. Do not claim full transactional durability against power loss, disk corruption, hostile same-account mutation, or unsupported filesystems.

### Download policy

- Use the standard HTTPS client with certificate and hostname verification enabled. No external `curl`, `wget`, browser, npm, or archive command runs from the installer.
- Allow initial URLs only from the embedded artifact manifest. Follow at most five redirects, only to exact approved HTTPS hosts. Reject URL userinfo, non-HTTPS destinations, and unapproved redirects.
- Use a 10-second connection/TLS-handshake timeout, 30-second response-header timeout, and 10-minute total timeout per artifact. Cancellation must close the request and stop installation.
- Require HTTP 200 for a complete download; do not implement resumed/range downloads in this release. Handle 403, 404, rate limiting, timeout, and network loss as retryable-by-the-user failures. No silent version substitution or registry fallback.
- Request the artifact bytes without transparent HTTP decompression. Limit the body to the pinned compressed size plus one byte and require exact length and SHA-256 before extraction. Do not trust `Content-Length` as enforcement.
- Respect standard proxy environment settings while preserving HTTPS certificate verification. Do not add a certificate-validation bypass. Redact proxy credentials and signed redirect query strings from diagnostics.
- Download into a private exclusively created staging file. No shared writable cache. Do not leave a partial file at a final artifact path.
- Routine launching, config commands, version/help commands, and the auth helper make no installer network requests.

### Extraction policy

- Treat archive paths and metadata as untrusted until checked against the embedded inventory, even after the archive hash matches.
- Reject absolute paths, traversal components, Windows drive/UNC/device names, alternate data streams, reserved Windows filenames, trailing-dot/space aliases, and output paths escaping staging. Check the destination OS's rules, not only the host's path-cleaning function.
- Reject duplicate output paths, case-folding collisions on relevant filesystems, unexpected files, excessive entry counts, and files that exceed their recorded or total size limits. Validate names before creating anything.
- Do not create device files, FIFOs, sockets, setuid/setgid files, hardlinks, or arbitrary symlinks from an archive. Ignore archive ownership and use private installation permissions with explicit executable flags from the reviewed manifest.
- If upstream needs a relative symlink, M0 must specify a bounded mapping to materialize the referenced, verified regular file at the required path. The target must be inside the same verified distribution and cycles must fail. No general-purpose symlink-following extractor is permitted.
- Stream files into private staging, enforce byte limits during decompression, and verify every required output hash and length. Never extract directly over an installed release.
- Preserve resource layout, executable names, licenses, and required metadata. Do not strip, re-sign, or modify upstream binaries as part of extraction.

## 9. Migration and platform policy

### Existing users

- Keep the default prefix and settings location. Keep `CODEX_OPENROUTER_HOME` semantics for both installation and execution.
- On Unix preserve the public PATH directory `~/.codex-openrouter/bin`. The npm symlink at `bin/codex-openrouter` may be replaced only if it resolves to this prefix's expected `lib/node_modules/codex-openrouter/codex-openrouter.mjs` and the surrounding installation passes the legacy checks. No other symlink is an allowed overwrite target.
- On Windows preserve the public PATH directory `~/.codex-openrouter`. After native activation, move recognized npm-generated `codex-openrouter`, `codex-openrouter.cmd`, and `codex-openrouter.ps1` shims into `.legacy-shims/<transaction-id>/` so PowerShell/cmd do not choose them ahead of the executable. The transaction ID is generated by the installer and contains no path separators. Do not rewrite shim bodies or execute them during detection. If a file cannot be recognized safely, fail with its path and leave it intact.
- Do not remove the old npm package tree during migration. It supports recovery and may still be used by a running legacy session. A running new Go session uses its immutable release-local auth helper.
- Never touch a separate `codex`, `open-codex`, or `codex-kimi` installation; do not migrate their settings implicitly.
- Document rollback to an earlier Go release by running that trusted downloaded executable's installer. Document rollback to the Node version by using the earlier checkout's installer. Both preserve `config.json`; the user remains responsible for having the required runtime for the old implementation.
- Document uninstall by removing the user's dedicated prefix and PATH entry. State that deleting the entire prefix also deletes the wrapper's defaults; it does not remove ordinary Codex state. Do not add an automatic recursive-delete command in this migration.

### Platforms

Candidate targets are `darwin/amd64`, `darwin/arm64`, `linux/amd64`, `linux/arm64`, `windows/amd64`, and `windows/arm64`. Resolve the exact upstream artifact mapping and OS minimums in M0. Do not assume a musl/glibc choice, Rosetta behavior, Windows emulation, or a minimum OS solely from the Go compiler's supported targets.

For each target distinguish:

1. **Build verified:** the launcher cross-compiles and its metadata is correct.
2. **Runtime verified:** install, config, direct native invocation, auth helper, and synthetic-provider tests passed on that OS/architecture.
3. **Terminal verified:** interactive startup, Ctrl-C, and platform-specific command resolution were exercised on that platform.

Only targets with the required runtime and terminal evidence are called supported. Cross-compiled targets without execution evidence may be labeled experimental; publish that limitation next to their downloads. Do not claim six-platform support because six binaries compiled.

Windows tests must cover PowerShell and cmd resolution, spaces/Unicode, hardlinks/junctions, replacement of an existing executable, and refusal when the destination is in use. Unix tests must cover ownership/mode checks, executable permissions, and signals. WSL is a Linux environment, not evidence for native Windows.

Build/package the launcher without mutating upstream code-signing data. Verify actual macOS launch behavior of downloaded artifacts. Signing/notarization or Windows publisher signing can be added when credentials are available; do not claim those guarantees without producing and verifying them, and do not teach users to disable platform security checks.

## 10. Verification plan

Tests protect observable contracts and concrete regression risks. Do not add a test merely because a file was ported, to check every constant, or to assert that JavaScript files disappeared. Prefer porting/extending the existing behavior cases. Keep fixtures small and use native Go helper executables, not Node-based test doubles.

### Required behavior coverage

| Boundary/regression | Required evidence |
| --- | --- |
| Lost defaults or overridden user policy | Existing config survives reinstall and session overrides; changing only one setting preserves the other; ordinary Codex TOML remains byte-for-byte unchanged. |
| Permissive Go JSON decoding | Secret-bearing/unknown/case-variant/duplicate keys, malformed JSON, partial objects, trailing documents, and oversized files fail without echoing contents. |
| Argument or TOML injection | A native fake child observes the original argument vector, including empty arguments, quotes, shell metacharacters, backslashes, and Unicode; crafted settings cannot add Codex options. |
| Redirected writes | Symlink/junction/hardlink and Unix permission cases protect an unrelated sentinel file/directory; failed operations preserve original contents. |
| Concurrent settings writes | Concurrent model-only and reasoning-only updates serialize and preserve both changes; killed writers do not leave a permanent stale lock. |
| Auth regression | Literal synthetic keys reach only helper stdout and authenticated mock-provider requests; missing/blank keys fail without output; config/help/version work without a key where specified. |
| Accidental updater interception | Existing command-position examples retain behavior; prompts, option values, nested commands, and `--` are forwarded. |
| Native resource omission | The installed distribution executes meaningful Codex tools requiring the packaged resources, not merely `--version`; inventory hashes and companion executables are present. |
| Dependency substitution | A deliberately wrong archive hash fails before extraction/execution and preserves the existing command/settings. |
| Hostile or damaged archive | Traversal, duplicates, size overruns, unsupported links, and case collisions fail without writing outside staging. |
| Partial download and activation failure | Interrupted HTTP, disk/write failure, failed native probe, concurrent install, and failed replacement preserve the documented old/new state. |
| Installed credential isolation | Real pinned Codex calls a loopback mock provider with a synthetic key, executes the controlled environment probe, preserves user environment rules, and leaves no synthetic secrets in active shell snapshots. |
| Terminal/process regression | Exit status, interactive I/O, Ctrl-C, and Unix programmatic signals or Windows console behavior are correct on actual target systems. |
| Legacy command shadowing | Migrating a representative npm installation selects the native executable; a cleanup failure is reported as incomplete with a usable explicit path. |
| Runtime requirement | The installed artifact works with Node/npm unavailable and with no Go compiler available at runtime. This tests the promised deployment contract, not source-file absence. |

Preserve both the legacy exclusion/inclusion-array and canonical-filter variants in the real-Codex credential smoke test. Use synthetic keys, a fresh `CODEX_HOME`, and a loopback server implemented with `net/http`. Port the environment probe to a small Go executable; do not retain a hidden Node dependency in the verification path.

The controlled mock-provider probe may use `danger-full-access` solely to execute the generated probe without provisioning OS sandboxes. Keep that confined to tests and explicitly state that it does not validate the Codex sandbox. Production settings must not inherit that testing choice.

Use internal test seams for HTTP, filesystem-failure points, and fake child execution. Do not ship URL/hash bypass flags, synthetic-credential modes, or arbitrary executable overrides in the public CLI to make tests easier. Existing native copies may be provisioned into the exact candidate layout for early-stage tests; this is development setup, not a supported unverified installer.

### CI and release checks

- Run formatting checks, `go vet ./...`, and `go test ./...` using the pinned toolchain. Run the race detector on a supported native CI target for the lock/write behavior; its build prerequisites are separate from the shipped `CGO_ENABLED=0` executable.
- Before candidate upload, verify checksums and run the real-Codex HTTPS installation/loopback contracts using the executable extracted from the generated archive, without rebuilding it. The configured candidate job runs on macOS ARM64; Check runs race tests on Linux and macOS.
- Build all candidate targets and run native integration tests on the actual available target runners. Record missing runtime coverage instead of silently passing skipped platform checks.
- Verify the shipped executable's Go module/build metadata and that production imports introduce no external modules. Build ordinary code/tests with module download disabled once the toolchain is provisioned.
- Run a pinned Go vulnerability scanner against source/builds, including the standard library. Separately review Codex advisories and the bundled native resources; a clean Go scan does not cover those.
- Keep GitHub Actions pinned by commit. Replace the Node-version matrix with the tested Go/platform matrix. Hosted CI actions may themselves use Node; this does not make Node a user installation/runtime dependency, but they remain part of the build supply chain.
- Publish `codex-openrouter_<version>_<os>_<arch>.tar.gz` for Unix and `.zip` for Windows, containing the launcher, installation instructions, and applicable wrapper notices. The installer obtains Codex separately from its embedded manifest.
- Publish checksums, source commit, exact Go version, target, artifact-lock digest, and available build provenance. Checksum the final artifacts after any signing step. Do not publish from dirty or unreviewed source, or overwrite an existing release identity.
- A release job must build/package candidate artifacts without publication during normal validation. Publishing is a separate explicit release action, not a side effect of testing or drafting this specification.
- Publication requires passing hosted/native checks and a retained terminal report tied to the final archive/binary SHA-256, launcher BuildID and OS/architecture. With isolated HOME/ZDOTDIR/CODEX_HOME and a synthetic key/loopback provider, record real TUI startup, Ctrl-C exit zero and termination by SIGTERM (signal 15), without timeout kills. Future publishing automation must require this report rather than infer it from candidate-job success. See [the release procedure](releasing.md).

## 11. Milestones and exit gates

Work proceeds in order. A milestone is complete only when its stated evidence exists. Do not delete the working Node implementation to manufacture progress.

### M0 — Resolve native distribution and platform facts

**Deliverables:** the real artifact manifest, exact Go toolchain pin, and a short evidence record of native resource discovery and supported target minimums.

Tasks:

- Fetch and inspect the exact selected upstream artifacts through a reviewed source; determine whether they include all needed helpers/resources and a standard-library-compatible format.
- Record hashes, lengths, file inventory, redirect hosts, upstream provenance, and notices.
- Establish the correct managed-package/resource environment for direct native execution.
- Run the pinned native executable plus a resource-using tool on the primary development platform with Node unavailable.
- Select the initial release's verified targets separately from cross-compile targets.

**Exit gate:** direct Node-free native execution is demonstrated, the selected files are fully specified, and no unresolved artifact-format/resource question is being hidden behind the word “binary.” If this fails, stop the implementation at this milestone and revise the distribution choice; do not add npm fallback.

### M1 — Port configuration, command construction, and auth

**Deliverables:** Go module, small CLI entry, settings handling, provider argument construction, internal auth mode, and focused behavior tests.

**Exit gate:** the current defaults/settings contract, malicious/invalid settings behavior, literal argument handling, command-position update interception, file-write safety, and key-free modes work. Existing user settings require no migration. No third-party Go packages have been added.

### M2 — Prove the runnable Go launcher

**Deliverables:** a candidate executable on the primary host, direct managed native launch, release-local auth, and the ported real-Codex loopback smoke test.

**Exit gate:** authenticated mock-provider requests succeed; both environment-policy variants retain the existing credential protections; packaged tools work; version/help, exit status, terminal startup, and termination are checked. The Go candidate works without Node/npm at runtime. The existing Node installation still works independently.

**Measured on 2026-09-30 (darwin/arm64):** `internal/integration/TestNativeLoopback` builds a CGO-disabled local test candidate with an identity derived from the actual source/build inputs, verifies every file in the pinned 42-file native inventory before copying, and provisions the shared release layout with all eight notices, the exact audit manifest, and completion metadata written last. The test runs real Codex against a bounded loopback Responses mock with only synthetic credentials. The release-local helper path includes spaces, Unicode, both quote characters, a backslash, and DEL; successful Authorization headers verify that native command auth accepts the generated TOML.

Version/help work without a key. A model-only settings update preserves `medium` reasoning, and both legacy `exclude`/`include_only` and canonical `filters` sessions use `vendor/session-override` while leaving settings and user TOML bytes unchanged. The native Go probe's tool output confirms empty/absent KEY, SECRET, and TOKEN variables, preserved sentinel/inherited values, preserved exclusions, and no Node/npm/Go in its runtime PATH. Snapshot checks during requests and after completion find no synthetic credentials. A separate negative-control source copy with only the injected `ignore_default_excludes=false` removed fails both native environment-policy variants, demonstrating regression sensitivity. Bundled ripgrep performs a content search and bundled zsh executes with `-f`. The controlled `danger-full-access` session executes only the probe; it provides no OS-sandbox evidence and uses no paid inference. The existing installed Node launcher independently reports Codex 0.155.1 with a scrubbed, credential-free environment.

The staged Go 1.27.1 toolchain runs formatting, vet, ordinary tests, and race checks with `GOTOOLCHAIN=local GOPROXY=off` and scratch caches. Ordinary tests explicitly skip the native integration when `CODEX_OPENROUTER_TEST_NATIVE` is absent; the fixture-enabled integration has actually passed. A supplied fixture on an unsupported host fails. The local mock needs permission to bind localhost in this workspace's test environment. All six target candidate builds compile; only darwin/arm64 has runtime evidence and a published native inventory.

The Unix process tests exercise production `execNative` and verify argument/environment boundaries, working directory, unchanged PID, redirected stdin/stdout/stderr, exit 42, and SIGTERM. Separate PTY checks reach the actual Codex 0.155.1 TUI with `vendor/changed` and `medium` in the caller's directory: one Ctrl-C exits zero, and SIGTERM exits by signal 15, without timeout kills. Review repairs also protect standalone settings help, unsupported-host diagnostics, trusted linked parents, and canonical test trust paths under macOS's `/var` alias. Subsequent installer evidence is recorded below.

**Timebox checkpoint:** M0–M2 are the intended one-to-two-hour experiment, not a guaranteed duration. If they pass, the rewrite's core value is demonstrated. If they do not, report the specific unfinished gate and revised effort. Do not describe M2 as a finished replacement release or keep expanding scope without reassessing its value.

### M3 — Implement verified installation and legacy transition

**Deliverables:** the managed Go installer, reviewed download/extraction logic, immutable release layout, platform replacement operations, legacy detection, and recovery documentation.

**Measured on 2026-09-30 (darwin/arm64):** the `--install` candidate obtains the exact embedded package URL over verified HTTPS, verifies compressed length/SHA-256 before extraction, checks the complete native inventory and gzip footer with a total decompression ceiling, probes native version/help without credentials, and publishes a complete immutable release before replacing the public command. The macOS 15 minimum, unsupported target, and unstamped-build checks precede prefix initialization. No settings file is created by a fresh installation, and a repeat install preserves saved settings and ordinary Codex TOML byte-for-byte. Configuration commands and normal launch perform no installer OS-version query or network request.

The fixture-enabled integration with `CODEX_OPENROUTER_TEST_INSTALL=1` performs an actual HTTPS install into a disposable prefix and runs both shell-policy variants through `bin/codex-openrouter`; it does not supply a production URL/hash/path override. An independent check confirms all 42 native files (317,010,263 bytes), the exact native directories, eight embedded notices, launcher/helper checksums, completion identity, audit manifest, private permissions, and no extra native entries. Separate installed-command and PTY checks pass: native version/help/config modes work with `PATH=/usr/bin:/bin`, Ctrl-C exits zero, and SIGTERM terminates by signal 15 without timeout kills. Only synthetic credentials and isolated HOME/ZDOTDIR/CODEX_HOME were used.

Focused synthetic tests exercise real filesystem transitions for first/repeat installs, upgrade/downgrade, earlier artifact identities, exact legacy npm links, unrelated-entry refusal, failed/partial downloads, native probe failures and bounded output, cancellation with a complete inactive release, publication denied by directory permissions, replacement failure/ambiguous success, cleanup after activation, and explicit move-aside repair. The old npm package tree and prior releases remain intact. Recognizing public bytes identical to the trusted downloaded candidate allows repair after its damaged release inventory is moved aside; unrelated bytes remain refused. Formatting, vet, ordinary/race tests, and six candidate cross-builds pass. Windows migration/replacement/ACL behavior and all other native targets remain withheld. M3 is verified for the inventoried platform.

**Exit gate:** fresh install, repeat install, upgrade, explicitly chosen downgrade, and legacy migration work in disposable prefixes. Wrong hashes, malformed archives, interrupted downloads, simultaneous installs, failed probes, and activation failures leave the documented state. Existing defaults and ordinary Codex files remain untouched. A post-activation cleanup failure is distinguishable from a pre-activation failure.

This is the main additional engineering risk beyond the basic port. It must not be claimed complete solely because a successful install was demonstrated once.

### M4 — Verify supported platforms and prepare release artifacts

**Deliverables:** Go CI, native platform evidence, candidate archives/checksums/provenance, and an explicit support matrix.

The [candidate build and maintenance procedure](releasing.md) describes exact-commit source-archive builds, local artifact provenance, configured CI and withheld targets. Only darwin-arm64 receives an installer archive; six-target compile checks do not widen support. Hosted CI has not run in this workspace.

**Exit gate:** every platform labeled supported passes installation, relevant regression/security checks, real-Codex integration, and terminal checks. Windows executable replacement and npm-shim selection have actual Windows evidence. Unexecuted architecture variants are labeled experimental or withheld. A user can run the downloaded release without installing Node, npm, or Go.

### M5 — Cut over documentation and remove the obsolete implementation

**Deliverables:** new README installation/update/recovery instructions, refreshed SECURITY.md, release notes listing deliberate interface changes, and removal of obsolete Node implementation/build files.

Tasks:

- Preserve existing user-authored README content and update only what the new implementation changes. State the dependency claim accurately: the launcher uses Go's standard library and runs a separate native Codex distribution.
- Record the actual validation date, wrapper/Codex/toolchain versions, platform evidence, and remaining security boundaries. Do not carry forward old test claims as evidence for new binaries.
- Remove `package.json`, `package-lock.json`, `.mjs` production/tests, the old smoke script, and the Node installer only after their required behavior is covered by the Go release candidate.
- Before deleting baseline source files, record immutable `git show COMMIT:PATH` references so the specification's evidence remains accessible without inventing a hosted repository URL.
- Update CI/dependency monitoring for the Go toolchain, GitHub Actions, and the custom Codex artifact manifest. A plain JSON pin will not receive npm Dependabot updates automatically; document the maintainer's review/update procedure.
- Keep credential-store work deferred and keep normal Codex configuration unaffected.

**Exit gate:** a clean checkout builds/tests using the documented Go workflow; a fresh user follows the README successfully; existing users have a tested migration and recovery path; every support/security claim has corresponding evidence. Only then is the Node implementation retired.

## 12. Completion and maintenance

The migration is complete when M0–M5 pass for the declared supported targets. Building a Go executable, passing unit tests, or getting one model response alone is not completion.

For each later release:

- Review Go security releases and rebuild when relevant runtime/standard-library fixes land. A statically included library is not automatically patched by updating the host's Go installation.
- Review the Codex pin and the complete native bundle. Update artifact hashes/inventories and repeat the resource/auth/environment/updater checks when changing it.
- Maintain the small CLI recognizer against upstream option changes and keep the helper protocol backward compatible for sessions using retained releases.
- Publish new immutable artifacts and clear update instructions. Do not add background network checks merely to compensate for a missing maintenance process.

The decision to proceed should be revisited at M2 using observed effort. A small, well-structured Go wrapper is a reasonable maintenance preference. A large custom installer ecosystem is outside this proposal, and a deadline must not justify quietly dropping existing security protections.
