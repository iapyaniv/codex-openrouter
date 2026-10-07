package install

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"
)

type probeOutput struct {
	mu        sync.Mutex
	data      bytes.Buffer
	remaining int
	cancel    context.CancelFunc
}

func (output *probeOutput) Write(data []byte) (int, error) {
	output.mu.Lock()
	defer output.mu.Unlock()
	if len(data) > output.remaining {
		output.cancel()
		return 0, errors.New("probe output exceeds its limit")
	}
	output.remaining -= len(data)
	return output.data.Write(data)
}

func probe(ctx context.Context, binary, directory, version string) error {
	for _, name := range []string{"home", "codex-home", "tmp"} {
		if err := os.Mkdir(filepath.Join(directory, name), 0o700); err != nil && !errors.Is(err, os.ErrExist) {
			return err
		}
	}
	for _, argument := range []string{"--version", "--help"} {
		deadline, cancel := context.WithTimeout(ctx, 10*time.Second)
		output := &probeOutput{remaining: 16 << 10, cancel: cancel}
		command := exec.CommandContext(deadline, binary, argument)
		command.Dir = directory
		command.Env = []string{"HOME=" + filepath.Join(directory, "home"), "CODEX_HOME=" + filepath.Join(directory, "codex-home"), "ZDOTDIR=" + filepath.Join(directory, "home"), "TMPDIR=" + filepath.Join(directory, "tmp"), "PATH=/usr/bin:/bin", "TERM=dumb"}
		command.WaitDelay = time.Second
		command.Stdout, command.Stderr = output, output
		err := command.Run()
		cancel()
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err != nil {
			return errors.New("verified native Codex failed its bounded startup probe")
		}
		if argument == "--version" && !bytes.Equal(bytes.TrimSpace(output.data.Bytes()), []byte("codex-cli "+version)) {
			return errors.New("verified native Codex reported an unexpected version")
		}
	}
	return nil
}
