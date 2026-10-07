package integration

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	jsonv2 "encoding/json/v2"

	"codex-openrouter/internal/distribution"
	"codex-openrouter/internal/platform"
)

const candidateVersion = "0.1.0-test-candidate"

var syntheticSecrets = map[string]string{
	"OPENROUTER_API_KEY":    "local-smoke-test-only",
	"OTHER_API_KEY":         "other-key-smoke-test-only",
	"OTHER_TOKEN":           "other-token-smoke-test-only",
	"AWS_SECRET_ACCESS_KEY": "aws-secret-smoke-test-only",
}

func TestNativeLoopback(t *testing.T) {
	fixture := os.Getenv("CODEX_OPENROUTER_TEST_NATIVE")
	supplied, suppliedMode := os.LookupEnv("CODEX_OPENROUTER_TEST_CANDIDATE")
	if suppliedMode && supplied == "" {
		t.Fatal("explicit candidate path is empty")
	}
	installed := os.Getenv("CODEX_OPENROUTER_TEST_INSTALL") == "1"
	if suppliedMode {
		installed = true
	}
	if fixture == "" && !installed {
		t.Skip("real-Codex fixture not supplied; set CODEX_OPENROUTER_TEST_NATIVE, CODEX_OPENROUTER_TEST_INSTALL=1, or CODEX_OPENROUTER_TEST_CANDIDATE to an extracted candidate executable")
	}
	target, err := distribution.CurrentTarget()
	if err != nil {
		t.Fatal("native fixture supplied for a host without an inventoried distribution")
	}
	root := t.TempDir()
	if retained := os.Getenv("CODEX_OPENROUTER_TEST_RETAIN"); retained != "" {
		if !filepath.IsAbs(retained) {
			t.Fatal("test retention parent must be absolute")
		}
		root, err = os.MkdirTemp(retained, "native-")
		if err != nil {
			t.Fatal(err)
		}
	}
	root, err = filepath.EvalSymlinks(root)
	must(t, err)
	repo, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	var identity distribution.ReleaseIdentity
	var suppliedBytes []byte
	candidate := filepath.Join(root, "codex-openrouter")
	probe := filepath.Join(root, "envprobe")
	buildEnv := []string{
		"HOME=" + root, "PATH=/usr/bin:/bin", "GOTOOLCHAIN=local", "GOPROXY=off", "GOENV=off", "CGO_ENABLED=0",
		"GOCACHE=" + filepath.Join(root, "go-cache"), "GOMODCACHE=" + filepath.Join(root, "go-mod-cache"),
	}
	goBinary := filepath.Join(runtime.GOROOT(), "bin", "go")
	if suppliedMode {
		candidate = supplied
		identity, suppliedBytes = archiveCandidate(t, candidate)
	} else {
		buildID := "test-" + sourceDigest(t, repo)
		identity = distribution.CurrentReleaseIdentity(candidateVersion, buildID)
		buildArgs := []string{"build", "-trimpath", "-buildvcs=false", "-ldflags", "-X codex-openrouter/internal/launcher.Version=" + candidateVersion + " -X codex-openrouter/internal/launcher.BuildID=" + buildID, "-o", candidate, "./cmd/codex-openrouter"}
		run(t, repo, buildEnv, goBinary, buildArgs...)
	}
	run(t, repo, buildEnv, goBinary, "build", "-trimpath", "-buildvcs=false", "-o", probe, "./internal/integration/testdata/envprobe")

	prefix := filepath.Join(root, "install space café")
	if runtime.GOOS == "darwin" {
		prefix += " \"quote\" 'single' \\backslash \x7f"
	}
	paths := distribution.ReleaseLayout(prefix, identity, target)
	home := filepath.Join(root, "home")
	codexHome := filepath.Join(root, "codex-home")
	for _, directory := range []string{home, codexHome} {
		must(t, os.Mkdir(directory, 0o700))
	}
	environ := []string{
		"HOME=" + home, "ZDOTDIR=" + home, "CODEX_HOME=" + codexHome, "CODEX_OPENROUTER_HOME=" + prefix,
		"PATH=/usr/bin:/bin", "SHELL=/bin/zsh", "LANG=en_US.UTF-8", "TERM=dumb", "TMPDIR=" + root,
		"SMOKE_EXCLUDED=must-be-excluded", "OUTSIDE_INCLUDE_ONLY=must-be-excluded", "SMOKE_INHERITED=inherited",
	}
	downloaded := candidate
	if suppliedMode {
		expected := "codex-openrouter " + identity.Describe() + ", pinned Codex " + distribution.CodexVersion() + "\n"
		if out := run(t, root, environ, downloaded, "--launcher-version"); out != expected {
			t.Fatal("supplied executable identity differs from BUILD.json")
		}
	}
	if installed {
		run(t, root, environ, downloaded, "--install")
		candidate = filepath.Join(prefix, "bin", "codex-openrouter")
		if _, err := os.Stat(filepath.Join(prefix, "config.json")); !os.IsNotExist(err) {
			t.Fatal("fresh installation wrote settings")
		}
		if suppliedMode && (!bytes.Equal(read(t, candidate), suppliedBytes) || !bytes.Equal(read(t, paths.Helper), suppliedBytes)) {
			t.Fatal("installed public/helper bytes differ from the supplied archive executable")
		}
	} else {
		paths = provision(t, fixture, candidate, prefix, target, identity)
	}
	if out := run(t, root, environ, candidate, "--version"); !strings.Contains(out, "codex-cli "+distribution.CodexVersion()) {
		t.Fatal("candidate did not report the pinned Codex version")
	}
	if out := run(t, root, environ, candidate, "--help"); !strings.Contains(out, "Codex CLI") {
		t.Fatal("candidate did not forward native help")
	}
	run(t, root, environ, candidate, "--set-default", "vendor/chosen", "--reasoning", "medium")
	run(t, root, environ, candidate, "--set-default", "vendor/changed")
	settingsPath := filepath.Join(prefix, "config.json")
	settings := read(t, settingsPath)
	var saved struct{ Model, Reasoning string }
	must(t, json.Unmarshal(settings, &saved))
	if saved.Model != "vendor/changed" || saved.Reasoning != "medium" {
		t.Fatal("model-only update lost persisted reasoning")
	}

	included := []string{"PATH", "HOME", "ZDOTDIR", "TMPDIR", "OPENROUTER_API_KEY", "OTHER_*", "AWS_*", "SMOKE_*"}
	legacy := "exclude = [\"SMOKE_EXCLUDED\"]\ninclude_only = " + jsonString(included)
	canonical := `filters = { SMOKE_EXCLUDED = "exclude"`
	for _, name := range included {
		canonical += ", " + jsonString(name) + ` = "include"`
	}
	canonical += " }"
	for _, tc := range []struct{ name, filters string }{{"legacy", legacy}, {"canonical", canonical}} {
		t.Run(tc.name, func(t *testing.T) {
			config := []byte("model = \"ordinary-model\"\n[features]\nshell_snapshot = true\n[shell_environment_policy]\ninherit = \"all\"\nignore_default_excludes = true\n" + tc.filters + "\n[shell_environment_policy.set]\nSMOKE_SENTINEL = \"kept\"\n[projects." + jsonString(root) + "]\ntrust_level = \"trusted\"\n")
			configPath := filepath.Join(codexHome, "config.toml")
			write(t, configPath, config, 0o600)
			if installed && tc.name == "legacy" {
				run(t, root, environ, downloaded, "--install")
				if !bytes.Equal(read(t, settingsPath), settings) || !bytes.Equal(read(t, configPath), config) {
					t.Fatal("repeat installation changed settings or user TOML")
				}
			}
			mock := newProvider(t, shellQuote(probe), codexHome)
			keyEnv := append([]string(nil), environ...)
			for name, value := range syntheticSecrets {
				keyEnv = append(keyEnv, name+"="+value)
			}
			// The mock can request only this probe, so no OS sandbox setup or paid inference is needed.
			out := run(t, root, keyEnv, candidate, "-c", "model_providers.codex_openrouter.base_url="+jsonString(mock.server.URL), "exec", "--ephemeral", "--skip-git-repo-check", "--sandbox", "danger-full-access", "-m", "vendor/session-override", "Run the environment probe, then reply smoke-ok.")
			if !strings.Contains(out, "smoke-ok") {
				t.Fatal("mock session did not complete")
			}
			mock.verify(t)
			if !bytes.Equal(read(t, settingsPath), settings) || !bytes.Equal(read(t, configPath), config) {
				t.Fatal("session overrides changed saved settings or user TOML")
			}
			must(t, checkSnapshotSecrets(codexHome))
		})
	}
	searchFile := filepath.Join(root, "search.txt")
	write(t, searchFile, []byte("before\nneedle café\nafter\n"), 0o600)
	if out := run(t, root, environ, filepath.Join(paths.CodexTree, "codex-path", "rg"), "--fixed-strings", "needle café", searchFile); out != "needle café\n" {
		t.Fatal("bundled ripgrep did not perform the content search")
	}
	if out := run(t, root, environ, filepath.Join(paths.CodexTree, "codex-resources", "zsh", "bin", "zsh"), "-f", "-c", `print -r -- 'bundled-zsh-ok'`); out != "bundled-zsh-ok\n" {
		t.Fatal("bundled zsh did not execute its command")
	}
	t.Logf("candidate=%s\nprefix=%s\nroot=%s\nbuildID=%s", candidate, prefix, root, identity.BuildID)
}

