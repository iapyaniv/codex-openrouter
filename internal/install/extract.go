package install

import (
	"archive/tar"
	"bufio"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"codex-openrouter/internal/distribution"
)

type boundedReader struct {
	ctx       context.Context
	source    io.Reader
	remaining int64
}

func (reader *boundedReader) Read(buffer []byte) (int, error) {
	if err := reader.ctx.Err(); err != nil {
		return 0, err
	}
	if int64(len(buffer)) > reader.remaining+1 {
		buffer = buffer[:reader.remaining+1]
	}
	n, err := reader.source.Read(buffer)
	reader.remaining -= int64(n)
	if reader.remaining < 0 {
		return n, errors.New("decompressed artifact exceeds its size bound")
	}
	return n, err
}

func extract(ctx context.Context, archive, destination string, target distribution.Target) error {
	file, err := os.Open(archive)
	if err != nil {
		return err
	}
	defer file.Close()
	compressed := bufio.NewReader(file)
	zip, err := gzip.NewReader(compressed)
	if err != nil {
		return errors.New("artifact is not valid gzip")
	}
	defer zip.Close()
	zip.Multistream(false)
	// Next consumes PAX/GNU metadata internally, so payload limits alone are insufficient.
	stream := &boundedReader{ctx: ctx, source: zip, remaining: target.Extraction.MaxBytes}
	reader := tar.NewReader(stream)
	files := make(map[string]distribution.InventoryEntry)
	directories := make(map[string]bool)
	for _, entry := range target.Inventory {
		files[entry.Path] = entry
		for parent := path.Dir(entry.Path); parent != "."; parent = path.Dir(parent) {
			directories[parent] = true
		}
	}
	if len(files) != target.Extraction.FileCount || len(directories) != target.Extraction.DirectoryCount {
		return errors.New("embedded extraction inventory is inconsistent")
	}
	seen := make(map[string]bool)
	regularCount, directoryCount := 0, 0
	var total int64
	for {
		header, err := reader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return errors.New("artifact tar stream is invalid or exceeds its bounds")
		}
		name := header.Name
		if header.Typeflag == tar.TypeDir {
			name = strings.TrimSuffix(name, "/")
		}
		if !safeArchivePath(name) || header.Mode&0o6000 != 0 {
			return errors.New("artifact contains a forbidden path or mode")
		}
		folded := strings.ToLower(name)
		if seen[folded] {
			return errors.New("artifact contains a duplicate or case-colliding path")
		}
		seen[folded] = true
		output := filepath.Join(destination, filepath.FromSlash(name))
		switch header.Typeflag {
		case tar.TypeDir:
			if !directories[name] || header.Size != 0 {
				return errors.New("artifact contains an unexpected directory")
			}
			directoryCount++
			if err := os.MkdirAll(output, 0o700); err != nil {
				return err
			}
		case tar.TypeReg, tar.TypeRegA:
			entry, ok := files[name]
			if !ok || header.Size != entry.Bytes {
				return errors.New("artifact contains an unexpected file or size")
			}
			regularCount++
			total += entry.Bytes
			if regularCount > target.Extraction.MaxFiles || total > target.Extraction.MaxBytes {
				return errors.New("artifact inventory exceeds its bounds")
			}
			if err := os.MkdirAll(filepath.Dir(output), 0o700); err != nil {
				return err
			}
			mode := os.FileMode(0o600)
			if entry.Executable {
				mode = 0o700
			}
			file, err := os.OpenFile(output, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
			if err != nil {
				return err
			}
			digest := sha256.New()
			n, copyErr := io.Copy(io.MultiWriter(file, digest), reader)
			syncErr := file.Sync()
			closeErr := file.Close()
			if copyErr != nil || syncErr != nil || closeErr != nil {
				return errors.Join(copyErr, syncErr, closeErr)
			}
			if n != entry.Bytes || hex.EncodeToString(digest.Sum(nil)) != entry.SHA256 {
				return errors.New("extracted file does not match the embedded inventory")
			}
		default:
			return errors.New("artifact contains a link or special entry")
		}
	}
	// Tar EOF can precede gzip EOF; draining checks the footer and bounds hidden trailing work.
	buffer := make([]byte, 32<<10)
	for {
		n, err := stream.Read(buffer)
		for _, value := range buffer[:n] {
			if value != 0 {
				return errors.New("artifact contains data after the tar end marker")
			}
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return errors.New("artifact gzip footer is invalid or its size bound was exceeded")
		}
	}
	if _, err := compressed.ReadByte(); err != io.EOF {
		return errors.New("artifact contains an additional compressed stream or trailing bytes")
	}
	if regularCount != target.Extraction.FileCount || directoryCount != target.Extraction.DirectoryCount || total != target.Extraction.ExtractedBytes {
		return fmt.Errorf("artifact inventory is incomplete")
	}
	return ctx.Err()
}

func safeArchivePath(value string) bool {
	if value == "" || !utf8.ValidString(value) || strings.HasPrefix(value, "/") || strings.ContainsAny(value, "\\:\x00") {
		return false
	}
	for _, part := range strings.Split(value, "/") {
		if part == "" || part == "." || part == ".." || strings.HasSuffix(part, ".") || strings.HasSuffix(part, " ") {
			return false
		}
		base := strings.ToUpper(strings.SplitN(part, ".", 2)[0])
		if base == "CON" || base == "PRN" || base == "AUX" || base == "NUL" || (len(base) == 4 && (strings.HasPrefix(base, "COM") || strings.HasPrefix(base, "LPT")) && '1' <= base[3] && base[3] <= '9') {
			return false
		}
	}
	return true
}
