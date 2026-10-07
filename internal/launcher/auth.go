package launcher

import (
	"errors"
	"io"
	"os"
	"strings"
)

// InternalAuth implements the credential helper protocol: the trimmed
// OPENROUTER_API_KEY goes to stdout with no newline; a missing or blank key
// returns a generic error with no stdout output and no value leakage.
func InternalAuth(stdout io.Writer) error {
	key, ok := os.LookupEnv("OPENROUTER_API_KEY")
	if !ok {
		return errors.New("OPENROUTER_API_KEY is not set")
	}
	trimmed := strings.TrimSpace(key)
	if trimmed == "" {
		return errors.New("OPENROUTER_API_KEY is not set")
	}
	if _, err := io.WriteString(stdout, trimmed); err != nil {
		return errors.New("cannot write the credential")
	}
	return nil
}
