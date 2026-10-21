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
	"github.com/MOMIEDEJIYI/ployenv/internal/platform"
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

	parts := planParts(plan)
	caches := make([]string, 0, len(parts))
	for _, part := range parts {
		cachePath := filepath.Join(config.CacheDir(), part.File)
		if opts.Force || !fsutil.FileExists(cachePath) {
			label := fmt.Sprintf("Downloading %s %s", spec.Name, plan.Exact)
			if len(parts) > 1 {
				label = fmt.Sprintf("Downloading %s %s (%s)", spec.Name, plan.Exact, part.File)
			}
			if err := netx.Download(plan.Bases, part.URLFor, cachePath, label); err != nil {
				return "", false, err
			}
		} else {
			logx.Verbosef("using the cached archive %s", cachePath)
		}
		if err := verifyArchive(spec, plan, part, cachePath); err != nil {
			return "", false, err
		}
		caches = append(caches, cachePath)
	}

	dest := config.VersionDir(spec.Name, plan.Exact)
	if err := os.RemoveAll(dest); err != nil {
		return "", false, err
	}
	if err := os.MkdirAll(dest, 0o755); err != nil {
		return "", false, err
	}
	logx.Stepf("Extracting %s %s", spec.Name, plan.Exact)
	for _, cachePath := range caches {
		if err := archive.Extract(cachePath, dest); err != nil {
			_ = os.RemoveAll(dest)
			return "", false, err
		}
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
	if err := assertExecutable(spec, dest, plan.Exact); err != nil {
		_ = os.RemoveAll(dest)
		return "", false, err
	}
	if spec.Verify != nil {
		if reported, verr := spec.Verify(dest); verr != nil {
			logx.Warnf("post-install self-check failed (%v), but the files are in place: %s", verr, dest)
		} else {
			logx.Verbosef("self-check reports %s", reported)
		}
	}
	// The downloaded archives are a means, not a result: drop them unless the
	// user asked to keep a cache (config cache.keep).
	if !config.CacheKeep() {
		for _, cachePath := range caches {
			if err := os.Remove(cachePath); err == nil {
				logx.Verbosef("removed the cached archive %s", filepath.Base(cachePath))
			}
		}
	}
	return plan.Exact, false, nil
}

// archivePart is one download of a plan, unified for single- and multi-archive
// runtimes so the verification below has exactly one shape to handle.
type archivePart struct {
	File   string
	URLFor func(base string) string
	SHA256 string
	SHA1   string
	SHA512 string
}

// planParts expands a plan into the archives to fetch. A runtime that ships one
// archive keeps its SHA-1/SHA-512 checks; a multi-archive runtime carries a
// SHA-256 per part (what Rust publishes as a .sha256 sidecar).
func planParts(plan runtime.Plan) []archivePart {
	if len(plan.Parts) == 0 {
		return []archivePart{{File: plan.File, URLFor: plan.URLFor, SHA256: plan.SHA256, SHA1: plan.SHA1, SHA512: plan.SHA512}}
	}
	out := make([]archivePart, 0, len(plan.Parts))
	for _, p := range plan.Parts {
		out = append(out, archivePart{File: p.File, URLFor: p.URLFor, SHA256: p.SHA256})
	}
	return out
}

// verifyArchive checks one downloaded archive, dropping it when it does not
// match: an unverified file must never reach the extractor, and a bad one must
// not survive in the cache.
func verifyArchive(spec *runtime.Spec, plan runtime.Plan, part archivePart, cachePath string) error {
	if part.SHA256 != "" {
		sum, err := sha256File(cachePath)
		if err != nil {
			return err
		}
		if !strings.EqualFold(sum, part.SHA256) {
			_ = os.Remove(cachePath)
			return fmt.Errorf("%s@%s checksum mismatch (expected %s, got %s)", spec.Name, plan.Exact, part.SHA256, sum)
		}
	}
	if part.SHA1 != "" {
		sum, err := sha1File(cachePath)
		if err != nil {
			return err
		}
		if !strings.EqualFold(sum, part.SHA1) {
			_ = os.Remove(cachePath)
			return fmt.Errorf("%s@%s SHA-1 mismatch (expected %s, got %s)", spec.Name, plan.Exact, part.SHA1, sum)
		}
	}
	if part.SHA512 != "" {
		sum, err := sha512File(cachePath)
		if err != nil {
			return err
		}
		if !strings.EqualFold(sum, part.SHA512) {
			_ = os.Remove(cachePath)
			return fmt.Errorf("%s@%s SHA-512 mismatch (expected %s, got %s)", spec.Name, plan.Exact, part.SHA512, sum)
		}
	}
	return nil
}

// assertExecutable enforces FR-808. On the Unix-likes a version directory whose
// primary tool carries no execute bit is unusable, and nothing else notices: the
// file exists, so HasBinary passes, the install reports success, and the failure
// only surfaces much later as "permission denied" from the tool itself. An
// archive that cannot express the mode (a zip built on Windows) drops it
// silently, so the mode is asserted here rather than trusted.
func assertExecutable(spec *runtime.Spec, dest, ver string) error {
	if platform.IsWindows() {
		return nil
	}
	p := spec.BinaryPath(dest, ver)
	st, err := os.Stat(p)
	if err != nil {
		return err
	}
	if !executableFor(platform.OS(), st.Mode()) {
		return fmt.Errorf("%s@%s: %s is not executable (mode %04o) - the archive did not carry its file mode; rolled back (retry with --force)",
			spec.Name, ver, filepath.Base(p), st.Mode().Perm())
	}
	return nil
}

// executableFor is the OS-parameterised rule behind assertExecutable, in the same
// spirit as exeFor and javaLayoutFor: an execute bit only means something off
// Windows, so taking the OS as an argument is what makes both branches assertable
// from any runner rather than only from the one that runs the check for real.
func executableFor(osName string, mode os.FileMode) bool {
	if osName == "windows" {
		return true
	}
	return mode&0o111 != 0
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
