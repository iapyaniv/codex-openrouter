package launcher

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"codex-openrouter/internal/distribution"
)

func runCLI(args ...string) (int, string, string) {
	var stdout, stderr bytes.Buffer
	status := Run(args, &stdout, &stderr)
	return status, stdout.String(), stderr.String()
}

func TestInternalAuthProtocol(t *testing.T) {
	key := "\t\r\n\u00a0\u3000test-only-key \u3000\u00a0\n"
	t.Setenv("OPENROUTER_API_KEY", key)
	status, stdout, stderr := runCLI("--internal-auth")
	if status != 0 || stderr != "" {
		t.Fatalf("status=%d stderr=%q", status, stderr)
	}
	if stdout != "test-only-key" {
		t.Fatalf("stdout=%q", stdout)
	}

	t.Setenv("OPENROUTER_API_KEY", "  \t")
	if status, stdout, _ := runCLI("--internal-auth"); status != 1 || stdout != "" {
		t.Fatalf("blank key: status=%d stdout=%q", status, stdout)
	}
	os.Unsetenv("OPENROUTER_API_KEY")
	status, stdout, stderr = runCLI("--internal-auth")
	if status != 1 || stdout != "" {
		t.Fatalf("missing key: status=%d stdout=%q", status, stdout)
	}
	if !strings.HasPrefix(stderr, "codex-openrouter: ") {
		t.Fatalf("stderr=%q", stderr)
	}

	marker := filepath.Join(t.TempDir(), "executed")
	injection := `$(touch "` + marker + `") & %PATH% "literal"`
	t.Setenv("OPENROUTER_API_KEY", injection)
	if status, stdout, _ := runCLI("--internal-auth"); status != 0 || stdout != injection {
		t.Fatalf("status=%d stdout=%q", status, stdout)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("key was interpreted as a command")
	}
}

func TestKeyFreeModesNeedNoPrefixOrKey(t *testing.T) {
	os.Unsetenv("OPENROUTER_API_KEY")
	missing := filepath.Join(t.TempDir(), "prefix")
	t.Setenv("CODEX_OPENROUTER_HOME", missing)

	status, stdout, _ := runCLI("--launcher-version")
	if status != 0 || !strings.Contains(stdout, "pinned Codex "+PinnedCodexVersion) {
		t.Fatalf("status=%d stdout=%q", status, stdout)
	}
	status, stdout, _ = runCLI("--launcher-help")
	if status != 0 || !strings.Contains(stdout, "--set-default") {
		t.Fatalf("status=%d stdout=%q", status, stdout)
	}
	status, stdout, _ = runCLI("--install", "--help")
	if status != 0 || !strings.Contains(stdout, "--install") {
		t.Fatalf("status=%d stdout=%q", status, stdout)
	}
	for _, help := range []string{"--help", "-h"} {
		status, stdout, stderr := runCLI("--set-default", help)
		if status != 0 || !strings.HasPrefix(stdout, "Usage:") || stderr != "" {
			t.Fatalf("%s: status=%d stdout=%q stderr=%q", help, status, stdout, stderr)
		}
	}
	if status, stdout, _ := runCLI("--install"); status != 1 || stdout != "" {
		t.Fatal("unsupported or unstamped installation was accepted")
	}
	if _, err := os.Stat(missing); !os.IsNotExist(err) {
		t.Fatal("key-free mode created the prefix")
	}
}

