package install

import (
	"errors"
	"os"
)

func checkLinkOwner(os.FileInfo) error {
	return errors.New("native Windows migration is not available")
}
