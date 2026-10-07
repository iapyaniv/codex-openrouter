package install

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"

	"codex-openrouter/internal/distribution"
	"codex-openrouter/internal/platform"
)

func Preflight(identity distribution.ReleaseIdentity) error {
	target, err := distribution.CurrentTarget()
	if err != nil {
		return errors.New("this platform has no published managed Codex distribution in this build")
	}
	if !identity.IsRelease() {
		return errors.New("an unstamped development build cannot install; use a downloaded release")
	}
	if identity.Target != runtime.GOOS+"-"+runtime.GOARCH || identity.ArtifactDigest != distribution.ManifestDigest() {
		return errors.New("launcher installation identity is inconsistent")
	}
	return checkMinimumOS(target.OSMinimum)
}

type Result struct {
	PublicPath   string
	Version      string
	CodexVersion string
}

type operations struct {
	transport http.RoundTripper
	probe     func(context.Context, string, string, string) error
	replace   func(string, string) error
	cleanup   func(string) error
}

func Run(ctx context.Context, prefix string, identity distribution.ReleaseIdentity, progress io.Writer) (Result, error) {
	target, err := distribution.CurrentTarget()
	if err != nil {
		return Result{}, err
	}
	source, err := os.Executable()
	if err != nil {
		return Result{}, errors.New("cannot locate the downloaded launcher")
	}
	return run(ctx, prefix, identity, target, source, progress, operations{})
}

