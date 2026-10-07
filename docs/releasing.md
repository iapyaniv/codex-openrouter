# Releasing

Releases contain one Apple Silicon (darwin/arm64) binary. Use a new version number for every release.

## Build a release

On an Apple Silicon Mac, from a clean checkout of the commit you want to ship:

```sh
VERSION=0.2.0
OUT="$HOME/codex-openrouter-release-$VERSION"
git status --short    # must print nothing
go run ./cmd/package-candidate --version "$VERSION" \
  --source-commit "$(git rev-parse HEAD)" --out "$OUT"
```

The packager builds from `git archive` of that commit, not from your working tree. It builds the launcher twice with separate caches and fails if the two binaries differ. It also runs the binary to check its version stamp. `$OUT` must not exist yet and must be outside the repository.

`$OUT` then contains:

| File | Content |
| --- | --- |
| `codex-openrouter_VERSION_darwin_arm64.tar.gz` | The launcher, `INSTALL.md`, `BUILD.json` and the bundled licenses |
| `codex-openrouter_VERSION_source.tar.gz` | The exact source that was built |
| `codex-openrouter` | The launcher binary on its own |
| `candidate.json` | Build inputs and file hashes |
| `SHA256SUMS` | Hashes of the files above |

The [Candidate workflow](../.github/workflows/release.yml) does the same steps on a GitHub `macos-15` runner. Start it from the Actions tab with a version number, then download the files from the run's artifact.

## Test the release

```sh
(cd "$OUT" && shasum -a 256 -c SHA256SUMS)
mkdir "$OUT/extracted" && tar -xzf "$OUT/codex-openrouter_${VERSION}_darwin_arm64.tar.gz" -C "$OUT/extracted"
CODEX_OPENROUTER_TEST_CANDIDATE="$OUT/extracted/codex-openrouter" go test -count=1 -v ./internal/integration
```

This installs the release binary into a temporary directory, downloads the pinned Codex bundle, and runs real Codex against a local mock provider with a fake key. Before you publish, also start the installed `codex-openrouter` by hand and make sure that one Ctrl-C exits the session.

## Publish

```sh
git tag "v$VERSION" && git push origin "v$VERSION"
rm -rf "$OUT/extracted"
gh release create "v$VERSION" --title "v$VERSION" --generate-notes "$OUT"/*
```

Then update `VERSION` in the [README install steps](../README.md#install).

## Checks

- `go vet ./...` and `go test ./...` run the ordinary tests. Add `-short` to skip the packager test, which takes about two minutes.
- `CODEX_OPENROUTER_TEST_INSTALL=1 go test -count=1 -v ./internal/integration` builds the launcher, installs it over HTTPS into a temporary directory and runs it against real Codex. It needs network access and an Apple Silicon Mac.
- The [Check workflow](../.github/workflows/test.yml) runs the tests on Linux, macOS and Windows, cross-compiles six targets, and runs the native install test on macOS arm64.

## Update the Codex pin

1. Choose a Codex release. Check its [security advisories](https://github.com/openai/codex/security/advisories) and the advisories for the crates in its `codex-rs/Cargo.lock`.
2. Download `codex-package-aarch64-apple-darwin.tar.gz` from the release. Record its size and SHA-256, and the size and SHA-256 of every extracted file.
3. Update `internal/distribution/codex-artifacts.json` and the license files in `internal/distribution/licenses/`.
4. Check the minimum macOS version of each binary (`vtool -show-build`). Update `osMinimum` if it changed.
5. Compare the Codex CLI options with the lists in `internal/launcher/launch.go`. `IsUpdateCommand` uses them to find a command-position `update`.
6. Run the native install test and the manual Ctrl-C check, then make a new release.

[native-distribution.md](native-distribution.md) explains the bundle layout and the manifest fields.

## Update Go

Change `.go-version` and the `go` line in `go.mod` together. Run the tests, then scan the source and the release binary:

```sh
go run golang.org/x/vuln/cmd/govulncheck@v1.8.0 ./...
go run golang.org/x/vuln/cmd/govulncheck@v1.8.0 -mode=binary "$OUT/codex-openrouter"
```

A binary that users already installed keeps the old Go runtime until they install a new release.
