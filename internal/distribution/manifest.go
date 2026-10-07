// Package distribution holds the embedded, reviewed upstream Codex artifact
// manifest and the release identity derived from it. The embedded manifest
// is the authority for download URLs, hashes, and the native layout; no
// environment variable or local file can override it.
package distribution

import (
	"bytes"
	_ "embed"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"runtime"
	"strings"
	"time"

	json "encoding/json/v2"
)

//go:embed codex-artifacts.json
var manifestBytes []byte

// ManifestBytes returns the exact embedded manifest bytes. The installed
// audit copy must be written from these bytes, never regenerated.
func ManifestBytes() []byte {
	return bytes.Clone(manifestBytes)
}

// ManifestDigest is the SHA-256 of the exact embedded manifest bytes, in
// hex. It identifies the artifact lock as part of the release identity.
func ManifestDigest() string {
	return sha256Of(manifestBytes)
}

type manifest struct {
	SchemaVersion int             `json:"schemaVersion"`
	Release       manifestRelease `json:"release"`
	Layout        manifestLayout  `json:"layout"`
	Environment   struct {
		Unset []string `json:"unset"`
	} `json:"environment"`
	Notices    manifestNotices    `json:"notices"`
	Provenance manifestProvenance `json:"provenance"`
	Targets    []Target           `json:"targets"`
}

type manifestRelease struct {
	CodexVersion string `json:"codexVersion"`
	UpstreamTag  string `json:"upstreamTag"`
	SourceCommit string `json:"sourceCommit"`
	ReleaseURL   string `json:"releaseUrl"`
	PublishedAt  string `json:"publishedAt"`
}

type manifestLayout struct {
	Kind          string `json:"kind"`
	LayoutVersion int    `json:"layoutVersion"`
	ResourcesDir  string `json:"resourcesDir"`
	PathDir       string `json:"pathDir"`
}

// Audit anchors are checked for completeness and syntax; this does not
// establish external signature authenticity.
type manifestProvenance struct {
	ChecksumSource        string `json:"checksumSource"`
	FetchedAt             string `json:"fetchedAt"`
	SHA256SumsAssetSHA256 string `json:"sha256sumsAssetSha256"`
	Sigstore              string `json:"sigstore"`
	CodeSignature         struct {
		AppleTeamID string `json:"appleTeamId"`
	} `json:"codeSignature"`
}

// manifestNotices lists the notice texts retained in the repository and
// installed into each release's LICENSES directory; every record is
// cross-checked against the embedded copy.
type manifestNotices struct {
	CodexLicense            string            `json:"codexLicense"`
	RetainFromPackage       []string          `json:"retainFromPackage"`
	WrapperNotice           *noticeRecord     `json:"wrapperNotice"`
	CodexRootNotices        []noticeRecord    `json:"codexRootNotices"`
	BundledNotices          []noticeRecord    `json:"bundledNotices"`
	BundledNoticeProvenance map[string]string `json:"bundledNoticeProvenance"`
}

// noticeRecord pins one retained notice text by repository path, size, and
// digest.
type noticeRecord struct {
	Path      string `json:"path"`
	Bytes     int64  `json:"bytes"`
	SHA256    string `json:"sha256"`
	SourceURL string `json:"sourceUrl"`
	// MatchesRepositoryFile names the repository file this notice copy
	// must stay byte-identical to; empty for upstream notices.
	MatchesRepositoryFile string `json:"matchesRepositoryFile"`
}

// Target is the validated manifest record for one supported platform.
type Target struct {
	ID         string `json:"id"`
	RustTarget string `json:"rustTarget"`
	OS         string `json:"os"`
	Arch       string `json:"arch"`
	Entrypoint string `json:"entrypoint"`
	OSMinimum  string `json:"osMinimum"`
	Archive    struct {
		URL           string   `json:"url"`
		Format        string   `json:"format"`
		Bytes         int64    `json:"bytes"`
		SHA256        string   `json:"sha256"`
		RedirectHosts []string `json:"redirectHosts"`
	} `json:"archive"`
	Extraction struct {
		ExtractedBytes int64    `json:"extractedBytes"`
		FileCount      int      `json:"fileCount"`
		DirectoryCount int      `json:"directoryCount"`
		MaxFiles       int      `json:"maxFiles"`
		MaxBytes       int64    `json:"maxBytes"`
		AllowedTypes   []string `json:"allowedTypes"`
	} `json:"extraction"`
	Inventory []InventoryEntry `json:"inventory"`
}

// InventoryEntry is one reviewed file in the native distribution layout.
type InventoryEntry struct {
	Path       string `json:"path"`
	SHA256     string `json:"sha256"`
	Bytes      int64  `json:"bytes"`
	Executable bool   `json:"executable"`
}

// CodexVersion is the pinned upstream Codex release.
func CodexVersion() string { return cachedManifest.Release.CodexVersion }

// UnsetEnvironment lists the package-manager marker variables removed from
// the Codex child environment.
func UnsetEnvironment() []string {
	return append([]string(nil), cachedManifest.Environment.Unset...)
}

