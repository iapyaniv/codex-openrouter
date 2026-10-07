package install

import (
	"fmt"
	"strconv"
	"strings"
	"syscall"
)

func checkMinimumOS(minimum string) error {
	version, err := syscall.Sysctl("kern.osproductversion")
	if err != nil {
		return fmt.Errorf("cannot determine macOS version before installation: %w", err)
	}
	current := strings.Split(version, ".")
	required := strings.Split(minimum, ".")
	for i := 0; i < len(required); i++ {
		want, err := strconv.Atoi(required[i])
		if err != nil {
			return fmt.Errorf("invalid embedded macOS minimum")
		}
		have := 0
		if i < len(current) {
			have, err = strconv.Atoi(current[i])
			if err != nil {
				return fmt.Errorf("cannot parse macOS version")
			}
		}
		if have > want {
			return nil
		}
		if have < want {
			return fmt.Errorf("installation requires macOS %s or later", minimum)
		}
	}
	return nil
}
