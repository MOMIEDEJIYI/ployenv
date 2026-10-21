package fsutil

import (
	"os"
	"path/filepath"
	"testing"
)

// A merge, not a move: Rust ships rustc and cargo as separate archives and both
// carry bin/, so the second component must be merged into the first directory
// rather than skipped.
func TestMergeDirIntoMergesDirectories(t *testing.T) {
	root := t.TempDir()
	from := filepath.Join(root, "component")
	to := filepath.Join(root, "target")
	for _, d := range []string{filepath.Join(from, "bin"), filepath.Join(from, "share"), filepath.Join(to, "bin")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatalf("MkdirAll: %v", err)
		}
	}
	write := func(p, body string) {
		t.Helper()
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatalf("writing %s: %v", p, err)
		}
	}
	write(filepath.Join(to, "bin", "rustc"), "rustc\n")
	write(filepath.Join(from, "bin", "cargo"), "cargo\n")
	write(filepath.Join(from, "share", "doc.txt"), "doc\n")

	if err := MergeDirInto(from, to); err != nil {
		t.Fatalf("MergeDirInto: %v", err)
	}
	for _, want := range [][2]string{{"bin", "rustc"}, {"bin", "cargo"}, {"share", "doc.txt"}} {
		p := filepath.Join(to, want[0], want[1])
		if _, err := os.Stat(p); err != nil {
			t.Errorf("%s is missing after the merge: %v", p, err)
		}
	}
}

// An existing file wins: the order the archives are unpacked in must not change
// the result, and overwriting would make it depend on that order.
func TestMergeDirIntoKeepsFilesAlreadyInPlace(t *testing.T) {
	root := t.TempDir()
	from := filepath.Join(root, "component")
	to := filepath.Join(root, "target")
	if err := os.MkdirAll(from, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.MkdirAll(to, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(filepath.Join(from, "keep.txt"), []byte("new\n"), 0o644); err != nil {
		t.Fatalf("writing: %v", err)
	}
	if err := os.WriteFile(filepath.Join(to, "keep.txt"), []byte("old\n"), 0o644); err != nil {
		t.Fatalf("writing: %v", err)
	}

	if err := MergeDirInto(from, to); err != nil {
		t.Fatalf("MergeDirInto: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(to, "keep.txt"))
	if err != nil {
		t.Fatalf("reading: %v", err)
	}
	if string(data) != "old\n" {
		t.Errorf("keep.txt = %q, want the file that was already there", data)
	}
}
