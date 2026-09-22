# codex-openrouter

Run [Codex CLI](https://github.com/openai/codex) directly with OpenRouter, without a local proxy. Defaults to [DeepSeek V4.1 Flash](https://openrouter.ai/deepseek/deepseek-v4.1-flash) with explicit `high` reasoning. Your regular Codex configuration still applies; model and provider overrides apply only to this command and do not rewrite your normal Codex defaults.

Requires **Node.js 22+ with npm 10+**. Targets macOS, Linux (including WSL), and native Windows on x64/ARM64, where Codex provides binaries. Locally tested on macOS ARM64; Linux validation is pending, and Windows testing and fixes from contributors are welcome. CI is configured for all three platforms.

## Install

Download or clone this repository, open a terminal in its directory, and run:

```sh
node install.mjs
# Or choose a default during installation:
node install.mjs --model deepseek/deepseek-v4.1-flash --reasoning high
```

A model can also be the first argument: `node install.mjs provider/model`. On macOS/Linux, `sh install.sh` accepts the same arguments. Installation needs network access; no administrator privileges are needed.

Add the installation directory to PATH and enter an [OpenRouter API key](https://openrouter.ai/settings/keys) without putting it in shell history:

**macOS/Linux (Bash or Zsh)**

```sh
export PATH="$HOME/.codex-openrouter/bin:$PATH"
read -rs OPENROUTER_API_KEY
export OPENROUTER_API_KEY
codex-openrouter
```

**Windows (PowerShell)**

```powershell
$env:Path = "$HOME\.codex-openrouter;$env:Path"
$credential = Get-Credential -UserName OpenRouter -Message 'Enter your API key as the password'
$env:OPENROUTER_API_KEY = $credential.GetNetworkCredential().Password
codex-openrouter.cmd
```

These environment changes apply to the current terminal. Persist PATH in your shell profile or Windows user environment settings; supply the key per session or through a secret manager.

By default, shell tools receive an empty OpenRouter key, filter other `KEY`/`SECRET`/`TOKEN` variables, and disable legacy snapshots. This does not isolate secrets from all code running as your user. See the dated [security review](SECURITY.md) for findings and limits.

## Change models

```sh
codex-openrouter --set-default provider/model --reasoning high
codex-openrouter --show-config
codex-openrouter -m provider/model          # One session only
codex-openrouter exec "Explain this project"
```

On Windows use `codex-openrouter.cmd`. Other arguments pass through to Codex. Use a full OpenRouter model ID; reasoning support varies by model.

You can also edit `~/.codex-openrouter/config.json` (`$HOME\.codex-openrouter\config.json` on Windows):

```json
{
  "model": "deepseek/deepseek-v4.1-flash",
  "reasoning": "high"
}
```

Only these two fields are accepted. Keep API keys in `OPENROUTER_API_KEY`. Set `CODEX_OPENROUTER_HOME` to an absolute private directory before installing **and** running to relocate the installation/config.

Rerun the installer from an updated checkout to update; saved defaults are preserved. `codex-openrouter update` shows these instructions. Codex's own update prompts are disabled by default.

To uninstall, remove `~/.codex-openrouter` and its PATH entry. Earlier `open-codex` and `codex-kimi` installations remain separate; reinstall under the new name and update PATH. Their saved defaults are not migrated automatically.

Run `npm test` for offline regression checks. MIT licensed.

## To do

- Saved-key authentication using the OS credential store. For now, export `OPENROUTER_API_KEY` for each terminal session.
