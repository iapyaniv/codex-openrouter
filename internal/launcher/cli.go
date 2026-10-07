package launcher

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"codex-openrouter/internal/distribution"
	"codex-openrouter/internal/install"
)

// Release builds stamp these from their reviewed source and build inputs.
var (
	Version = "0.2.0-dev"
	BuildID = "dev"
)

var PinnedCodexVersion = distribution.CodexVersion()

func releaseIdentity() distribution.ReleaseIdentity {
	return distribution.CurrentReleaseIdentity(Version, BuildID)
}

const setDefaultUsage = "Usage: codex-openrouter --set-default MODEL [--reasoning LEVEL]"

const launcherHelp = `codex-openrouter launches a managed Codex CLI with OpenRouter defaults.

Wrapper modes (first argument only):
  --set-default MODEL [--reasoning LEVEL]   Save default model and reasoning
  --set-default --model MODEL [--reasoning LEVEL]
  --set-default --reasoning LEVEL           Change reasoning only
  --show-config                             Print the settings path and saved defaults
  --launcher-version                        Print launcher and pinned Codex versions
  --launcher-help                           Print this help
  --install                                 Install the managed Codex distribution
  --internal-auth                           Internal credential helper; not for direct use

Any other invocation launches Codex with the saved defaults. To update,
download a new codex-openrouter release and run its --install.`

const installHelp = `Usage: codex-openrouter --install

Downloads and installs the pinned Codex distribution into the private
codex-openrouter prefix. Saved defaults are preserved. Run this command
from a downloaded launcher, then add the printed directory to your PATH.`

// Run dispatches on the first argument only. It returns the process exit
// status. Diagnostics go to stderr with the wrapper prefix; command output
// goes to stdout.
func Run(args []string, stdout, stderr io.Writer) int {
	if len(args) > 0 {
		switch args[0] {
		case "--set-default":
			return runSetDefault(args[1:], stdout, stderr)
		case "--show-config":
			if len(args) != 1 {
				return fail(stderr, "--show-config takes no additional arguments")
			}
			return runShowConfig(stdout, stderr)
		case "--launcher-version":
			if len(args) != 1 {
				return fail(stderr, "--launcher-version takes no additional arguments")
			}
			fmt.Fprintf(stdout, "codex-openrouter %s, pinned Codex %s\n",
				releaseIdentity().Describe(), PinnedCodexVersion)
			return 0
		case "--launcher-help":
			if len(args) != 1 {
				return fail(stderr, "--launcher-help takes no additional arguments")
			}
			fmt.Fprintln(stdout, launcherHelp)
			return 0
		case "--install":
			if len(args) == 2 && (args[1] == "--help" || args[1] == "-h") {
				fmt.Fprintln(stdout, installHelp)
				return 0
			}
			if len(args) != 1 {
				return fail(stderr, "--install takes no additional arguments")
			}
			return runInstall(stdout, stderr)
		case "--internal-auth":
			if len(args) != 1 {
				return fail(stderr, "--internal-auth takes no additional arguments")
			}
			if err := InternalAuth(stdout); err != nil {
				return fail(stderr, err.Error())
			}
			return 0
		}
	}
	if IsUpdateCommand(args) {
		fmt.Fprintln(stdout, UpdateInstructions)
		return 0
	}
	return runLaunch(args, stdout, stderr)
}

