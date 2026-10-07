//go:build unix

package platform

import (
	"errors"
	"fmt"
	"os"
	"syscall"
)

func lock(file *os.File) error {
	err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
	if errors.Is(err, syscall.EWOULDBLOCK) {
		return ErrBusy
	}
	return err
}

func unlock(file *os.File) error {
	return syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
}

// replace is a plain rename: on Unix a same-directory rename atomically
// substitutes an existing destination, so after rename(2) returns success
// the new content is in place and after it returns failure the old content
// is. The destination must share the filesystem with the temporary file;
// os.CreateTemp in the destination directory guarantees that.
func replace(temp, path string) error {
	return os.Rename(temp, path)
}

// openChecked opens path only after the directory entry passes the type,
// link, and privacy checks, then repeats the identity and type checks on the
// opened handle so a swap between check and open is caught. os.O_NOFOLLOW
// keeps a symlink planted after the Lstat from being followed.
func openChecked(path string, mode openMode) (*os.File, error) {
	info, err := os.Lstat(path)
	if err != nil {
		if mode == openModeLock && errors.Is(err, os.ErrNotExist) {
			return createLockFile(path)
		}
		return nil, err
	}
	if err := checkManagedEntry(info, path); err != nil {
		return nil, err
	}
	flag := os.O_RDONLY
	if mode == openModeLock {
		flag = os.O_RDWR
	}
	file, err := os.OpenFile(path, flag|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return nil, err
	}
	opened, err := file.Stat()
	if err != nil {
		file.Close()
		return nil, err
	}
	if !os.SameFile(info, opened) {
		file.Close()
		return nil, fmt.Errorf("file changed while opening: %s", path)
	}
	if err := checkManagedEntry(opened, path); err != nil {
		file.Close()
		return nil, err
	}
	return file, nil
}

func createLockFile(path string) (*os.File, error) {
	file, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_EXCL|syscall.O_NOFOLLOW, 0o600)
	if errors.Is(err, os.ErrExist) {
		return openChecked(path, openModeLock)
	}
	if err != nil {
		return nil, err
	}
	info, err := file.Stat()
	if err != nil {
		file.Close()
		return nil, err
	}
	if err := checkManagedEntry(info, path); err != nil {
		file.Close()
		return nil, err
	}
	return file, nil
}

// checkManagedEntry rejects non-regular files, extra hard links, and paths
// owned by another user or writable by group/others, matching the existing
// wrapper's trust checks.
func checkManagedEntry(info os.FileInfo, path string) error {
	if !info.Mode().IsRegular() {
		return fmt.Errorf("not a regular file: %s", path)
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return fmt.Errorf("cannot inspect %s", path)
	}
	if stat.Nlink > 1 {
		return fmt.Errorf("refusing a file with extra hard links: %s", path)
	}
	if stat.Uid != uint32(os.Getuid()) {
		return fmt.Errorf("refusing a path owned by another user: %s", path)
	}
	if info.Mode().Perm()&0o022 != 0 {
		return fmt.Errorf("refusing a path writable by others: %s", path)
	}
	return nil
}

// CheckManagedDir rejects non-directories and symlinks; Unix carries no
// reparse-point concept beyond that.
func CheckManagedDir(info os.FileInfo, path string) error {
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("not a real directory: %s", path)
	}
	return nil
}

// CheckPrivate rejects a path owned by another user or writable by group or
// others, matching the existing wrapper's trust checks.
func CheckPrivate(info os.FileInfo, path string) error {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return fmt.Errorf("cannot inspect ownership of %s", path)
	}
	if stat.Uid != uint32(os.Getuid()) {
		return fmt.Errorf("refusing a path owned by another user: %s", path)
	}
	if info.Mode().Perm()&0o022 != 0 {
		return fmt.Errorf("refusing a path writable by others: %s", path)
	}
	return nil
}
