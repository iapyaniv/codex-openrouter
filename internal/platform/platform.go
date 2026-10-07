package platform

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// ErrBusy reports that a cooperating process holds the lock.
var ErrBusy = errors.New("busy")

func ReplacePrepared(temporary, destination string) error {
	return replace(temporary, destination)
}

type openMode int

const (
	openModeRead openMode = iota
	openModeLock
)

// Lock acquires a nonblocking exclusive OS lock on the file at path and
// returns its release function. The lock is bound to the OS handle, so a
// crashing process releases it without leaving a stale sentinel. When the
// file is missing it is created exclusively (O_EXCL) with private
// permissions, never truncating or following a link planted in between.
// An existing path must be a plain regular file without reparse points or
// extra hard links; see openChecked for the full check list.
func Lock(path string) (func() error, error) {
	file, err := openChecked(path, openModeLock)
	if err != nil {
		return nil, err
	}
	if err := lock(file); err != nil {
		file.Close()
		if errors.Is(err, ErrBusy) {
			return nil, ErrBusy
		}
		return nil, err
	}
	return func() error {
		err := unlock(file)
		return errors.Join(err, file.Close())
	}, nil
}

// OpenChecked opens a managed file for reading after validating both the
// directory entry and the opened handle: it must be a regular file without
// reparse points or extra hard links, privately owned where the OS exposes
// POSIX ownership. On Unix the opened handle is also compared against the
// pre-open metadata, catching a swap between check and open; on Windows
// that initial-identity comparison is not yet reliable (see
// files_windows.go). Callers still treat the directory itself as trusted
// against same-account hostile races.
func OpenChecked(path string) (*os.File, error) {
	return openChecked(path, openModeRead)
}

// ReplaceFile writes data to a uniquely named temporary file in the
// destination directory, syncs and closes it, then substitutes the
// destination with the platform replace operation. The destination is never
// removed first. When the operation fails before substitution, the previous
// file is retained and only this operation's temporary file is removed.
// Whether substitution is atomic, and whether an ambiguous failure can have
// already taken effect, is platform-specific; see the replace implementations
// in files_unix.go and files_windows.go for the honest limits.
func ReplaceFile(path string, data []byte, perm os.FileMode) (err error) {
	temp, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return fmt.Errorf("create temporary file: %w", err)
	}
	tempName := temp.Name()
	defer func() {
		if err != nil {
			os.Remove(tempName)
		}
	}()
	if err := temp.Chmod(perm); err != nil {
		temp.Close()
		return fmt.Errorf("set temporary file permissions: %w", err)
	}
	if _, err := temp.Write(data); err != nil {
		temp.Close()
		return fmt.Errorf("write temporary file: %w", err)
	}
	if err := temp.Sync(); err != nil {
		temp.Close()
		return fmt.Errorf("sync temporary file: %w", err)
	}
	if err := temp.Close(); err != nil {
		return fmt.Errorf("close temporary file: %w", err)
	}
	if err := replace(tempName, path); err != nil {
		return fmt.Errorf("replace file: %w", err)
	}
	return nil
}

// ReadFileLimit reads at most limit+1 bytes from reader and fails when the
// content exceeds limit, so oversize input is rejected while reading rather
// than trusted to a pre-read size.
func ReadFileLimit(reader io.Reader, limit int64) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(reader, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, errors.New("content exceeds size limit")
	}
	return data, nil
}