func archiveCandidate(t *testing.T, candidate string) (distribution.ReleaseIdentity, []byte) {
	t.Helper()
	if !filepath.IsAbs(candidate) {
		t.Fatal("supplied candidate path must be absolute")
	}
	file, err := platform.OpenChecked(filepath.Join(filepath.Dir(candidate), "BUILD.json"))
	must(t, err)
	metadata, err := platform.ReadFileLimit(file, 64<<10)
	file.Close()
	must(t, err)
	var record struct {
		SchemaVersion int                          `json:"schemaVersion"`
		Identity      distribution.ReleaseIdentity `json:"identity"`
		ReleaseID     string                       `json:"releaseId"`
		CodexVersion  string                       `json:"codexVersion"`
		Files         map[string]struct {
			Bytes  int64  `json:"bytes"`
			SHA256 string `json:"sha256"`
		} `json:"files"`
	}
	must(t, jsonv2.Unmarshal(metadata, &record))
	identity := distribution.CurrentReleaseIdentity(record.Identity.Version, record.Identity.BuildID)
	if record.SchemaVersion != 1 || !identity.IsRelease() || identity.Version == "" || record.Identity != identity || record.ReleaseID != identity.ID() || record.CodexVersion != distribution.CodexVersion() {
		t.Fatal("supplied candidate BUILD.json has an incompatible identity")
	}
	file, err = platform.OpenChecked(candidate)
	must(t, err)
	data, err := platform.ReadFileLimit(file, 128<<20)
	file.Close()
	must(t, err)
	sum := sha256.Sum256(data)
	entry := record.Files["codex-openrouter"]
	if entry.Bytes != int64(len(data)) || entry.SHA256 != hex.EncodeToString(sum[:]) {
		t.Fatal("supplied candidate bytes differ from BUILD.json")
	}
	return identity, data
}

