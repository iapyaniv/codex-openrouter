package platform

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestLockBusyUntilRelease(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.lock")
	release, err := Lock(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Lock(path); !errors.Is(err, ErrBusy) {
		t.Fatalf("second lock: got %v, want the ErrBusy sentinel", err)
	}
	if err := release(); err != nil {
		t.Fatal(err)
	}
	again, err := Lock(path)
	if err != nil {
		t.Fatalf("lock after release: %v", err)
	}
	if err := again(); err != nil {
		t.Fatal(err)
	}
}

func TestLockRefusesUnsafePaths(t *testing.T) {
	directory := t.TempDir()
	target := filepath.Join(directory, "target")
	if err := os.WriteFile(target, []byte("data"), 0o600); err != nil {
		t.Fatal(err)
	}
	hardlink := filepath.Join(directory, "hard.lock")
	if err := os.Link(target, hardlink); err != nil {
		t.Fatal(err)
	}
	if _, err := Lock(hardlink); err == nil {
		t.Fatal("lock accepted a hard-linked file")
	}
	if _, err := Lock(filepath.Join(directory, "missing", "x.lock")); err == nil {
		t.Fatal("lock created missing parent directories")
	}
	if err := os.Mkdir(filepath.Join(directory, "dir.lock"), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := Lock(filepath.Join(directory, "dir.lock")); err == nil {
		t.Fatal("lock accepted a directory")
	}
}

func TestLockReleasedByCrash(t *testing.T) {
	path := filepath.Join(t.TempDir(), "crash.lock")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	helper := exec.CommandContext(ctx, os.Args[0], "-test.run=TestLockHelperProcess", "--", path)
	helper.Env = append(os.Environ(), "GO_WANT_LOCK_HELPER=1")
	stdout, err := helper.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdin, err := helper.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := helper.Start(); err != nil {
		t.Fatal(err)
	}
	waited := false
	t.Cleanup(func() {
		cancel()
		stdin.Close()
		if !waited {
			helper.Process.Kill()
			helper.Wait()
		}
	})
	ready := make([]byte, 1)
	if _, err := stdout.Read(ready); err != nil {
		t.Fatalf("helper never acquired the lock: %v", err)
	}
	if _, err := Lock(path); !errors.Is(err, ErrBusy) {
		t.Fatalf("lock during helper: got %v, want the ErrBusy sentinel", err)
	}
	if err := helper.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	if err := helper.Wait(); err == nil {
		t.Fatal("killed helper reported a clean exit")
	}
	waited = true
	release, err := Lock(path)
	if err != nil {
		t.Fatalf("lock after crash: %v", err)
	}
	if err := release(); err != nil {
		t.Fatal(err)
	}
}

func TestLockHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_LOCK_HELPER") != "1" {
		t.Skip("helper process")
	}
	path := os.Args[len(os.Args)-1]
	release, err := Lock(path)
	if err != nil {
		os.Exit(2)
	}
	defer release()
	if _, err := os.Stdout.Write([]byte{1}); err != nil {
		os.Exit(3)
	}
	// Hold the lock until the parent kills this process or closes stdin; the
	// OS releases the handle-based lock on process death.
	io.Copy(io.Discard, os.Stdin)
}

func TestReplaceFileSubstitutesExisting(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "config.json")
	if err := os.WriteFile(path, []byte("old data"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := ReplaceFile(path, []byte("new data"), 0o600); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "new data" {
		t.Fatalf("got %q", data)
	}
}

func TestReplaceFileFailureKeepsOldBytes(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("os.Chmod is not a directory ACL on Windows; native failure evidence pending")
	}
	if os.Getuid() == 0 {
		t.Skip("permission checks do not constrain root")
	}
	directory := t.TempDir()
	path := filepath.Join(directory, "config.json")
	if err := os.WriteFile(path, []byte("old data"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(directory, 0o500); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(directory, 0o700)
	err := ReplaceFile(path, []byte("new data"), 0o600)
	if err == nil {
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			t.Fatal(readErr)
		}
		if string(data) != "old data" {
			t.Fatal("replace unexpectedly succeeded with new data")
		}
		t.Skip("filesystem permitted the operation without directory write permission")
	}
	data, readErr := os.ReadFile(path)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if string(data) != "old data" {
		t.Fatalf("old data lost after failed replace: %q", data)
	}
	entries, readErr := os.ReadDir(directory)
	if readErr != nil {
		t.Fatal(readErr)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".tmp-") {
			t.Fatalf("temporary file left behind: %s", entry.Name())
		}
	}
}
