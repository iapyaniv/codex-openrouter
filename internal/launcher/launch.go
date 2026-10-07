package launcher

import (
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	json "encoding/json/v2"
)

// tomlBasicString encodes a TOML basic string: JSON string encoding is valid
// TOML except raw DEL (U+007F), which is escaped; invalid UTF-8 is an error.
func tomlBasicString(value string) (string, error) {
	if !utf8.ValidString(value) {
		return "", errors.New("value is not valid UTF-8")
	}
	data, err := json.Marshal(value)
	if err != nil {
		return "", fmt.Errorf("cannot encode value: %w", err)
	}
	return strings.ReplaceAll(string(data), "\x7f", `\u007f`), nil
}

// CodexArguments builds the injected -c configuration, in order, followed by
// the caller's untouched argument vector. Wrapper defaults precede caller
// arguments so explicit caller Codex options retain precedence. helperPath
// is the immutable release-local copy of this launcher used for command
// auth; it must not be a mutable PATH entry. A helper path or setting that
// cannot be encoded as a TOML basic string is an error here, before launch,
// rather than a rewritten value.
func CodexArguments(settings Settings, helperPath string, args []string) ([]string, error) {
	helper, err := tomlBasicString(helperPath)
	if err != nil {
		return nil, fmt.Errorf("helper path cannot be used in the provider configuration: %w", err)
	}
	model, err := tomlBasicString(settings.Model)
	if err != nil {
		return nil, fmt.Errorf("model cannot be used in the provider configuration: %w", err)
	}
	reasoning, err := tomlBasicString(settings.Reasoning)
	if err != nil {
		return nil, fmt.Errorf("reasoning cannot be used in the provider configuration: %w", err)
	}
	auth := `{ command = ` + helper + `, args = ["--internal-auth"] }`
	provider := `{ name = "OpenRouter", base_url = "https://openrouter.ai/api/v1", wire_api = "responses", auth = ` + auth + ` }`
	built := []string{
		"-c", `model_provider="codex_openrouter"`,
		"-c", `model_providers.codex_openrouter=` + provider,
		"-c", `model=` + model,
		"-c", `model_reasoning_effort=` + reasoning,
		"-c", `check_for_update_on_startup=false`,
		// Auth keeps the original environment, so shell filtering and
		// disabling legacy snapshots are both needed.
		"-c", `shell_environment_policy.ignore_default_excludes=false`,
		"-c", `shell_environment_policy.set.OPENROUTER_API_KEY=""`,
		"-c", `features.shell_snapshot=false`,
	}
	return append(built, args...), nil
}

var valueOptions = map[string]bool{
	"-c": true, "--config": true, "--enable": true, "--disable": true,
	"--remote": true, "--remote-auth-token-env": true,
	"-i": true, "--image": true, "-m": true, "--model": true,
	"--local-provider": true, "-p": true, "--profile": true,
	"-s": true, "--sandbox": true, "-C": true, "--cd": true,
	"--add-dir": true, "-a": true, "--ask-for-approval": true,
}

var flags = map[string]bool{
	"--strict-config": true, "--oss": true, "--approve-for-me": true,
	"--dangerously-bypass-approvals-and-sandbox": true,
	"--dangerously-bypass-hook-trust":            true,
	"--yolo":                                     true, "--not-so-yolo": true,
	"--worktree": true, "--search": true, "--no-alt-screen": true,
	"-h": true, "--help": true, "-V": true, "--version": true,
}

// IsUpdateCommand mirrors the existing wrapper's command-position recognizer.
// It reports true only when `update` appears as the first positional command,
// so prompt text, `exec update`, `plugin update`, and `help update` still
// reach Codex. An unknown option makes interception conservative: the
// arguments are forwarded and Codex decides. Review this against the Codex
// pin; it is not a reimplementation of Codex's parser.
func IsUpdateCommand(args []string) bool {
	for index := 0; index < len(args); index++ {
		arg := args[index]
		if arg == "--" {
			return false
		}
		if !strings.HasPrefix(arg, "-") {
			return arg == "update"
		}
		var option string
		if strings.HasPrefix(arg, "--") {
			option, _, _ = strings.Cut(arg, "=")
		} else {
			option = arg[:min(2, len(arg))]
		}
		if valueOptions[option] {
			if arg == option {
				index++
				// Codex's separated --image values continue until the next
				// option; attached values do not.
				if option == "-i" || option == "--image" {
					for index+1 < len(args) && !strings.HasPrefix(args[index+1], "-") {
						index++
					}
				}
			}
		} else if !flags[arg] {
			return false
		}
	}
	return false
}

// UpdateInstructions is the interception message for command-position
// `update`; the managed installer replaces npm-based updating.
const UpdateInstructions = "To update codex-openrouter and its pinned Codex CLI, download a new codex-openrouter release and run its --install. Saved defaults are preserved."