func sourceDigest(t *testing.T, repo string) string {
	t.Helper()
	files := []string{"go.mod", ".go-version", "LICENSE"}
	for _, directory := range []string{"cmd", "internal"} {
		must(t, filepath.WalkDir(filepath.Join(repo, directory), func(path string, entry os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if !entry.IsDir() {
				relative, err := filepath.Rel(repo, path)
				if err != nil {
					return err
				}
				files = append(files, relative)
			}
			return nil
		}))
	}
	sort.Strings(files)
	digest := sha256.New()
	fmt.Fprintf(digest, "%s\x00%s\x00%s/%s\x00%s\x00%s\x00", candidateVersion, runtime.Version(), runtime.GOOS, runtime.GOARCH, distribution.BuildRecipeID, "-trimpath -buildvcs=false CGO_ENABLED=0")
	for _, name := range files {
		data := read(t, filepath.Join(repo, name))
		fmt.Fprintf(digest, "%d:%s%d:", len(name), name, len(data))
		digest.Write(data)
	}
	return hex.EncodeToString(digest.Sum(nil))
}

func provision(t *testing.T, fixture, candidate, prefix string, target distribution.Target, identity distribution.ReleaseIdentity) distribution.ReleasePaths {
	t.Helper()
	for _, entry := range target.Inventory {
		file, err := platform.OpenChecked(filepath.Join(fixture, filepath.FromSlash(entry.Path)))
		must(t, err)
		digest := sha256.New()
		n, err := io.Copy(digest, file)
		file.Close()
		must(t, err)
		if n != entry.Bytes || hex.EncodeToString(digest.Sum(nil)) != entry.SHA256 {
			t.Fatalf("native fixture does not match the pinned inventory: %s", entry.Path)
		}
	}
	paths := distribution.ReleaseLayout(prefix, identity, target)
	for _, entry := range target.Inventory {
		path := filepath.Join(paths.CodexTree, filepath.FromSlash(entry.Path))
		must(t, os.MkdirAll(filepath.Dir(path), 0o700))
		source, err := platform.OpenChecked(filepath.Join(fixture, filepath.FromSlash(entry.Path)))
		must(t, err)
		mode := os.FileMode(0o600)
		if entry.Executable {
			mode = 0o700
		}
		dest, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
		must(t, err)
		digest := sha256.New()
		n, err := io.Copy(io.MultiWriter(dest, digest), source)
		source.Close()
		must(t, err)
		must(t, dest.Sync())
		must(t, dest.Close())
		if n != entry.Bytes || hex.EncodeToString(digest.Sum(nil)) != entry.SHA256 {
			t.Fatalf("copied native file does not match the pinned inventory: %s", entry.Path)
		}
	}
	helper := read(t, candidate)
	write(t, paths.Helper, helper, 0o700)
	write(t, paths.AuditManifest, distribution.ManifestBytes(), 0o600)
	for _, name := range distribution.NoticeNames() {
		notice, err := distribution.Notice(name)
		must(t, err)
		write(t, filepath.Join(paths.Licenses, name), notice, 0o600)
	}
	record, err := distribution.NewInstallation(identity, helper)
	must(t, err)
	metadata, err := distribution.FormatInstallation(record)
	must(t, err)
	write(t, paths.Installation, metadata, 0o600)
	must(t, distribution.ValidateRelease(paths, target, identity))
	return paths
}

