package install

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestNativeProbeEnvironmentAndOutputFailures(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix probe fixture")
	}
	for _, name := range []string{"clean environment", "wrong version", "failed output", "output cap", "canceled"} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			t.Setenv("OPENROUTER_API_KEY", "synthetic-parent-key")
			t.Setenv("OTHER_TOKEN", "synthetic-parent-token")
			t.Setenv("CODEX_MANAGED_BY_NPM", "1")
			body := `if [ -n "$OPENROUTER_API_KEY$OTHER_TOKEN$CODEX_MANAGED_BY_NPM" ]; then exit 9; fi
if [ "$1" = '--version' ]; then printf 'codex-cli 0.155.1\n'; else printf 'Codex CLI\n'; fi
`
			switch name {
			case "wrong version":
				body = "printf 'codex-cli 0.0.0\\n'\n"
			case "failed output":
				body = "printf 'synthetic-private-output' >&2\nexit 7\n"
			case "output cap":
				body = "printf '%17000s' 'synthetic-private-output'\n"
			}
			script := filepath.Join(root, "native-probe")
			if err := os.WriteFile(script, []byte("#!/bin/sh\n"+body), 0o700); err != nil {
				t.Fatal(err)
			}
			ctx := context.Background()
			if name == "canceled" {
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}
			err := probe(ctx, script, root, "0.155.1")
			if name == "clean environment" {
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			if err == nil {
				t.Fatal("invalid native probe accepted")
			}
			if strings.Contains(err.Error(), "synthetic-private-output") {
				t.Fatal("probe diagnostic echoed arbitrary output")
			}
		})
	}
}
