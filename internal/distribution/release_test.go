package distribution

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestValidateReleaseAcceptsComplete(t *testing.T) {
	paths, target, identity := fixtureRelease(t)
	if err := ValidateRelease(paths, target, identity); err != nil {
		t.Fatalf("complete fixture release rejected: %v", err)
	}
}

func TestValidateReleaseRejectsDamage(t *testing.T) {
	unixOnly := runtime.GOOS == "windows"
	cases := []struct {
		name   string
		skip   bool
		damage func(t *testing.T, paths ReleasePaths, target Target)
		want   string
	}{
		{"helper absent", false, func(t *testing.T, paths ReleasePaths, target Target) {
			if err := os.Remove(paths.Helper); err != nil {
				t.Fatal(err)
			}
		}, "helper"},
		{"helper not executable", unixOnly, func(t *testing.T, paths ReleasePaths, target Target) {
			if err := os.Chmod(paths.Helper, 0o600); err != nil {
				t.Fatal(err)
			}
		}, "executable"},
		{"helper wrong bytes", false, func(t *testing.T, paths ReleasePaths, target Target) {
			data, err := os.ReadFile(paths.Helper)
			if err != nil {
				t.Fatal(err)
			}
			data[0] ^= 1
			if err := os.WriteFile(paths.Helper, data, 0o700); err != nil {
				t.Fatal(err)
			}
		}, "helper"},
		{"helper hardlinked", unixOnly, func(t *testing.T, paths ReleasePaths, target Target) {
			data, err := os.ReadFile(paths.Helper)
			if err != nil {
				t.Fatal(err)
			}
			twin := filepath.Join(t.TempDir(), "twin")
			if err := os.WriteFile(twin, data, 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.Remove(paths.Helper); err != nil {
				t.Fatal(err)
			}
			if err := os.Link(twin, paths.Helper); err != nil {
				t.Fatal(err)
			}
		}, "hard link"},
		{"completion metadata absent", false, func(t *testing.T, paths ReleasePaths, target Target) {
			if err := os.Remove(paths.Installation); err != nil {
				t.Fatal(err)
			}
		}, "completion metadata"},
		{"completion metadata empty", false, func(t *testing.T, paths ReleasePaths, target Target) {
			if err := os.WriteFile(paths.Installation, nil, 0o600); err != nil {
				t.Fatal(err)
			}
		}, "completion metadata"},
		{"completion metadata duplicate key", false, func(t *testing.T, paths ReleasePaths, target Target) {
			data, err := os.ReadFile(paths.Installation)
			if err != nil {
				t.Fatal(err)
			}
			data = bytes.Replace(data, []byte(`"schema": 1`), []byte(`"schema": 1, "schema": 1`), 1)
			if err := os.WriteFile(paths.Installation, data, 0o600); err != nil {
				t.Fatal(err)
			}
		}, "not valid installation JSON"},
		{"completion metadata unknown key", false, func(t *testing.T, paths ReleasePaths, target Target) {
			data, err := os.ReadFile(paths.Installation)
			if err != nil {
				t.Fatal(err)
			}
			data = bytes.Replace(data, []byte("{"), []byte(`{"executable":"/untrusted/codex",`), 1)
			if err := os.WriteFile(paths.Installation, data, 0o600); err != nil {
				t.Fatal(err)
			}
		}, "not valid installation JSON"},
		{"completion metadata invalid UTF-8", false, func(t *testing.T, paths ReleasePaths, target Target) {
			data, err := os.ReadFile(paths.Installation)
			if err != nil {
				t.Fatal(err)
			}
			data = bytes.Replace(data, []byte(`"version": "`), append([]byte(`"version": "`), 0xff), 1)
			if err := os.WriteFile(paths.Installation, data, 0o600); err != nil {
				t.Fatal(err)
			}
		}, "not valid installation JSON"},
		{"completion metadata wrong identity", false, func(t *testing.T, paths ReleasePaths, target Target) {
			helper, err := os.ReadFile(paths.Helper)
			if err != nil {
				t.Fatal(err)
			}
			other := CurrentReleaseIdentity("9.9.9", "stamped-elsewhere")
			record, err := NewInstallation(other, helper)
			if err != nil {
				t.Fatal(err)
			}
			data, err := FormatInstallation(record)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(paths.Installation, data, 0o600); err != nil {
				t.Fatal(err)
			}
		}, "does not match"},
		{"completion metadata oversized", false, func(t *testing.T, paths ReleasePaths, target Target) {
			bloated := append([]byte(`{"schema":1,"pad":"`), bytes.Repeat([]byte("x"), maxInstallationBytes)...)
			bloated = append(bloated, '"', '}')
			if err := os.WriteFile(paths.Installation, bloated, 0o600); err != nil {
				t.Fatal(err)
			}
		}, "completion metadata"},
		{"audit manifest replaced", false, func(t *testing.T, paths ReleasePaths, target Target) {
			if err := os.WriteFile(paths.AuditManifest, []byte("{}"), 0o600); err != nil {
				t.Fatal(err)
			}
		}, "audit manifest"},
		{"native file wrong size", false, func(t *testing.T, paths ReleasePaths, target Target) {
			path := filepath.Join(paths.CodexTree, "codex-resources", "data.txt")
			if err := os.WriteFile(path, []byte("short"), 0o600); err != nil {
				t.Fatal(err)
			}
		}, "bytes"},
		{"native file lost executable bit", unixOnly, func(t *testing.T, paths ReleasePaths, target Target) {
			path := filepath.Join(paths.CodexTree, "bin", "codex")
			if err := os.Chmod(path, 0o600); err != nil {
				t.Fatal(err)
			}
		}, "executable"},
		{"native file hardlinked", unixOnly, func(t *testing.T, paths ReleasePaths, target Target) {
			path := filepath.Join(paths.CodexTree, "bin", "helper")
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			twin := filepath.Join(t.TempDir(), "twin")
			if err := os.WriteFile(twin, data, 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.Remove(path); err != nil {
				t.Fatal(err)
			}
			if err := os.Link(twin, path); err != nil {
				t.Fatal(err)
			}
		}, "hard link"},
		{"native layout metadata corrupted", false, func(t *testing.T, paths ReleasePaths, target Target) {
			path := filepath.Join(paths.CodexTree, "codex-package.json")
			mutated := append(append([]byte(nil), fixturePackageJSON[:len(fixturePackageJSON)-2]...), ' ', '}')
			if err := os.WriteFile(path, mutated, 0o600); err != nil {
				t.Fatal(err)
			}
		}, "layout metadata"},
		{"managed file replaced by link", unixOnly, func(t *testing.T, paths ReleasePaths, target Target) {
			path := filepath.Join(paths.CodexTree, "codex-resources", "data.txt")
			if err := os.Remove(path); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink("/etc/passwd", path); err != nil {
				t.Fatal(err)
			}
		}, ""},
		{"managed directory replaced by link", unixOnly, func(t *testing.T, paths ReleasePaths, target Target) {
			directory := filepath.Join(paths.CodexTree, "codex-resources")
			if err := os.RemoveAll(directory); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(t.TempDir(), directory); err != nil {
				t.Fatal(err)
			}
		}, ""},
		{"releases parent is a link", unixOnly, func(t *testing.T, paths ReleasePaths, target Target) {
			releases := filepath.Dir(paths.Root)
			if err := os.Rename(releases, releases+".real"); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(releases+".real", releases); err != nil {
				t.Fatal(err)
			}
		}, "real directory"},
		{"release root writable by group", unixOnly, func(t *testing.T, paths ReleasePaths, target Target) {
			if err := os.Chmod(paths.Root, 0o770); err != nil {
				t.Fatal(err)
			}
		}, "writable"},
		{"nested native directory writable by group", unixOnly, func(t *testing.T, paths ReleasePaths, target Target) {
			if err := os.Chmod(filepath.Join(paths.CodexTree, "bin"), 0o770); err != nil {
				t.Fatal(err)
			}
		}, "writable"},
		{"codex tree writable by group", unixOnly, func(t *testing.T, paths ReleasePaths, target Target) {
			if err := os.Chmod(paths.CodexTree, 0o770); err != nil {
				t.Fatal(err)
			}
		}, "writable"},
		{"notice omitted", false, func(t *testing.T, paths ReleasePaths, target Target) {
			names := NoticeNames()
			if len(names) < 2 {
				t.Fatal("fixture expects several notices")
			}
			if err := os.Remove(filepath.Join(paths.Licenses, names[len(names)-1])); err != nil {
				t.Fatal(err)
			}
		}, "notice"},
		{"notice wrong bytes", false, func(t *testing.T, paths ReleasePaths, target Target) {
			name := NoticeNames()[0]
			if err := os.WriteFile(filepath.Join(paths.Licenses, name), []byte("substituted notice"), 0o600); err != nil {
				t.Fatal(err)
			}
		}, "notice"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.skip {
				t.Skip("requires Unix link/permission semantics")
			}
			paths, target, identity := fixtureRelease(t)
			tc.damage(t, paths, target)
			err := ValidateRelease(paths, target, identity)
			if err == nil {
				t.Fatal("damaged release accepted")
			}
			if tc.want != "" && !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error %q, want substring %q", err, tc.want)
			}
		})
	}
}
