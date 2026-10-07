# Releasing

A release has one archive for each target in `internal/distribution/codex-artifacts.json`: macOS (Apple Silicon and Intel), Linux (x64 and arm64) and Windows (x64 and arm64). Use a new version number for every release.

## Build a release

On an Apple Silicon Mac, from a clean checkout of the commit you want to ship. Any host that is one of the targets works, but the steps below assume Apple Silicon.

```sh
VERSION=0.2.0
OUT="$HOME/codex-openrouter-release-$VERSION"
git status --short    # must print nothing
go run ./cmd/package-candidate --version "$VERSION" \
  --source-commit "$(git rev-parse HEAD)" --out "$OUT"
```

The packager builds from `git archive` of that commit, not from your working tree. It cross-compiles every target twice with separate caches and fails if any two builds differ. It runs the host's binary to check its version stamp, and checks the build settings and stamp of the others. It takes about 8 minutes. `$OUT` must not exist yet and must be outside the repository.

`$OUT` then contains:

| File | Content |
| --- | --- |
| `codex-openrouter_VERSION_OS_ARCH.tar.gz` | macOS and Linux: the launcher, `INSTALL.md`, `BUILD.json` and the bundled licenses |
| `codex-openrouter_VERSION_windows_ARCH.zip` | Windows: the same, with `codex-openrouter.exe` |
| `codex-openrouter_VERSION_source.tar.gz` | The exact source that was built |
| `candidate.json` | Build inputs and hashes for every target |
| `SHA256SUMS` | Hashes of the files above |

The [Candidate workflow](../.github/workflows/release.yml) does the same steps on a GitHub `macos-15` runner. Start it from the Actions tab with a version number, then download the files from the run's artifact.

## Test the release

```sh
(cd "$OUT" && shasum -a 256 -c SHA256SUMS)
mkdir "$OUT/extracted" && tar -xzf "$OUT/codex-openrouter_${VERSION}_darwin_arm64.tar.gz" -C "$OUT/extracted"
CODEX_OPENROUTER_TEST_CANDIDATE="$OUT/extracted/codex-openrouter" go test -count=1 -v ./internal/integration
```

This installs the Apple Silicon release binary into a temporary directory, downloads the pinned Codex bundle, and runs real Codex against a local mock provider with a fake key. Before you publish, also start the installed `codex-openrouter` by hand and make sure that one Ctrl-C exits the session. The other targets are covered by the Check workflow's native install test on the same commit.

## Publish

```sh
git tag "v$VERSION" && git push origin "v$VERSION"
rm -rf "$OUT/extracted"
gh release create "v$VERSION" --title "v$VERSION" --generate-notes "$OUT"/*
```

Then update `VERSION` and `$Version` in the [README install steps](../README.md#install-on-macos-or-linux).

## Checks

- `go vet ./...` and `go test ./...` run the ordinary tests. Add `-short` to skip the packager test, which takes about two minutes.
- `CODEX_OPENROUTER_TEST_INSTALL=1 go test -count=1 -v ./internal/integration` builds the launcher, installs it over HTTPS into a temporary directory and runs it against real Codex. It needs network access and runs on any of the six targets.
- The [Check workflow](../.github/workflows/test.yml) runs the tests on Linux, macOS and Windows, cross-compiles six targets, and runs the native install test on all six targets.

## Update the Codex pin

1. Choose a Codex release. Check its [security advisories](https://github.com/openai/codex/security/advisories) and the advisories for the crates in its `codex-rs/Cargo.lock`.
2. For each target, download `codex-package-<rust-target>.tar.gz` from the release. Check its size and SHA-256 against the release asset digest, and record the size, SHA-256 and execute bit of every extracted file.
3. Update `internal/distribution/codex-artifacts.json` and the license files in `internal/distribution/licenses/`. Check `scripts/codex_package/layout.py` in the Codex source for new bundled tools; each one needs its license notice.
4. Check the minimum macOS version of each Mac binary (`vtool -show-build`). Update `osMinimum` if it changed.
5. Compare the Codex CLI options with the lists in `internal/launcher/launch.go`. `IsUpdateCommand` uses them to find a command-position `update`.
6. Run the native install test and the manual Ctrl-C check, then make a new release.

[native-distribution.md](native-distribution.md) explains the bundle layout and the manifest fields.

## Update Go

Change `.go-version` and the `go` line in `go.mod` together. Run the tests, then scan the source and the release binary:

```sh
go run golang.org/x/vuln/cmd/govulncheck@v1.8.0 ./...
go run golang.org/x/vuln/cmd/govulncheck@v1.8.0 -mode=binary "$OUT/extracted/codex-openrouter"
```

A binary that users already installed keeps the old Go runtime until they install a new release.
