//go:build windows

package platform

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"unsafe"
)

// BUG-001: LinkDir used to shell out to `cmd /c mklink /J` with a pre-built
// command string, which Go re-quoted and cmd then mis-parsed, so every junction
// failed. These tests keep the "usable junction, untouched target" contract of
// FR-5 pinned down.
func TestLinkDirSurvivesSpacesAndLeavesTargetIntact(t *testing.T) {
	base := t.TempDir()
	target := filepath.Join(base, "target dir")
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(target, "go.exe")
	if err := os.WriteFile(marker, []byte("original"), 0o644); err != nil {
		t.Fatal(err)
	}

	link := filepath.Join(base, "link dir")
	if err := LinkDir(link, target); err != nil {
		t.Fatalf("LinkDir: %v", err)
	}

	st, err := os.Lstat(link)
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("%s is not a link: mode = %v", link, st.Mode())
	}

	resolved, err := filepath.EvalSymlinks(link)
	if err != nil {
		t.Fatalf("EvalSymlinks(%s): %v", link, err)
	}
	// Compare by file identity, not by path text (BUG-019). A GitHub Windows
	// runner hands back the 8.3 short form of the target (RUNNER~1 for
	// runneradmin), which is a different string for the very same directory.
	resolvedInfo, err := os.Stat(resolved)
	if err != nil {
		t.Fatalf("stat %s: %v", resolved, err)
	}
	targetInfo, err := os.Stat(target)
	if err != nil {
		t.Fatalf("stat %s: %v", target, err)
	}
	if !os.SameFile(resolvedInfo, targetInfo) {
		t.Fatalf("the link resolves to %s, which is not the target directory %s", resolved, target)
	}

	data, err := os.ReadFile(filepath.Join(link, "go.exe"))
	if err != nil {
		t.Fatalf("reading through the link: %v", err)
	}
	if string(data) != "original" {
		t.Fatalf("read %q through the link, want %q", data, "original")
	}

	// Unlinking must leave the target alone: that is what `uninstall` relies on.
	if err := os.Remove(link); err != nil {
		t.Fatalf("removing the link: %v", err)
	}
	if _, err := os.Lstat(link); !os.IsNotExist(err) {
		t.Errorf("the link should be gone, Lstat err = %v", err)
	}
	if got, err := os.ReadFile(marker); err != nil || string(got) != "original" {
		t.Fatalf("the target must survive unlinking: data=%q err=%v", got, err)
	}
}

func TestLinkDirReplacesAnExistingLink(t *testing.T) {
	base := t.TempDir()
	first := filepath.Join(base, "first")
	second := filepath.Join(base, "second")
	for _, dir := range []string{first, second} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(first, "a.txt"), []byte("a"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(second, "b.txt"), []byte("b"), 0o644); err != nil {
		t.Fatal(err)
	}

	link := filepath.Join(base, "link")
	if err := LinkDir(link, first); err != nil {
		t.Fatalf("LinkDir(first): %v", err)
	}
	if err := LinkDir(link, second); err != nil {
		t.Fatalf("LinkDir(second): %v", err)
	}
	if _, err := os.Stat(filepath.Join(link, "b.txt")); err != nil {
		t.Fatalf("the link should now point at %s: %v", second, err)
	}
	if _, err := os.Stat(filepath.Join(first, "a.txt")); err != nil {
		t.Fatalf("the first target must survive: %v", err)
	}
}

func TestLinkDirRefusesToClobberUserData(t *testing.T) {
	base := t.TempDir()
	target := filepath.Join(base, "target")
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(base, "link")
	if err := os.MkdirAll(link, 0o755); err != nil {
		t.Fatal(err)
	}
	keep := filepath.Join(link, "keep.txt")
	if err := os.WriteFile(keep, []byte("user data"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := LinkDir(link, target); err == nil {
		t.Fatal("expected an error instead of deleting user files")
	}
	if _, err := os.Stat(keep); err != nil {
		t.Fatalf("user data must be untouched: %v", err)
	}
}

func TestLinkDirRejectsMissingTarget(t *testing.T) {
	base := t.TempDir()
	err := LinkDir(filepath.Join(base, "link"), filepath.Join(base, "does-not-exist"))
	if err == nil {
		t.Fatal("expected an error for a missing target")
	}
}

// BUG-019 pinned down: a Windows runner may spell a path with its 8.3 short
// name, so the same directory has two different strings. This reproduces that
// locally (skipping when the volume has no short names) and shows why the
// assertion must compare file identity: the old string comparison fails here
// even though both paths name one directory.
func TestShortAndLongSpellingAreTheSameDirectory(t *testing.T) {
	base := t.TempDir()
	long := filepath.Join(base, "long target directory")
	if err := os.MkdirAll(long, 0o755); err != nil {
		t.Fatal(err)
	}

	short, err := shortPath(long)
	if err != nil || short == "" || strings.EqualFold(short, long) {
		t.Skip("this volume does not produce 8.3 short names")
	}

	// The situation from CI: two spellings, one directory. A string comparison
	// reports a mismatch here, which is exactly the failure BUG-019 describes.
	if strings.EqualFold(short, long) {
		t.Fatalf("expected the short form to differ from %s", long)
	}

	longInfo, err := os.Stat(long)
	if err != nil {
		t.Fatal(err)
	}
	shortInfo, err := os.Stat(short)
	if err != nil {
		t.Fatalf("stat %s: %v", short, err)
	}
	if !os.SameFile(longInfo, shortInfo) {
		t.Fatalf("%s and %s name the same directory, but os.SameFile disagrees", short, long)
	}
}

// shortPath asks Windows for the 8.3 form of path; the result equals path when
// the volume keeps no short names.
func shortPath(path string) (string, error) {
	proc := syscall.NewLazyDLL("kernel32.dll").NewProc("GetShortPathNameW")
	ptr, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return "", err
	}
	buf := make([]uint16, syscall.MAX_PATH)
	n, _, callErr := proc.Call(
		uintptr(unsafe.Pointer(ptr)),
		uintptr(unsafe.Pointer(&buf[0])),
		uintptr(len(buf)),
	)
	if n == 0 {
		return "", callErr
	}
	return syscall.UTF16ToString(buf), nil
}
