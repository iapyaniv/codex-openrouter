//go:build unix

package launcher

import (
	"fmt"
	"io"
	"os"
	"syscall"

	"codex-openrouter/internal/distribution"
)

// execNative replaces this process with the managed native Codex: absolute
// path, caller argv boundaries preserved after the injected defaults, and
// the caller's working directory, PID, streams, and signal semantics
// retained. Package-manager marker variables are removed per the manifest.
func execNative(binary string, args []string, stderr io.Writer) int {
	argv := append([]string{binary}, args...)
	env := distribution.ManagedChildEnvironment(os.Environ())
	if err := syscall.Exec(binary, argv, env); err != nil {
		return fail(stderr, fmt.Sprintf("cannot execute the managed Codex at %s: %v", binary, err))
	}
	return 1
}