func TestSetDefaultVariations(t *testing.T) {
	home := testHome(t)
	cases := []struct {
		args      []string
		wantModel string
		wantLevel string
	}{
		{[]string{"vendor/first:free", "--reasoning", "low"}, "vendor/first:free", "low"},
		{[]string{"--model", "vendor/second"}, "vendor/second", "low"},
		{[]string{"--reasoning", "max"}, "vendor/second", "max"},
		{[]string{"--model=vendor/third", "--reasoning=minimal"}, "vendor/third", "minimal"},
		{[]string{"--", "vendor/fourth"}, "vendor/fourth", "minimal"},
	}
	for _, tc := range cases {
		args := append([]string{"--set-default"}, tc.args...)
		status, stdout, stderr := runCLI(args...)
		if status != 0 {
			t.Fatalf("%v: status=%d stderr=%q", tc.args, status, stderr)
		}
		want := "Default: " + tc.wantModel + " (" + tc.wantLevel + ")\n"
		if stdout != want {
			t.Fatalf("%v: stdout=%q want %q", tc.args, stdout, want)
		}
		settings, err := ReadSettings(home)
		if err != nil {
			t.Fatal(err)
		}
		if settings.Model != tc.wantModel || settings.Reasoning != tc.wantLevel {
			t.Fatalf("%v: got %+v", tc.args, settings)
		}
	}

	before, err := os.ReadFile(filepath.Join(home, "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{},
		{"one", "two"},
		{"one", "--model", "two"},
		{"--model"},
		{"--reasoning"},
		{"--model", "vendor/a", "--model", "vendor/b"},
		{"--model=vendor/a", "--model=vendor/b"},
		{"--reasoning", "low", "--reasoning", "high"},
		{"--unknown", "vendor/a"},
		{"--", "one", "two"},
		{"--help", "--unknown"},
		{"vendor/first", "vendor/second", "--help"},
		{"vendor/first", "-h"},
		{"--model", "--help"},
		{"--reasoning", "-h"},
		{"--", "--help"},
	} {
		status, stdout, _ := runCLI(append([]string{"--set-default"}, args...)...)
		if status != 1 || stdout != "" {
			t.Fatalf("%v: status=%d stdout=%q", args, status, stdout)
		}
		after, err := os.ReadFile(filepath.Join(home, "config.json"))
		if err != nil {
			t.Fatal(err)
		}
		if string(after) != string(before) {
			t.Fatalf("%v: settings file changed", args)
		}
	}
	status, stdout, _ := runCLI("--set-default", "--help")
	if status != 0 || !strings.HasPrefix(stdout, "Usage:") {
		t.Fatalf("--help: status=%d stdout=%q", status, stdout)
	}
}

func TestSetDefaultIdempotentRewrite(t *testing.T) {
	home := testHome(t)
	status, _, stderr := runCLI("--set-default", "vendor/same", "--reasoning", "low")
	if status != 0 {
		t.Fatalf("initial: %q", stderr)
	}
	for _, args := range [][]string{
		{"vendor/same", "--reasoning", "low"},
		{"--model", "vendor/same"},
		{"--reasoning", "low"},
		{"--model=vendor/same", "--reasoning=low"},
	} {
		status, stdout, stderr := runCLI(append([]string{"--set-default"}, args...)...)
		if status != 0 {
			t.Fatalf("%v: status=%d stderr=%q", args, status, stderr)
		}
		if stdout != "Default: vendor/same (low)\n" {
			t.Fatalf("%v: stdout=%q", args, stdout)
		}
	}
	settings, err := ReadSettings(home)
	if err != nil {
		t.Fatal(err)
	}
	if settings.Model != "vendor/same" || settings.Reasoning != "low" {
		t.Fatalf("got %+v", settings)
	}
}

func TestShowConfig(t *testing.T) {
	home := testHome(t)
	status, stdout, _ := runCLI("--show-config")
	if status != 0 {
		t.Fatalf("status=%d", status)
	}
	want := filepath.Join(home, "config.json") + "\n{\n  \"model\": \"deepseek/deepseek-v4.1-flash\",\n  \"reasoning\": \"high\"\n}\n"
	if stdout != want {
		t.Fatalf("stdout=%q want %q", stdout, want)
	}
	if status, _, _ := runCLI("--show-config", "extra"); status != 1 {
		t.Fatal("extra argument accepted")
	}
}

func TestCodexArguments(t *testing.T) {
	settings := Settings{Model: "vendor/model", Reasoning: "low"}
	caller := []string{"exec", "-m", "vendor/temporary", `spaces "quotes" & $(echo nope) café`}
	built, err := CodexArguments(settings, `/im mutable\helper"path`, caller)
	if err != nil {
		t.Fatal(err)
	}

	if len(built) != 16+len(caller) {
		t.Fatalf("built %d arguments", len(built))
	}
	for i, arg := range caller {
		if built[len(built)-len(caller)+i] != arg {
			t.Fatalf("caller argument %d changed: %q", i, built[len(built)-len(caller)+i])
		}
	}
	joined := strings.Join(built, "\n")
	for _, want := range []string{
		`model_provider="codex_openrouter"`,
		`model="vendor/model"`,
		`model_reasoning_effort="low"`,
		`check_for_update_on_startup=false`,
		`shell_environment_policy.ignore_default_excludes=false`,
		`shell_environment_policy.set.OPENROUTER_API_KEY=""`,
		`features.shell_snapshot=false`,
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("missing %q in:\n%s", want, joined)
		}
	}
	// The helper path is JSON-encoded inside the TOML auth table, preserving
	// backslashes and quotes without Go-specific escapes.
	if !strings.Contains(joined, `{ command = "/im mutable\\helper\"path", args = ["--internal-auth"] }`) {
		t.Fatalf("auth helper encoding wrong:\n%s", joined)
	}
	if strings.Contains(joined, `\x`) {
		t.Fatal("Go-specific escape in TOML output")
	}
	// Defaults precede caller arguments so caller options win downstream.
	if built[0] != "-c" || built[len(built)-len(caller)-1] != "features.shell_snapshot=false" {
		t.Fatal("ordering wrong")
	}
}