func run(t *testing.T, directory string, environ []string, binary string, args ...string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, binary, args...)
	command.Dir, command.Env = directory, environ
	command.WaitDelay = 5 * time.Second
	var stdout, stderr bytes.Buffer
	command.Stdout, command.Stderr = &stdout, &stderr
	if err := command.Run(); err != nil {
		t.Fatalf("%s failed: %v\n%s", filepath.Base(binary), err, stderr.String())
	}
	return stdout.String()
}

func write(t *testing.T, path string, data []byte, mode os.FileMode) {
	t.Helper()
	must(t, os.MkdirAll(filepath.Dir(path), 0o700))
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if os.IsExist(err) {
		err = os.WriteFile(path, data, mode)
		must(t, err)
		return
	}
	must(t, err)
	_, err = file.Write(data)
	must(t, err)
	must(t, file.Sync())
	must(t, file.Close())
}

func read(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	must(t, err)
	return data
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func jsonString(value any) string {
	data, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return string(data)
}

func shellQuote(value string) string { return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'" }

type providerRequest struct {
	Model     string                  `json:"model"`
	Reasoning struct{ Effort string } `json:"reasoning"`
	Tools     []struct{ Name string } `json:"tools"`
	Input     []struct {
		Type   string          `json:"type"`
		Output json.RawMessage `json:"output"`
	} `json:"input"`
}

type provider struct {
	server        *httptest.Server
	mu            sync.Mutex
	requests      []providerRequest
	authorization []string
	toolSent      bool
	errors        []string
}

func newProvider(t *testing.T, probeCommand, codexHome string) *provider {
	t.Helper()
	mock := &provider{}
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mock.mu.Lock()
		defer mock.mu.Unlock()
		if strings.HasPrefix(r.URL.Path, "/models") {
			w.Header().Set("Content-Type", "application/json")
			io.WriteString(w, `{"models":[]}`)
			return
		}
		if r.URL.Path != "/responses" || r.Method != http.MethodPost || len(mock.requests) >= 16 {
			mock.errors = append(mock.errors, "unexpected request")
			http.Error(w, "unexpected request", http.StatusBadRequest)
			return
		}
		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 2<<20))
		var request providerRequest
		if err != nil || json.Unmarshal(body, &request) != nil {
			mock.errors = append(mock.errors, "invalid or oversized request")
			http.Error(w, "invalid request", http.StatusBadRequest)
			return
		}
		mock.requests = append(mock.requests, request)
		mock.authorization = append(mock.authorization, r.Header.Get("Authorization"))
		if err := checkSnapshotSecrets(codexHome); err != nil {
			mock.errors = append(mock.errors, err.Error())
		}
		item := map[string]any{"id": "msg_smoke", "type": "message", "role": "assistant", "content": []any{map[string]any{"type": "output_text", "text": "smoke-ok"}}}
		for _, tool := range request.Tools {
			if !mock.toolSent && tool.Name == "exec_command" {
				mock.toolSent = true
				item = map[string]any{"id": "fc_probe", "type": "function_call", "call_id": "call_probe", "name": "exec_command", "arguments": jsonString(map[string]any{"cmd": probeCommand, "login": false, "max_output_tokens": 1000, "yield_time_ms": 1000})}
				break
			}
		}
		w.Header().Set("Content-Type", "text/event-stream")
		for _, event := range []map[string]any{
			{"type": "response.created", "response": map[string]any{"id": "resp_smoke"}},
			{"type": "response.output_item.done", "output_index": 0, "item": item},
			{"type": "response.completed", "response": map[string]any{"id": "resp_smoke", "status": "completed", "output": []any{item}, "usage": map[string]int{"input_tokens": 1, "output_tokens": 1, "total_tokens": 2}}},
		} {
			fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event["type"], jsonString(event))
		}
	})
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	must(t, err)
	mock.server = &httptest.Server{Listener: listener, Config: &http.Server{
		Handler: handler, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second,
		WriteTimeout: 10 * time.Second, IdleTimeout: 10 * time.Second, MaxHeaderBytes: 8 << 10,
	}}
	mock.server.Start()
	t.Cleanup(mock.server.Close)
	return mock
}

