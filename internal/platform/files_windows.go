//go:build windows

package platform

import (
	"errors"
	"fmt"
	"os"
	"syscall"
	"unsafe"
)

var (
	kernel32         = syscall.NewLazyDLL("kernel32.dll")
	procLockFileEx   = kernel32.NewProc("LockFileEx")
	procUnlockFileEx = kernel32.NewProc("UnlockFileEx")
)

const (
	lockfileExclusiveLock   = 0x2
	lockfileFailImmediately = 0x1
	errorLockViolation      = 33
)

// lock takes an exclusive nonblocking byte-range lock. Closing the handle on
// process exit or crash releases it; there is no stale sentinel file.
func lock(file *os.File) error {
	var ov syscall.Overlapped
	ret, _, err := procLockFileEx.Call(
		file.Fd(),
		lockfileExclusiveLock|lockfileFailImmediately,
		0, 1, 0,
		uintptr(unsafe.Pointer(&ov)),
	)
	if ret == 0 {
		if errors.Is(err, syscall.Errno(errorLockViolation)) {
			return ErrBusy
		}
		return err
	}
	return nil
}

func unlock(file *os.File) error {
	var ov syscall.Overlapped
	ret, _, err := procUnlockFileEx.Call(file.Fd(), 0, 1, 0, uintptr(unsafe.Pointer(&ov)))
	if ret == 0 {
		return err
	}
	return nil
}

// replace uses os.Rename: MoveFileExW with MOVEFILE_REPLACE_EXISTING, so the
// destination is substituted without being deleted first. Delete-first and
// ReplaceFileW without a backup are avoided because both can lose the old
// file while the replacement keeps its temporary name. Runtime limits are
// recorded in docs/native-distribution.md.
func replace(temp, path string) error {
	return os.Rename(temp, path)
}

// openChecked opens path only after the directory entry passes the type and
// reparse checks, then repeats the type and link-count checks on the opened
// handle. FILE_FLAG_OPEN_REPARSE_POINT keeps a symlink or junction planted
// after the Lstat from being followed; Go maps the resulting
// ERROR_CANT_ACCESS_FILE to an open error. Go's syscall.Open omits
// FILE_SHARE_DELETE, so a file being replaced concurrently can fail to open;
// the caller surfaces that error.
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
	file, err := os.OpenFile(path, flag|syscall.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if err != nil {
		return nil, err
	}
	// In Go 1.27.1, os.SameFile on Windows resolves the Lstat result's
	// identity lazily by re-opening the saved path, so it is not an initial
	// identity snapshot and cannot prove the path was not swapped between
	// Lstat and open. The real handle checks below remain; capturing an
	// initial file ID from a stable no-follow handle is a future gate.
	var data syscall.ByHandleFileInformation
	if err := syscall.GetFileInformationByHandle(syscall.Handle(file.Fd()), &data); err != nil {
		file.Close()
		return nil, fmt.Errorf("cannot inspect %s: %w", path, err)
	}
	if err := checkHandleEntry(data, path); err != nil {
		file.Close()
		return nil, err
	}
	return file, nil
}

func createLockFile(path string) (*os.File, error) {
	// O_CREATE|O_EXCL already sets FILE_FLAG_OPEN_REPARSE_POINT in Go's
	// syscall.Open, so a planted link is refused rather than followed.
	file, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0o600)
	if errors.Is(err, os.ErrExist) {
		return openChecked(path, openModeLock)
	}
	if err != nil {
		return nil, err
	}
	var data syscall.ByHandleFileInformation
	if err := syscall.GetFileInformationByHandle(syscall.Handle(file.Fd()), &data); err != nil {
		file.Close()
		return nil, fmt.Errorf("cannot inspect %s: %w", path, err)
	}
	if err := checkHandleEntry(data, path); err != nil {
		file.Close()
		return nil, err
	}
	return file, nil
}

// checkManagedEntry rejects non-regular files and any reparse point. Go's
// os reports symlinks as ModeSymlink, but directory junctions and other
// reparse points only surface through the Win32 attribute, so the explicit
// attribute check is required rather than redundant.
func checkManagedEntry(info os.FileInfo, path string) error {
	if !info.Mode().IsRegular() {
		return fmt.Errorf("not a regular file: %s", path)
	}
	if attrs, ok := windowsAttributes(info); ok && attrs&syscall.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
		return fmt.Errorf("refusing a reparse point: %s", path)
	}
	return nil
}

func checkHandleEntry(data syscall.ByHandleFileInformation, path string) error {
	if data.FileAttributes&syscall.FILE_ATTRIBUTE_DIRECTORY != 0 {
		return fmt.Errorf("not a regular file: %s", path)
	}
	if data.FileAttributes&syscall.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
		return fmt.Errorf("refusing a reparse point: %s", path)
	}
	if data.NumberOfLinks > 1 {
		return fmt.Errorf("refusing a file with extra hard links: %s", path)
	}
	return nil
}

func windowsAttributes(info os.FileInfo) (uint32, bool) {
	// Both stat results used here come from os.Lstat on Windows, whose
	// Sys value is *syscall.Win32FileAttributeData.
	stat, ok := info.Sys().(*syscall.Win32FileAttributeData)
	if !ok {
		return 0, false
	}
	return stat.FileAttributes, true
}

// CheckManagedDir rejects non-directories and any reparse point. The mode
// check alone is insufficient: Go's os reports symlinks as ModeSymlink, but
// directory junctions and other reparse points only surface through the
// Win32 attribute.
func CheckManagedDir(info os.FileInfo, path string) error {
	if !info.IsDir() {
		return fmt.Errorf("not a directory: %s", path)
	}
	if attrs, ok := windowsAttributes(info); ok && attrs&syscall.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
		return fmt.Errorf("refusing a reparse point: %s", path)
	}
	return nil
}

// CheckPrivate cannot audit a Windows ACL with POSIX semantics; the caller
// documents that limitation. Reparse points on managed directories are
// rejected separately by CheckManagedDir.
func CheckPrivate(info os.FileInfo, path string) error {
	return nil
}
