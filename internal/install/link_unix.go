//go:build unix

package install

import (
	"errors"
	"os"
	"syscall"
)

func checkLinkOwner(info os.FileInfo) error {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat.Uid != uint32(os.Getuid()) {
		return errors.New("refusing a public link owned by another user")
	}
	return nil
}
