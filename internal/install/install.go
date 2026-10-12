// Package install turns a version request into an installed runtime: it
// resolves the request against the remote index, downloads the archive,
// unpacks it and verifies the result.
package install

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/MOMIEDEJIYI/ployenv/internal/archive"
	"github.com/MOMIEDEJIYI/ployenv/internal/config"
	"github.com/MOMIEDEJIYI/ployenv/internal/fsutil"
	"github.com/MOMIEDEJIYI/ployenv/internal/logx"
	"github.com/MOMIEDEJIYI/ployenv/internal/netx"
	"github.com/MOMIEDEJIYI/ployenv/internal/runtime"
	"github.com/MOMIEDEJIYI/ployenv/internal/version"
)

// Options controls an install.
type Options struct {
	Force bool   // reinstall even when already present
	From  string // adopt an existing directory instead of downloading
}

// Ensure installs (or adopts) a runtime and returns the exact version. The
// boolean reports that nothing was done because the version was already present
// (BUG-017), so callers do not claim a fresh install that never happened.
func Ensure(spec *runtime.Spec, request string, opts Options) (version string, already bool, err error) {
	request = strings.TrimSpace(request)
	if opts.From != "" {
		return Adopt(spec, request, opts.From, opts.Force)
	}

	exact, err := resolveExact(spec, request)
	if err != nil {
		return "", false, err
	}

	if !opts.Force {
		for _, v := range config.InstalledVersions(spec.Name) {
			if v == exact {
				logx.Infof("%s@%s is already installed, skipping (use --force to reinstall)", spec.Name, exact)
				return exact, true, nil
			}
		}
	}

	plan, err := spec.Plan(exact)
	if err != nil {
		return "", false, err
	}

	if err := config.EnsureRoot(); err != nil {
		return "", false, err
	}
	cachePath := filepath.Join(config.CacheDir(), plan.File)
	if opts.Force || !fsutil.FileExists(cachePath) {
		label := fmt.Sprintf("Downloading %s %s", spec.Name, plan.Exact)
		if err := netx.Download(plan.Bases, plan.URLFor, cachePath, label); err != nil {
			return "", false, err
		}
	} else {
		logx.Verbosef("using the cached archive %s", cachePath)
	}

	if plan.SHA256 != "" {
		sum, err := sha256File(cachePath)
		if err != nil {
			return "", false, err
		}
		if !strings.EqualFold(sum, plan.SHA256) {
			_ = os.Remove(cachePath)
			return "", false, fmt.Errorf("%s@%s checksum mismatch (expected %s, got %s)", spec.Name, plan.Exact, plan.SHA256, sum)
		}
	}

	dest := config.VersionDir(spec.Name, plan.Exact)
	if err := os.RemoveAll(dest); err != nil {
		return "", false, err
	}
	if err := os.MkdirAll(dest, 0o755); err != nil {
		return "", false, err
	}
	logx.Stepf("Extracting %s %s", spec.Name, plan.Exact)
	if err := archive.Extract(cachePath, dest); err != nil {
		_ = os.RemoveAll(dest)
		return "", false, err
	}
	if err := applyLayout(plan.Layout, dest); err != nil {
		_ = os.RemoveAll(dest)
		return "", false, err
	}
	if !spec.HasBinary(dest, plan.Exact) {
		_ = os.RemoveAll(dest)
		return "", false, fmt.Errorf("%s@%s: %s not found after extraction; rolled back (retry with --force)",
			spec.Name, plan.Exact, spec.Primary)
	}
	if spec.Verify != nil {
		if reported, verr := spec.Verify(dest); verr != nil {
			logx.Warnf("post-install self-check failed (%v), but the files are in place: %s", verr, dest)
		} else {
			logx.Verbosef("self-check reports %s", reported)
		}
	}
	// The downloaded archive is a means, not a result: drop it unless the user
	// asked to keep a cache (config cache.keep).
	if !config.CacheKeep() {
		if err := os.Remove(cachePath); err == nil {
			logx.Verbosef("removed the cached archive %s", filepath.Base(cachePath))
		}
	}
	return plan.Exact, false, nil
}

// resolveExact turns a partial request ("22", "lts", "3.12") into one version.
func resolveExact(spec *runtime.Spec, request string) (string, error) {
	rels, err := spec.List(request)
	if err != nil {
		// An exact version can be installed without consulting the index, so a
		// flaky or unreachable index must not block `install node@20.20.2`.
		if version.IsExact(request) {
			logx.Verbosef("index unavailable (%v); continuing with the exact version %s", err, request)
			return request, nil
		}
		return "", err
	}
	if len(rels) == 0 && version.IsExact(request) {
		return request, nil
	}
	if len(rels) == 0 {
		if request == "" {
			return "", fmt.Errorf("%s has no installable versions", spec.Name)
		}
		return "", fmt.Errorf("%s has no version matching %q", spec.Name, request)
	}
	best := rels[0].Version
	for _, r := range rels[1:] {
		if version.Compare(r.Version, best) > 0 {
			best = r.Version
		}
	}
	return best, nil
}

