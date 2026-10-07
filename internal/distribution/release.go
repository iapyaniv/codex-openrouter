package distribution

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"codex-openrouter/internal/platform"
)

const LicensesDirName = "LICENSES"

type ReleasePaths struct {
	Root          string
	Helper        string
	CodexTree     string
	CodexBinary   string
	AuditManifest string
	Installation  string
	Licenses      string
}

func ReleaseLayout(prefix string, identity ReleaseIdentity, target Target) ReleasePaths {
	root := filepath.Join(prefix, "releases", identity.ID())
	helperName := "codex-openrouter"
	if runtime.GOOS == "windows" {
		helperName = "codex-openrouter.exe"
	}
	tree := filepath.Join(root, "codex")
	return ReleasePaths{
		Root:          root,
		Helper:        filepath.Join(root, helperName),
		CodexTree:     tree,
		CodexBinary:   filepath.Join(tree, filepath.FromSlash(target.Entrypoint)),
		AuditManifest: filepath.Join(root, "codex-artifacts.json"),
		Installation:  filepath.Join(root, "installation.json"),
		Licenses:      filepath.Join(root, LicensesDirName),
	}
}

// The multi-hundred-megabyte native tree is not re-hashed per launch; full
// byte verification is the installer's job. Managed files remain trusted
// against hostile same-account races; these checks catch damage and
// tampering evident at check time, not a concurrent attacker.
// The prefix itself is validated by ReadSettings before ValidateRelease is
// reached; managed checks here start at releases/.
func ValidateRelease(paths ReleasePaths, target Target, identity ReleaseIdentity) error {
	if err := checkManagedAncestors(paths.Root); err != nil {
		return err
	}
	if err := checkManagedTree(paths.Root); err != nil {
		return err
	}
	for _, entry := range target.Inventory {
		if err := checkInventoryFile(paths.CodexTree, entry); err != nil {
			return err
		}
	}
	record, err := ReadInstallation(paths.Installation)
	if err != nil {
		return err
	}
	if err := validateInstallation(record, identity); err != nil {
		return err
	}
	if err := checkHelper(paths.Helper, record); err != nil {
		return err
	}
	if err := checkAuditManifest(paths.AuditManifest, identity.ArtifactDigest); err != nil {
		return err
	}
	return checkNotices(paths.Licenses)
}

// Installation and reuse verify all native bytes; ordinary launch retains its cheaper structural check.
func VerifyRelease(paths ReleasePaths, target Target, identity ReleaseIdentity) error {
	if err := ValidateRelease(paths, target, identity); err != nil {
		return err
	}
	files := make(map[string]bool)
	directories := map[string]bool{".": true}
	for _, entry := range target.Inventory {
		files[filepath.FromSlash(entry.Path)] = true
		for parent := filepath.Dir(filepath.FromSlash(entry.Path)); parent != "."; parent = filepath.Dir(parent) {
			directories[parent] = true
		}
		file, err := platform.OpenChecked(filepath.Join(paths.CodexTree, filepath.FromSlash(entry.Path)))
		if err != nil {
			return err
		}
		digest := sha256.New()
		_, err = io.Copy(digest, file)
		file.Close()
		if err != nil {
			return err
		}
		if hex.EncodeToString(digest.Sum(nil)) != entry.SHA256 {
			return fmt.Errorf("native file does not match the embedded inventory: %s", entry.Path)
		}
	}
	return filepath.WalkDir(paths.CodexTree, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(paths.CodexTree, path)
		if err != nil {
			return err
		}
		if (entry.IsDir() && !directories[relative]) || (!entry.IsDir() && !files[relative]) {
			return errors.New("native release contains an unexpected entry")
		}
		return nil
	})
}

// Ancestors above releases/ stay trusted, allowing system links such as /var.
func checkManagedAncestors(root string) error {
	chain := []string{filepath.Dir(root), root}
	for _, directory := range chain {
		info, err := os.Lstat(directory)
		if err != nil {
			return fmt.Errorf("release is incomplete: %w", err)
		}
		if err := platform.CheckManagedDir(info, directory); err != nil {
			return err
		}
		if err := platform.CheckPrivate(info, directory); err != nil {
			return err
		}
	}
	return nil
}

