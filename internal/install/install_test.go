package install

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"codex-openrouter/internal/distribution"
	"codex-openrouter/internal/platform"
)

func testSource(t *testing.T, root string, data []byte) string {
	t.Helper()
	path := filepath.Join(root, "downloaded-launcher")
	if err := os.WriteFile(path, data, 0o700); err != nil {
		t.Fatal(err)
	}
	return path
}
func archiveTransport(data []byte) http.RoundTripper {
	return roundTripFunc(func(request *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(bytes.NewReader(data)), Request: request}, nil
	})
}
func noProbe(context.Context, string, string, string) error { return nil }

func TestTransactionFailureBoundaries(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix installer activation")
	}
	for _, name := range []string{"hash before probe", "partial download", "bad probe", "publication failure", "activation failure", "cancel before activation", "cleanup after activation", "ambiguous new activation"} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			prefix := filepath.Join(root, "prefix")
			if err := os.Mkdir(prefix, 0o700); err != nil {
				t.Fatal(err)
			}
			settings := []byte("{\"model\":\"vendor/unchanged\",\"reasoning\":\"medium\"}\n")
			settingsPath := filepath.Join(prefix, "config.json")
			if err := os.WriteFile(settingsPath, settings, 0o600); err != nil {
				t.Fatal(err)
			}
			data, target := bundle(t, nil)
			identity := distribution.CurrentReleaseIdentity("0.1.0-test", "fixture-one")
			source := testSource(t, root, []byte("launcher one"))
			ops := operations{transport: archiveTransport(data), probe: noProbe}
			if _, err := run(context.Background(), prefix, identity, target, source, io.Discard, ops); err != nil {
				t.Fatal(err)
			}
			public := filepath.Join(prefix, "bin", "codex-openrouter")
			old, err := os.ReadFile(public)
			if err != nil {
				t.Fatal(err)
			}
			identity = distribution.CurrentReleaseIdentity("0.2.0-test", "fixture-two")
			newBytes := []byte("launcher two")
			source = testSource(t, root, newBytes)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			probeCalls := 0
			ops.probe = func(context.Context, string, string, string) error { probeCalls++; return nil }
			switch name {
			case "hash before probe":
				target.Archive.SHA256 = strings.Repeat("0", 64)
			case "partial download":
				ops.transport = roundTripFunc(func(request *http.Request) (*http.Response, error) {
					return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(&brokenReader{prefix: bytes.NewReader(data[:len(data)/2])}), Request: request}, nil
				})
			case "bad probe":
				ops.probe = func(context.Context, string, string, string) error {
					probeCalls++
					return errors.New("startup probe failed")
				}
			case "publication failure":
				if os.Geteuid() == 0 {
					t.Skip("root bypasses directory write permissions")
				}
				releases := filepath.Join(prefix, "releases")
				if err := os.Chmod(releases, 0o500); err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() {
					if err := os.Chmod(releases, 0o700); err != nil {
						t.Error(err)
					}
				})
			case "activation failure":
				ops.replace = func(string, string) error { return errors.New("activation refused") }
			case "cancel before activation":
				unused := ops
				unused.replace = func(string, string) error { return errors.New("leave complete release inactive") }
				if _, err := run(context.Background(), prefix, identity, target, source, io.Discard, unused); err == nil {
					t.Fatal("inactive-release preparation unexpectedly activated")
				}
				ops.probe = func(context.Context, string, string, string) error { cancel(); return nil }
			case "cleanup after activation":
				ops.cleanup = func(string) error { return errors.New("transaction cleanup failed") }
			case "ambiguous new activation":
				ops.replace = func(temp, dest string) error {
					if err := platform.ReplacePrepared(temp, dest); err != nil {
						return err
					}
					return errors.New("replacement result unavailable")
				}
			}
			_, err = run(ctx, prefix, identity, target, source, io.Discard, ops)
			if err == nil {
				t.Fatal("failed transaction reported success")
			}
			current, e := os.ReadFile(public)
			if e != nil {
				t.Fatal(e)
			}
			if name == "cleanup after activation" || name == "ambiguous new activation" {
				if !bytes.Equal(current, newBytes) || !strings.Contains(err.Error(), "installed at") {
					t.Fatalf("new state not reported: %v", err)
				}
			} else if !bytes.Equal(current, old) {
				t.Fatal("preactivation failure changed old public executable")
			}
			if (name == "hash before probe" || name == "partial download") && probeCalls != 0 {
				t.Fatal("unverified download executed a native probe")
			}
			if name == "publication failure" {
				if !errors.Is(err, os.ErrPermission) || probeCalls != 1 {
					t.Fatalf("did not reach release publication: probes=%d, error=%v", probeCalls, err)
				}
				if _, e := os.Stat(distribution.ReleaseLayout(prefix, identity, target).Root); !os.IsNotExist(e) {
					t.Fatal("failed publication left a new release")
				}
			}
			after, e := os.ReadFile(settingsPath)
			if e != nil || !bytes.Equal(after, settings) {
				t.Fatal("installation changed settings")
			}
			entries, e := os.ReadDir(prefix)
			if e != nil {
				t.Fatal(e)
			}
			if name != "cleanup after activation" {
				for _, entry := range entries {
					if strings.HasPrefix(entry.Name(), ".staging-") {
						t.Fatal("failed transaction left its staging directory")
					}
				}
			}
		})
	}
}

