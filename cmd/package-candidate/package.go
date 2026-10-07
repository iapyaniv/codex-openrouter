package main

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"debug/buildinfo"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"time"

	"codex-openrouter/internal/distribution"
)

// targets limits packaging to these manifest target IDs; empty means all.
type packageOptions struct {
	version, commit, output string
	targets                 []string
}

type fileDigest struct {
	Bytes  int    `json:"bytes"`
	SHA256 string `json:"sha256"`
}

// candidateMetadata is one target's BUILD.json record.
type candidateMetadata struct {
	SchemaVersion int                          `json:"schemaVersion"`
	SourceCommit  string                       `json:"sourceCommit"`
	Identity      distribution.ReleaseIdentity `json:"identity"`
	ReleaseID     string                       `json:"releaseId"`
	CodexVersion  string                       `json:"codexVersion"`
	MinimumOS     string                       `json:"minimumOS"`
	BuildFlags    []string                     `json:"buildFlags"`
	BuildEnv      []string                     `json:"buildEnvironment"`
	Reproduced    bool                         `json:"reproduced"`
	Provenance    string                       `json:"provenance"`
	Signing       string                       `json:"signing"`
	Files         map[string]fileDigest        `json:"files"`
}

// releaseMetadata is the top-level candidate.json record.
type releaseMetadata struct {
	SchemaVersion int                          `json:"schemaVersion"`
	SourceCommit  string                       `json:"sourceCommit"`
	Version       string                       `json:"version"`
	CodexVersion  string                       `json:"codexVersion"`
	Targets       map[string]candidateMetadata `json:"targets"`
	Files         map[string]fileDigest        `json:"files"`
}

func digest(data []byte) fileDigest {
	sum := sha256.Sum256(data)
	return fileDigest{len(data), hex.EncodeToString(sum[:])}
}

func executableName(target distribution.Target) string {
	if target.OS == "windows" {
		return "codex-openrouter.exe"
	}
	return "codex-openrouter"
}

func targetEnvironment(target distribution.Target) []string {
	env := []string{"GOOS=" + target.OS, "GOARCH=" + target.Arch}
	switch target.Arch {
	case "amd64":
		env = append(env, "GOAMD64=v1")
	case "arm64":
		env = append(env, "GOARM64=v8.0")
	}
	return env
}

func isHost(target distribution.Target) bool {
	return target.OS == runtime.GOOS && target.Arch == runtime.GOARCH
}

// selectTargets returns the requested manifest targets. One of them must be
// the host, because only a launcher that runs here can prove its stamp by
// execution.
func selectTargets(ids []string) ([]distribution.Target, error) {
	all := distribution.Targets()
	selected := all
	if len(ids) != 0 {
		selected = nil
		for _, id := range ids {
			found := false
			for _, target := range all {
				if target.ID == id {
					selected, found = append(selected, target), true
				}
			}
			if !found {
				return nil, fmt.Errorf("target %s is not in the manifest", id)
			}
		}
	}
	for _, target := range selected {
		if isHost(target) {
			return selected, nil
		}
	}
	return nil, fmt.Errorf("packaging must run on one of the packaged targets to execute the built launcher; %s/%s is not one", runtime.GOOS, runtime.GOARCH)
}

