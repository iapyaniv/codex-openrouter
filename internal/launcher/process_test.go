//go:build unix

package launcher

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// Dispatch before m.Run keeps the test flag parser away from the raw argv.
const (
	execBinaryEnv  = "GO_EXEC_HELPER_BINARY"
	childReportEnv = "GO_PROCESS_CHILD"
)

func TestMain(m *testing.M) {
	if binary := os.Getenv(execBinaryEnv); binary != "" {
		os.Unsetenv(execBinaryEnv)
		if code := execNative(binary, os.Args[1:], os.Stderr); code != 0 {
			fmt.Fprintf(os.Stderr, "execNative returned %d\n", code)
			os.Exit(code)
		}
		fmt.Fprintln(os.Stderr, "execNative returned success")
		os.Exit(1)
	}
	if os.Getenv(childReportEnv) != "" {
		os.Exit(childMain(os.Args[1:]))
	}
	os.Exit(m.Run())
}

func execRole(t *testing.T, args []string, environ []string) *exec.Cmd {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	command := exec.CommandContext(ctx, os.Args[0], args...)
	command.Env = append(environ, execBinaryEnv+"="+os.Args[0], childReportEnv+"=1")
	return command
}

type childReport struct {
	Args []string          `json:"args"`
	Env  map[string]string `json:"env"`
	Cwd  string            `json:"cwd"`
	Pid  int               `json:"pid"`
}

func runExecChild(t *testing.T, args []string, environ []string) childReport {
	t.Helper()
	command := execRole(t, args, environ)
	out, err := command.Output()
	if err != nil {
		t.Fatalf("exec role failed: %v\n%s", err, out)
	}
	var report childReport
	if err := json.Unmarshal(out, &report); err != nil {
		t.Fatalf("child report undecodable: %v (%q)", err, out)
	}
	return report
}

func TestExecArgvFidelity(t *testing.T) {
	args := []string{
		"",
		"plain",
		"with spaces",
		`quotes "double" 'single'`,
		`back\slash C:\path`,
		"meta $(touch /tmp/nope) & | ; `cmd` *",
		"unicode café 日本語",
		"del\x7fchar",
		"--",
		"-c",
	}
	report := runExecChild(t, args, []string{"PATH=/usr/bin:/bin"})
	if len(report.Args) != len(args) {
		t.Fatalf("child saw %d arguments, want %d: %q", len(report.Args), len(args), report.Args)
	}
	for i, want := range args {
		if report.Args[i] != want {
			t.Fatalf("argument %d: child saw %q, want %q", i, report.Args[i], want)
		}
	}
}

func TestExecEnvironmentClearing(t *testing.T) {
	environ := []string{
		"PATH=/usr/bin:/bin",
		"OPENROUTER_API_KEY=synthetic-key",
		"CODEX_MANAGED_BY_NPM=1",
		"CODEX_MANAGED_BY_BUN=1",
		"CODEX_MANAGED_BY_PNPM=1",
		"CODEX_MANAGED_BY_VITE_PLUS=1",
		"CODEX_MANAGED_PACKAGE_ROOT=/somewhere",
	}
	report := runExecChild(t, nil, environ)
	for _, marker := range []string{"CODEX_MANAGED_BY_NPM", "CODEX_MANAGED_BY_BUN", "CODEX_MANAGED_BY_PNPM", "CODEX_MANAGED_BY_VITE_PLUS", "CODEX_MANAGED_PACKAGE_ROOT"} {
		if value, ok := report.Env[marker]; ok {
			t.Fatalf("marker %s=%q reached the child", marker, value)
		}
	}
	if report.Env["OPENROUTER_API_KEY"] != "synthetic-key" {
		t.Fatal("ordinary environment did not reach the child")
	}
	if _, ok := report.Env[execBinaryEnv]; ok {
		t.Fatal("the exec-role variable leaked into the child")
	}
}

// The reported PID must equal the operating system's PID for the started
// process, which a spawn-and-wait wrapper cannot satisfy.
func TestExecCwdAndPID(t *testing.T) {
	directory := t.TempDir()
	command := execRole(t, nil, []string{"PATH=/usr/bin:/bin"})
	command.Dir = directory
	out, err := command.Output()
	if err != nil {
		t.Fatalf("exec role failed: %v\n%s", err, out)
	}
	var report childReport
	if err := json.Unmarshal(out, &report); err != nil {
		t.Fatalf("child report undecodable: %v (%q)", err, out)
	}
	resolved, err := filepath.EvalSymlinks(directory)
	if err != nil {
		t.Fatal(err)
	}
	if report.Cwd != resolved {
		t.Fatalf("child cwd %q, want %q", report.Cwd, resolved)
	}
	if report.Pid != command.Process.Pid {
		t.Fatalf("child reported pid %d, but the launched process was %d; exec did not replace the process image", report.Pid, command.Process.Pid)
	}
}

func TestExecExitStatusPreserved(t *testing.T) {
	command := execRole(t, []string{"exit", "42"}, []string{"PATH=/usr/bin:/bin"})
	err := command.Run()
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("got %v, want an exit error", err)
	}
	if exitErr.ExitCode() != 42 {
		t.Fatalf("exit status %d, want 42", exitErr.ExitCode())
	}
}

func TestExecRedirectedStreams(t *testing.T) {
	command := execRole(t, []string{"echo-streams"}, []string{"PATH=/usr/bin:/bin"})
	command.Stdin = strings.NewReader("first line\nsecond line\n")
	var stderr bytes.Buffer
	command.Stderr = &stderr
	out, err := command.Output()
	if err != nil {
		t.Fatalf("exec role failed: %v\n%s", err, out)
	}
	if string(out) != "first line\nsecond line\n" {
		t.Fatalf("stdout %q", out)
	}
	if stderr.String() != "child stderr\n" {
		t.Fatalf("stderr %q", stderr.String())
	}
}

func TestExecSIGTERM(t *testing.T) {
	command := execRole(t, []string{"sleep"}, []string{"PATH=/usr/bin:/bin"})
	stdout, err := command.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	waited := false
	t.Cleanup(func() {
		if !waited {
			command.Process.Kill()
			command.Wait()
		}
	})
	line, err := bufio.NewReader(stdout).ReadBytes('\n')
	if err != nil {
		t.Fatalf("child never reported readiness: %v", err)
	}
	var ready struct {
		Pid int `json:"pid"`
	}
	if err := json.Unmarshal(line, &ready); err != nil {
		t.Fatalf("readiness record undecodable: %v (%q)", err, line)
	}
	if ready.Pid != command.Process.Pid {
		t.Fatalf("child reported pid %d, but the launched process was %d", ready.Pid, command.Process.Pid)
	}
	if err := command.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	err = command.Wait()
	waited = true
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("got %v, want signal termination", err)
	}
	status, ok := exitErr.Sys().(syscall.WaitStatus)
	if !ok || status.Signal() != syscall.SIGTERM {
		t.Fatalf("wait status %v, want SIGTERM", exitErr)
	}
}
