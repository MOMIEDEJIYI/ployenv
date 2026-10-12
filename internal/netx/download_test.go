package netx

import (
	"os"
	"path/filepath"
	"testing"
)

// TestAbortRemovesOpenPartial guards the Windows behaviour that matters here:
// a file that is still open cannot be deleted, so Abort must close it first.
func TestAbortRemovesOpenPartial(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "x.zip.part")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString("partial"); err != nil {
		t.Fatal(err)
	}
	registerPartial(path, f)

	Abort()

	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("partial file was not removed (stat err=%v)", err)
	}
}
