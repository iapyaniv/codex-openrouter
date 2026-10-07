package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"debug/buildinfo"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"codex-openrouter/internal/distribution"
)

func TestExactCommittedCandidate(t *testing.T) {
	host, err := distribution.CurrentTarget()
	if err != nil || runtime.GOOS == "windows" {
		t.Skip("candidate packaging is tested on Unix hosts that the manifest publishes")
	}
	if testing.Short() {
		t.Skip("builds the launcher twice")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	root := t.TempDir()
	fixture := filepath.Join(root, "source")
	if err := os.Mkdir(fixture, 0o700); err != nil {
		t.Fatal(err)
	}
	working, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	repo := filepath.Join(working, "..", "..")
	for _, directory := range []string{"cmd", "internal"} {
		if err := os.CopyFS(filepath.Join(fixture, directory), os.DirFS(filepath.Join(repo, directory))); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"go.mod", ".go-version", "LICENSE"} {
		data, err := os.ReadFile(filepath.Join(repo, name))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(fixture, name), data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	tools, err := newBuildTools(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"init", "--quiet"}, {"add", "."},
		{"-c", "user.name=fixture", "-c", "user.email=fixture@example.invalid", "-c", "core.hooksPath=" + filepath.Join(root, "no-hooks"), "commit", "--quiet", "--no-gpg-sign", "-m", "fixture"},
	} {
		if _, err := tools.git(ctx, fixture, args...); err != nil {
			t.Fatal(err)
		}
	}
	head, err := tools.git(ctx, fixture, "rev-parse", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(fixture); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(working); err != nil {
			t.Error(err)
		}
	})
	options := packageOptions{version: "0.2.0-local", commit: strings.TrimSpace(string(head)), output: filepath.Join(root, "first"), targets: []string{host.ID}}
	if err := packageCandidate(ctx, options); err != nil {
		t.Fatal(err)
	}
	t.Run("missing linker stamp", func(t *testing.T) {
		data, err := os.ReadFile(filepath.Join(options.output, "candidate.json"))
		if err != nil {
			t.Fatal(err)
		}
		var release releaseMetadata
		if err := json.Unmarshal(data, &release); err != nil {
			t.Fatal(err)
		}
		metadata := release.Targets[host.ID]
		binary := filepath.Join(root, "missing-stamp")
		flags := "-X codex-openrouter/internal/launcher.Version=" + metadata.Identity.Version + " -X codex-openrouter/internal/launcher.MissingBuildID=" + metadata.Identity.BuildID
		if _, err := tools.command(ctx, fixture, targetEnvironment(host), tools.goBinary, "build", "-trimpath", "-buildvcs=false", "-ldflags", flags, "-o", binary, "./cmd/codex-openrouter"); err != nil {
			t.Fatal(err)
		}
		archive := filepath.Join(options.output, "codex-openrouter_"+options.version+"_"+host.OS+"_"+host.Arch+".tar.gz")
		valid, err := buildinfo.ReadFile(extractExecutable(t, archive, filepath.Join(root, "valid")))
		if err != nil {
			t.Fatal(err)
		}
		unstamped, err := buildinfo.ReadFile(binary)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(valid, unstamped) {
			t.Fatal("missing-stamp binary did not retain the valid candidate's Go build metadata")
		}
		if err := tools.verifyBinary(ctx, binary, metadata.Identity, host); err == nil {
			t.Fatal("verification accepted a missing linker stamp despite valid Go build metadata")
		}
	})
	ignoredCandidate := filepath.Join(fixture, "internal", "launcher", "ignored.go")
	ignoredTool := filepath.Join(fixture, "cmd", "package-candidate", "ignored.go")
	if err := os.WriteFile(filepath.Join(fixture, ".git", "info", "exclude"), []byte("/internal/launcher/ignored.go\n/cmd/package-candidate/ignored.go\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(ignoredCandidate, []byte("package launcher\nfunc init() { panic(\"uncommitted input was built\") }\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(fixture, ".git", "info", "attributes"), []byte("LICENSE export-ignore\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := tools.git(ctx, fixture, "config", "tar.umask", "0077"); err != nil {
		t.Fatal(err)
	}
	options.output = filepath.Join(root, "second")
	if err := packageCandidate(ctx, options); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(filepath.Join(root, "first"))
	if err != nil {
		t.Fatal(err)
	}
	artifacts := make(map[string][]byte)
	for _, entry := range entries {
		first, err := os.ReadFile(filepath.Join(root, "first", entry.Name()))
		if err != nil {
			t.Fatal(err)
		}
		artifacts[entry.Name()] = first
		second, err := os.ReadFile(filepath.Join(root, "second", entry.Name()))
		if err != nil || !bytes.Equal(first, second) {
			t.Fatalf("ignored source changed candidate artifact %s: %v", entry.Name(), err)
		}
	}
	for _, name := range []string{"ignored tool input", "dirty source", "different commit", "unsafe version", "existing output", "source case alias", "committed license drift"} {
		t.Run(name, func(t *testing.T) {
			invalid := options
			invalid.output = filepath.Join(root, "refused")
			switch name {
			case "ignored tool input":
				if err := os.WriteFile(ignoredTool, []byte("package main\nconst hiddenInput = 1\n"), 0o600); err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { os.Remove(ignoredTool) })
			case "dirty source":
				file := filepath.Join(fixture, "cmd", "codex-openrouter", "main.go")
				original, err := os.ReadFile(file)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(file, append(original, '\n'), 0o600); err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { os.WriteFile(file, original, 0o600) })
			case "different commit":
				invalid.commit = strings.Repeat("0", 40)
			case "unsafe version":
				invalid.version = "0.2.0-local/escape"
			case "existing output":
				invalid.output = filepath.Join(root, "first")
			case "source case alias":
				alias := strings.ToUpper(fixture)
				originalInfo, err := os.Stat(fixture)
				if err != nil {
					t.Fatal(err)
				}
				aliasInfo, err := os.Stat(alias)
				if os.IsNotExist(err) {
					t.Skip("filesystem has no directory case alias")
				}
				if err != nil {
					t.Fatal(err)
				}
				if !os.SameFile(originalInfo, aliasInfo) {
					t.Skip("uppercase directory names a different filesystem entry")
				}
				invalid.output = filepath.Join(alias, "refused")
			case "committed license drift":
				if err := os.WriteFile(filepath.Join(fixture, "LICENSE"), []byte("different license\n"), 0o600); err != nil {
					t.Fatal(err)
				}
				if _, err := tools.git(ctx, fixture, "-c", "user.name=fixture", "-c", "user.email=fixture@example.invalid", "-c", "core.hooksPath="+filepath.Join(root, "no-hooks"), "commit", "--quiet", "--no-gpg-sign", "-am", "license drift"); err != nil {
					t.Fatal(err)
				}
				head, err := tools.git(ctx, fixture, "rev-parse", "HEAD")
				if err != nil {
					t.Fatal(err)
				}
				invalid.commit = strings.TrimSpace(string(head))
			}
			if err := packageCandidate(ctx, invalid); err == nil {
				t.Fatal("invalid packaging request succeeded")
			}
			if name != "existing output" {
				if _, err := os.Stat(invalid.output); !os.IsNotExist(err) {
					t.Fatal("refused packaging request created an output directory")
				}
			}
		})
	}
	for name, original := range artifacts {
		current, err := os.ReadFile(filepath.Join(root, "first", name))
		if err != nil || !bytes.Equal(original, current) {
			t.Fatalf("refused packaging request changed existing artifact %s: %v", name, err)
		}
	}
}

func extractExecutable(t *testing.T, archive, destination string) string {
	t.Helper()
	file, err := os.Open(archive)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	stream, err := gzip.NewReader(file)
	if err != nil {
		t.Fatal(err)
	}
	reader := tar.NewReader(stream)
	for {
		header, err := reader.Next()
		if err != nil {
			t.Fatalf("archive has no launcher: %v", err)
		}
		if header.Name == "codex-openrouter" {
			data, err := io.ReadAll(reader)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(destination, data, 0o700); err != nil {
				t.Fatal(err)
			}
			return destination
		}
	}
}
