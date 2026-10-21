package install

import (
	"crypto/sha1"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/MOMIEDEJIYI/ployenv/internal/fsutil"
	"github.com/MOMIEDEJIYI/ployenv/internal/platform"
	"github.com/MOMIEDEJIYI/ployenv/internal/runtime"
)

func platformLink(link, target string) error { return platform.LinkDir(link, target) }

// applyLayout normalises an unpacked archive to a version directory.
func applyLayout(l runtime.Layout, dest string) error {
	switch l {
	case runtime.LayoutFlatten:
		return fsutil.MoveContentsUp(dest)
	case runtime.LayoutMacJDK:
		return flattenMacJDK(dest)
	case runtime.LayoutNuGet:
		return flattenNuGet(dest)
	case runtime.LayoutRust:
		return flattenRust(dest)
	default:
		return nil
	}
}

// flattenMacJDK normalises Adoptium's macOS archive. macOS JDKs are bundles:
// the real JDK home (bin/, lib/, ...) sits at <wrapper>/Contents/Home, next to
// the bundle's own Contents/MacOS and Contents/Info.plist. That second level has
// siblings, so MoveContentsUp - which only lifts a directory holding exactly one
// child directory - cannot reach it: without this, the version directory kept a
// Contents/ where bin/ belongs and every macOS install rolled back with "not
// found after extraction".
func flattenMacJDK(dest string) error {
	if err := fsutil.MoveContentsUp(dest); err != nil {
		return err
	}
	contents := filepath.Join(dest, "Contents")
	home := filepath.Join(contents, "Home")
	if !fsutil.DirExists(home) {
		return nil // a flat archive after all: nothing left to peel
	}
	if err := fsutil.MoveEntries(home, dest); err != nil {
		return err
	}
	return os.RemoveAll(contents)
}

// flattenNuGet moves a NuGet package's tools/ contents up and drops metadata.
func flattenNuGet(dest string) error {
	tools := filepath.Join(dest, "tools")
	if !fsutil.DirExists(tools) {
		return nil
	}
	entries, err := os.ReadDir(tools)
	if err != nil {
		return err
	}
	for _, e := range entries {
		from := filepath.Join(tools, e.Name())
		to := filepath.Join(dest, e.Name())
		if _, err := os.Stat(to); err == nil {
			continue
		}
		if err := os.Rename(from, to); err != nil {
			return err
		}
	}
	for _, drop := range []string{"tools", "_rels", "package", "images", "python.nuspec", "[Content_Types].xml", ".signature.p7s"} {
		_ = os.RemoveAll(filepath.Join(dest, drop))
	}
	return nil
}

// rustComponentPrefixes are the top-level directories a Rust distribution
// unpacks into: <component>-<version>-<triple>/.
var rustComponentPrefixes = []string{"rustc-", "cargo-", "rust-std-"}

// rustPayloadDir finds the directory that actually carries the component's
// files. rust-installer nests the payload one level deeper than the wrapper -
// <component>-<version>-<triple>/<component>/bin/... - and puts its own metadata
// (install.sh, components, version, the licences) beside it, so merging the
// wrapper would leave a bin/ that does not exist and every install would fail
// its "primary tool" check. Everything outside the payload is dropped with the
// wrapper.
func rustPayloadDir(top, component string) (string, error) {
	entries, err := os.ReadDir(top)
	if err != nil {
		return "", err
	}
	var dirs []string
	for _, e := range entries {
		if e.IsDir() {
			dirs = append(dirs, e.Name())
		}
	}
	if len(dirs) == 1 {
		return filepath.Join(top, dirs[0]), nil
	}
	for _, d := range dirs {
		if strings.HasPrefix(d, component) {
			return filepath.Join(top, d), nil
		}
	}
	return "", fmt.Errorf("%s: expected one payload directory, found %v", filepath.Base(top), dirs)
}

func flattenRust(dest string) error {
	entries, err := os.ReadDir(dest)
	if err != nil {
		return err
	}
	merged := 0
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		component := ""
		for _, prefix := range rustComponentPrefixes {
			if strings.HasPrefix(e.Name(), prefix) {
				component = strings.TrimSuffix(prefix, "-")
				break
			}
		}
		if component == "" {
			continue
		}
		top := filepath.Join(dest, e.Name())
		payload, err := rustPayloadDir(top, component)
		if err != nil {
			return err
		}
		if err := fsutil.MergeDirInto(payload, dest); err != nil {
			return err
		}
		if err := os.RemoveAll(top); err != nil {
			return err
		}
		merged++
	}
	if merged == 0 {
		return fmt.Errorf("no Rust component directory (rustc-/cargo-/rust-std-) after extraction")
	}
	// Some of rust-installer's metadata sits inside the payload (cargo carries
	// its own manifest.in), so it arrives with the merge and is dropped here.
	for _, drop := range []string{"install.sh", "components", "manifest.in", "rust-installer-version", "version"} {
		_ = os.Remove(filepath.Join(dest, drop))
	}
	return nil
}

func sha256File(p string) (string, error) {
	f, err := os.Open(p)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func sha1File(p string) (string, error) {
	f, err := os.Open(p)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha1.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func sha512File(p string) (string, error) {
	f, err := os.Open(p)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha512.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
