package distribution

import (
	"os"
	"path/filepath"
	"testing"
)

func fixtureIdentity() ReleaseIdentity {
	return CurrentReleaseIdentity("0.1.0", "stamped-fixture")
}

func fixtureTarget() Target {
	return Target{
		ID:         "fixture-target",
		OS:         "fixture",
		Arch:       "fixture",
		Entrypoint: "bin/codex",
		Inventory: []InventoryEntry{
			{Path: "bin/codex", SHA256: sha256Of([]byte("native entrypoint")), Bytes: int64(len("native entrypoint")), Executable: true},
			{Path: "bin/helper", SHA256: sha256Of([]byte("native helper")), Bytes: int64(len("native helper")), Executable: true},
			{Path: "codex-package.json", SHA256: sha256Of(fixturePackageJSON), Bytes: int64(len(fixturePackageJSON))},
			{Path: "codex-resources/data.txt", SHA256: sha256Of([]byte("resource data")), Bytes: int64(len("resource data"))},
		},
	}
}

var fixturePackageJSON = []byte(`{"layoutVersion":1,"entrypoint":"bin/codex"}` + "\n")

func fixtureRelease(t *testing.T) (ReleasePaths, Target, ReleaseIdentity) {
	t.Helper()
	target := fixtureTarget()
	identity := fixtureIdentity()
	paths := ReleaseLayout(t.TempDir(), identity, target)
	write := func(path string, data []byte, perm os.FileMode) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, data, perm); err != nil {
			t.Fatal(err)
		}
	}
	contents := map[string][]byte{
		"bin/codex":                []byte("native entrypoint"),
		"bin/helper":               []byte("native helper"),
		"codex-package.json":       fixturePackageJSON,
		"codex-resources/data.txt": []byte("resource data"),
	}
	for _, entry := range target.Inventory {
		perm := os.FileMode(0o600)
		if entry.Executable {
			perm = 0o700
		}
		write(filepath.Join(paths.CodexTree, filepath.FromSlash(entry.Path)), contents[entry.Path], perm)
	}
	helper := []byte("fixture launcher helper bytes")
	write(paths.Helper, helper, 0o700)
	write(paths.AuditManifest, ManifestBytes(), 0o600)
	record, err := NewInstallation(identity, helper)
	if err != nil {
		t.Fatal(err)
	}
	data, err := FormatInstallation(record)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range NoticeNames() {
		notice, err := Notice(name)
		if err != nil {
			t.Fatal(err)
		}
		write(filepath.Join(paths.Licenses, name), notice, 0o600)
	}
	write(paths.Installation, data, 0o600)
	return paths, target, identity
}
