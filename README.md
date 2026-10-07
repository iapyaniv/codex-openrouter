# codex-openrouter

Run the [Codex CLI](https://github.com/openai/codex) with any [OpenRouter](https://openrouter.ai) model, without a local proxy.

`codex-openrouter` installs its own pinned copy of Codex (0.155.1) and starts it with OpenRouter as the model provider. The default model is [DeepSeek V4.1 Flash](https://openrouter.ai/deepseek/deepseek-v4.1-flash) with `high` reasoning. Your normal `codex` install and its configuration are not changed.

**Everything runs through OpenRouter.** If you select a model in the Codex model picker, Codex does not start its normal login flow, and OpenRouter bills the request to your key. To use your OpenAI account and the regular ChatGPT models, run the normal `codex` command.

This is an unofficial tool. It is not affiliated with OpenAI or OpenRouter.

**You need:** an [OpenRouter API key](https://openrouter.ai/settings/keys) and one of these systems:

| System | Release archive |
| --- | --- |
| macOS 15 or later, Apple Silicon | `codex-openrouter_VERSION_darwin_arm64.tar.gz` |
| macOS 15 or later, Intel | `codex-openrouter_VERSION_darwin_amd64.tar.gz` |
| Linux, x64 | `codex-openrouter_VERSION_linux_amd64.tar.gz` |
| Linux, arm64 | `codex-openrouter_VERSION_linux_arm64.tar.gz` |
| Windows, x64 | `codex-openrouter_VERSION_windows_amd64.zip` |
| Windows, arm64 | `codex-openrouter_VERSION_windows_arm64.zip` |

Apple Silicon Macs get the most testing. The other systems pass the install test in CI.

## Install on macOS or Linux

```sh
VERSION=0.3.0
OS=$(uname -s | tr '[:upper:]' '[:lower:]')
ARCH=$(uname -m | sed 's/x86_64/amd64/; s/aarch64/arm64/')
mkdir -p ~/Downloads/codex-openrouter-$VERSION && cd ~/Downloads/codex-openrouter-$VERSION
curl -fLO "https://github.com/iapyaniv/codex-openrouter/releases/download/v$VERSION/codex-openrouter_${VERSION}_${OS}_${ARCH}.tar.gz"
tar -xzf "codex-openrouter_${VERSION}_${OS}_${ARCH}.tar.gz"
./codex-openrouter --install
```

The installer downloads Codex (about 130 MB) from its GitHub release, checks its SHA-256, and installs it in `~/.codex-openrouter`. It does not need `sudo` and does not edit your shell profile.

The binary is not signed or notarized by Apple. `curl` downloads do not trigger Gatekeeper, so the steps above work. If you download the archive with a browser, macOS blocks the binary. To allow it, run `xattr -d com.apple.quarantine codex-openrouter`.

Add the install directory to your `PATH`. To keep it for new terminals, also add this line to your shell profile (`~/.zshrc` or `~/.bashrc`):

```sh
export PATH="$HOME/.codex-openrouter/bin:$PATH"
```

Then set your key and start a session:

```sh
read -rs OPENROUTER_API_KEY && export OPENROUTER_API_KEY
codex-openrouter
```

`read -s` keeps the key out of your shell history. To keep the key for new terminals, add `export OPENROUTER_API_KEY="..."` to your shell profile with a text editor and run `chmod 600` on that file. The file then holds the key in plain text.

## Install on Windows

In PowerShell:

```powershell
$Version = "0.3.0"
$Arch = if ($env:PROCESSOR_ARCHITECTURE -eq "ARM64") { "arm64" } else { "amd64" }
$Dir = "$HOME\Downloads\codex-openrouter-$Version"
New-Item -ItemType Directory -Force $Dir | Out-Null; Set-Location $Dir
curl.exe -fLO "https://github.com/iapyaniv/codex-openrouter/releases/download/v$Version/codex-openrouter_${Version}_windows_$Arch.zip"
Expand-Archive "codex-openrouter_${Version}_windows_$Arch.zip" -DestinationPath .
.\codex-openrouter.exe --install
```

The installer puts Codex in `%USERPROFILE%\.codex-openrouter`. The binary is not signed. If you download the zip with a browser and SmartScreen stops the program, select **More info → Run anyway**, or run `Unblock-File .\codex-openrouter.exe` first.

Add the install directory to your user `PATH`, then open a new terminal:

```powershell
[Environment]::SetEnvironmentVariable("Path", "$HOME\.codex-openrouter\bin;" + [Environment]::GetEnvironmentVariable("Path", "User"), "User")
```

Set your key for this terminal and start a session:

```powershell
$env:OPENROUTER_API_KEY = [Net.NetworkCredential]::new("", (Read-Host -AsSecureString "OpenRouter API key")).Password
codex-openrouter
```

To keep the key for new terminals, run `[Environment]::SetEnvironmentVariable("OPENROUTER_API_KEY", "...", "User")`. Windows then stores the key in plain text in your user registry.

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

Use full OpenRouter model IDs, for example `deepseek/deepseek-v4.1-flash`. Reasoning levels are `none`, `minimal`, `low`, `medium`, `high`, `xhigh` and `max`. Not all models support reasoning. The settings file is `config.json` in the install directory.

To install somewhere other than `~/.codex-openrouter`, set `CODEX_OPENROUTER_HOME` to an absolute path. Set it for both `--install` and normal use.

## Update

Download the new release and run its `--install`, as in the install steps above. Your saved defaults stay. Codex's own updater is turned off because it cannot update this install; `codex-openrouter update` prints these steps.

## Uninstall

Delete the install directory and remove it from your `PATH`:

```sh
rm -rf ~/.codex-openrouter                                  # macOS or Linux
Remove-Item -Recurse -Force "$HOME\.codex-openrouter"      # Windows PowerShell
```

This does not touch your normal Codex install or `~/.codex`.

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

On Windows, name the output `codex-openrouter.exe`. The installer refuses a build without a `BuildID`. Run the tests with `go test ./...`. [docs/releasing.md](docs/releasing.md) shows how to make and publish a release.

## To do

- Store the API key in the OS credential store.
- Run the full mock-provider session test on Windows. CI installs and starts Codex there, but the session test uses POSIX shell commands.
- Sign and notarize the macOS binary, and sign the Windows binary.

## License

MIT. Each release includes the licenses of the bundled Codex components and Go.
