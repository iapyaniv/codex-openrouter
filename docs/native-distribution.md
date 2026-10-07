# Native Codex distribution

This document records how the launcher obtains and lays out the upstream
Codex native distribution, and what has actually been validated. Values are
marked **measured** (run on the development host, macOS 26.5.1 arm64),
**source-derived** (read from upstream code/docs at the pinned tag), or
**unverified** (metadata inventoried but not executed). The machine-readable
manifest is [../internal/distribution/codex-artifacts.json](../internal/distribution/codex-artifacts.json); it holds the
typed values the launcher embeds. This document explains the decisions and
the evidence behind them.

Pinned upstream release:
[`rust-v0.155.1`](https://github.com/openai/codex/releases/tag/rust-v0.155.1),
Codex 0.155.1, published 2026-09-18T20:03:04Z.

## Selected distribution

Upstream publishes several artifact families per target. The relevant ones:

- `codex-<target>.tar.gz/.zst` — the CLI binary plus
  `bin/codex-code-mode-host` only (no bundled ripgrep, zsh, or voice
  runtime). Insufficient alone.
- `codex-package-<target>.tar.gz` — the full package layout used by the
  official standalone installer: `bin/codex`, `bin/codex-code-mode-host`,
  `codex-path/rg`, `codex-resources/zsh/bin/zsh`, `codex-resources/voice/*`,
  and `codex-package.json`. **This is the selected artifact family.**
- `.dmg` (macOS app installer) and Python wheels — different packaging; the
  wheels ship a reduced layout and are not used.
- `codex-app-server-*` and `codex-provisioned-package-*` — the optional app
  server and Desktop-app provisioning bundles. Not required by the launcher's
  CLI use; excluded.

Rationale: the launcher contract requires bundled helpers/resources
(ripgrep, shell resources, code-mode host), and
`codex-package-<target>.tar.gz` is the smallest upstream artifact that
contains all of them in a layout the binary discovers without
package-manager glue. Format is tar+gzip, decodable with Go's `archive/tar`
+ `compress/gzip` from the standard library; no `.zst`, external extractor,
or third-party decompression library is needed.

## Provenance and integrity (measured)

- Release asset URLs, byte lengths, and SHA-256 digests come from the GitHub
  release asset metadata and the `codex-package_SHA256SUMS` asset (its own
  sha256
  `e7c267fa8cda3fb783be8380ef9c2b660ceea740426bbae17ac1df8cd1012937`).
- `codex-package-aarch64-apple-darwin.tar.gz` was downloaded and re-hashed:
  122,962,085 bytes, sha256
  `e6e08717da9e35b72332eff753527fe79a9ae876081033c5c6820a8e5f58b943` —
  matches.
- Redirect behavior: `github.com/.../releases/download/...` redirects to
  `release-assets.githubusercontent.com` (previously
  `objects.githubusercontent.com`); both are recorded as approved redirect
  hosts in the manifest.
- Upstream also publishes `.sigstore` bundles, but only for the linux-musl
  archives; this manifest does not consume them.

These checks bind the reviewed bytes to the manifest. They do not prove
upstream code is safe; the trust anchor remains the release publisher.

### Code signature (measured)

The darwin executables are Apple-signed by Team `2DC432GLL2` (OpenAI), and
`codesign --verify --deep --strict bin/codex` run outside the sandbox
succeeds (exit 0): the signature is valid. Gatekeeper/browser first-run
acceptance of the downloaded launcher and its payload is unverified and
remains a later platform check. The installer must not strip, mutate, or
re-sign upstream files.

## Extracted layout and resource discovery (measured + source-derived)

The darwin/arm64 archive contains 42 regular files and 10 directories (52
entries total) — no symlinks, hardlinks, or device entries (verified from
the tar listing and after extraction), so the spec's symlink-materialization
rule is not needed for this artifact. Top level:

```text
bin/codex                  entrypoint (228,803,200 bytes)
bin/codex-code-mode-host   code-mode helper
codex-package.json         layoutVersion 1, version 0.155.1, entrypoint bin/codex
codex-path/rg              bundled ripgrep 15.2.0
codex-resources/zsh/bin/zsh
codex-resources/voice/     voice host, GStreamer/GLib dylibs, licenses, manifest
```

Discovery semantics (source-derived from
`codex-rs/install-context/src/lib.rs` at the pinned tag, then measured): the
running `codex` binary canonicalizes its own executable path, recognizes a
package layout when its parent directory is named `bin` and a
`codex-package.json` sits beside it, and then resolves `codex-path/rg`,
`codex-resources/zsh/bin/zsh`, the code-mode host, and voice resources from
those sibling directories. `codex` itself prepends `codex-path` to `PATH`
for the tool environments it spawns (`codex-rs/core/src/tools/runtimes/mod.rs`,
`codex-rs/arg0/src/lib.rs`).

Consequences for the launcher/installer:

- Keep the extracted tree intact as one directory; do not cherry-pick the
  `codex` binary. `codex doctor` detects the package layout and resolves
  bundled search paths from it, but its installation check covers
  PATH/package-manager provenance only — not file inventory
  (`codex-rs/cli/src/doctor.rs` at the pinned source commit). Installation
  completeness instead rests on the verified 42-file inventory below and on
  the source inspection of the layout detection.
- Do **not** create the npm-style root-level `codex -> bin/codex` symlink:
  it is unnecessary (the launcher execs `bin/codex` by absolute path) and
  the layout detection keys off a real `bin/` directory.
- Environment: remove `CODEX_MANAGED_BY_NPM/BUN/PNPM/VITE_PLUS` and
  `CODEX_MANAGED_PACKAGE_ROOT` from the child environment and set no
  replacement. Under a direct package layout those variables only mislabel
  codex's install-context reporting and, per `codex-rs/cli/src/doctor.rs`,
  can produce a spurious "npm-managed launch is missing package-root
  provenance" finding. Resource discovery needs none of them.
- Linux targets additionally ship `codex-resources/bwrap` (per the upstream
  installer's completeness checks); the inventory for those targets is not
  pinned yet.

## Per-file inventory (measured, darwin/arm64)

All 42 extracted files were hashed (SHA-256) and sized; the full list with
executable flags is `targets[0].inventory` in
[../internal/distribution/codex-artifacts.json](../internal/distribution/codex-artifacts.json). Extracted total:
317,010,263 bytes across 42 files; install-time bounds are set to 64 files /
320 MiB (`extraction.maxFiles` / `extraction.maxBytes`). Independently, the
37 files listed in the bundle's own `codex-resources/voice/manifest.json`
sha256 table all match the extracted bytes (0 mismatches), covering the
voice runtime and its notices; the five files that manifest does not cover
(`bin/codex-code-mode-host`, `codex-package.json`, `codex-path/rg`,
`codex-resources/voice/manifest.json`, `codex-resources/zsh/bin/zsh`) are
pinned by our own inventory instead. The `bin/codex` hash in the voice
manifest also matches our extracted `bin/codex`.

Notices to retain: `codex-resources/voice/NOTICE.md`, `sources.json`, and
`licenses/` (LGPL-2.1 for GStreamer/GLib, plus libffi, PCRE2, Opus, zlib,
proxy-libintl, sljit). Codex itself is Apache-2.0.

Beyond the package-carried texts, the release materials embed the following
upstream notices from `internal/distribution/licenses/` (build inputs; no
runtime downloads), each pinned by bytes/SHA-256/source URL in the
manifest's `notices` section:

- Codex `LICENSE`/`NOTICE` (Apache-2.0), from the pinned source commit
  `be2951ea34f0d295ed0becf97079f92fa5f6950e`.
- ripgrep 15.2.0 `COPYING`, `LICENSE-MIT`, and `UNLICENSE` at commit
  `e89fff89ac9af12e8d4ce9d5fd07beb408ca730f`; the bundled `codex-path/rg`
  reports revision `e89fff89ac` (measured). The project is offered under
  MIT or Unlicense; all supplied texts are retained.
- Zsh `LICENCE` at commit `77045ef899e53b9598bebc5a41db93a548a40ca6`. The
  bundled zsh is the `codex-zsh` artifact from upstream release
  `rust-v0.134.0-alpha.3`, selected by `scripts/codex_package/codex-zsh` at
  the pinned source commit; its build workflow
  (`.github/workflows/rust-release-zsh.yml` at that release) pins
  `ZSH_COMMIT` to the commit above and applies
  `codex-rs/shell-escalation/patches/zsh-exec-wrapper.patch`. The bundled
  binary reports `zsh 5.9.0.3-test` (measured).
- Go `LICENSE` from the staged Go 1.27.1 toolchain (`go1.27.1`); the
  statically linked Go runtime in the launcher requires this notice in the
  release materials.

This records the exact included notices and their provenance. It is not a
license certification or legal audit, and it does not claim a complete
audit of every transitive native component.

## Preliminary Node-free execution evidence (measured, 2026-09-22)

All runs used `env -i` with only `HOME`, `PATH=/usr/bin:/bin`, and a
disposable `CODEX_HOME` under the scratch prefix — no Node, no
package-manager markers, no API credentials:

- `bin/codex --version` → `codex-cli 0.155.1`, exit 0.
- `bin/codex --help` → full usage, exit 0.
- `codex-path/rg --version` → `ripgrep 15.2.0 (rev e89fff89ac)`, and a
  content search on a sample file matched — the bundled tool executes.
- `codex doctor --json` → package layout detected and
  `runtime.search: ok - search command found (bundled)` resolving to the
  extracted `codex-path/rg`. This is layout-detection and bundled-path
  evidence, not an inventory audit: the doctor installation check covers
  PATH/package-manager provenance only. Remaining findings were the
  expected no-credentials/no-network/`TERM=dumb` ones from the scrubbed
  environment, not resource failures.
- `codex exec` with the wrapper's exact injected `-c` set
  (`model_provider`, inline provider table with `auth={command=...}`
  pointing at a stub helper, model, reasoning,
  `check_for_update_on_startup=false`, the two `shell_environment_policy`
  lines, `features.shell_snapshot=false`) was accepted by the real binary:
  the session started, the config parsed, and it proceeded to contact the
  (deliberately unreachable) loopback provider. This establishes that the
  injected configuration is parsed and a connection is attempted. It does
  **not** by itself prove the command-backed auth helper was invoked or that
  authentication succeeded. The completed 2026-09-30 installed-command checks
  below use an instrumented mock provider and verify authentication. No real
  provider traffic or credentials were used in these preliminary runs.

`--version`/`--help` alone do not exercise bundled resources; the `rg`,
doctor, and exec checks above are what is claimed. M0 did not validate
interactive TUI behavior or Ctrl-C/signal handling; the subsequent Go
candidate evidence below covers those checks. Voice, code-mode-host
execution, and the zsh-exec bridge remain unverified.

## Platform minimums

Measured from Mach-O load commands (`vtool`) of the shipped darwin/arm64
executables:

| File | Minimum |
| --- | --- |
| `bin/codex` | macOS 11.0 |
| `bin/codex-code-mode-host` | macOS 11.0 |
| `codex-path/rg` | macOS 11.0 |
| `codex-resources/voice/bin/codex-voice-host` | macOS 14.0 |
| `codex-resources/zsh/bin/zsh` | macOS 15.0 |

The **proposed initial full-package floor is macOS 15.0**. This is a
source-derived floor, not a runtime-observed one: the bundled
`codex-resources/zsh/bin/zsh` declares a macOS 15.0 minimum (measured), and
the pinned Go 1.27 toolchain itself requires macOS 13 or later per the
[Go 1.27 release notes](https://go.dev/doc/go1.27) (linker section), so the
Go launcher cannot claim macOS 11 compatibility. The most restrictive
component is the bundled zsh at 15.0, which sets the full-package floor.

Runtime evidence so far is limited to the development host, macOS 26.5.1
arm64 — the binaries were executed there and nowhere older. The core
`bin/codex` Mach-O minimum of 11.0 is what the binary declares, not a
verified support floor for the full package. Linux (musl static builds) and
Windows minimums are **unverified**: no host was available to run or inspect
them. Do not infer them from Go's supported-platform list.

## Go toolchain pin

- Selected **Go 1.27.1** (`go1.27.1`, the latest stable patch release listed
  by the official download metadata on 2026-09-22). The host's preinstalled
  Go 1.24.5 was deliberately not used: it is no longer the supported line
  and was not chosen as the baseline.
- Download: `go1.27.1.darwin-arm64.tar.gz`, 68,100,347 bytes, sha256
  `ee215d57e0ec269c60cc9ceca68e6bda321ba9ee5afe24f4b0988703c2d87d12`,
  taken from `https://go.dev/dl/?mode=json&include=all` and re-hashed after
  download — match.
- Staged at `toolchain/go` under the scratch prefix (not in the repository,
  not in Homebrew, no global state changed). `.go-version` records `1.27.1`.
- Hygiene check: a trivial scratch module builds with `GOTOOLCHAIN=local
  GOPROXY=off` and a scratch `GOCACHE`. This proves the staged toolchain
  itself compiles offline and cannot silently switch toolchains. CI must set
  the same variables.
- Repository evidence (measured 2026-09-22, M1): with `GOTOOLCHAIN=local
  GOPROXY=off` and scratch `GOCACHE`/`GOMODCACHE`, the staged toolchain ran
  `go build ./...`, `go vet ./...`, `go test ./...`, and `go test -race
  ./...` offline with no module downloads — the module is standard-library
  only. On 2026-09-30 the M2 candidate also passed vet, ordinary and race
  tests, and real-Codex loopback integration against a fully verified test
  release layout. The test builds with `CGO_ENABLED=0`, `-trimpath`, and
  `-buildvcs=false`, stamps the actual source/build-input digest, and checks
  that Node/npm/Go are absent from the native tool environment. The subsequent
  installer evidence below covers disposable installation only; no
  replacement release has been published.

## Go candidate launch evidence (measured, 2026-09-30)

The integration test in `internal/integration/` copies all 42 verified native
files into the launcher's shared immutable release layout, retains all eight
embedded notices and the exact audit manifest, and writes completion metadata
last. It uses isolated HOME/ZDOTDIR/CODEX_HOME and synthetic credentials. Real
Codex authenticates through the immutable Go helper from a prefix containing
spaces, Unicode, quotes, a backslash, and DEL. Both legacy and canonical shell
filters preserve the existing credential isolation and settings contracts;
bundled ripgrep searches a sample file and bundled zsh executes with `-f`.
The test's controlled probe runs with `danger-full-access`; no OS-sandbox,
voice, or code-mode-host execution is claimed.

Separate terminal checks reach the native Codex TUI with the selected saved
model/reasoning and the correct working directory. A single Ctrl-C exits zero
and SIGTERM terminates by signal 15, with no timeout kills. Unix subprocess
regressions verify exact PID preservation, argv/environment/cwd, redirected
streams, and native exit 42. Six candidate target builds compile; this adds no
runtime evidence for the other architectures or operating systems. Regression
checks cover linked trusted parents and the canonical macOS temporary
directory used in integration trust configuration.

## Go installer evidence (measured, 2026-09-30)

The candidate's `--install` downloads the pinned full package over actual
verified HTTPS into a disposable prefix, verifies compressed bytes before
extraction, enforces total decompression and exact inventory limits, and runs
bounded credential-free native version/help probes. It installs all notices
and the exact audit manifest, writes completion metadata last, then publishes
the release and activates a regular public executable. An independent check
confirms all 42 native file lengths/hashes, exact native directories, eight
notices, helper/public digest and completion identity, and private permissions.

The installed public command passes real-Codex loopback integration for both
shell-policy formats. Fresh installation leaves settings absent; repeat
installation preserves saved defaults and ordinary user TOML. Installed-command
version/help/config checks and separate Ctrl-C/SIGTERM terminal checks pass
without timeout kills. The tests use synthetic credentials and private
HOME/ZDOTDIR/CODEX_HOME; the user's prefix and shell profiles remain untouched.

Only darwin/arm64 is enabled for installation, with the manifest's macOS 15
minimum checked before prefix initialization. Other platforms, native Windows
migration/ACL/replacement behavior remain
open. The initial installer and [local candidate packaging](releasing.md) are
verified; hosted CI has not run. The Go source replaces the historical Node
implementation, whose installed package tree remains available for recovery.
Build and verify each final artifact from its exact clean source before use.

## Other targets (unverified, not installable)

Only `darwin-arm64` is fully inventoried and installable in the manifest.
The targets below have URL/byte-length/SHA-256 pinned from official release
metadata (release asset digests and `codex-package_SHA256SUMS`, fetched
2026-09-22), but they were **not** executed and their per-file inventories
are **not** pinned. They are recorded here for planning only and are **not
installable** from the runtime manifest. Do not treat this metadata as a
runtime or terminal-support claim.

| Target | Rust target | Archive bytes | SHA-256 |
| --- | --- | --- | --- |
| darwin/amd64 | `x86_64-apple-darwin` | 133,128,625 | `be752aebb2ac022c5bfed3fa14f46943d11ddb36a950b553b058794aba22496a` |
| linux/amd64 | `x86_64-unknown-linux-musl` | 138,838,055 | `a65b895c6ac1a73629bbe4b864640c86133e94a43b4d67b3103044e1a306d5a2` |
| linux/arm64 | `aarch64-unknown-linux-musl` | 130,233,502 | `71857dbc9bea3613410e8a69cfb46b07c0402d6d20fec18843dbaffd757634bd` |
| windows/amd64 | `x86_64-pc-windows-msvc` | 139,547,261 | `f45c273b7835c192aaa9cef5b93aa9528966ac7301444632de80a565a9bf14e8` |
| windows/arm64 | `aarch64-pc-windows-msvc` | 129,273,387 | `f53deb24650d288fddfd1724ef952f6ad9cb6119c75ecc12a6223b98fd97719a` |

Notes:

- darwin/amd64 was downloaded and re-hashed (matches) and its extracted
  layout confirmed to contain the same 42 files and `codex-package.json`
  (layoutVersion 1); its core binaries declare a macOS 10.12 Mach-O minimum
  (`LC_VERSION_MIN_MACOSX`), voice host 14.0, zsh 15.0. It was not executed
  (no x86_64 macOS host or Rosetta available) and its per-file inventory is
  not pinned.
- linux targets are musl static builds and are expected to ship
  `codex-resources/bwrap`; layout unconfirmed. Upstream publishes `.sigstore`
  bundles for these two archives only.
- windows layout and minimums are unconfirmed.

## Platform verification and release gates

Launcher-level runtime gates that remain open, by platform:

- **Windows.** Withheld from the initial release. Cross-compilation of the
  application and test packages is checked, but cross-compilation is not
  execution. Open items with no Windows runtime evidence yet: `os.Rename`
  (MoveFileExW with MOVEFILE_REPLACE_EXISTING) cannot replace a running
  executable image or a file held open without delete sharing, and its
  behavior on ambiguous failures is unverified; `os.Chmod` is not a
  directory ACL, so the replace-failure test cannot force an error there
  and native failure evidence remains pending; the directory checks reject
  reparse points but are not a full Windows ACL audit. The native runtime
  gate in the specification stays open until these cases are exercised on
  hardware.
  A private user-controlled prefix is a prerequisite for any Windows
  installation, and the manifest publishes no Windows target. Native Windows
  support requires resolving the privacy (ACL audit) and
  replacement/console gates on hardware before publication.
- **darwin/amd64, linux, windows archives.** Unverified and not installable
  as recorded above; per-file inventories unpinned except darwin/arm64.
