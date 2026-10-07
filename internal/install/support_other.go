//go:build !darwin

package install

import "errors"

func checkMinimumOS(minimum string) error {
	if minimum != "" {
		return errors.New("cannot check the minimum OS version on this platform")
	}
	return nil
}
