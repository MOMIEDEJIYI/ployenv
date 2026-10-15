// Package fsutil holds small filesystem and formatting helpers shared across
// ployenv. It has no ployenv-specific dependencies.
package fsutil

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// FileExists reports whether p exists and is a regular file.
func FileExists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && !st.IsDir()
}

// DirExists reports whether p exists and is a directory.
func DirExists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.IsDir()
}

// SamePath reports whether two paths refer to the same location, applying the
// case rules of the host platform: Windows folds case, the Unix-likes do not
// (BUG-011).
func SamePath(a, b string) bool {
	a, b = filepath.Clean(a), filepath.Clean(b)
	if runtime.GOOS == "windows" {
		return strings.EqualFold(a, b)
	}
	return a == b
}

// ReadTextFile is os.ReadFile with a leading UTF-8 byte-order mark removed.
//
// Editors on Windows (Notepad, PowerShell's `Set-Content -Encoding UTF8`) put a
// BOM in front of text files. Go's strings.TrimSpace does not treat U+FEFF as
// space - it is Cf, not White_Space - so the first line's key reads as
// "\ufeffkey" and is silently skipped, and json.Unmarshal rejects the file
// outright. Everything ployenv parses therefore reads through here rather than
// os.ReadFile. Only a leading BOM is dropped; bytes elsewhere are untouched.
func ReadTextFile(p string) ([]byte, error) {
	data, err := os.ReadFile(p)
	if err != nil {
		return nil, err
	}
	if len(data) >= 3 && data[0] == 0xEF && data[1] == 0xBB && data[2] == 0xBF {
		return data[3:], nil
	}
	return data, nil
}

// HumanBytes formats a byte count as "1.5 MB" style text.
func HumanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(n)/float64(div), "KMGTPE"[exp])
}

// CopyFile copies src to dst, creating parent directories as needed.
func CopyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

// MoveEntries moves every entry of from into to, keeping whatever already
// exists in to: the first one wins, so a second pass can never clobber a file
// that is already in place.
func MoveEntries(from, to string) error {
	entries, err := os.ReadDir(from)
	if err != nil {
		return err
	}
	for _, e := range entries {
		src := filepath.Join(from, e.Name())
		dst := filepath.Join(to, e.Name())
		if _, err := os.Stat(dst); err == nil {
			continue
		}
		if err := os.Rename(src, dst); err != nil {
			return err
		}
	}
	return nil
}

// MoveContentsUp flattens a directory that holds a single sub-directory: the
// archive layout used by Node, Go and the Python standalone builds. It is a
// no-op when the directory does not have exactly one child directory.
func MoveContentsUp(dir string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	if len(entries) != 1 || !entries[0].IsDir() {
		return nil
	}
	inner := filepath.Join(dir, entries[0].Name())
	if err := MoveEntries(inner, dir); err != nil {
		return err
	}
	return os.RemoveAll(inner)
}
