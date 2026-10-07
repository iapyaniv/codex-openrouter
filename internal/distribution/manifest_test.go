package distribution

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func repoRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	return root
}

func TestWrapperNoticeMatchesRootLicense(t *testing.T) {
	root, err := os.ReadFile(filepath.Join(repoRoot(t), "LICENSE"))
	if err != nil {
		t.Fatal(err)
	}
	notice, err := Notice("codex-openrouter-LICENSE.txt")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(root, notice) {
		t.Fatal("licenses/codex-openrouter-LICENSE.txt drifted from the repository root LICENSE")
	}
}

func TestCurrentTargetMatchesHost(t *testing.T) {
	target, err := CurrentTarget()
	if runtime.GOOS == "darwin" && runtime.GOARCH == "arm64" {
		if err != nil {
			t.Fatalf("darwin/arm64 must be published: %v", err)
		}
		if target.Entrypoint != "bin/codex" || len(target.Inventory) != target.Extraction.FileCount {
			t.Fatalf("unexpected target: %+v", target)
		}
		return
	}
	if err != nil && !strings.Contains(err.Error(), "no published") {
		t.Fatalf("unexpected target error: %v", err)
	}
}

func TestDevIdentityIsUnstamped(t *testing.T) {
	identity := CurrentReleaseIdentity("0.1.0-dev", "dev")
	if identity.IsRelease() {
		t.Fatal("a dev build must not identify as a release")
	}
	if _, err := NewInstallation(identity, []byte("helper")); err == nil {
		t.Fatal("an unstamped identity was certified as an installation")
	}
	if identity.Toolchain != runtime.Version() {
		t.Fatalf("toolchain %q, want the actual compiler %q", identity.Toolchain, runtime.Version())
	}
	again := CurrentReleaseIdentity("0.1.0-dev", "dev")
	if again.ID() != identity.ID() {
		t.Fatal("release ID must be deterministic for identical inputs")
	}
}

func TestReleaseIDBindsIdentity(t *testing.T) {
	base := CurrentReleaseIdentity("0.1.0", "stamped-a")
	variants := []ReleaseIdentity{
		CurrentReleaseIdentity("0.1.1", "stamped-a"),
		CurrentReleaseIdentity("0.1.0", "stamped-b"),
	}
	changedToolchain := base
	changedToolchain.Toolchain = "go9.9.9"
	variants = append(variants, changedToolchain)
	changedRecipe := base
	changedRecipe.Recipe = "other-recipe"
	variants = append(variants, changedRecipe)
	changedTarget := base
	changedTarget.Target = "other-target"
	variants = append(variants, changedTarget)
	changedManifest := base
	changedManifest.ArtifactDigest = strings.Repeat("a", 64)
	variants = append(variants, changedManifest)
	for _, variant := range variants {
		if variant.ID() == base.ID() {
			t.Fatalf("identity %+v collides with the base identity", variant)
		}
	}
	for _, r := range base.ID() {
		switch {
		case 'a' <= r && r <= 'z', 'A' <= r && r <= 'Z', '0' <= r && r <= '9', r == '.', r == '-', r == '_':
		default:
			t.Fatalf("release ID %q contains unsafe character %q", base.ID(), r)
		}
	}
}

func TestManifestValidationBoundaries(t *testing.T) {
	valid := func() Target {
		var target Target
		target.ID = "t"
		target.OS = "os"
		target.Arch = "arch"
		target.Entrypoint = "bin/codex"
		target.Archive.Format = "tar+gzip"
		target.Archive.URL = "https://example.invalid/a.tar.gz"
		target.Archive.Bytes = 10
		target.Archive.SHA256 = strings.Repeat("a", 64)
		target.Extraction.FileCount = 2
		target.Extraction.MaxFiles = 4
		target.Extraction.ExtractedBytes = 3
		target.Extraction.MaxBytes = 8
		target.Inventory = []InventoryEntry{
			{Path: "bin/codex", SHA256: strings.Repeat("b", 64), Bytes: 2, Executable: true},
			{Path: "data/x", SHA256: strings.Repeat("c", 64), Bytes: 1},
		}
		return target
	}
	if err := valid().validate(); err != nil {
		t.Fatalf("valid synthetic target rejected: %v", err)
	}
	cases := []struct {
		name   string
		mutate func(*Target)
	}{
		{"duplicate path", func(target *Target) {
			target.Inventory[1] = target.Inventory[0]
		}},
		{"case collision", func(target *Target) {
			target.Inventory[1].Path = "BIN/CODEX"
		}},
		{"entrypoint absent", func(target *Target) {
			target.Inventory[0].Path = "bin/other"
		}},
		{"entrypoint not executable", func(target *Target) {
			target.Inventory[0].Executable = false
		}},
		{"size total mismatch", func(target *Target) {
			target.Extraction.ExtractedBytes = 99
		}},
		{"count mismatch", func(target *Target) {
			target.Extraction.FileCount = 7
		}},
		{"traversal path", func(target *Target) {
			target.Inventory[1].Path = "../escape"
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			target := valid()
			tc.mutate(&target)
			if err := target.validate(); err == nil {
				t.Fatal("invalid target accepted")
			}
		})
	}
	for _, tc := range []struct {
		name   string
		mutate func(*manifest)
	}{
		{"missing source commit", func(decoded *manifest) { decoded.Release.SourceCommit = "" }},
		{"abbreviated source commit", func(decoded *manifest) { decoded.Release.SourceCommit = decoded.Release.SourceCommit[:12] }},
		{"nonhex source commit", func(decoded *manifest) { decoded.Release.SourceCommit = strings.Repeat("z", 40) }},
		{"missing release URL", func(decoded *manifest) { decoded.Release.ReleaseURL = "" }},
		{"nonHTTPS release URL", func(decoded *manifest) {
			decoded.Release.ReleaseURL = strings.Replace(decoded.Release.ReleaseURL, "https:", "http:", 1)
		}},
		{"release URL tag mismatch", func(decoded *manifest) { decoded.Release.UpstreamTag += "-other" }},
		{"malformed publication timestamp", func(decoded *manifest) { decoded.Release.PublishedAt = "not-a-timestamp" }},
		{"missing provenance", func(decoded *manifest) { decoded.Provenance = manifestProvenance{} }},
		{"blank checksum source", func(decoded *manifest) { decoded.Provenance.ChecksumSource = " \n" }},
		{"missing checksum asset digest", func(decoded *manifest) { decoded.Provenance.SHA256SumsAssetSHA256 = "" }},
		{"malformed checksum asset digest", func(decoded *manifest) { decoded.Provenance.SHA256SumsAssetSHA256 = strings.Repeat("g", 64) }},
		{"fetch date includes timestamp", func(decoded *manifest) { decoded.Provenance.FetchedAt += "T00:00:00Z" }},
		{"blank Sigstore assessment", func(decoded *manifest) { decoded.Provenance.Sigstore = "\t" }},
		{"missing Darwin team ID", func(decoded *manifest) { decoded.Provenance.CodeSignature.AppleTeamID = "" }},
		{"malformed Darwin team ID", func(decoded *manifest) { decoded.Provenance.CodeSignature.AppleTeamID = "not a team" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			decoded := *cachedManifest
			tc.mutate(&decoded)
			if err := decoded.validate(); err == nil {
				t.Fatal("invalid manifest audit metadata accepted")
			}
		})
	}
}
