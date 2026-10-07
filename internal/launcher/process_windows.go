//go:build windows

package launcher

import (
	"fmt"
	"io"
	"os"
	"os/exec"

	"codex-openrouter/internal/distribution"
)

// The child inherits streams/console without signal.Ignore or manual forwarding.
// Console behavior remains unverified; Windows is withheld from the manifest.
func execNative(binary string, args []string, stderr io.Writer) int {
	command := exec.Command(binary, args...)
	command.Stdin = os.Stdin
	command.Stdout = os.Stdout
	command.Stderr = os.Stderr
	command.Env = distribution.ManagedChildEnvironment(os.Environ())
	if err := command.Run(); err != nil {
		if exit, ok := err.(*exec.ExitError); ok {
			return exit.ExitCode()
		}
		return fail(stderr, fmt.Sprintf("cannot execute the managed Codex at %s: %v", binary, err))
	}
	return 0
}