func TestTomlBasicStringEncoding(t *testing.T) {
	cases := []struct {
		value string
		want  string
	}{
		{"plain", `"plain"`},
		{"spaces and ünïcode", `"spaces and ünïcode"`},
		{`quote " and back\slash`, `"quote \" and back\\slash"`},
		{"tab\tnewline\nreturn\r", `"tab\tnewline\nreturn\r"`},
		{"del\x7fchar", `"del\u007fchar"`},
		{"bell\a", `"bell\u0007"`},
	}
	for _, tc := range cases {
		got, err := tomlBasicString(tc.value)
		if err != nil {
			t.Fatalf("%q: %v", tc.value, err)
		}
		if got != tc.want {
			t.Fatalf("%q: got %s, want %s", tc.value, got, tc.want)
		}
	}
	if _, err := tomlBasicString("bad\xffutf8"); err == nil {
		t.Fatal("invalid UTF-8 accepted")
	}
	if _, err := CodexArguments(Settings{Model: "vendor/model", Reasoning: "low"}, "bad\xffpath", nil); err == nil {
		t.Fatal("invalid UTF-8 helper path accepted")
	}
}
func TestUpdateRecognition(t *testing.T) {
	intercepted := [][]string{
		{"update"},
		{"-c", "x=y", "update"},
		{"--config=x=y", "--no-alt-screen", "update"},
		{"-cx=y", "-m", "vendor/model", "update"},
		{"--profile", "update", "--search", "update"},
		{"--image", "one.png", "two.png", "--search", "update"},
		{"--image=one.png", "update"},
		{"-ione.png", "update"},
		{"--yolo", "update"},
		{"--not-so-yolo", "update"},
		{"--yolo", "--search", "update"},
	}
	for _, args := range intercepted {
		if !IsUpdateCommand(args) {
			t.Fatalf("%v should be intercepted", args)
		}
	}
	forwarded := [][]string{
		{"Please update the dependencies"},
		{"exec", "update"},
		{"plugin", "update", "sample"},
		{"help", "update"},
		{"--", "update"},
		{"-c", "x=y", "--", "update"},
		{"exec", "--", "update"},
		{"--profile", "update", "Explain this project"},
		{"--image", "one.png", "update"},
		{"--unknown-option", "update"},
		{"--yolo", "Please update the dependencies"},
		{"--not-so-yolo", "exec", "update"},
	}
	for _, args := range forwarded {
		if IsUpdateCommand(args) {
			t.Fatalf("%v should be forwarded", args)
		}
	}

	os.Unsetenv("OPENROUTER_API_KEY")
	testHome(t)
	status, stdout, stderr := runCLI("update")
	if status != 0 || stderr != "" || stdout != UpdateInstructions+"\n" {
		t.Fatalf("status=%d stdout=%q stderr=%q", status, stdout, stderr)
	}
}

func TestOrdinaryLaunchReportsMissingInstallation(t *testing.T) {
	t.Setenv("OPENROUTER_API_KEY", "test-only-key")
	testHome(t)
	status, stdout, stderr := runCLI("exec", "hello")
	if status != 1 || stdout != "" {
		t.Fatalf("status=%d stdout=%q", status, stdout)
	}
	if _, err := distribution.CurrentTarget(); err != nil {
		if !strings.Contains(stderr, "no published managed Codex distribution") {
			t.Fatalf("stderr=%q", stderr)
		}
		return
	}
	if !strings.Contains(stderr, "missing or corrupt") || !strings.Contains(stderr, "--install") {
		t.Fatalf("stderr=%q", stderr)
	}
}

func TestOrdinaryLaunchRequiresKeyBeforeInstallCheck(t *testing.T) {
	os.Unsetenv("OPENROUTER_API_KEY")
	testHome(t)
	status, stdout, stderr := runCLI("exec", "hello")
	if status != 1 || stdout != "" {
		t.Fatalf("status=%d stdout=%q", status, stdout)
	}
	want := "OPENROUTER_API_KEY is not set"
	if _, err := distribution.CurrentTarget(); err != nil {
		want = "no published managed Codex distribution"
	}
	if !strings.Contains(stderr, want) {
		t.Fatalf("stderr=%q", stderr)
	}
}
