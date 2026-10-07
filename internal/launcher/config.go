package launcher

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"

	"encoding/json/jsontext"
	json "encoding/json/v2"

	"codex-openrouter/internal/platform"
)

const (
	DefaultModel     = "deepseek/deepseek-v4.1-flash"
	DefaultReasoning = "high"
)

const maxSettingsBytes = 4096

var (
	modelPattern = regexp.MustCompile(`^~?[a-zA-Z0-9][a-zA-Z0-9._-]*/[a-zA-Z0-9][a-zA-Z0-9._:~/-]*$`)
	reasonings   = map[string]bool{
		"none": true, "minimal": true, "low": true, "medium": true,
		"high": true, "xhigh": true, "max": true,
	}
)

type Settings struct {
	Model     string `json:"model"`
	Reasoning string `json:"reasoning"`
}

func defaultSettings() Settings {
	return Settings{Model: DefaultModel, Reasoning: DefaultReasoning}
}

// HomeDirectory resolves the private wrapper prefix: CODEX_OPENROUTER_HOME
// when set, otherwise the OS user's home plus .codex-openrouter. The value
// must be absolute and non-root; it is not shell-expanded.
func HomeDirectory() (string, error) {
	directory, ok := os.LookupEnv("CODEX_OPENROUTER_HOME")
	if ok {
		if directory == "" {
			return "", errors.New("CODEX_OPENROUTER_HOME is set but empty")
		}
	} else {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("cannot determine the user's home directory: %w", err)
		}
		directory = filepath.Join(home, ".codex-openrouter")
	}
	return ValidatePrefix(directory)
}

// ValidatePrefix returns the cleaned prefix or refuses it. The contract: an
// absolute, non-root directory; on Windows, exactly an ASCII drive-letter
// path — UNC, device, and drive-relative spellings are rejected rather than
// normalized. Cleaning precedes validation so inputs are judged on their
// normalized target.
func ValidatePrefix(directory string) (string, error) {
	if runtime.GOOS == "windows" {
		if strings.HasPrefix(directory, `\\`) || strings.HasPrefix(directory, "//") {
			return "", errors.New("CODEX_OPENROUTER_HOME must be a local directory, not a UNC, network, or device path")
		}
	}
	cleaned := filepath.Clean(directory)
	if !filepath.IsAbs(cleaned) {
		return "", errors.New("CODEX_OPENROUTER_HOME must be an absolute, non-root directory")
	}
	if runtime.GOOS == "windows" {
		volume := filepath.VolumeName(cleaned)
		if len(volume) != 2 || volume[1] != ':' || !isASCIILetter(volume[0]) {
			return "", errors.New("CODEX_OPENROUTER_HOME must be a local directory, not a UNC, network, or device path")
		}
	}
	if cleaned == filepath.VolumeName(cleaned)+string(filepath.Separator) {
		return "", errors.New("CODEX_OPENROUTER_HOME must be an absolute, non-root directory")
	}
	return cleaned, nil
}

func isASCIILetter(b byte) bool {
	return 'a' <= b && b <= 'z' || 'A' <= b && b <= 'Z'
}

// EnsureHome creates the final prefix directory when missing and verifies it
// is a privately owned real directory. The trusted parent must already
// exist and pass ownership/write checks; links in that parent are followed.
func EnsureHome(directory string) error {
	parent := filepath.Dir(directory)
	parentInfo, err := os.Stat(parent)
	if err != nil {
		return fmt.Errorf("installation parent %s must already exist: %w", parent, err)
	}
	if err := platform.CheckManagedDir(parentInfo, parent); err != nil {
		return err
	}
	if err := platform.CheckPrivate(parentInfo, parent); err != nil {
		return err
	}
	if err := os.Mkdir(directory, 0o700); err != nil && !errors.Is(err, os.ErrExist) {
		return err
	}
	info, err := os.Lstat(directory)
	if err != nil {
		return err
	}
	if err := platform.CheckManagedDir(info, directory); err != nil {
		return err
	}
	if err := platform.CheckPrivate(info, directory); err != nil {
		return err
	}
	if runtime.GOOS != "windows" {
		if err := os.Chmod(directory, 0o700); err != nil {
			return err
		}
	}
	return nil
}