// CurrentTarget returns the validated manifest target for this platform,
// or an error when the manifest publishes none (for example Windows).
func CurrentTarget() (Target, error) {
	return TargetFor(runtime.GOOS, runtime.GOARCH)
}

// TargetFor returns the validated target for a platform pair; used by
// cross-platform build and installer checks.
func TargetFor(goos, goarch string) (Target, error) {
	for _, target := range cachedManifest.Targets {
		if target.OS == goos && target.Arch == goarch {
			return target, nil
		}
	}
	return Target{}, fmt.Errorf("no published Codex distribution for %s/%s", goos, goarch)
}

var cachedManifest = loadManifest()

func loadManifest() *manifest {
	var decoded manifest
	if err := json.Unmarshal(manifestBytes, &decoded, json.RejectUnknownMembers(true)); err != nil {
		panic("embedded codex-artifacts.json is not valid JSON: " + err.Error())
	}
	if err := decoded.validate(); err != nil {
		panic("embedded codex-artifacts.json is invalid: " + err.Error())
	}
	return &decoded
}

func (decoded *manifest) validate() error {
	if decoded.SchemaVersion != 1 {
		return fmt.Errorf("schemaVersion %d, want 1", decoded.SchemaVersion)
	}
	if strings.TrimSpace(decoded.Release.CodexVersion) == "" || strings.TrimSpace(decoded.Release.UpstreamTag) == "" {
		return errors.New("release identity is incomplete")
	}
	release := decoded.Release
	if len(release.SourceCommit) != 40 && len(release.SourceCommit) != 64 {
		return errors.New("release source commit must be a full hexadecimal Git commit")
	}
	if _, err := hex.DecodeString(release.SourceCommit); err != nil {
		return errors.New("release source commit must be a full hexadecimal Git commit")
	}
	releaseURL, err := url.Parse(release.ReleaseURL)
	if err != nil || releaseURL.Scheme != "https" || releaseURL.Host != "github.com" || releaseURL.User != nil || releaseURL.RawQuery != "" || releaseURL.Fragment != "" || releaseURL.Path != "/openai/codex/releases/tag/"+release.UpstreamTag {
		return errors.New("release URL must name the upstream HTTPS release tag")
	}
	if _, err := time.Parse(time.RFC3339, release.PublishedAt); err != nil {
		return errors.New("release publication time must be RFC3339")
	}
	provenance := decoded.Provenance
	if strings.TrimSpace(provenance.ChecksumSource) == "" || strings.TrimSpace(provenance.Sigstore) == "" {
		return errors.New("checksum source and Sigstore assessment are required")
	}
	if !isHexSHA256(provenance.SHA256SumsAssetSHA256) {
		return errors.New("checksum asset digest must be SHA-256")
	}
	if _, err := time.Parse(time.DateOnly, provenance.FetchedAt); err != nil {
		return errors.New("provenance fetch date must be YYYY-MM-DD")
	}
	if decoded.Layout.Kind != "codex-package" || decoded.Layout.LayoutVersion != 1 {
		return errors.New("unsupported layout kind or version")
	}
	if !isCleanRelative(decoded.Layout.ResourcesDir) || !isCleanRelative(decoded.Layout.PathDir) {
		return errors.New("layout resource/path directories are not clean relative paths")
	}
	if err := decoded.Notices.validate(); err != nil {
		return err
	}
	if len(decoded.Targets) == 0 {
		return errors.New("no targets")
	}
	seenTargets := make(map[string]bool)
	appleTeamIDPattern := regexp.MustCompile(`^[A-Z0-9]{10}$`)
	for _, target := range decoded.Targets {
		if target.OS == "darwin" && !appleTeamIDPattern.MatchString(provenance.CodeSignature.AppleTeamID) {
			return errors.New("Darwin provenance requires an Apple team identifier")
		}
		if seenTargets[target.OS+"/"+target.Arch] {
			return fmt.Errorf("duplicate target for %s/%s", target.OS, target.Arch)
		}
		seenTargets[target.OS+"/"+target.Arch] = true
		if err := target.validate(); err != nil {
			return fmt.Errorf("target %s: %v", target.ID, err)
		}
	}
	return nil
}

