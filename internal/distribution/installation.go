package distribution

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"runtime"

	"encoding/json/jsontext"
	json "encoding/json/v2"

	"codex-openrouter/internal/platform"
)

const maxInstallationBytes = 4096

// Installation is the typed completion metadata the installer writes last
// into a release directory. It is an audit and completion record only: it
// never selects native paths, URLs, or inventory hashes; those stay embedded
// in the launcher. It records the copied helper's size and digest.
type Installation struct {
	Schema         int    `json:"schema"`
	Version        string `json:"version"`
	BuildID        string `json:"buildID"`
	Target         string `json:"target"`
	Toolchain      string `json:"toolchain"`
	Recipe         string `json:"recipe"`
	ArtifactDigest string `json:"artifactDigest"`
	ReleaseID      string `json:"releaseID"`
	HelperBytes    int64  `json:"helperBytes"`
	HelperSHA256   string `json:"helperSHA256"`
}

// NewInstallation records that helper completes the release identified by
// identity. An unstamped development identity is refused: the installer
// must never certify a build that makes no immutability claim.
func NewInstallation(identity ReleaseIdentity, helper []byte) (Installation, error) {
	if !identity.IsRelease() {
		return Installation{}, errors.New("an unstamped development build cannot complete a managed installation")
	}
	sum := sha256.Sum256(helper)
	return Installation{
		Schema:         1,
		Version:        identity.Version,
		BuildID:        identity.BuildID,
		Target:         identity.Target,
		Toolchain:      identity.Toolchain,
		Recipe:         identity.Recipe,
		ArtifactDigest: identity.ArtifactDigest,
		ReleaseID:      identity.ID(),
		HelperBytes:    int64(len(helper)),
		HelperSHA256:   hex.EncodeToString(sum[:]),
	}, nil
}

func FormatInstallation(record Installation) ([]byte, error) {
	data, err := json.Marshal(record, jsontext.WithIndent("  "))
	if err != nil {
		return nil, err
	}
	return append(data, '\n'), nil
}

func ReadInstallation(path string) (Installation, error) {
	file, err := platform.OpenChecked(path)
	if err != nil {
		return Installation{}, fmt.Errorf("release completion metadata is missing or unsafe: %w", err)
	}
	defer file.Close()
	data, err := platform.ReadFileLimit(file, maxInstallationBytes)
	if err != nil {
		return Installation{}, fmt.Errorf("release completion metadata is unreadable or oversized: %w", err)
	}
	var record Installation
	if err := json.Unmarshal(data, &record, json.RejectUnknownMembers(true)); err != nil {
		return Installation{}, errors.New("release completion metadata is not valid installation JSON")
	}
	return record, nil
}

func (record Installation) Identity() (ReleaseIdentity, error) {
	identity := ReleaseIdentity{Version: record.Version, BuildID: record.BuildID, Target: record.Target, Toolchain: record.Toolchain, Recipe: record.Recipe, ArtifactDigest: record.ArtifactDigest}
	if !identity.IsRelease() || identity.Version == "" || identity.Target == "" || identity.Toolchain == "" || identity.Recipe == "" || !isHexSHA256(identity.ArtifactDigest) {
		return ReleaseIdentity{}, errors.New("release completion identity is invalid")
	}
	if err := validateInstallation(record, identity); err != nil {
		return ReleaseIdentity{}, err
	}
	return identity, nil
}

func validateInstallation(record Installation, identity ReleaseIdentity) error {
	if record.Schema != 1 {
		return fmt.Errorf("release completion metadata schema %d, want 1", record.Schema)
	}
	expected := Installation{
		Schema:         1,
		Version:        identity.Version,
		BuildID:        identity.BuildID,
		Target:         identity.Target,
		Toolchain:      identity.Toolchain,
		Recipe:         identity.Recipe,
		ArtifactDigest: identity.ArtifactDigest,
		ReleaseID:      identity.ID(),
		HelperBytes:    record.HelperBytes,
		HelperSHA256:   record.HelperSHA256,
	}
	if record != expected {
		return errors.New("release completion metadata does not match this launcher's release identity")
	}
	if record.HelperBytes <= 0 || !isHexSHA256(record.HelperSHA256) {
		return errors.New("release completion metadata records an invalid helper size or digest")
	}
	return nil
}

// Native command auth executes the helper later, so launch rehashes this copy;
// the much larger native tree receives full hashing only at installation.
func checkHelper(path string, record Installation) error {
	file, err := platform.OpenChecked(path)
	if err != nil {
		return fmt.Errorf("release helper is missing or unsafe: %w", err)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return err
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0o100 == 0 {
		return fmt.Errorf("release helper lost its executable bit: %s", path)
	}
	if info.Size() != record.HelperBytes {
		return fmt.Errorf("release helper has %d bytes, the completion record says %d", info.Size(), record.HelperBytes)
	}
	digest := sha256.New()
	if _, err := io.Copy(digest, file); err != nil {
		return fmt.Errorf("cannot read the release helper: %w", err)
	}
	if sum := hex.EncodeToString(digest.Sum(nil)); sum != record.HelperSHA256 {
		return fmt.Errorf("release helper digest %s does not match the completion record %s", sum, record.HelperSHA256)
	}
	return nil
}
