# codex-openrouter

Run the [Codex CLI](https://github.com/openai/codex) with any [OpenRouter](https://openrouter.ai) model, without a local proxy.

`codex-openrouter` installs its own pinned copy of Codex (0.155.1) and starts it with OpenRouter as the model provider. The default model is [DeepSeek V4.1 Flash](https://openrouter.ai/deepseek/deepseek-v4.1-flash) with `high` reasoning. Your normal `codex` install and its configuration are not changed.

This is an unofficial tool. It is not affiliated with OpenAI or OpenRouter.

**You need:** an Apple Silicon Mac with macOS 15 or later, and an [OpenRouter API key](https://openrouter.ai/settings/keys).

## Install

Download the release and run its installer:

```sh
VERSION=0.2.0
mkdir -p ~/Downloads/codex-openrouter-$VERSION && cd ~/Downloads/codex-openrouter-$VERSION
curl -fLO "https://github.com/iapyaniv/codex-openrouter/releases/download/v$VERSION/codex-openrouter_${VERSION}_darwin_arm64.tar.gz"
tar -xzf "codex-openrouter_${VERSION}_darwin_arm64.tar.gz"
./codex-openrouter --install
```

The installer downloads Codex (about 120 MB) from its GitHub release, checks its SHA-256, and installs it in `~/.codex-openrouter`. It does not need `sudo` and does not edit your shell profile.

The binary is not signed or notarized by Apple. `curl` downloads do not trigger Gatekeeper, so the steps above work. If you download the archive with a browser, macOS blocks the binary. To allow it, run `xattr -d com.apple.quarantine codex-openrouter`.

Add the install directory to your `PATH`. To keep it for new terminals, also add this line to `~/.zshrc`:

```sh
export PATH="$HOME/.codex-openrouter/bin:$PATH"
```

## Set your API key

```sh
read -rs OPENROUTER_API_KEY && export OPENROUTER_API_KEY
codex-openrouter
```

`read -s` keeps the key out of your shell history. To keep the key for new terminals, add `export OPENROUTER_API_KEY="..."` to `~/.zshrc` with a text editor and run `chmod 600 ~/.zshrc`. That file then holds the key in plain text.

## Use

```sh
codex-openrouter                                # interactive session
codex-openrouter exec "Explain this project"    # one-shot task
codex-openrouter -m provider/model              # different model for this session
```

Arguments go to Codex unchanged, except for the wrapper options below.

| Option | Effect |
| --- | --- |
| `--set-default MODEL [--reasoning LEVEL]` | Save the default model and reasoning level |
| `--show-config` | Print the settings file and the saved defaults |
| `--install` | Install or update (run it from a downloaded copy) |
| `--launcher-version` | Print the wrapper and pinned Codex versions |
| `--launcher-help` | Print the wrapper help |

Use full OpenRouter model IDs, for example `deepseek/deepseek-v4.1-flash`. Reasoning levels are `none`, `minimal`, `low`, `medium`, `high`, `xhigh` and `max`. Not all models support reasoning. The settings file is `~/.codex-openrouter/config.json`.

To install somewhere other than `~/.codex-openrouter`, set `CODEX_OPENROUTER_HOME` to an absolute path. Set it for both `--install` and normal use.

## Update

Download the new release and run its `./codex-openrouter --install`, as in [Install](#install). Your saved defaults stay. Codex's own updater is turned off because it cannot update this install; `codex-openrouter update` prints these steps.

## Uninstall

```sh
rm -rf ~/.codex-openrouter
```

Then remove the `PATH` line from your shell profile. This does not touch your normal Codex install or `~/.codex`.

## Security

Codex normally lets the commands it runs see your environment, which includes `OPENROUTER_API_KEY`. On each launch, `codex-openrouter` gives those commands an empty key, turns on Codex's filter for other `*KEY*`, `*SECRET*` and `*TOKEN*` variables, and turns off shell snapshots. This is not a sandbox. See [SECURITY.md](SECURITY.md) for the limits.

## Build from source

You need Git and Go 1.21 or later. Go downloads the pinned 1.27.1 toolchain automatically.

```sh
git clone https://github.com/iapyaniv/codex-openrouter.git && cd codex-openrouter
go build -o /tmp/codex-openrouter \
  -ldflags "-X codex-openrouter/internal/launcher.BuildID=$(git rev-parse --short HEAD)" \
  ./cmd/codex-openrouter
/tmp/codex-openrouter --install
```

The installer refuses a build without a `BuildID`. Run the tests with `go test ./...`. [docs/releasing.md](docs/releasing.md) shows how to make and publish a release.

## To do

- Store the API key in the macOS Keychain.
- Support Intel Macs, Linux and Windows. These compile, but Codex bundles for them are not pinned or tested yet.
- Sign and notarize the macOS binary.

## License

MIT. Each release includes the licenses of the bundled Codex, ripgrep, zsh and Go components.