func runInstall(stdout, stderr io.Writer) int {
	identity := releaseIdentity()
	if err := install.Preflight(identity); err != nil {
		return fail(stderr, err.Error())
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	directory, err := HomeDirectory()
	if err != nil {
		return fail(stderr, err.Error())
	}
	if err := ctx.Err(); err != nil {
		return fail(stderr, err.Error())
	}
	if _, err := ReadSettings(directory); err != nil {
		return fail(stderr, err.Error())
	}
	result, err := install.Run(ctx, directory, identity, stderr)
	if err != nil {
		return fail(stderr, err.Error())
	}
	if _, err := fmt.Fprintf(stdout, "Installed codex-openrouter %s, Codex %s\nExecutable: %s\nSettings: %s\nAdd this directory to your user PATH: %s\n", result.Version, result.CodexVersion, result.PublicPath, settingsPath(directory), filepath.Dir(result.PublicPath)); err != nil {
		return fail(stderr, "launcher installed, but installation instructions could not be written")
	}
	return 0
}

func fail(stderr io.Writer, message string) int {
	fmt.Fprintf(stderr, "codex-openrouter: %s\n", message)
	return 1
}

func runSetDefault(args []string, stdout, stderr io.Writer) int {
	if len(args) == 1 && (args[0] == "--help" || args[0] == "-h") {
		fmt.Fprintln(stdout, setDefaultUsage)
		return 0
	}
	var model, reasoning string
	var haveModel, haveReasoning bool
	var positionals []string
	positionalOnly := false
	for index := 0; index < len(args); index++ {
		arg := args[index]
		if positionalOnly {
			positionals = append(positionals, arg)
			continue
		}
		if arg == "--" {
			positionalOnly = true
			continue
		}
		option, attached, hasAttached := strings.Cut(arg, "=")
		switch {
		case arg == "--help" || arg == "-h":
			return fail(stderr, "--set-default help takes no additional arguments\n"+setDefaultUsage)
		case option == "--model" || option == "--reasoning":
			if !hasAttached {
				if index+1 >= len(args) {
					return fail(stderr, option+" requires a value\n"+setDefaultUsage)
				}
				index++
				attached = args[index]
			}
			if option == "--model" {
				if haveModel {
					return fail(stderr, "--model was supplied more than once\n"+setDefaultUsage)
				}
				model, haveModel = attached, true
			} else {
				if haveReasoning {
					return fail(stderr, "--reasoning was supplied more than once\n"+setDefaultUsage)
				}
				reasoning, haveReasoning = attached, true
			}
		case len(arg) > 0 && arg[0] == '-':
			return fail(stderr, "unknown option for --set-default\n"+setDefaultUsage)
		default:
			positionals = append(positionals, arg)
		}
	}
	if len(positionals) > 1 || (len(positionals) == 1 && haveModel) {
		return fail(stderr, "supply one model, either as a positional argument or with --model\n"+setDefaultUsage)
	}
	if len(positionals) == 1 {
		model, haveModel = positionals[0], true
	}
	if !haveModel && !haveReasoning {
		return fail(stderr, "invocation changes nothing\n"+setDefaultUsage)
	}

	directory, err := HomeDirectory()
	if err != nil {
		return fail(stderr, err.Error())
	}
	settings, err := WriteSettings(directory, func(current Settings) Settings {
		next := current
		if haveModel {
			next.Model = model
		}
		if haveReasoning {
			next.Reasoning = reasoning
		}
		return next
	})
	if err != nil {
		return fail(stderr, err.Error())
	}
	fmt.Fprintf(stdout, "Default: %s (%s)\n", settings.Model, settings.Reasoning)
	return 0
}

func runShowConfig(stdout, stderr io.Writer) int {
	directory, err := HomeDirectory()
	if err != nil {
		return fail(stderr, err.Error())
	}
	settings, err := ReadSettings(directory)
	if err != nil {
		return fail(stderr, err.Error())
	}
	data, err := formatSettings(settings)
	if err != nil {
		return fail(stderr, err.Error())
	}
	fmt.Fprintln(stdout, settingsPath(directory))
	if _, err := stdout.Write(data); err != nil {
		return fail(stderr, "cannot write settings")
	}
	return 0
}

// runLaunch prepares and performs the direct native launch. Informational
// --version/-V/--help/-h need no key; ordinary launches validate the key
// before native execution. There is no PATH search, shell, or proxy
// fallback: a missing or corrupt managed release fails with reinstall
// instructions.
func runLaunch(args []string, stdout, stderr io.Writer) int {
	target, err := distribution.CurrentTarget()
	if err != nil {
		return fail(stderr, "this platform has no published managed Codex distribution in this build")
	}
	informational := len(args) == 1 && (args[0] == "--version" || args[0] == "-V" || args[0] == "--help" || args[0] == "-h")
	if !informational {
		if err := InternalAuth(io.Discard); err != nil {
			return fail(stderr, err.Error())
		}
	}
	directory, err := HomeDirectory()
	if err != nil {
		return fail(stderr, err.Error())
	}
	settings, err := ReadSettings(directory)
	if err != nil {
		return fail(stderr, err.Error())
	}
	identity := releaseIdentity()
	paths := distribution.ReleaseLayout(directory, identity, target)
	if err := distribution.ValidateRelease(paths, target, identity); err != nil {
		return fail(stderr, distribution.MissingInstallationError(directory, identity, err).Error())
	}
	codexArgs, err := CodexArguments(settings, paths.Helper, args)
	if err != nil {
		return fail(stderr, err.Error())
	}
	return execNative(paths.CodexBinary, codexArgs, stderr)
}