func packageCandidate(ctx context.Context, options packageOptions) (err error) {
	targets, err := selectTargets(options.targets)
	if err != nil {
		return err
	}
	versionPattern := `^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-[0-9A-Za-z]+([.-][0-9A-Za-z]+)*)?$`
	if len(options.version) > 64 || !regexp.MustCompile(versionPattern).MatchString(options.version) {
		return errors.New("--version must be an explicit safe version, for example 0.2.0-local")
	}
	if !regexp.MustCompile(`^([0-9a-f]{40}|[0-9a-f]{64})$`).MatchString(options.commit) {
		return errors.New("--source-commit must be a full lowercase Git commit hash")
	}
	if !filepath.IsAbs(options.output) {
		return errors.New("--out must be a new absolute directory outside source")
	}
	work, err := os.MkdirTemp("", "package-candidate-")
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, os.RemoveAll(work)) }()
	tools, err := newBuildTools(work)
	if err != nil {
		return err
	}
	repoBytes, err := tools.git(ctx, "", "rev-parse", "--show-toplevel")
	if err != nil {
		return err
	}
	repo, err := filepath.EvalSymlinks(strings.TrimSpace(string(repoBytes)))
	if err != nil {
		return err
	}
	head, err := tools.git(ctx, repo, "rev-parse", "HEAD")
	if err != nil || strings.TrimSpace(string(head)) != options.commit {
		return errors.New("--source-commit must equal the source repository HEAD")
	}
	status, err := tools.git(ctx, repo, "status", "--porcelain", "--untracked-files=all")
	if err != nil {
		return err
	}
	if len(status) != 0 {
		return errors.New("candidate packaging requires clean committed source")
	}
	parent, err := filepath.EvalSymlinks(filepath.Dir(options.output))
	if err != nil {
		return err
	}
	output := filepath.Join(parent, filepath.Base(options.output))
	repoInfo, err := os.Stat(repo)
	if err != nil {
		return err
	}
	// Lexical paths can differ in case while naming the same directory.
	for ancestor := parent; ; ancestor = filepath.Dir(ancestor) {
		info, err := os.Stat(ancestor)
		if err != nil {
			return err
		}
		if os.SameFile(repoInfo, info) {
			return errors.New("candidate output must be outside the source repository")
		}
		if filepath.Dir(ancestor) == ancestor {
			break
		}
	}
	if _, err := os.Lstat(output); !errors.Is(err, os.ErrNotExist) {
		return errors.New("candidate output already exists or cannot be checked")
	}
	pin, err := os.ReadFile(filepath.Join(repo, ".go-version"))
	if err != nil {
		return err
	}
	goVersion := "go" + strings.TrimSpace(string(pin))
	actualGo, err := tools.command(ctx, repo, nil, tools.goBinary, "env", "GOVERSION")
	if err != nil || strings.TrimSpace(string(actualGo)) != goVersion || runtime.Version() != goVersion {
		return fmt.Errorf("packaging requires the pinned compiler and runtime %s", goVersion)
	}
	if err := tools.verifyToolInputs(ctx, repo, options.commit); err != nil {
		return err
	}
	archive, err := tools.sourceArchive(ctx, repo, options.commit, work)
	if err != nil {
		return err
	}
	sourceArchive, err := compress(archive)
	if err != nil {
		return err
	}
	base := "codex-openrouter_" + options.version
	sourceName := base + "_source.tar.gz"
	files := map[string][]byte{sourceName: sourceArchive}
	release := releaseMetadata{
		SchemaVersion: 2, SourceCommit: options.commit, Version: options.version,
		CodexVersion: distribution.CodexVersion(), Targets: make(map[string]candidateMetadata),
	}
	type build struct {
		target     distribution.Target
		identity   distribution.ReleaseIdentity
		flags      []string
		executable []byte
	}
	builds := make([]build, len(targets))
	for i, target := range targets {
		tuple, err := json.Marshal([7]string{options.commit, digest(sourceArchive).SHA256, options.version, target.ID, goVersion, distribution.BuildRecipeID, distribution.ManifestDigest()})
		if err != nil {
			return err
		}
		buildID := "source-" + digest(tuple).SHA256
		identity := distribution.CurrentReleaseIdentity(options.version, buildID)
		identity.Target = target.ID
		flags := []string{"-trimpath", "-buildvcs=false", "-ldflags", "-X codex-openrouter/internal/launcher.Version=" + options.version + " -X codex-openrouter/internal/launcher.BuildID=" + buildID}
		builds[i] = build{target: target, identity: identity, flags: flags}
	}
	for attempt := 0; attempt < 2; attempt++ {
		source := filepath.Join(work, fmt.Sprintf("source-%d", attempt))
		if err := os.Mkdir(source, 0o700); err != nil {
			return err
		}
		if err := extractSource(archive, source); err != nil {
			return err
		}
		license, err := os.ReadFile(filepath.Join(source, "LICENSE"))
		if err != nil {
			return err
		}
		notice, err := distribution.Notice("codex-openrouter-LICENSE.txt")
		if err != nil {
			return err
		}
		if !bytes.Equal(license, notice) {
			return errors.New("archived LICENSE differs from the retained wrapper notice")
		}
		// Targets share one cache per attempt; each attempt still starts cold.
		cache := "GOCACHE=" + filepath.Join(work, fmt.Sprintf("cache-%d", attempt))
		for i := range builds {
			b := &builds[i]
			binary := filepath.Join(work, fmt.Sprintf("launcher-%d-%s", attempt, b.target.ID), executableName(b.target))
			if err := os.MkdirAll(filepath.Dir(binary), 0o700); err != nil {
				return err
			}
			args := append([]string{"build"}, b.flags...)
			args = append(args, "-o", binary, "./cmd/codex-openrouter")
			buildEnv := append(targetEnvironment(b.target), cache)
			if _, err := tools.command(ctx, source, buildEnv, tools.goBinary, args...); err != nil {
				return err
			}
			if err := tools.verifyBinary(ctx, binary, b.identity, b.target); err != nil {
				return fmt.Errorf("%s: %w", b.target.ID, err)
			}
			data, err := os.ReadFile(binary)
			if err != nil {
				return err
			}
			if attempt == 0 {
				b.executable = data
			} else if !bytes.Equal(b.executable, data) {
				return fmt.Errorf("%s: candidate rebuild produced different executable bytes", b.target.ID)
			}
		}
	}
	for _, b := range builds {
		name := executableName(b.target)
		metadata := candidateMetadata{
			SchemaVersion: 1, SourceCommit: options.commit, Identity: b.identity, ReleaseID: b.identity.ID(),
			CodexVersion: distribution.CodexVersion(), MinimumOS: b.target.OSMinimum, BuildFlags: b.flags,
			BuildEnv:   append([]string{"CGO_ENABLED=0", "GOTOOLCHAIN=local", "GOPROXY=off", "GOENV=off", "GOWORK=off", "GOFLAGS=", "GOEXPERIMENT="}, targetEnvironment(b.target)...),
			Reproduced: true, Provenance: "local Git commit; no hosted build attestation", Signing: "no publisher signature or notarization",
			Files: map[string]fileDigest{name: digest(b.executable), sourceName: digest(sourceArchive)},
		}
		buildRecord, err := json.MarshalIndent(metadata, "", "  ")
		if err != nil {
			return err
		}
		contents := map[string][]byte{
			name:         b.executable,
			"BUILD.json": append(buildRecord, '\n'),
			"INSTALL.md": []byte(installInstructions(options.version, options.commit, b.target)),
		}
		for _, notice := range distribution.NoticeNames() {
			contents["LICENSES/"+notice], err = distribution.Notice(notice)
			if err != nil {
				return err
			}
		}
		archiveName := base + "_" + b.target.OS + "_" + b.target.Arch
		if b.target.OS == "windows" {
			archiveName += ".zip"
			files[archiveName], err = zipArchive(contents, name)
		} else {
			archiveName += ".tar.gz"
			files[archiveName], err = candidateArchive(contents, name)
		}
		if err != nil {
			return err
		}
		release.Targets[b.target.ID] = metadata
	}
	release.Files = make(map[string]fileDigest)
	for name, data := range files {
		release.Files[name] = digest(data)
	}
	files["candidate.json"], err = json.MarshalIndent(release, "", "  ")
	if err != nil {
		return err
	}
	files["candidate.json"] = append(files["candidate.json"], '\n')
	var sums strings.Builder
	for _, name := range sortedNames(files) {
		fmt.Fprintf(&sums, "%s  %s\n", digest(files[name]).SHA256, name)
	}
	files["SHA256SUMS"] = []byte(sums.String())
	staging, err := os.MkdirTemp(parent, ".candidate-")
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, os.RemoveAll(staging)) }()
	for name, data := range files {
		if err := os.WriteFile(filepath.Join(staging, name), data, 0o600); err != nil {
			return err
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return os.Rename(staging, output)
}

func (tools buildTools) verifyBinary(ctx context.Context, binary string, identity distribution.ReleaseIdentity, target distribution.Target) error {
	info, err := buildinfo.ReadFile(binary)
	if err != nil {
		return err
	}
	if info.GoVersion != identity.Toolchain || info.Path != "codex-openrouter/cmd/codex-openrouter" || info.Main.Path != "codex-openrouter" || len(info.Deps) != 0 {
		return errors.New("candidate build metadata has unexpected toolchain or modules")
	}
	settings := make(map[string]string)
	for _, setting := range info.Settings {
		settings[setting.Key] = setting.Value
	}
	expected := map[string]string{"CGO_ENABLED": "0", "-trimpath": "true"}
	for _, pair := range targetEnvironment(target) {
		key, value, _ := strings.Cut(pair, "=")
		expected[key] = value
	}
	for key, value := range expected {
		if settings[key] != value {
			return fmt.Errorf("candidate build setting %s does not match the recipe", key)
		}
	}
	if settings["vcs.revision"] != "" {
		return errors.New("candidate unexpectedly includes automatic VCS stamping")
	}
	// Go omits -ldflags from build metadata under -trimpath, and a mistyped -X
	// is silently ignored, so look for the stamped build ID in the bytes.
	data, err := os.ReadFile(binary)
	if err != nil {
		return err
	}
	if !bytes.Contains(data, []byte(identity.BuildID)) {
		return errors.New("candidate executable does not contain its build stamp")
	}
	if !isHost(target) {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	output, err := tools.command(ctx, filepath.Dir(binary), nil, binary, "--launcher-version")
	if err != nil {
		return fmt.Errorf("candidate identity probe: %w", err)
	}
	line := fmt.Sprintf("codex-openrouter %s, pinned Codex %s\n", identity.Describe(), distribution.CodexVersion())
	if string(output) != line {
		return errors.New("candidate executable identity does not match the packaged identity")
	}
	return nil
}

func compress(data []byte) ([]byte, error) {
	var buffer bytes.Buffer
	writer, err := gzip.NewWriterLevel(&buffer, gzip.BestCompression)
	if err != nil {
		return nil, err
	}
	if _, err := writer.Write(data); err != nil {
		return nil, err
	}
	if err := writer.Close(); err != nil {
		return nil, err
	}
	return buffer.Bytes(), nil
}

func sortedNames[T any](files map[string]T) []string {
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func candidateArchive(contents map[string][]byte, executable string) ([]byte, error) {
	var buffer bytes.Buffer
	writer := tar.NewWriter(&buffer)
	for _, name := range sortedNames(contents) {
		mode := int64(0o644)
		if name == executable {
			mode = 0o755
		}
		if err := writer.WriteHeader(&tar.Header{Name: name, Mode: mode, Size: int64(len(contents[name])), Typeflag: tar.TypeReg, Format: tar.FormatUSTAR}); err != nil {
			return nil, err
		}
		if _, err := writer.Write(contents[name]); err != nil {
			return nil, err
		}
	}
	if err := writer.Close(); err != nil {
		return nil, err
	}
	return compress(buffer.Bytes())
}

// A fixed time keeps rebuilt archives byte-identical. Zip DOS timestamps
// start in 1980; noon keeps every time zone from showing 1979.
var zipEpoch = time.Date(1980, 1, 1, 12, 0, 0, 0, time.UTC)

func zipArchive(contents map[string][]byte, executable string) ([]byte, error) {
	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	for _, name := range sortedNames(contents) {
		header := &zip.FileHeader{Name: name, Method: zip.Deflate, Modified: zipEpoch}
		mode := os.FileMode(0o644)
		if name == executable {
			mode = 0o755
		}
		header.SetMode(mode)
		file, err := writer.CreateHeader(header)
		if err != nil {
			return nil, err
		}
		if _, err := file.Write(contents[name]); err != nil {
			return nil, err
		}
	}
	if err := writer.Close(); err != nil {
		return nil, err
	}
	return buffer.Bytes(), nil
}

var platformNames = map[string]string{
	"darwin-arm64": "Apple Silicon Macs", "darwin-amd64": "Intel Macs",
	"linux-amd64": "Linux on x86-64", "linux-arm64": "Linux on ARM64",
	"windows-amd64": "Windows on x64", "windows-arm64": "Windows on ARM64",
}

func installInstructions(version, commit string, target distribution.Target) string {
	platform := platformNames[target.ID]
	if platform == "" {
		platform = target.OS + "/" + target.Arch
	}
	if target.OSMinimum != "" {
		platform += " with macOS " + target.OSMinimum + " or later"
	}
	var steps string
	switch target.OS {
	case "windows":
		steps = `Install from this directory in PowerShell:

    .\codex-openrouter.exe --install

The installer downloads the pinned Codex bundle into
%USERPROFILE%\.codex-openrouter and keeps your saved defaults. Add its bin
directory to your user PATH, then open a new terminal:

    [Environment]::SetEnvironmentVariable("Path", [Environment]::GetEnvironmentVariable("Path", "User") + ";$env:USERPROFILE\.codex-openrouter\bin", "User")

Set OPENROUTER_API_KEY and run codex-openrouter. To update later, run
--install from a newly downloaded release, not from the installed command.

This binary is not signed. If SmartScreen blocks it, select More info, then
Run anyway. To remove the download mark first, run:

    Unblock-File .\codex-openrouter.exe
`
	case "darwin":
		steps = unixSteps + `
This binary is not signed or notarized. If macOS blocks it, run:

    xattr -d com.apple.quarantine codex-openrouter
`
	default:
		steps = unixSteps
	}
	return fmt.Sprintf("# codex-openrouter %s\n\nFor %s.\n\n%s\nSource commit: %s. BUILD.json records the compiler and build inputs.\n", version, platform, steps, commit)
}

const unixSteps = `Install from this directory:

    ./codex-openrouter --install

The installer downloads the pinned Codex bundle into ~/.codex-openrouter,
keeps your saved defaults, and prints the directory to add to PATH. Then set
OPENROUTER_API_KEY and run codex-openrouter. To update later, run --install
from a newly downloaded release, not from the installed command.
`
