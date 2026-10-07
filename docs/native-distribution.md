# Native Codex distribution

This document records how the launcher obtains and lays out the upstream
Codex native distribution. Values are
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

The core `bin/codex` Mach-O minimum of 11.0 is what the binary declares, not
a verified support floor for the full package.

## Other targets

All six targets use the same `codex-package-<rust-target>.tar.gz` family from
the pinned release. `scripts/codex_package/layout.py` in the Codex source
decides the contents per target. Each archive's size and SHA-256 match the
GitHub release asset digest, and every file in it is pinned in the manifest.

| Target | Rust target | Files | Extracted bytes | Differences from darwin/arm64 |
| --- | --- | --- | --- | --- |
| darwin/arm64 | `aarch64-apple-darwin` | 42 | 317,010,263 | — |
| darwin/amd64 | `x86_64-apple-darwin` | 42 | 337,806,099 | Same paths and execute bits |
| linux/amd64 | `x86_64-unknown-linux-musl` | 44 | 370,481,745 | `codex-resources/bwrap`; voice libraries are `.so` files |
| linux/arm64 | `aarch64-unknown-linux-musl` | 44 | 327,519,799 | Same as linux/amd64 |
| windows/amd64 | `x86_64-pc-windows-msvc` | 6 | 407,517,591 | `.exe` names; no zsh or voice; adds `codex-command-runner.exe` and `codex-windows-sandbox-setup.exe` |
| windows/arm64 | `aarch64-pc-windows-msvc` | 6 | 351,330,200 | Same as windows/amd64 |

Notes:

- Linux `bwrap` is bubblewrap 0.11.2, built from
  `codex-rs/vendor/bubblewrap` in the pinned Codex source. Its LGPL notice is
  retained with the other notices.
- Intel Macs use the same macOS 15.0 floor as Apple Silicon (the bundled zsh
  declares it). Linux packages are static musl builds, and Windows has no
  manifest minimum, so the installer does not check an OS version there.
- Windows cannot replace a running executable. If a Codex session uses the
  public command during an update, the installer reports an error; close the
  session and run the installer again.
- CI runs the native install test on all six targets. On Windows, the test
  installs, reinstalls and starts Codex, but skips the mock-provider session,
  because that session runs a POSIX shell command.
