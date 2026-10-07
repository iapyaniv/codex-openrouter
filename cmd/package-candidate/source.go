package main

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

type buildTools struct {
	goBinary, gitBinary string
	env                 []string
}

func newBuildTools(work string) (buildTools, error) {
	git, err := exec.LookPath("git")
	if err != nil {
		return buildTools{}, errors.New("Git is required to package a candidate")
	}
	name := "go"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	tools := buildTools{goBinary: filepath.Join(runtime.GOROOT(), "bin", name), gitBinary: git}
	tools.env = []string{
		"HOME=" + work, "USERPROFILE=" + work, "TMPDIR=" + work, "TMP=" + work, "TEMP=" + work,
		"PATH=" + filepath.Dir(tools.goBinary), "LANG=C", "TZ=UTC",
		"GOTOOLCHAIN=local", "GOPROXY=off", "GOSUMDB=off", "GOENV=off", "GOWORK=off", "GOFLAGS=", "GOEXPERIMENT=",
		"CGO_ENABLED=0", "GOCACHE=" + filepath.Join(work, "cache"), "GOMODCACHE=" + filepath.Join(work, "modules"),
		"GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=" + os.DevNull, "GIT_ATTR_NOSYSTEM=1", "GIT_TERMINAL_PROMPT=0", "GIT_NO_REPLACE_OBJECTS=1",
	}
	if runtime.GOOS == "windows" {
		tools.env = append(tools.env, "SystemRoot="+os.Getenv("SystemRoot"))
	}
	return tools, nil
}

type boundedBuffer struct {
	bytes.Buffer
	remaining int
}

func (buffer *boundedBuffer) Write(data []byte) (int, error) {
	if len(data) > buffer.remaining {
		return 0, errors.New("command output exceeds its limit")
	}
	buffer.remaining -= len(data)
	return buffer.Buffer.Write(data)
}

func (tools buildTools) command(ctx context.Context, directory string, env []string, binary string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, binary, args...)
	cmd.Dir, cmd.Env = directory, append(append([]string(nil), tools.env...), env...)
	cmd.WaitDelay = time.Second
	stdout, stderr := &boundedBuffer{remaining: 32 << 20}, &boundedBuffer{remaining: 16 << 10}
	cmd.Stdout, cmd.Stderr = stdout, stderr
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("%s failed: %w: %s", filepath.Base(binary), err, strings.TrimSpace(stderr.String()))
	}
	return stdout.Bytes(), nil
}

func (tools buildTools) git(ctx context.Context, directory string, args ...string) ([]byte, error) {
	// A repository-local fsmonitor can execute outside the archived source.
	args = append([]string{"-c", "core.fsmonitor=false", "-c", "core.pager=cat", "-c", "core.attributesFile=" + os.DevNull}, args...)
	return tools.command(ctx, directory, nil, tools.gitBinary, args...)
}

func (tools buildTools) sourceArchive(ctx context.Context, repo, commit, work string) ([]byte, error) {
	format, err := tools.git(ctx, repo, "rev-parse", "--show-object-format")
	if err != nil {
		return nil, err
	}
	store := filepath.Join(work, "objects")
	if _, err := tools.git(ctx, work, "init", "--bare", "--template=", "--object-format="+strings.TrimSpace(string(format)), store); err != nil {
		return nil, err
	}
	if _, err := tools.git(ctx, store, "fetch", "--quiet", "--no-tags", "--depth=1", "--no-recurse-submodules", repo, commit); err != nil {
		return nil, err
	}
	// Archive must not read the source repository's untracked attributes or tar configuration.
	return tools.git(ctx, store, "-c", "tar.umask=0002", "archive", "--format=tar", commit)
}

func (tools buildTools) verifyToolInputs(ctx context.Context, repo, commit string) error {
	data, err := tools.command(ctx, repo, nil, tools.goBinary, "list", "-deps", "-json", "./cmd/package-candidate")
	if err != nil {
		return err
	}
	inputs := map[string]bool{"go.mod": true, ".go-version": true}
	decoder := json.NewDecoder(bytes.NewReader(data))
	for {
		var pkg struct {
			Dir, ImportPath string
			Standard        bool
			Module          *struct {
				Path string
				Main bool
			}
			GoFiles, CgoFiles, EmbedFiles, SFiles, SysoFiles []string
		}
		if err := decoder.Decode(&pkg); err == io.EOF {
			break
		} else if err != nil {
			return err
		}
		if pkg.Standard {
			continue
		}
		if pkg.Module == nil || !pkg.Module.Main || pkg.Module.Path != "codex-openrouter" || len(pkg.CgoFiles) != 0 {
			return fmt.Errorf("unexpected packaging dependency: %s", pkg.ImportPath)
		}
		for _, files := range [][]string{pkg.GoFiles, pkg.EmbedFiles, pkg.SFiles, pkg.SysoFiles} {
			for _, file := range files {
				rel, err := filepath.Rel(repo, filepath.Join(pkg.Dir, file))
				if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
					return errors.New("packaging input is outside the source repository")
				}
				inputs[filepath.ToSlash(rel)] = true
			}
		}
	}
	for input := range inputs {
		committed, err := tools.git(ctx, repo, "cat-file", "blob", commit+":"+input)
		if err != nil {
			return fmt.Errorf("packaging input is not committed: %s", input)
		}
		info, err := os.Lstat(filepath.Join(repo, filepath.FromSlash(input)))
		if err != nil || !info.Mode().IsRegular() {
			return fmt.Errorf("packaging input is not a regular source file: %s", input)
		}
		actual, err := os.ReadFile(filepath.Join(repo, filepath.FromSlash(input)))
		if err != nil || !bytes.Equal(actual, committed) {
			return fmt.Errorf("packaging input differs from the source commit: %s", input)
		}
	}
	return nil
}

func extractSource(data []byte, destination string) error {
	reader := tar.NewReader(bytes.NewReader(data))
	for {
		header, err := reader.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		// Git records the commit in a global PAX header, not a source file.
		if header.Typeflag == tar.TypeXGlobalHeader {
			continue
		}
		name := strings.TrimSuffix(header.Name, "/")
		if name == "." || name == ".." || path.Clean(name) != name || strings.HasPrefix(name, "/") || strings.HasPrefix(name, "../") || strings.ContainsAny(name, "\\:") {
			return fmt.Errorf("unsupported source archive path: %q", header.Name)
		}
		dest := filepath.Join(destination, filepath.FromSlash(name))
		switch header.Typeflag {
		case tar.TypeDir:
			err = os.MkdirAll(dest, 0o700)
		case tar.TypeReg:
			if err = os.MkdirAll(filepath.Dir(dest), 0o700); err != nil {
				return err
			}
			mode := os.FileMode(0o600)
			if header.Mode&0o111 != 0 {
				mode = 0o700
			}
			file, e := os.OpenFile(dest, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
			if e != nil {
				return e
			}
			_, copyErr := io.Copy(file, reader)
			err = errors.Join(copyErr, file.Close())
		default:
			return fmt.Errorf("unsupported source archive entry: %s", header.Name)
		}
		if err != nil {
			return err
		}
	}
}