func checkManagedTree(root string) error {
	return filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return fmt.Errorf("cannot inspect the managed release: %w", err)
		}
		if path == root {
			return nil
		}
		if entry.IsDir() {
			info, err := entry.Info()
			if err != nil {
				return fmt.Errorf("cannot inspect the managed directory %s: %w", path, err)
			}
			if err := platform.CheckManagedDir(info, path); err != nil {
				return err
			}
			return platform.CheckPrivate(info, path)
		}
		if !entry.Type().IsRegular() {
			return fmt.Errorf("refusing a linked or special managed release entry: %s", path)
		}
		return nil
	})
}

// checkInventoryFile verifies one native file against the embedded
// inventory. Files are opened through the checked platform primitive, which
// rejects links, reparse points, extra hard links, and foreign or
// group/other-writable entries. The native layout metadata is small and
// security-relevant, so its digest is verified against the pinned record;
// other files are size-checked only, with full hashing left to the
// installer.
func checkInventoryFile(tree string, entry InventoryEntry) error {
	path := filepath.Join(tree, filepath.FromSlash(entry.Path))
	file, err := platform.OpenChecked(path)
	if err != nil {
		return fmt.Errorf("release is incomplete: %w", err)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return err
	}
	if info.Size() != entry.Bytes {
		return fmt.Errorf("release file %s has %d bytes, want %d", path, info.Size(), entry.Bytes)
	}
	if entry.Executable && runtime.GOOS != "windows" && info.Mode().Perm()&0o100 == 0 {
		return fmt.Errorf("release file %s lost its executable bit", path)
	}
	if entry.Path == "codex-package.json" {
		data, err := platform.ReadFileLimit(file, entry.Bytes)
		if err != nil {
			return fmt.Errorf("cannot read the native layout metadata: %w", err)
		}
		if sha256Of(data) != entry.SHA256 {
			return fmt.Errorf("native layout metadata %s does not match the embedded manifest", entry.Path)
		}
	}
	return nil
}

func checkAuditManifest(path, manifestDigest string) error {
	file, err := platform.OpenChecked(path)
	if err != nil {
		return fmt.Errorf("release audit manifest is missing or unsafe: %w", err)
	}
	defer file.Close()
	data, err := platform.ReadFileLimit(file, int64(len(manifestBytes)))
	if err != nil {
		return fmt.Errorf("cannot read the audit manifest: %w", err)
	}
	if !bytes.Equal(data, manifestBytes) {
		return fmt.Errorf("audit manifest digest %s does not match the embedded manifest %s", sha256Of(data), manifestDigest)
	}
	return nil
}

func checkNotices(directory string) error {
	info, err := os.Lstat(directory)
	if err != nil {
		return fmt.Errorf("release notices directory is missing: %w", err)
	}
	if err := platform.CheckManagedDir(info, directory); err != nil {
		return err
	}
	if err := platform.CheckPrivate(info, directory); err != nil {
		return err
	}
	for _, name := range NoticeNames() {
		want, err := Notice(name)
		if err != nil {
			return err
		}
		path := filepath.Join(directory, name)
		file, err := platform.OpenChecked(path)
		if err != nil {
			return fmt.Errorf("release notice %s is missing or unsafe: %w", name, err)
		}
		data, err := platform.ReadFileLimit(file, int64(len(want)))
		file.Close()
		if err != nil {
			return fmt.Errorf("cannot read release notice %s: %w", name, err)
		}
		if !bytes.Equal(data, want) {
			return fmt.Errorf("release notice %s does not match the embedded notice", name)
		}
	}
	return nil
}

func sha256Of(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// ReinstallAdvice is the standard recovery line for a missing or corrupt
// installation; the launcher never downloads automatically.
const ReinstallAdvice = "download a new codex-openrouter release and run its --install to reinstall"

// MissingInstallationError describes a launch attempted without a usable
// managed release.
func MissingInstallationError(prefix string, identity ReleaseIdentity, cause error) error {
	if cause != nil {
		return fmt.Errorf("managed Codex release %s under %s is missing or corrupt: %v; %s", identity.ID(), prefix, cause, ReinstallAdvice)
	}
	return fmt.Errorf("managed Codex release %s is not installed under %s; %s", identity.ID(), prefix, ReinstallAdvice)
}

// ManagedChildEnvironment returns the environment for the native Codex
// child: the caller's environment with the package-manager marker variables
// named by the manifest removed and no replacements invented.
func ManagedChildEnvironment(environ []string) []string {
	unset := make(map[string]bool)
	for _, name := range UnsetEnvironment() {
		unset[name] = true
	}
	kept := environ[:0]
	for _, pair := range environ {
		name, _, _ := strings.Cut(pair, "=")
		if !unset[name] {
			kept = append(kept, pair)
		}
	}
	return kept
}