// Adopt links an existing installation directory into ployenv's store.
// Adopt links an existing installation directory into ployenv's store. The
// boolean reports that the version was already registered and nothing was
// changed (BUG-017).
func Adopt(spec *runtime.Spec, ver, from string, force bool) (version string, already bool, err error) {
	absFrom, err := filepath.Abs(from)
	if err != nil {
		return "", false, err
	}
	absFrom = filepath.Clean(absFrom)
	if !fsutil.DirExists(absFrom) {
		return "", false, fmt.Errorf("directory does not exist: %s", absFrom)
	}
	if ver == "" {
		ver = inferVersion(filepath.Base(absFrom))
	}
	if ver == "" {
		return "", false, fmt.Errorf("cannot infer a version; specify one explicitly, e.g. ployenv install %s@21.0.12.1 --from %q", spec.Name, absFrom)
	}
	dest := config.VersionDir(spec.Name, ver)
	if fsutil.SamePath(dest, absFrom) {
		return ver, true, nil
	}
	if fsutil.DirExists(dest) {
		if !force {
			// BUG-017: report this instead of pretending a link was just created.
			return ver, true, nil
		}
		warnIfReplacingUserData(dest)
		if err := removeVersionDir(spec.Name, ver); err != nil {
			return "", false, err
		}
	}
	if err := os.MkdirAll(config.LangVersionsDir(spec.Name), 0o755); err != nil {
		return "", false, err
	}
	if err := platformLink(dest, absFrom); err != nil {
		return "", false, err
	}
	return ver, false, nil
}

// warnIfReplacingUserData warns before --force wipes an existing version
// directory that actually holds files (BUG-018). logx.Warnf already prefixes
// "warning: ".
func warnIfReplacingUserData(dest string) {
	if !shouldWarnBeforeReplacing(dest) {
		return
	}
	entries, err := os.ReadDir(dest)
	if err != nil {
		return
	}
	logx.Warnf("replacing the existing directory %s: its %d entries will be deleted (use a different --root to keep them)", dest, len(entries))
}

// shouldWarnBeforeReplacing reports whether dest holds data that is about to be
// deleted. A junction ployenv created carries no data of its own, and an empty
// leftover directory is harmless, so neither is worth a warning (BUG-018).
func shouldWarnBeforeReplacing(dest string) bool {
	st, err := os.Lstat(dest)
	if err != nil || !st.IsDir() || st.Mode()&os.ModeSymlink != 0 {
		return false
	}
	entries, err := os.ReadDir(dest)
	return err == nil && len(entries) > 0
}

var versionLike = regexp.MustCompile(`\d+(?:\.\d+)+`)

// inferVersion extracts a version from a directory name such as
// "node-v20.20.2-win-x64", "jdk-21.0.2+13" or "static-go-1.27.1".
//
// A name that carries no recognisable version yields "" so the caller can ask
// for an explicit @version, instead of registering a directory name such as
// "nodejs" as a version (BUG-009, FR-5).
func inferVersion(base string) string {
	for _, candidate := range []string{
		versionLike.FindString(base),
		strings.TrimLeft(base, "vV"),
	} {
		candidate = strings.ReplaceAll(candidate, "+", ".")
		if looksLikeVersion(candidate) {
			return candidate
		}
	}
	return ""
}

// looksLikeVersion reports whether s is a dotted numeric version with at least
// two components ("20.20.2", "1.27"). version.IsExact is not enough here: it
// counts components but does not check that they are numbers.
func looksLikeVersion(s string) bool {
	parts := strings.Split(s, ".")
	if len(parts) < 2 {
		return false
	}
	for _, p := range parts {
		if _, err := strconv.Atoi(p); err != nil {
			return false
		}
	}
	return true
}

// Uninstall deletes a version installed by ployenv.
func Uninstall(lang, ver string) error {
	spec := runtime.Lookup(lang)
	if spec == nil {
		return fmt.Errorf("unsupported runtime %q", lang)
	}
	if err := removeVersionDir(spec.Name, ver); err != nil {
		return err
	}
	cfg := config.Load()
	if cfg.Defaults[spec.Name] == ver {
		delete(cfg.Defaults, spec.Name)
		return config.Save(cfg)
	}
	return nil
}

// removeVersionDir deletes only inside <root>/versions/<lang>/.
func removeVersionDir(lang, ver string) error {
	base := filepath.Clean(config.LangVersionsDir(lang))
	target := filepath.Clean(config.VersionDir(lang, ver))
	rel, err := filepath.Rel(base, target)
	if err != nil || rel == "." || strings.HasPrefix(rel, "..") || filepath.IsAbs(rel) {
		return fmt.Errorf("refusing to delete a path outside the versions directory: %s", target)
	}
	st, err := os.Lstat(target)
	if err != nil {
		return fmt.Errorf("%s@%s is not installed", lang, ver)
	}
	if st.Mode()&os.ModeSymlink != 0 {
		return os.Remove(target)
	}
	return os.RemoveAll(target)
}