func run(ctx context.Context, prefix string, identity distribution.ReleaseIdentity, target distribution.Target, source string, progress io.Writer, ops operations) (result Result, err error) {
	publicDir := filepath.Join(prefix, "bin")
	public := filepath.Join(publicDir, "codex-openrouter")
	result = Result{PublicPath: public, Version: identity.Version, CodexVersion: distribution.CodexVersion()}
	if !identity.IsRelease() {
		return result, errors.New("unstamped launcher cannot install")
	}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	if err := checkedDirectory(prefix, false); err != nil {
		return result, err
	}
	unlock, err := platform.Lock(filepath.Join(prefix, ".install.lock"))
	if err != nil {
		if errors.Is(err, platform.ErrBusy) {
			return result, fmt.Errorf("installation %w; retry", platform.ErrBusy)
		}
		return result, err
	}
	activated := false
	stage, prepared := "", ""
	if ops.probe == nil {
		ops.probe = probe
	}
	if ops.replace == nil {
		ops.replace = platform.ReplacePrepared
	}
	if ops.cleanup == nil {
		ops.cleanup = os.RemoveAll
	}
	defer func() {
		var cleanupErr error
		if prepared != "" {
			e := os.Remove(prepared)
			if !errors.Is(e, os.ErrNotExist) {
				cleanupErr = errors.Join(cleanupErr, e)
			}
		}
		if stage != "" {
			cleanupErr = errors.Join(cleanupErr, ops.cleanup(stage))
		}
		cleanupErr = errors.Join(cleanupErr, unlock())
		err = errors.Join(err, cleanupErr)
		if activated && err != nil {
			err = fmt.Errorf("native launcher installed at %s, but activation or cleanup is incomplete; retry using the downloaded launcher: %w", public, err)
		}
	}()
	if err := checkedDirectory(publicDir, true); err != nil {
		return result, err
	}
	if err := checkedDirectory(filepath.Join(prefix, "releases"), true); err != nil {
		return result, err
	}
	helper, sourceInfo, err := readChecked(source, maxLauncherBytes)
	if err != nil {
		return result, fmt.Errorf("cannot read the downloaded launcher: %w", err)
	}
	prior, err := inspectPublic(prefix, public, sourceInfo, helper)
	if err != nil {
		return result, err
	}
	stage, err = os.MkdirTemp(prefix, ".staging-*")
	if err != nil {
		return result, err
	}
	paths := distribution.ReleaseLayout(prefix, identity, target)
	probeDir := filepath.Join(stage, "probe")
	if err := os.Mkdir(probeDir, 0o700); err != nil {
		return result, err
	}
	if _, statErr := os.Lstat(paths.Root); statErr == nil {
		if err := distribution.VerifyRelease(paths, target, identity); err != nil {
			return result, repairError(paths.Root, err)
		}
		record, err := distribution.ReadInstallation(paths.Installation)
		if err != nil {
			return result, repairError(paths.Root, err)
		}
		if record.HelperSHA256 != sum(helper) || record.HelperBytes != int64(len(helper)) {
			return result, repairError(paths.Root, errors.New("release identity already contains different launcher bytes"))
		}
		if err := ops.probe(ctx, paths.CodexBinary, probeDir, distribution.CodexVersion()); err != nil {
			return result, err
		}
	} else {
		if !errors.Is(statErr, os.ErrNotExist) {
			return result, statErr
		}
		archive := filepath.Join(stage, "native.tar.gz")
		fmt.Fprintf(progress, "Downloading pinned Codex %s...\n", distribution.CodexVersion())
		if err := download(ctx, target, archive, ops.transport); err != nil {
			return result, err
		}
		staged := distribution.ReleaseLayout(stage, identity, target)
		if err := os.MkdirAll(staged.CodexTree, 0o700); err != nil {
			return result, err
		}
		if err := extract(ctx, archive, staged.CodexTree, target); err != nil {
			return result, err
		}
		if err := ops.probe(ctx, staged.CodexBinary, probeDir, distribution.CodexVersion()); err != nil {
			return result, err
		}
		if err := writeSynced(staged.Helper, helper, 0o700); err != nil {
			return result, err
		}
		copy, _, err := readChecked(staged.Helper, maxLauncherBytes)
		if err != nil || sum(copy) != sum(helper) {
			return result, errors.New("staged launcher copy failed verification")
		}
		if err := writeSynced(staged.AuditManifest, distribution.ManifestBytes(), 0o600); err != nil {
			return result, err
		}
		if err := os.Mkdir(staged.Licenses, 0o700); err != nil {
			return result, err
		}
		for _, name := range distribution.NoticeNames() {
			notice, err := distribution.Notice(name)
			if err != nil {
				return result, err
			}
			if err := writeSynced(filepath.Join(staged.Licenses, name), notice, 0o600); err != nil {
				return result, err
			}
		}
		record, err := distribution.NewInstallation(identity, helper)
		if err != nil {
			return result, err
		}
		metadata, err := distribution.FormatInstallation(record)
		if err != nil {
			return result, err
		}
		if err := writeSynced(staged.Installation, metadata, 0o600); err != nil {
			return result, err
		}
		if err := distribution.VerifyRelease(staged, target, identity); err != nil {
			return result, err
		}
		if err := ctx.Err(); err != nil {
			return result, err
		}
		if _, err := os.Lstat(paths.Root); !errors.Is(err, os.ErrNotExist) {
			return result, repairError(paths.Root, errors.New("release destination changed before publication"))
		}
		if err := os.Rename(staged.Root, paths.Root); err != nil {
			return result, err
		}
	}
	prepared, err = preparePublic(publicDir, helper)
	if err != nil {
		return result, err
	}
	if prior.info != nil {
		if err := writeSynced(filepath.Join(stage, "prior-public"), prior.data, 0o700); err != nil {
			return result, err
		}
	}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	if err := ops.replace(prepared, public); err != nil {
		current, _, observedErr := readChecked(public, maxLauncherBytes)
		if observedErr == nil && sum(current) == sum(helper) {
			activated = true
			return result, fmt.Errorf("replacement reported an error, and the public entry matches the new launcher: %w", err)
		}
		if prior.data != nil && observedErr == nil && sum(current) == sum(prior.data) {
			return result, fmt.Errorf("activation failed; previous public executable remains: %w", err)
		}
		if prior.info == nil && errors.Is(observedErr, os.ErrNotExist) {
			return result, fmt.Errorf("activation failed; no public executable was installed: %w", err)
		}
		return result, fmt.Errorf("activation result is ambiguous; inspect %s before retrying: %w", public, err)
	}
	activated = true
	return result, nil
}

func repairError(path string, cause error) error {
	return fmt.Errorf("managed release %s is incomplete or mismatched; close affected sessions, move aside this release directory, then rerun the downloaded installer: %w", path, cause)
}
