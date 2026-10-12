package runtime

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/MOMIEDEJIYI/ployenv/internal/platform"
)

// I-09: on Windows the Scripts directory used to be appended unconditionally, so
// an install without one (the NuGet layout of python 3.12.10) put a directory
// that does not exist on PATH -- `env --shell json` advertised an unresolvable
// entry and the DSH plugin's `path on` injected it into the harness PATH.
func TestPythonPathDirsOnlyReturnsExistingDirs(t *testing.T) {
	dir := t.TempDir()

	if platform.IsWindows() {
		got := pythonPathDirs(dir)
		if len(got) != 1 || got[0] != dir {
			t.Fatalf("without Scripts, pythonPathDirs(%q) = %v, want [%q]", dir, got, dir)
		}
		scripts := filepath.Join(dir, "Scripts")
		if err := os.MkdirAll(scripts, 0o755); err != nil {
			t.Fatalf("MkdirAll: %v", err)
		}
		got = pythonPathDirs(dir)
		if len(got) != 2 || got[0] != dir || got[1] != scripts {
			t.Fatalf("with Scripts, pythonPathDirs(%q) = %v, want [%q %q]", dir, got, dir, scripts)
		}
		for _, d := range got {
			if _, err := os.Stat(d); err != nil {
				t.Fatalf("pythonPathDirs(%q) returned %q, which does not exist (%v)", dir, d, err)
			}
		}
		return
	}

	want := filepath.Join(dir, "bin")
	got := pythonPathDirs(dir)
	if len(got) != 1 || got[0] != want {
		t.Fatalf("pythonPathDirs(%q) = %v, want [%q]", dir, got, want)
	}
}
