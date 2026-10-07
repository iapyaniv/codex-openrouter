package install

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"codex-openrouter/internal/distribution"
)

type tarEntry struct {
	header tar.Header
	data   []byte
}

func bundle(t *testing.T, mutate func([]tarEntry) []tarEntry) ([]byte, distribution.Target) {
	t.Helper()
	entries := []tarEntry{
		{tar.Header{Name: "bin", Typeflag: tar.TypeDir, Mode: 0o755}, nil},
		{tar.Header{Name: "bin/codex", Typeflag: tar.TypeReg, Mode: 0o755}, []byte("native fixture")},
		{tar.Header{Name: "codex-package.json", Typeflag: tar.TypeReg, Mode: 0o644}, []byte("{\"layoutVersion\":1}\n")},
	}
	target := distribution.Target{Entrypoint: "bin/codex"}
	target.Extraction.FileCount, target.Extraction.DirectoryCount = 2, 1
	target.Extraction.MaxFiles, target.Extraction.MaxBytes = 4, 32<<10
	for i := range entries {
		entries[i].header.Size = int64(len(entries[i].data))
		if entries[i].header.Typeflag == tar.TypeReg {
			target.Inventory = append(target.Inventory, distribution.InventoryEntry{Path: entries[i].header.Name, Bytes: entries[i].header.Size, SHA256: sum(entries[i].data), Executable: entries[i].header.Mode&0o100 != 0})
			target.Extraction.ExtractedBytes += entries[i].header.Size
		}
	}
	if mutate != nil {
		entries = mutate(entries)
	}
	var compressed bytes.Buffer
	zip := gzip.NewWriter(&compressed)
	writer := tar.NewWriter(zip)
	for _, entry := range entries {
		if err := writer.WriteHeader(&entry.header); err != nil {
			t.Fatal(err)
		}
		if _, err := writer.Write(entry.data); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := zip.Close(); err != nil {
		t.Fatal(err)
	}
	target.Archive.URL, target.Archive.Bytes = "https://artifact.example/native.tar.gz", int64(compressed.Len())
	target.Archive.SHA256 = sum(compressed.Bytes())
	target.Archive.RedirectHosts = []string{"assets.example"}
	return compressed.Bytes(), target
}

func TestExtractInventoryAndUnsafeArchives(t *testing.T) {
	cases := []struct {
		name   string
		mutate func([]tarEntry) []tarEntry
	}{
		{"complete", nil},
		{"traversal", func(e []tarEntry) []tarEntry { e[1].header.Name = "../escape"; return e }},
		{"absolute", func(e []tarEntry) []tarEntry { e[1].header.Name = "/escape"; return e }},
		{"drive and stream", func(e []tarEntry) []tarEntry { e[1].header.Name = "C:escape:stream"; return e }},
		{"reserved name", func(e []tarEntry) []tarEntry { e[1].header.Name = "bin/CON.txt"; return e }},
		{"trailing alias", func(e []tarEntry) []tarEntry { e[1].header.Name = "bin/codex."; return e }},
		{"unknown", func(e []tarEntry) []tarEntry { e[1].header.Name = "bin/unknown"; return e }},
		{"missing", func(e []tarEntry) []tarEntry { return e[:2] }},
		{"duplicate", func(e []tarEntry) []tarEntry { return append(e, e[1]) }},
		{"case collision", func(e []tarEntry) []tarEntry { more := e[1]; more.header.Name = "BIN/CODEX"; return append(e, more) }},
		{"symlink", func(e []tarEntry) []tarEntry {
			e[1].header.Typeflag = tar.TypeSymlink
			e[1].header.Linkname = "elsewhere"
			e[1].header.Size = 0
			e[1].data = nil
			return e
		}},
		{"hardlink", func(e []tarEntry) []tarEntry {
			e[1].header.Typeflag = tar.TypeLink
			e[1].header.Linkname = "codex-package.json"
			e[1].header.Size = 0
			e[1].data = nil
			return e
		}},
		{"fifo", func(e []tarEntry) []tarEntry {
			e[1].header.Typeflag = tar.TypeFifo
			e[1].header.Size = 0
			e[1].data = nil
			return e
		}},
		{"setuid", func(e []tarEntry) []tarEntry { e[1].header.Mode |= 0o4000; return e }},
		{"wrong size", func(e []tarEntry) []tarEntry { e[1].data = append(e[1].data, 'x'); e[1].header.Size++; return e }},
		{"wrong hash", func(e []tarEntry) []tarEntry { e[1].data[0] ^= 1; return e }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			mutate := tc.mutate
			if tc.name == "absolute" {
				mutate = func(e []tarEntry) []tarEntry { e[1].header.Name = filepath.Join(root, "absolute-escape"); return e }
			}
			data, target := bundle(t, mutate)
			archive := filepath.Join(root, "archive.gz")
			if err := os.WriteFile(archive, data, 0o600); err != nil {
				t.Fatal(err)
			}
			dest := filepath.Join(root, "tree")
			err := extract(context.Background(), archive, dest, target)
			if tc.mutate != nil {
				if err == nil {
					t.Fatal("unsafe or incomplete archive accepted")
				}
				for _, path := range []string{filepath.Join(root, "escape"), filepath.Join(root, "absolute-escape")} {
					if _, err := os.Lstat(path); !os.IsNotExist(err) {
						t.Fatal("archive wrote outside the extraction directory")
					}
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			for _, entry := range target.Inventory {
				b, err := os.ReadFile(filepath.Join(dest, entry.Path))
				if err != nil || sum(b) != entry.SHA256 {
					t.Fatal("extracted bytes changed")
				}
			}
		})
	}
}

func TestExtractHiddenMetadataFooterAndCancellation(t *testing.T) {
	data, target := bundle(t, nil)
	for _, name := range []string{"metadata bound", "footer corruption", "trailing data", "extra gzip member", "cancel"} {
		t.Run(name, func(t *testing.T) {
			archiveData := bytes.Clone(data)
			candidate := target
			ctx := context.Background()
			switch name {
			case "metadata bound":
				archiveData, _ = bundle(t, func(e []tarEntry) []tarEntry {
					e[1].header.PAXRecords = map[string]string{"comment": strings.Repeat("x", 64<<10)}
					e[1].header.Format = tar.FormatPAX
					return e
				})
			case "footer corruption":
				archiveData[len(archiveData)-8] ^= 1
			case "trailing data":
				var raw bytes.Buffer
				z := gzip.NewWriter(&raw)
				r, err := gzip.NewReader(bytes.NewReader(data))
				if err != nil {
					t.Fatal(err)
				}
				if _, err := io.Copy(z, r); err != nil {
					t.Fatal(err)
				}
				z.Write([]byte("unexpected"))
				z.Close()
				archiveData = raw.Bytes()
			case "extra gzip member":
				archiveData = append(archiveData, data...)
			case "cancel":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}
			root := t.TempDir()
			archive := filepath.Join(root, "archive.gz")
			if err := os.WriteFile(archive, archiveData, 0o600); err != nil {
				t.Fatal(err)
			}
			err := extract(ctx, archive, filepath.Join(root, "tree"), candidate)
			if err == nil {
				t.Fatal("unbounded, damaged, or canceled extraction accepted")
			}
			if name == "cancel" && !errors.Is(err, context.Canceled) {
				t.Fatalf("cancellation: %v", err)
			}
		})
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) { return fn(request) }

func TestDownloadIntegrityRedirectsAndRedaction(t *testing.T) {
	data, target := bundle(t, nil)
	for _, name := range []string{"complete", "short", "oversize", "wrong hash", "status", "interrupt", "cancel", "allowed redirect", "HTTP redirect", "wrong host", "userinfo", "wrong port", "redirect loop", "proxy error"} {
		t.Run(name, func(t *testing.T) {
			calls := 0
			ctx := context.Background()
			var cancel context.CancelFunc
			if name == "cancel" {
				ctx, cancel = context.WithCancel(ctx)
				defer cancel()
			}
			transport := roundTripFunc(func(request *http.Request) (*http.Response, error) {
				calls++
				if name == "proxy error" {
					return nil, errors.New("proxy user:private-password at https://assets.example/?signed=private-query")
				}
				if name == "cancel" {
					cancel()
					return nil, ctx.Err()
				}
				response := &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(bytes.NewReader(data)), Request: request}
				switch name {
				case "short":
					response.Body = io.NopCloser(bytes.NewReader(data[:len(data)-1]))
				case "oversize":
					response.Body = io.NopCloser(bytes.NewReader(append(bytes.Clone(data), 'x')))
				case "wrong hash":
					body := bytes.Clone(data)
					body[0] ^= 1
					response.Body = io.NopCloser(bytes.NewReader(body))
				case "status":
					response.StatusCode = 403
					response.Body = io.NopCloser(strings.NewReader("private-response"))
				case "interrupt":
					response.Body = io.NopCloser(&brokenReader{prefix: bytes.NewReader(data[:len(data)/2])})
				case "allowed redirect", "HTTP redirect", "wrong host", "userinfo", "wrong port", "redirect loop":
					if calls == 1 || name == "redirect loop" {
						response.StatusCode = 302
						location := "https://assets.example/native?signature=private-query"
						switch name {
						case "HTTP redirect":
							location = "http://assets.example/native"
						case "wrong host":
							location = "https://untrusted.example/native"
						case "userinfo":
							location = "https://user:private-password@assets.example/native"
						case "wrong port":
							location = "https://assets.example:444/native"
						}
						response.Header.Set("Location", location)
					}
				}
				return response, nil
			})
			output := filepath.Join(t.TempDir(), "artifact.gz")
			err := download(ctx, target, output, transport)
			if name == "complete" || name == "allowed redirect" {
				if err != nil {
					t.Fatal(err)
				}
				b, e := os.ReadFile(output)
				if e != nil || !bytes.Equal(b, data) {
					t.Fatal("download changed bytes")
				}
				return
			}
			if err == nil {
				t.Fatal("bad download accepted")
			}
			for _, secret := range []string{"private-query", "private-password", "private-response"} {
				if strings.Contains(err.Error(), secret) {
					t.Fatal("download diagnostic exposed sensitive remote data")
				}
			}
			if name == "cancel" && !errors.Is(err, context.Canceled) {
				t.Fatalf("cancellation: %v", err)
			}
			if name == "redirect loop" && calls != 6 {
				t.Fatalf("redirect bound: %d", calls)
			}
		})
	}
}

type brokenReader struct{ prefix *bytes.Reader }

func (reader *brokenReader) Read(buffer []byte) (int, error) {
	if reader.prefix.Len() > 0 {
		return reader.prefix.Read(buffer)
	}
	return 0, errors.New("private-response")
}
