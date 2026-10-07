# Candidate builds and maintenance

The initial installer target is **darwin/arm64, macOS 15 or later**. Installation,
real-Codex authentication/environment sessions, bundled tools and terminal
termination have been exercised on macOS 26.5.1 ARM64. The macOS 15 floor comes
from the bundle's Mach-O requirements; it has not been runtime-tested on macOS
15. Darwin amd64, Linux amd64/arm64 and Windows amd64/arm64 remain withheld.
Compiling their application and tests does not establish runtime support.

## Build a local candidate

Local packaging requires Apple Silicon macOS, Git and the exact Go version in
[.go-version](../.go-version). The packager executes each built launcher to
verify its identity; other hosts are refused. Trust the compiler, Git and the
selected committed code before executing it. Self-verification cannot
authenticate code already executing: an ignored package-main file can run
`init` before any packager checks, even with clean Git status.

Use the following bootstrap instead of running Go in an existing working tree.
Choose the trusted full commit explicitly, an absolute verified compiler path,
and a new output directory outside both the original source and bootstrap.
The private bootstrap root must also be outside the original source; this
macOS example creates it under `/private/tmp`.

```sh
SOURCE_REPOSITORY=/absolute/path/to/source-repository
SOURCE_COMMIT=FULL_TRUSTED_COMMIT_HASH
GO_BINARY=/absolute/path/to/verified-go-1.27.1/bin/go
CANDIDATE_OUT=/absolute/path/outside-source/candidate
BOOTSTRAP_ROOT=$(/usr/bin/mktemp -d /private/tmp/codex-openrouter-build.XXXXXX) && \
/usr/bin/env -i PATH=/usr/bin:/bin HOME="$BOOTSTRAP_ROOT/home" TMPDIR="$BOOTSTRAP_ROOT" \
  GOTOOLCHAIN=local GOPROXY=off GOENV=off GOWORK=off GOFLAGS= GOEXPERIMENT= CGO_ENABLED=0 \
  GOCACHE="$BOOTSTRAP_ROOT/go-cache" GOMODCACHE="$BOOTSTRAP_ROOT/go-mod-cache" \
  GIT_CONFIG_NOSYSTEM=1 GIT_CONFIG_GLOBAL=/dev/null GIT_ATTR_NOSYSTEM=1 GIT_NO_REPLACE_OBJECTS=1 \
  SOURCE_REPOSITORY="$SOURCE_REPOSITORY" SOURCE_COMMIT="$SOURCE_COMMIT" \
  GO_BINARY="$GO_BINARY" CANDIDATE_OUT="$CANDIDATE_OUT" CANDIDATE_VERSION=0.2.0-local \
  /bin/sh -eu -c '
    umask 077
    case "$SOURCE_COMMIT" in ""|*[!0-9a-f]*) exit 1;; esac
    test "${#SOURCE_COMMIT}" -eq 40 || test "${#SOURCE_COMMIT}" -eq 64
    for path in "$SOURCE_REPOSITORY" "$GO_BINARY" "$CANDIDATE_OUT"; do
      case "$path" in /*) ;; *) exit 1;; esac
    done
    source_root=$(cd "$SOURCE_REPOSITORY" && pwd -P)
    bootstrap_root=$(cd "$TMPDIR" && pwd -P)
    candidate_parent=$(cd "$(/usr/bin/dirname "$CANDIDATE_OUT")" && pwd -P)
    while :; do
      if test "$candidate_parent" -ef "$source_root" || test "$candidate_parent" -ef "$bootstrap_root"; then
        printf "%s\n" "candidate output must be outside original source and bootstrap" >&2
        exit 1
      fi
      test "$candidate_parent" = / && break
      candidate_parent=$(/usr/bin/dirname "$candidate_parent")
    done
    mkdir -m 700 "$HOME"
    git() { /usr/bin/git -c core.hooksPath=/dev/null -c core.fsmonitor=false -c core.attributesFile=/dev/null -c core.autocrlf=false "$@"; }
    git clone --quiet --no-local --no-checkout --template= -- "$SOURCE_REPOSITORY" "$TMPDIR/source"
    git -C "$TMPDIR/source" checkout --quiet --detach "$SOURCE_COMMIT"
    test "$(git -C "$TMPDIR/source" rev-parse HEAD)" = "$SOURCE_COMMIT"
    cd "$TMPDIR/source"
    "$GO_BINARY" run ./cmd/package-candidate --version "$CANDIDATE_VERSION" \
      --source-commit "$SOURCE_COMMIT" --out "$CANDIDATE_OUT"
  '
```