func (target Target) validate() error {
	if target.ID == "" || target.OS == "" || target.Arch == "" {
		return errors.New("target identity is incomplete")
	}
	if target.Archive.Format != "tar+gzip" {
		return fmt.Errorf("unsupported archive format %q", target.Archive.Format)
	}
	if !strings.HasPrefix(target.Archive.URL, "https://") {
		return errors.New("archive URL is not HTTPS")
	}
	if target.Archive.Bytes <= 0 || !isHexSHA256(target.Archive.SHA256) {
		return errors.New("archive size or hash is invalid")
	}
	if !isCleanRelative(target.Entrypoint) {
		return fmt.Errorf("entrypoint %q is not a clean relative path", target.Entrypoint)
	}
	extraction := target.Extraction
	if extraction.FileCount <= 0 || extraction.MaxFiles < extraction.FileCount || extraction.MaxBytes < extraction.ExtractedBytes {
		return errors.New("extraction bounds are inconsistent")
	}
	if len(target.Inventory) != extraction.FileCount {
		return fmt.Errorf("inventory has %d entries, fileCount says %d", len(target.Inventory), extraction.FileCount)
	}
	seen := make(map[string]bool)
	folded := make(map[string]string)
	var total int64
	entrypointSeen := false
	for _, entry := range target.Inventory {
		if !isCleanRelative(entry.Path) {
			return fmt.Errorf("inventory path %q is not a clean relative path", entry.Path)
		}
		if entry.Bytes < 0 || !isHexSHA256(entry.SHA256) {
			return fmt.Errorf("inventory entry %q has an invalid size or hash", entry.Path)
		}
		if seen[entry.Path] {
			return fmt.Errorf("duplicate inventory path %q", entry.Path)
		}
		seen[entry.Path] = true
		lower := strings.ToLower(entry.Path)
		if first, ok := folded[lower]; ok && first != entry.Path {
			return fmt.Errorf("inventory paths %q and %q differ only by case", first, entry.Path)
		}
		folded[lower] = entry.Path
		total += entry.Bytes
		if entry.Path == target.Entrypoint {
			entrypointSeen = true
			if !entry.Executable {
				return fmt.Errorf("entrypoint %q is not marked executable", entry.Path)
			}
		}
	}
	if !entrypointSeen {
		return fmt.Errorf("entrypoint %q is not in the inventory", target.Entrypoint)
	}
	if total != extraction.ExtractedBytes {
		return fmt.Errorf("inventory totals %d bytes, extractedBytes says %d", total, extraction.ExtractedBytes)
	}
	return nil
}

func isHexSHA256(value string) bool {
	if len(value) != 64 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

// isCleanRelative reports whether path is a forward-slash relative path
// with no traversal, volume, or separator tricks. Native distribution
// layouts are Unix-style archives; backslashes are never valid separators
// in them, so one check serves every build target.
func isCleanRelative(path string) bool {
	if path == "" || strings.HasPrefix(path, "/") || strings.ContainsRune(path, '\\') {
		return false
	}
	if strings.ContainsRune(path, 0) {
		return false
	}
	for _, segment := range strings.Split(path, "/") {
		if segment == "" || segment == "." || segment == ".." {
			return false
		}
	}
	return true
}

// BuildRecipeID identifies the build recipe: the inputs other than source
// that change the produced bytes. It deliberately carries no source commit;
// reviewed stamping binds the source snapshot at packaging time.
const BuildRecipeID = "cgo0-trimpath-buildvcs0-stdlib-embed"

// ReleaseIdentity identifies one immutable installed release. The release
// ID is embedded at build time; publishing different bytes under the same
// ID is an error.
type ReleaseIdentity struct {
	Version        string
	BuildID        string
	Target         string
	Toolchain      string
	Recipe         string
	ArtifactDigest string
}

// A "dev" build ID is unstamped and cannot install. Embedded stamps, rather
// than the running file's bytes, identify the release because an upgrade may
// replace the public executable while old code keeps running.
func CurrentReleaseIdentity(version, buildID string) ReleaseIdentity {
	return ReleaseIdentity{
		Version:        version,
		BuildID:        buildID,
		Target:         runtime.GOOS + "-" + runtime.GOARCH,
		Toolchain:      runtime.Version(),
		Recipe:         BuildRecipeID,
		ArtifactDigest: ManifestDigest(),
	}
}

// IsRelease reports whether this identity is a stamped immutable release
// build rather than an unstamped development build.
func (identity ReleaseIdentity) IsRelease() bool {
	return identity.BuildID != "" && identity.BuildID != "dev"
}

// JSON preserves tuple boundaries; a delimiter or sanitized fields could
// alias distinct build inputs before hashing.
func (identity ReleaseIdentity) ID() string {
	tuple, err := json.Marshal([6]string{
		identity.Version,
		identity.BuildID,
		identity.Target,
		identity.Toolchain,
		identity.Recipe,
		identity.ArtifactDigest,
	})
	if err != nil {
		panic("release identity cannot be encoded: " + err.Error())
	}
	return sanitizeIDComponent(identity.Version) + "-" + sha256Of(tuple)
}

func sanitizeIDComponent(value string) string {
	var builder strings.Builder
	for _, r := range value {
		switch {
		case 'a' <= r && r <= 'z', 'A' <= r && r <= 'Z', '0' <= r && r <= '9', r == '.', r == '-', r == '_':
			builder.WriteRune(r)
		default:
			builder.WriteByte('-')
		}
	}
	return builder.String()
}

// Describe returns the human-readable identity line for --launcher-version.
func (identity ReleaseIdentity) Describe() string {
	return identity.Version + " (" + runtime.GOOS + "/" + runtime.GOARCH + ", build " + identity.BuildID + ", toolchain " + identity.Toolchain + ", recipe " + identity.Recipe + ", artifacts " + identity.ArtifactDigest[:12] + ")"
}