func TestRepeatedUpgradeDowngradeAndSelfInstall(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix installer activation")
	}
	root := t.TempDir()
	prefix := filepath.Join(root, "prefix")
	if err := os.Mkdir(prefix, 0o700); err != nil {
		t.Fatal(err)
	}
	data, target := bundle(t, nil)
	first := distribution.CurrentReleaseIdentity("0.1.0-test", "fixture-one")
	second := distribution.CurrentReleaseIdentity("0.2.0-test", "fixture-two")
	ops := operations{transport: archiveTransport(data), probe: noProbe}
	for i, identity := range []distribution.ReleaseIdentity{first, first, second, first} {
		helper := []byte("launcher one")
		if identity == second {
			helper = []byte("launcher two")
		}
		source := testSource(t, root, helper)
		if i == 1 {
			ops.transport = roundTripFunc(func(*http.Request) (*http.Response, error) {
				t.Fatal("repeat installation downloaded an already verified release")
				return nil, nil
			})
		} else {
			ops.transport = archiveTransport(data)
		}
		result, err := run(context.Background(), prefix, identity, target, source, io.Discard, ops)
		if err != nil {
			t.Fatal(err)
		}
		public, e := os.ReadFile(result.PublicPath)
		if e != nil || !bytes.Equal(public, helper) {
			t.Fatal("version selection did not activate its own bytes")
		}
		if _, e := os.Stat(filepath.Join(prefix, "config.json")); !os.IsNotExist(e) {
			t.Fatal("first install created settings")
		}
	}
	for _, identity := range []distribution.ReleaseIdentity{first, second} {
		if err := distribution.VerifyRelease(distribution.ReleaseLayout(prefix, identity, target), target, identity); err != nil {
			t.Fatal(err)
		}
	}
	public := filepath.Join(prefix, "bin", "codex-openrouter")
	if _, err := run(context.Background(), prefix, first, target, public, io.Discard, ops); err == nil || !strings.Contains(err.Error(), "downloaded copy") {
		t.Fatalf("installed public command was accepted as source: %v", err)
	}
	unlock, err := platform.Lock(filepath.Join(prefix, ".install.lock"))
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	if _, err := run(context.Background(), prefix, first, target, public, io.Discard, ops); !errors.Is(err, platform.ErrBusy) {
		t.Fatalf("contending installer: %v", err)
	}
}