The fresh checkout contains no original ignored files, hooks, local config or
`info/attributes`; system/global Git config and attributes are disabled.
Committed attributes remain part of the trusted source. Keep the bootstrap
until validation finishes, then remove only that disposable directory.

The output parent must already exist; bootstrap preflight resolves it and
rejects containment in either excluded root, including symlink/case aliases.
The output directory must not exist. The packager checks its Go and embedded
inputs against the requested commit, including files hidden by Git ignore
rules. Candidate compilation uses only a private extraction of `git archive`
from a fresh bare object store. Only committed attributes apply; source-local
archive config and system/global attributes are excluded. No inherited Go
flags/workspace or automatic VCS stamp apply. Its immutable
build identity binds the commit, actual source archive digest, version, target,
compiler, recipe and complete embedded artifact-manifest digest. Two builds
use separate source directories and fresh caches and must produce identical
executable bytes.

Outputs include the darwin-arm64 launcher archive, exact exported source
archive, a standalone copy of the launcher, `candidate.json` and `SHA256SUMS`.
The launcher archive contains `INSTALL.md`, `BUILD.json` and all eight retained
notices. Packaging refuses an archived root `LICENSE` that differs from the
retained wrapper notice. Go build metadata is checked for compiler, module/dependency boundary,
target, CGO and trimpath settings. Go omits linker flags from that metadata
with trimpath enabled; the JSON build record includes the controlled invocation.
Each executable must also report the exact expected launcher identity and
pinned Codex version in a bounded, credential-free `--launcher-version` probe
before any candidate artifacts are written.

These are local, unsigned candidates. A local commit is provenance, not a
hosted tag, publisher signature or build attestation. No publisher code-signing
or notarization credentials are configured. The upstream Codex executable's
code signature was checked separately; it does not sign this launcher. Do not claim that
local execution establishes how downloaded software passes macOS policy.
Rebuild from the final clean source after any source or documentation change;
an earlier artifact's provenance cannot describe a later checkout.

## Validation and CI

With the pinned compiler and `GOTOOLCHAIN=local GOPROXY=off`, run formatting,
`go vet ./...`, `go test ./...` and native `go test -race ./...`. `go list -m all`
must contain only `codex-openrouter`; application, developer tools and ordinary
tests have no external Go imports. Race-detector prerequisites are build-time
requirements, separate from the CGO-disabled shipped executable.

The explicit native gate is:

```sh
CODEX_OPENROUTER_TEST_INSTALL=1 GOTOOLCHAIN=local GOPROXY=off \
  go test -count=1 -v ./internal/integration
```

This downloads the embedded upstream HTTPS artifact into a disposable prefix,
runs both synthetic loopback-provider policies through the installed public
command, and checks resource tools and settings persistence. It needs network
and localhost-listener access. Ordinary tests explain their native-test skip;
an explicitly requested native gate on an unsupported architecture fails.
Terminal Ctrl-C/SIGTERM checks remain part of supported-platform validation.

To validate a packaged artifact, verify `SHA256SUMS`, extract the launcher
archive, then supply its executable with the adjacent `BUILD.json`:

```sh
CODEX_OPENROUTER_TEST_CANDIDATE=/absolute/path/extracted/codex-openrouter \
  GOTOOLCHAIN=local GOPROXY=off go test -count=1 -v ./internal/integration
```

This checks the supplied executable's hash and identity, installs that
executable and downloads its pinned Codex bundle over HTTPS, then runs the
same public-command contracts without rebuilding the launcher. The test still
builds its environment probe. Empty paths, missing or invalid metadata,
mismatched bytes and unsupported hosts fail explicitly.

