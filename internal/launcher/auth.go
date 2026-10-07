package launcher

import (
	"errors"
	"io"
	"os"
	"strings"
)

// trimCutset matches the JavaScript helper's String.prototype.trim set:
// U+0009–U+000D, U+0020, U+00A0, U+1680, U+2000–U+200A, U+2028, U+2029,
// U+202F, U+205F, U+3000, and U+FEFF. Notably it excludes U+0085, which
// strings.TrimSpace would remove. Do not substitute a different Unicode
// whitespace definition.
const trimCutset = "\t\n\v\f\r \u00a0\u1680\u2000\u2001\u2002\u2003\u2004\u2005\u2006\u2007\u2008\u2009\u200a\u2028\u2029\u202f\u205f\u3000\ufeff"

// InternalAuth implements the credential helper protocol: the trimmed
// OPENROUTER_API_KEY goes to stdout with no newline; a missing or blank key
// returns a generic error with no stdout output and no value leakage.
func InternalAuth(stdout io.Writer) error {
	key, ok := os.LookupEnv("OPENROUTER_API_KEY")
	if !ok {
		return errors.New("OPENROUTER_API_KEY is not set")
	}
	trimmed := strings.Trim(key, trimCutset)
	if trimmed == "" {
		return errors.New("OPENROUTER_API_KEY is not set")
	}
	if _, err := io.WriteString(stdout, trimmed); err != nil {
		return errors.New("cannot write the credential")
	}
	return nil
}