func TestPublicEntryRecognition(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix public links")
	}
	for _, name := range []string{"unrelated file", "unrelated link", "old artifact digest"} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			prefix := filepath.Join(root, "prefix")
			if err := os.Mkdir(prefix, 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.Mkdir(filepath.Join(prefix, "bin"), 0o700); err != nil {
				t.Fatal(err)
			}
			data, target := bundle(t, nil)
			identity := distribution.CurrentReleaseIdentity("0.2.0-test", "fixture-new")
			sourceBytes := []byte("new launcher")
			source := testSource(t, root, sourceBytes)
			ops := operations{transport: archiveTransport(data), probe: noProbe}
			public := filepath.Join(prefix, "bin", "codex-openrouter")
			var link string
			switch name {
			case "unrelated link":
				link = source
			case "unrelated file":
				if err := os.WriteFile(public, []byte("unrelated executable"), 0o700); err != nil {
					t.Fatal(err)
				}
			case "old artifact digest":
				old := distribution.CurrentReleaseIdentity("0.1.0-test", "fixture-old")
				oldSource := testSource(t, root, []byte("old launcher"))
				if _, err := run(context.Background(), prefix, old, target, oldSource, io.Discard, ops); err != nil {
					t.Fatal(err)
				}
				oldPaths := distribution.ReleaseLayout(prefix, old, target)
				audit := []byte("prior trusted artifact lock")
				old.ArtifactDigest = sum(audit)
				moved := distribution.ReleaseLayout(prefix, old, target)
				if err := os.Rename(oldPaths.Root, moved.Root); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(moved.AuditManifest, audit, 0o600); err != nil {
					t.Fatal(err)
				}
				helper, err := os.ReadFile(moved.Helper)
				if err != nil {
					t.Fatal(err)
				}
				record, err := distribution.NewInstallation(old, helper)
				if err != nil {
					t.Fatal(err)
				}
				metadata, err := distribution.FormatInstallation(record)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(moved.Installation, metadata, 0o600); err != nil {
					t.Fatal(err)
				}
				source = testSource(t, root, sourceBytes)
			}
			if link != "" {
				if err := os.Symlink(link, public); err != nil {
					t.Fatal(err)
				}
			}
			_, err := run(context.Background(), prefix, identity, target, source, io.Discard, ops)
			if strings.HasPrefix(name, "unrelated") {
				if err == nil {
					t.Fatal("unrecognized public entry overwritten")
				}
				if link != "" {
					after, e := os.Readlink(public)
					if e != nil || after != link {
						t.Fatal("refused public link changed")
					}
				} else {
					b, e := os.ReadFile(public)
					if e != nil || string(b) != "unrelated executable" {
						t.Fatal("refused public file changed")
					}
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			b, e := os.ReadFile(public)
			if e != nil || !bytes.Equal(b, sourceBytes) {
				t.Fatal("recognized public entry was not replaced")
			}
		})
	}
}

func TestDamagedReleaseRequiresExplicitMoveAside(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix installer activation")
	}
	root := t.TempDir()
	prefix := filepath.Join(root, "prefix")
	if err := os.Mkdir(prefix, 0o700); err != nil {
		t.Fatal(err)
	}
	data, target := bundle(t, nil)
	identity := distribution.CurrentReleaseIdentity("0.1.0-test", "fixture-repair")
	source := testSource(t, root, []byte("known launcher"))
	ops := operations{transport: archiveTransport(data), probe: noProbe}
	if _, err := run(context.Background(), prefix, identity, target, source, io.Discard, ops); err != nil {
		t.Fatal(err)
	}
	paths := distribution.ReleaseLayout(prefix, identity, target)
	native, err := os.ReadFile(paths.CodexBinary)
	if err != nil {
		t.Fatal(err)
	}
	native[0] ^= 1
	if err := os.WriteFile(paths.CodexBinary, native, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := run(context.Background(), prefix, identity, target, source, io.Discard, ops); err == nil || !strings.Contains(err.Error(), "move aside") {
		t.Fatalf("damaged release reuse: %v", err)
	}
	if err := os.Rename(paths.Root, paths.Root+".moved-aside"); err != nil {
		t.Fatal(err)
	}
	if _, err := run(context.Background(), prefix, identity, target, source, io.Discard, ops); err != nil {
		t.Fatal(err)
	}
	if err := distribution.VerifyRelease(paths, target, identity); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(paths.Root + ".moved-aside"); err != nil {
		t.Fatal("explicitly moved release was removed")
	}
}