[Check](../.github/workflows/test.yml) configures OS tests, Linux and macOS race checks,
six application/test cross-compiles and an explicit `macos-15` ARM64 install
gate. GitHub documents that label as an ARM64 runner; the job also asserts its
architecture. [GitHub runner reference](https://docs.github.com/en/actions/reference/runners/github-hosted-runners)
describes capacity, not evidence these repository jobs have run. The workflows
have been authored and checked locally; no hosted run is claimed.

[Candidate](../.github/workflows/release.yml) requires an explicit manual
dispatch/version and requires Check's ordinary suite and supported Mac ARM64
native installation gate at the same source commit before packaging/upload.
Its macOS ARM64 packaging job verifies checksums, extracts the generated
archive and runs the supplied-executable gate before scanning and uploading
test candidates for inspection. It does not publish a release. Actions use full commit pins, read-only content
permission and checkout without persisted credentials. The actions' internal
Node runtime is a CI dependency, not an installation or runtime requirement.
The job bootstraps the packager in a fresh detached checkout with the same
trust boundary as the local procedure.

Public releases are currently blocked by the [incomplete native dependency
and exception review](../SECURITY.md#native-gix-dependency-advisories). Resolve
that review with patches or substantiated applicability/risk dispositions.
Publishing a release also requires passing hosted/native checks and a manual
terminal report for the exact final artifact. Record the launcher
archive and binary SHA-256, launcher BuildID, OS version and architecture.
Install the extracted executable in a disposable prefix, use isolated
`HOME`, `ZDOTDIR` and `CODEX_HOME` with a synthetic key and loopback provider,
and record the real Codex TUI startup and termination: one Ctrl-C exits zero,
and SIGTERM terminates by signal 15. Neither result may come from a timeout
kill. Retain the terminal transcript and observed process/exit results with
the report. Repeat after any change to artifact bytes, including signing.
Future publishing automation must enforce this exact-artifact record and the
hosted/native checks; Candidate success alone cannot authorize publication.

The vulnerability scanner is a separately provisioned build tool:
`golang.org/x/vuln/cmd/govulncheck@v1.8.0`, installed with Go's public proxy and
checksum database. Its dependencies are separate from the application module.
Run its source scan and `-mode=binary` scan on the final launcher. Those checks
include Go's standard library; they do not audit the bundled Rust/C executables,
libraries or upstream resource code.

## Pin updates

- Review [Go releases](https://go.dev/dl/) and [security policy](https://go.dev/security)
  for patches. Update `.go-version` and `go.mod` together, verify the official
  toolchain archive, refresh the retained Go notice if needed, then rebuild and
  repeat source/binary scans. Updating a host compiler does not patch Go code
  already linked into a downloaded executable.
- Review [Codex releases](https://github.com/openai/codex/releases) and
  [advisories](https://github.com/openai/codex/security/advisories), the exact
  upstream tag's lockfile/dependency advisories, and bundled components.
  Record affected ranges, declared/enabled features, reachable calls and the
  disposition for each version match. Before public release, require a patch
  or a substantiated applicability/risk disposition; top-level Codex advisories
  and Go scans alone are insufficient. The [dated gix assessment](../SECURITY.md#native-gix-dependency-advisories)
  records this migration's targeted findings and unfinished native audit.
  Review upstream audit/deny exceptions independently rather than inheriting
  their ignored-advisory decisions. A pin change
  requires downloading the full package, verifying archive length/hash,
  inventorying every file/resource and notice, reviewing minimum OS/signatures,
  and updating `internal/distribution/codex-artifacts.json`. Repeat actual
  installation, auth/environment/resource and terminal gates. The custom JSON
  manifest is not automatically maintained by npm or Go Dependabot.
- Review weekly GitHub Actions Dependabot changes against upstream releases,
  retain full-SHA pins, and update the separately pinned scanner deliberately.
  A clean scanner result or one patched advisory is not a complete native audit.

Checksums must describe final artifacts after any future signing step. Publishing
requires a separate explicit release action; never replace different bytes under
an existing immutable release identity.
