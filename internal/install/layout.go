package install

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"

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
	case runtime.LayoutNuGet:
		return flattenNuGet(dest)
	default:
		return nil
	}
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
