package main

import (
	"archive/tar"
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

type packageOptions struct{ version, commit, output string }

type fileDigest struct {
	Bytes  int    `json:"bytes"`
	SHA256 string `json:"sha256"`
}

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

func digest(data []byte) fileDigest {
	sum := sha256.Sum256(data)
	return fileDigest{len(data), hex.EncodeToString(sum[:])}
}

func packageCandidate(ctx context.Context, options packageOptions) (err error) {
	if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
		return errors.New("candidate packaging requires Apple Silicon macOS to verify the built executable")
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
	target, err := distribution.TargetFor("darwin", "arm64")
	if err != nil {
		return err
	}
	tuple, err := json.Marshal([7]string{options.commit, digest(sourceArchive).SHA256, options.version, target.ID, goVersion, distribution.BuildRecipeID, distribution.ManifestDigest()})
	if err != nil {
		return err
	}
	buildID := "source-" + digest(tuple).SHA256
	identity := distribution.CurrentReleaseIdentity(options.version, buildID)
	identity.Target = target.ID
	flags := []string{"-trimpath", "-buildvcs=false", "-ldflags", "-X codex-openrouter/internal/launcher.Version=" + options.version + " -X codex-openrouter/internal/launcher.BuildID=" + buildID}
	targetEnv := []string{"GOOS=darwin", "GOARCH=arm64", "GOARM64=v8.0"}
	var executable []byte
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
		binary := filepath.Join(work, fmt.Sprintf("launcher-%d", attempt))
		args := append([]string{"build"}, flags...)
		args = append(args, "-o", binary, "./cmd/codex-openrouter")
		buildEnv := append(append([]string(nil), targetEnv...), "GOCACHE="+filepath.Join(work, fmt.Sprintf("cache-%d", attempt)))
		if _, err := tools.command(ctx, source, buildEnv, tools.goBinary, args...); err != nil {
			return err
		}
		if err := tools.verifyBinary(ctx, binary, identity); err != nil {
			return err
		}
		data, err := os.ReadFile(binary)
		if err != nil {
			return err
		}
		if attempt == 0 {
			executable = data
		} else if !bytes.Equal(executable, data) {
			return errors.New("candidate rebuild produced different executable bytes")
		}
	}
	metadata := candidateMetadata{
		SchemaVersion: 1, SourceCommit: options.commit, Identity: identity, ReleaseID: identity.ID(),
		CodexVersion: distribution.CodexVersion(), MinimumOS: target.OSMinimum, BuildFlags: flags,
		BuildEnv:   append([]string{"CGO_ENABLED=0", "GOTOOLCHAIN=local", "GOPROXY=off", "GOENV=off", "GOWORK=off", "GOFLAGS=", "GOEXPERIMENT="}, targetEnv...),
		Reproduced: true, Provenance: "local Git commit; no hosted build attestation", Signing: "no publisher signature or notarization; Go linker ad-hoc signature only",
		Files: map[string]fileDigest{"codex-openrouter": digest(executable)},
	}
	base := "codex-openrouter_" + options.version
	files := map[string][]byte{"codex-openrouter": executable}
	files[base+"_source.tar.gz"] = sourceArchive
	metadata.Files[base+"_source.tar.gz"] = digest(files[base+"_source.tar.gz"])
	buildRecord, err := json.MarshalIndent(metadata, "", "  ")
	if err != nil {
		return err
	}
	contents := map[string][]byte{
		"codex-openrouter": executable,
		"BUILD.json":       append(buildRecord, '\n'),
		"INSTALL.md":       []byte(installInstructions(options.version, options.commit, target.OSMinimum)),
	}
	for _, name := range distribution.NoticeNames() {
		contents["LICENSES/"+name], err = distribution.Notice(name)
		if err != nil {
			return err
		}
	}
	archiveName := base + "_darwin_arm64.tar.gz"
	files[archiveName], err = candidateArchive(contents)
	if err != nil {
		return err
	}
	metadata.Files[archiveName] = digest(files[archiveName])
	files["candidate.json"], err = json.MarshalIndent(metadata, "", "  ")
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
		mode := os.FileMode(0o600)
		if name == "codex-openrouter" {
			mode = 0o700
		}
		if err := os.WriteFile(filepath.Join(staging, name), data, mode); err != nil {
			return err
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return os.Rename(staging, output)
}

func (tools buildTools) verifyBinary(ctx context.Context, binary string, identity distribution.ReleaseIdentity) error {
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
	for key, value := range map[string]string{"CGO_ENABLED": "0", "GOOS": "darwin", "GOARCH": "arm64", "GOARM64": "v8.0", "-trimpath": "true"} {
		if settings[key] != value {
			return fmt.Errorf("candidate build setting %s does not match the recipe", key)
		}
	}
	if settings["vcs.revision"] != "" {
		return errors.New("candidate unexpectedly includes automatic VCS stamping")
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	output, err := tools.command(ctx, filepath.Dir(binary), nil, binary, "--launcher-version")
	if err != nil {
		return fmt.Errorf("candidate identity probe: %w", err)
	}
	expected := fmt.Sprintf("codex-openrouter %s, pinned Codex %s\n", identity.Describe(), distribution.CodexVersion())
	if string(output) != expected {
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

func candidateArchive(contents map[string][]byte) ([]byte, error) {
	var buffer bytes.Buffer
	writer := tar.NewWriter(&buffer)
	for _, name := range sortedNames(contents) {
		mode := int64(0o644)
		if name == "codex-openrouter" {
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

func installInstructions(version, commit, minimum string) string {
	return fmt.Sprintf(`# codex-openrouter %s

For Apple Silicon Macs with macOS %s or later.

Install from this directory:

    ./codex-openrouter --install

The installer downloads the pinned Codex bundle into ~/.codex-openrouter,
keeps your saved defaults, and prints the directory to add to PATH. Then set
OPENROUTER_API_KEY and run codex-openrouter. To update later, run --install
from a newly downloaded release, not from the installed command.

This binary is not signed or notarized. If macOS blocks it, run:

    xattr -d com.apple.quarantine codex-openrouter

Source commit: %s. BUILD.json records the compiler and build inputs.
`, version, minimum, commit)
}
