package launcher

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"codex-openrouter/internal/platform"
)

func testHome(t *testing.T) string {
	t.Helper()
	home := filepath.Join(t.TempDir(), "prefix")
	t.Setenv("CODEX_OPENROUTER_HOME", home)
	return home
}

func writeSettingsFile(t *testing.T, home, contents string) string {
	t.Helper()
	if err := os.MkdirAll(home, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(home, "config.json")
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestAbsentSettingsReturnDefaults(t *testing.T) {
	home := testHome(t)
	settings, err := ReadSettings(home)
	if err != nil {
		t.Fatal(err)
	}
	if settings != defaultSettings() {
		t.Fatalf("got %+v, want defaults", settings)
	}
	info, err := os.Stat(home)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o700 {
		t.Fatalf("prefix mode = %o, want 0700", info.Mode().Perm())
	}
}

func TestStrictSettingsDocuments(t *testing.T) {
	secret := "SYNTHETIC_SECRET_DO_NOT_ECHO"
	validDocument := `{"model": "vendor/model", "reasoning": "high"}`
	cases := map[string]string{
		"valid":                   validDocument,
		"whitespace padded":       " \n\t" + validDocument + " \r\n",
		"exact size limit":        strings.Repeat(" ", maxSettingsBytes-len(validDocument)) + validDocument,
		"oversize valid document": strings.Repeat(" ", maxSettingsBytes+1-len(validDocument)) + validDocument,
		"truncated secret":        `{"model": "` + secret,
		"extra field with secret": `{"model": "vendor/model", "reasoning": "high", "api_key": "` + secret + `"}`,
		"secret in model value":   `{"model": "vendor/` + secret + `\n", "reasoning": "high"}`,
		"duplicate key":           `{"model": "vendor/a", "model": "vendor/b", "reasoning": "high"}`,
		"wrong case key":          `{"Model": "vendor/model", "reasoning": "high"}`,
		"null model":              `{"model": null, "reasoning": "high"}`,
		"missing reasoning":       `{"model": "vendor/model"}`,
		"number model":            `{"model": 3, "reasoning": "high"}`,
		"array document":          `["model", "reasoning"]`,
		"trailing document":       `{"model": "vendor/model", "reasoning": "high"} {}`,
		"trailing garbage":        `{"model": "vendor/model", "reasoning": "high"} x`,
		"invalid utf8":            "{\"model\": \"vendor/\xff\", \"reasoning\": \"high\"}",
		"oversize":                strings.Repeat(" ", 4097),
		"bad reasoning":           `{"model": "vendor/model", "reasoning": "turbo"}`,
		"model without provider":  `{"model": "missing-provider", "reasoning": "high"}`,
		"model over 200 chars":    `{"model": "v/` + strings.Repeat("a", 199) + `", "reasoning": "high"}`,
	}
	home := testHome(t)
	for name, contents := range cases {
		path := writeSettingsFile(t, home, contents)
		settings, err := ReadSettings(home)
		if name == "valid" || name == "whitespace padded" || name == "exact size limit" {
			if err != nil {
				t.Fatalf("%s: unexpected error: %v", name, err)
			}
			if settings.Model != "vendor/model" || settings.Reasoning != "high" {
				t.Fatalf("%s: got %+v", name, settings)
			}
			continue
		}
		if err == nil {
			t.Fatalf("%s: expected error, got %+v", name, settings)
		}
		if strings.Contains(err.Error(), secret) {
			t.Fatalf("%s: error echoes secret: %v", name, err)
		}
		if !strings.Contains(err.Error(), "config.json") {
			t.Fatalf("%s: error should identify the path: %v", name, err)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if string(data) != contents {
			t.Fatalf("%s: settings file was modified", name)
		}
	}
}

func TestWriteSettingsFormatAndPermissions(t *testing.T) {
	home := testHome(t)
	settings, err := WriteSettings(home, func(current Settings) Settings {
		current.Model = "vendor/first:free"
		current.Reasoning = "low"
		return current
	})
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(home, "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	want := "{\n  \"model\": \"vendor/first:free\",\n  \"reasoning\": \"low\"\n}\n"
	if string(data) != want {
		t.Fatalf("file = %q, want %q", data, want)
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(filepath.Join(home, "config.json"))
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0o600 {
			t.Fatalf("settings mode = %o, want 0600", info.Mode().Perm())
		}
	}
	if settings.Model != "vendor/first:free" || settings.Reasoning != "low" {
		t.Fatalf("got %+v", settings)
	}
}

func TestWriteSettingsMergesUnderLock(t *testing.T) {
	home := testHome(t)
	if _, err := WriteSettings(home, func(s Settings) Settings {
		s.Model = "vendor/original"
		s.Reasoning = "low"
		return s
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := WriteSettings(home, func(s Settings) Settings {
		s.Model = "vendor/changed"
		return s
	}); err != nil {
		t.Fatal(err)
	}
	settings, err := WriteSettings(home, func(s Settings) Settings {
		s.Reasoning = "max"
		return s
	})
	if err != nil {
		t.Fatal(err)
	}
	if settings.Model != "vendor/changed" || settings.Reasoning != "max" {
		t.Fatalf("got %+v", settings)
	}
}

func TestInvalidUpdateLeavesPreviousFile(t *testing.T) {
	home := testHome(t)
	if _, err := WriteSettings(home, func(s Settings) Settings {
		s.Model = "vendor/valid"
		return s
	}); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(home, "config.json")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, model := range []string{
		"vendor/a\"\napproval_policy=\"never",
		"vendor/$(echo injected)",
		"missing-provider",
	} {
		if _, err := WriteSettings(home, func(s Settings) Settings {
			s.Model = model
			return s
		}); err == nil {
			t.Fatalf("model %q: expected error", model)
		}
		after, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if string(after) != string(before) {
			t.Fatalf("model %q: settings file changed", model)
		}
	}
	entries, err := os.ReadDir(home)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".tmp-") {
			t.Fatalf("temporary file left behind: %s", entry.Name())
		}
	}
}

func TestLinkedSettingsAreRefused(t *testing.T) {
	home := testHome(t)
	path := writeSettingsFile(t, home, `{"model": "vendor/model", "reasoning": "high"}`)
	target := filepath.Join(t.TempDir(), "unrelated.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(target, path); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadSettings(home); err == nil {
		t.Fatal("hard-linked settings accepted")
	}
	if _, err := WriteSettings(home, func(s Settings) Settings {
		s.Model = "vendor/changed"
		return s
	}); err == nil {
		t.Fatal("write through hard-linked settings accepted")
	}
	after, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(data) {
		t.Fatal("write escaped to the link target")
	}
	if runtime.GOOS == "windows" {
		// Symlink creation needs extra privilege; the hardlink check above
		// is the portable part of this test.
		return
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, path); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadSettings(home); err == nil {
		t.Fatal("symlinked settings accepted")
	}
}

func TestPrefixSymlinkBoundary(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation needs extra privilege")
	}
	root := t.TempDir()
	target := filepath.Join(root, "target")
	if err := os.Mkdir(target, 0o700); err != nil {
		t.Fatal(err)
	}
	home := filepath.Join(root, "prefix")
	if err := os.Symlink(target, home); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CODEX_OPENROUTER_HOME", home)
	if _, err := ReadSettings(home); err == nil {
		t.Fatal("symlinked prefix accepted")
	}
	entries, err := os.ReadDir(target)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatal("files created through the symlinked prefix")
	}
	parent := filepath.Join(root, "parent-link")
	if err := os.Symlink(target, parent); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadSettings(filepath.Join(parent, "private")); err != nil {
		t.Fatalf("trusted linked parent refused: %v", err)
	}
	if err := os.Chmod(target, 0o770); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadSettings(filepath.Join(parent, "blocked")); err == nil {
		t.Fatal("writable resolved parent accepted")
	}
	if _, err := os.Lstat(filepath.Join(target, "blocked")); !os.IsNotExist(err) {
		t.Fatal("prefix created under writable resolved parent")
	}
}

func TestGroupWritableSettingsRefused(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permission checks do not apply")
	}
	home := testHome(t)
	path := writeSettingsFile(t, home, `{"model": "vendor/model", "reasoning": "high"}`)
	if err := os.Chmod(path, 0o666); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadSettings(home); err == nil {
		t.Fatal("group-writable settings accepted")
	}
	if err := os.Chmod(path, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(home, 0o777); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadSettings(home); err == nil {
		t.Fatal("group-writable prefix accepted")
	}
}

func TestUnsafeLockPathsAreRefused(t *testing.T) {
	home := testHome(t)
	if err := os.MkdirAll(home, 0o700); err != nil {
		t.Fatal(err)
	}
	lock := filepath.Join(home, ".config.lock")
	target := filepath.Join(t.TempDir(), "target")
	if err := os.WriteFile(target, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(target, lock); err != nil {
		t.Fatal(err)
	}
	if _, err := WriteSettings(home, func(s Settings) Settings { return s }); err == nil {
		t.Fatal("write proceeded with a hard-linked lock file")
	}
	if err := os.Remove(lock); err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" {
		if err := os.Symlink(target, lock); err != nil {
			t.Fatal(err)
		}
		if _, err := WriteSettings(home, func(s Settings) Settings { return s }); err == nil {
			t.Fatal("write proceeded with a symlinked lock file")
		}
		after, err := os.ReadFile(target)
		if err != nil {
			t.Fatal(err)
		}
		if string(after) != "x" {
			t.Fatal("lock handling modified the symlink target")
		}
	}
}

func TestHomeDirectoryContract(t *testing.T) {
	t.Setenv("CODEX_OPENROUTER_HOME", "")
	if _, err := HomeDirectory(); err == nil {
		t.Fatal("empty CODEX_OPENROUTER_HOME accepted")
	}
	t.Setenv("CODEX_OPENROUTER_HOME", "relative/path")
	if _, err := HomeDirectory(); err == nil {
		t.Fatal("relative CODEX_OPENROUTER_HOME accepted")
	}
	t.Setenv("CODEX_OPENROUTER_HOME", "/")
	if _, err := HomeDirectory(); err == nil {
		t.Fatal("root CODEX_OPENROUTER_HOME accepted")
	}
	t.Setenv("CODEX_OPENROUTER_HOME", "~/literal")
	if _, err := HomeDirectory(); err == nil {
		t.Fatal("literal tilde CODEX_OPENROUTER_HOME accepted")
	}
}

func TestValidatePrefixCleansBeforeValidating(t *testing.T) {
	if runtime.GOOS == "windows" {
		for _, input := range []string{`C:\a\..`, `D:\a\b\..\..\..`} {
			if _, err := ValidatePrefix(input); err == nil {
				t.Fatalf("%s normalized to a drive root and was accepted", input)
			}
		}
		cleaned, err := ValidatePrefix(`C:\Users\me\..\me\prefix`)
		if err != nil {
			t.Fatal(err)
		}
		if cleaned != `C:\Users\me\prefix` {
			t.Fatalf("got %q", cleaned)
		}
		return
	}
	for _, input := range []string{"/private/tmp/../..", "/a/..", "/a/b/../../.."} {
		if _, err := ValidatePrefix(input); err == nil {
			t.Fatalf("%s normalized to root and was accepted", input)
		}
	}
	cleaned, err := ValidatePrefix("/private/tmp/../tmp/ok")
	if err != nil {
		t.Fatal(err)
	}
	if cleaned != "/private/tmp/ok" {
		t.Fatalf("got %q", cleaned)
	}
}

func TestValidatePrefixWindowsBoundaries(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows path semantics")
	}
	for _, input := range []string{
		`C:folder`, `c:`, `C:\`,
		`\\server\share`, `//server/share/prefix`, `\\server`,
		`\\.\NUL`, `\\?\C:\x`, `//?/C:/x`, `//./NUL`,
		`\\server\share\prefix`, `/\server/share`,
	} {
		if _, err := ValidatePrefix(input); err == nil {
			t.Fatalf("%s accepted", input)
		}
	}
	if _, err := ValidatePrefix(`C:\Users\me\prefix`); err != nil {
		t.Fatalf("valid absolute path refused: %v", err)
	}
	if cleaned, err := ValidatePrefix(`c:/users/me/prefix`); err != nil || cleaned != `c:\users\me\prefix` {
		t.Fatalf("got %q, %v", cleaned, err)
	}
}

func TestWriteSettingsBusyAndCallerRetry(t *testing.T) {
	home := testHome(t)
	if _, err := WriteSettings(home, func(s Settings) Settings {
		s.Model = "vendor/original"
		s.Reasoning = "low"
		return s
	}); err != nil {
		t.Fatal(err)
	}
	release, err := platform.Lock(filepath.Join(home, ".config.lock"))
	if err != nil {
		t.Fatal(err)
	}
	_, err = WriteSettings(home, func(s Settings) Settings {
		s.Reasoning = "max"
		return s
	})
	if !errors.Is(err, platform.ErrBusy) {
		t.Fatalf("contended write: got %v, want the ErrBusy sentinel", err)
	}
	if err.Error() != "settings busy; retry" {
		t.Fatalf("contended write message: got %q", err)
	}
	if err := release(); err != nil {
		t.Fatal(err)
	}
	var observed Settings
	settings, err := WriteSettings(home, func(current Settings) Settings {
		observed = current
		current.Reasoning = "max"
		return current
	})
	if err != nil {
		t.Fatal(err)
	}
	if observed.Model != "vendor/original" {
		t.Fatalf("update did not read under the lock: %+v", observed)
	}
	if settings.Model != "vendor/original" || settings.Reasoning != "max" {
		t.Fatalf("got %+v", settings)
	}
}

func TestCoordinatedWritersMerge(t *testing.T) {
	home := testHome(t)
	if _, err := WriteSettings(home, func(s Settings) Settings { return s }); err != nil {
		t.Fatal(err)
	}
	release, err := platform.Lock(filepath.Join(home, ".config.lock"))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var group sync.WaitGroup
	errs := make(chan error, 2)
	write := func(update func(Settings) Settings) {
		defer group.Done()
		for {
			_, err := WriteSettings(home, update)
			if err == nil {
				return
			}
			if !errors.Is(err, platform.ErrBusy) {
				errs <- err
				return
			}
			select {
			case <-ctx.Done():
				errs <- ctx.Err()
				return
			case <-time.After(5 * time.Millisecond):
			}
		}
	}
	group.Add(2)
	go write(func(s Settings) Settings { s.Model = "vendor/merged"; return s })
	go write(func(s Settings) Settings { s.Reasoning = "max"; return s })
	if err := release(); err != nil {
		t.Fatal(err)
	}
	group.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	settings, err := ReadSettings(home)
	if err != nil {
		t.Fatal(err)
	}
	if settings.Model != "vendor/merged" || settings.Reasoning != "max" {
		t.Fatalf("lost update: got %+v", settings)
	}
}
