package install

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"runtime"

	"codex-openrouter/internal/distribution"
	"codex-openrouter/internal/platform"
)

const maxLauncherBytes = 128 << 20

func sum(data []byte) string {
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:])
}

type priorEntry struct {
	data []byte
	info os.FileInfo
}

func checkedDirectory(path string, create bool) error {
	if create {
		if err := os.Mkdir(path, 0o700); err != nil && !errors.Is(err, os.ErrExist) {
			return err
		}
	}
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if err := platform.CheckManagedDir(info, path); err != nil {
		return err
	}
	return platform.CheckPrivate(info, path)
}

func readChecked(path string, limit int64) ([]byte, os.FileInfo, error) {
	file, err := platform.OpenChecked(path)
	if err != nil {
		return nil, nil, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, nil, err
	}
	data, err := platform.ReadFileLimit(file, limit)
	return data, info, err
}

func inspectPublic(prefix, public string, sourceInfo os.FileInfo, sourceBytes []byte) (priorEntry, error) {
	info, err := os.Lstat(public)
	if errors.Is(err, os.ErrNotExist) {
		return priorEntry{}, nil
	}
	if err != nil {
		return priorEntry{}, err
	}
	if os.SameFile(sourceInfo, info) {
		return priorEntry{}, errors.New("run --install from a downloaded copy outside the public command being replaced")
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return priorEntry{}, errors.New("refusing a public symlink; move it aside explicitly before installation")
	}
	data, opened, err := readChecked(public, maxLauncherBytes)
	if err != nil {
		return priorEntry{}, err
	}
	if os.SameFile(sourceInfo, opened) {
		return priorEntry{}, errors.New("run --install from a downloaded copy outside the public command being replaced")
	}
	// Moving aside a damaged release removes its inventory; the trusted downloaded bytes still identify this public copy.
	if bytes.Equal(data, sourceBytes) {
		return priorEntry{data: data, info: info}, nil
	}
	releases := filepath.Join(prefix, "releases")
	if err := checkedDirectory(releases, false); err != nil {
		return priorEntry{}, errors.New("refusing an unrecognized public executable")
	}
	children, err := os.ReadDir(releases)
	if err != nil {
		return priorEntry{}, err
	}
	for _, child := range children {
		root := filepath.Join(releases, child.Name())
		if !child.IsDir() || checkedDirectory(root, false) != nil {
			continue
		}
		record, err := distribution.ReadInstallation(filepath.Join(root, "installation.json"))
		if err != nil {
			continue
		}
		identity, err := record.Identity()
		if err != nil || identity.ID() != child.Name() || identity.Target != runtime.GOOS+"-"+runtime.GOARCH {
			continue
		}
		helper, _, err := readChecked(filepath.Join(root, "codex-openrouter"), maxLauncherBytes)
		if err != nil || int64(len(helper)) != record.HelperBytes || sum(helper) != record.HelperSHA256 || sum(data) != record.HelperSHA256 {
			continue
		}
		audit, _, err := readChecked(filepath.Join(root, "codex-artifacts.json"), 256<<10)
		if err != nil || sum(audit) != record.ArtifactDigest {
			continue
		}
		return priorEntry{data: data, info: info}, nil
	}
	return priorEntry{}, errors.New("refusing an unrecognized public executable; move it aside explicitly before installation")
}

func writeSynced(path string, data []byte, mode os.FileMode) error {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return err
	}
	_, writeErr := file.Write(data)
	syncErr := file.Sync()
	closeErr := file.Close()
	return errors.Join(writeErr, syncErr, closeErr)
}

func preparePublic(directory string, data []byte) (string, error) {
	file, err := os.CreateTemp(directory, ".codex-openrouter-*")
	if err != nil {
		return "", err
	}
	path := file.Name()
	modeErr := file.Chmod(0o700)
	_, writeErr := file.Write(data)
	syncErr := file.Sync()
	closeErr := file.Close()
	if err := errors.Join(modeErr, writeErr, syncErr, closeErr); err != nil {
		return path, err
	}
	copy, info, err := readChecked(path, maxLauncherBytes)
	if err != nil || sum(copy) != sum(data) || info.Mode().Perm()&0o100 == 0 {
		return path, errors.Join(errors.New("prepared public executable could not be verified"), err)
	}
	return path, nil
}
