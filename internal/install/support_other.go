//go:build !darwin

package install

import "errors"

func checkMinimumOS(string) error {
	return errors.New("installation is not available on this platform")
}