func (mock *provider) verify(t *testing.T) {
	t.Helper()
	mock.mu.Lock()
	defer mock.mu.Unlock()
	if len(mock.errors) != 0 || len(mock.requests) < 2 || !mock.toolSent {
		t.Fatalf("mock did not observe a complete tool session: requests=%d toolSent=%v errors=%v", len(mock.requests), mock.toolSent, mock.errors)
	}
	var outputs []string
	for i, request := range mock.requests {
		if mock.authorization[i] != "Bearer "+syntheticSecrets["OPENROUTER_API_KEY"] {
			t.Fatal("Responses request did not authenticate through the immutable helper")
		}
		if request.Model != "vendor/session-override" || request.Reasoning.Effort != "medium" {
			t.Fatal("session model or persisted reasoning did not reach the provider")
		}
		for _, input := range request.Input {
			if input.Type == "function_call_output" {
				var output string
				must(t, json.Unmarshal(input.Output, &output))
				outputs = append(outputs, output)
			}
		}
	}
	var report struct {
		Status string          `json:"status"`
		Checks map[string]bool `json:"checks"`
	}
	for _, output := range outputs {
		if start := strings.Index(output, `{"status":`); start >= 0 {
			must(t, json.NewDecoder(strings.NewReader(output[start:])).Decode(&report))
			break
		}
	}
	if report.Status != "credential-isolated" {
		t.Fatal("the real Codex tool output did not confirm the environment contract")
	}
	for _, name := range []string{"OPENROUTER_API_KEY", "OTHER_API_KEY", "OTHER_TOKEN", "AWS_SECRET_ACCESS_KEY", "SMOKE_EXCLUDED", "OUTSIDE_INCLUDE_ONLY", "sentinelPreserved", "inheritedPreserved"} {
		if !report.Checks[name] {
			t.Fatalf("tool output did not confirm %s", name)
		}
	}
}

func checkSnapshotSecrets(codexHome string) error {
	directory := filepath.Join(codexHome, "shell_snapshots")
	if _, err := os.Stat(directory); os.IsNotExist(err) {
		return nil
	}
	return filepath.WalkDir(directory, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.IsDir() {
			data, err := os.ReadFile(path)
			if err != nil {
				return fmt.Errorf("cannot inspect shell snapshots")
			}
			for _, secret := range syntheticSecrets {
				if bytes.Contains(data, []byte(secret)) {
					return fmt.Errorf("shell snapshot persisted a synthetic credential")
				}
			}
		}
		return nil
	})
}
