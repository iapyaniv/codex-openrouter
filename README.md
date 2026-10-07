# codex-openrouter

Run [Codex CLI](https://github.com/openai/codex) directly with OpenRouter, without a local proxy. Defaults to [DeepSeek V4.1 Flash](https://openrouter.ai/deepseek/deepseek-v4.1-flash) with explicit `high` reasoning. Your regular Codex configuration still applies; model and provider overrides apply only to this command and do not rewrite your normal Codex defaults.

The Go launcher runs without **Node, npm, or Go installed** and manages a separate pinned Codex **0.155.1** bundle. The initial installer supports **Apple Silicon Macs with macOS 15 or later**. Local execution was tested on macOS 26.5.1 ARM64; the macOS 15 minimum comes from bundle requirements. Other macOS, Linux, and Windows targets have compile checks only and remain withheld.

## Install

No downloadable release has been published. Public releases are blocked by the [unfinished native dependency review](SECURITY.md#native-gix-dependency-advisories). To prepare a local candidate on an Apple Silicon Mac, follow the [trusted-commit bootstrap procedure](docs/releasing.md#build-a-local-candidate) with Git and the pinned Go **1.27.1** compiler. It builds from a fresh checkout of the selected trusted full commit and verifies the launcher's identity.

In the output directory, compare the archive hash with its line in `SHA256SUMS`, then extract and install:

```sh
shasum -a 256 codex-openrouter_0.2.0-local_darwin_arm64.tar.gz
tar -xzf codex-openrouter_0.2.0-local_darwin_arm64.tar.gz
./codex-openrouter --install
```

The archive contains installation instructions, provenance and notices. Local checksums are not independently signed; this launcher has no publisher signature or notarization. See [candidate builds and verification limits](docs/releasing.md). The downloaded executable needs network access to fetch its pinned Codex bundle, but no Node/npm/Go or administrator privileges. Keep that copy outside the installation prefix for updates and recovery; the installed public command cannot install over itself. Existing saved defaults are preserved. To choose a default, run `--set-default` after installation.

Add the installation directory to PATH and enter an [OpenRouter API key](https://openrouter.ai/settings/keys) without putting it in shell history:

**macOS (Bash or Zsh)**

```sh
export PATH="$HOME/.codex-openrouter/bin:$PATH"
read -rs OPENROUTER_API_KEY
export OPENROUTER_API_KEY
codex-openrouter
```

These environment changes apply to the current terminal. Persist PATH in your shell profile; supply the key per session or through a secret manager. The installer prints the directory to add and does not edit your profile.

By default, shell tools receive an empty OpenRouter key, filter other `KEY`/`SECRET`/`TOKEN` variables, and disable legacy snapshots. This does not isolate secrets from all code running as your user. See the dated [security review](SECURITY.md) for findings and limits.

### Persist the key in Zsh

To load the key automatically in new Zsh terminals, open your startup file:

```sh
nano ~/.zshrc
```

Add this line with your actual key:

```sh
export OPENROUTER_API_KEY="your-openrouter-api-key"
```

Save with **Ctrl+O**, Enter, then exit with **Ctrl+X**. Restrict file access and load it in the current terminal:

```sh
chmod 600 ~/.zshrc
source ~/.zshrc
```

This stores the key **in plaintext**. Keep the file out of Git and shared dotfile backups. Editing the file avoids putting the key in shell command history. Shell tools that reload this profile can recover the key despite environment filtering.

## Change models

```sh
codex-openrouter --set-default provider/model --reasoning high
codex-openrouter --show-config
codex-openrouter -m provider/model          # One session only
codex-openrouter exec "Explain this project"
```

Other arguments pass through to Codex. Use a full OpenRouter model ID; reasoning support varies by model. A model-only `--set-default` preserves saved reasoning. `--version` reports Codex's version; `--launcher-version` reports the wrapper identity. Neither version nor help requires a key.

You can also edit `~/.codex-openrouter/config.json`:

```json
{
  "model": "deepseek/deepseek-v4.1-flash",
  "reasoning": "high"
}
```

Only these two fields are accepted. Keep API keys in `OPENROUTER_API_KEY`. Set `CODEX_OPENROUTER_HOME` to an absolute private directory before installing **and** running to relocate the installation/config.

## Update and recovery

Run `./codex-openrouter --install` from a newly verified downloaded candidate to update. `codex-openrouter update` prints these instructions; Codex's own update prompts are disabled. Installations retain previous releases and the old npm package tree, preserve saved defaults, and leave your separate ordinary Codex installation/configuration untouched.

If launch reports a missing release, rerun its trusted downloaded installer. If a release is corrupt or incomplete, close affected Codex sessions, move aside only the specific `releases/<release-id>` directory named in the diagnostic, then rerun the **same trusted candidate** installer. Do not merge into an incomplete release or delete arbitrary paths. A failure reported after activation may leave the new command installed; use the explicit path in the diagnostic and retry its downloaded installer.

To downgrade Go releases, close affected sessions and run an earlier trusted downloaded executable's `--install`. To return to the Node version, keep the retained npm package tree and restore Node 22+/npm 10+, then use a separate checkout of the historical installer:

```sh
git worktree add ../codex-openrouter-node d14e315523590e651862ec712a60758f2faae473
cd ../codex-openrouter-node
node install.mjs
```

That recovery path requires the original repository history; a source archive alone contains no Git history. The historical installer preserves default values. Set the same `CODEX_OPENROUTER_HOME` if you relocated the prefix. Keep retained releases until sessions using them have closed.

## Uninstall

Close affected sessions, then remove `~/.codex-openrouter` (or your chosen prefix) and its PATH entry. This deletes saved wrapper defaults, retained Go releases and the retained npm tree; it does not delete ordinary Codex state under `CODEX_HOME`. Earlier `open-codex` and `codex-kimi` installations remain separate; their saved defaults are not migrated automatically.

## Validation and interface changes

With the pinned compiler, run `GOTOOLCHAIN=local GOPROXY=off go test ./...` for offline regression checks. [Maintainer instructions](docs/releasing.md) cover race, native HTTPS/loopback integration and vulnerability scans. CI is configured but no hosted run is claimed.

The Go migration replaces `node install.mjs`/`install.sh` with the downloaded executable's `--install`, moves install-time model/reasoning selection to `--set-default`, adds `--launcher-version`, and uses immutable release-local authentication helpers. Only darwin-arm64 is initially enabled. Settings and normal Codex argument forwarding remain compatible. MIT licensed.

## To do

- Saved-key authentication using the OS credential store. For now, supply `OPENROUTER_API_KEY` through the environment, per session or through your shell profile as described above.