func settingsPath(directory string) string {
	return filepath.Join(directory, "config.json")
}

func lockPath(directory string) string {
	return filepath.Join(directory, ".config.lock")
}

// Diagnostics name the failing rule but never echo rejected values.
func validateSettings(settings Settings) error {
	if len(settings.Model) > 200 || !modelPattern.MatchString(settings.Model) {
		return errors.New("use a full OpenRouter model ID, such as deepseek/deepseek-v4.1-flash")
	}
	if !reasonings[settings.Reasoning] {
		return errors.New("reasoning must be none, minimal, low, medium, high, xhigh, or max")
	}
	return nil
}

// strictSettings mirrors Settings for decoding so required/null detection is
// explicit rather than inherited from encoding defaults.
type strictSettings struct {
	Model     *string `json:"model"`
	Reasoning *string `json:"reasoning"`
}

// parseSettings decodes the settings document with the exact key contract:
// only model and reasoning, exact case, both required, non-null strings, no
// duplicate keys, no invalid UTF-8, and no trailing content. Decoding errors
// are collapsed into one generic message because parser diagnostics can echo
// file contents, including accidentally pasted secrets.
func parseSettings(data []byte, path string) (Settings, error) {
	invalid := fmt.Errorf("invalid settings in %s", path)
	var raw strictSettings
	if err := json.Unmarshal(data, &raw, json.RejectUnknownMembers(true)); err != nil {
		return Settings{}, invalid
	}
	if raw.Model == nil || raw.Reasoning == nil {
		return Settings{}, invalid
	}
	settings := Settings{Model: *raw.Model, Reasoning: *raw.Reasoning}
	if err := validateSettings(settings); err != nil {
		return Settings{}, invalid
	}
	return settings, nil
}

func formatSettings(settings Settings) ([]byte, error) {
	data, err := json.Marshal(settings, jsontext.WithIndent("  "))
	if err != nil {
		return nil, err
	}
	return append(data, '\n'), nil
}

// ReadSettings loads validated settings from the prefix, returning compiled
// defaults when the file is absent. The file is opened through the checked
// platform primitive, which rejects links, reparse points, extra hard links,
// and swapped or non-regular files before any content is read; the size
// limit is enforced while reading.
func ReadSettings(directory string) (Settings, error) {
	if err := EnsureHome(directory); err != nil {
		return Settings{}, err
	}
	path := settingsPath(directory)
	file, err := platform.OpenChecked(path)
	if errors.Is(err, os.ErrNotExist) {
		return defaultSettings(), nil
	}
	if err != nil {
		return Settings{}, err
	}
	defer file.Close()
	data, err := platform.ReadFileLimit(file, maxSettingsBytes)
	if err != nil {
		return Settings{}, fmt.Errorf("invalid settings in %s", path)
	}
	return parseSettings(data, path)
}

// WriteSettings serializes cooperating writers with a nonblocking OS lock,
// re-reads the current settings under the lock so simultaneous model-only
// and reasoning-only updates merge instead of losing each other, applies
// update, and replaces the settings file. Lock contention is reported as
// busy immediately; retrying is the caller's decision.
func WriteSettings(directory string, update func(Settings) Settings) (Settings, error) {
	if err := EnsureHome(directory); err != nil {
		return Settings{}, err
	}
	release, err := platform.Lock(lockPath(directory))
	if err != nil {
		if errors.Is(err, platform.ErrBusy) {
			return Settings{}, fmt.Errorf("settings %w; retry", platform.ErrBusy)
		}
		return Settings{}, fmt.Errorf("cannot lock settings: %w", err)
	}
	defer release()
	current, err := ReadSettings(directory)
	if err != nil {
		return Settings{}, err
	}
	next := update(current)
	if err := validateSettings(next); err != nil {
		return Settings{}, err
	}
	data, err := formatSettings(next)
	if err != nil {
		return Settings{}, err
	}
	if err := platform.ReplaceFile(settingsPath(directory), data, 0o600); err != nil {
		return Settings{}, err
	}
	return next, nil
}
