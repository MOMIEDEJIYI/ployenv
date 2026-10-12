package install

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/MOMIEDEJIYI/ployenv/internal/config"
	"github.com/MOMIEDEJIYI/ployenv/internal/runtime"
)

// makeFakeInstall builds a directory that looks like an extracted runtime, so it
// can be adopted without touching the network.
func makeFakeInstall(t *testing.T, dir string) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "bin", "go.exe"), []byte("binary"), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

// BUG-017 / BUG-018. Everything shares one data directory on purpose: the root is
// resolved once per process (config.RootInfo uses sync.Once), so separate tests
// could not each point at their own temporary root.
func TestAdoptContract(t *testing.T) {
	root := t.TempDir()
	t.Setenv("PLOYENV_ROOT", root)

	spec := runtime.Lookup("go")
	if spec == nil {
		t.Fatal("the go runtime must be registered")
	}

	t.Run("first adopt links the directory", func(t *testing.T) {
		src := makeFakeInstall(t, filepath.Join(root, "outside", "static-go-9.9.9"))

		ver, already, err := Adopt(spec, "", src, false)
		if err != nil {
			t.Fatalf("Adopt: %v", err)
		}
		if already {
			t.Error("a first adopt must not report 'already registered'")
		}
		if ver != "9.9.9" {
			t.Fatalf("version = %q, want 9.9.9", ver)
		}
		if _, err := os.Stat(filepath.Join(config.VersionDir("go", "9.9.9"), "bin", "go.exe")); err != nil {
			t.Fatalf("the link should expose the source file: %v", err)
		}
		if _, err := os.Stat(filepath.Join(src, "bin", "go.exe")); err != nil {
			t.Fatalf("the source directory must survive: %v", err)
		}
	})

	t.Run("re-adopting reports already registered (BUG-017)", func(t *testing.T) {
		src := filepath.Join(root, "outside", "static-go-9.9.9")

		ver, already, err := Adopt(spec, "", src, false)
		if err != nil {
			t.Fatalf("Adopt: %v", err)
		}
		if !already {
			t.Error("re-adopting an existing version must report 'already registered'")
		}
		if ver != "9.9.9" {
			t.Fatalf("version = %q, want 9.9.9", ver)
		}
	})

	t.Run("a junction ployenv created is not user data (BUG-018)", func(t *testing.T) {
		// The junction resolves to a directory holding files, so only the
		// reparse-point check keeps this from warning.
		if shouldWarnBeforeReplacing(config.VersionDir("go", "9.9.9")) {
			t.Error("replacing our own junction must not warn")
		}
	})

	t.Run("--force replaces a real directory (BUG-018)", func(t *testing.T) {
		occupied := config.VersionDir("go", "8.8.8")
		if err := os.MkdirAll(occupied, 0o755); err != nil {
			t.Fatal(err)
		}
		userFile := filepath.Join(occupied, "keep.txt")
		if err := os.WriteFile(userFile, []byte("user data"), 0o644); err != nil {
			t.Fatal(err)
		}
		if !shouldWarnBeforeReplacing(occupied) {
			t.Fatal("this is exactly the case that must warn before deleting")
		}

		src := makeFakeInstall(t, filepath.Join(root, "outside", "static-go-8.8.8"))
		if _, _, err := Adopt(spec, "", src, true); err != nil {
			t.Fatalf("Adopt --force: %v", err)
		}
		if _, err := os.Lstat(userFile); !os.IsNotExist(err) {
			t.Errorf("--force replaces the directory, so the old file should be gone (err=%v)", err)
		}
		if _, err := os.Stat(filepath.Join(occupied, "bin", "go.exe")); err != nil {
			t.Errorf("the link should point at the source: %v", err)
		}
	})
}

// TestShouldWarnBeforeReplacing pins the decision itself; the message goes
// through logx, which binds os.Stderr at package init and so cannot be captured
// from here.
func TestShouldWarnBeforeReplacing(t *testing.T) {
	base := t.TempDir()

	if shouldWarnBeforeReplacing(filepath.Join(base, "missing")) {
		t.Error("a missing directory needs no warning")
	}

	empty := filepath.Join(base, "empty")
	if err := os.MkdirAll(empty, 0o755); err != nil {
		t.Fatal(err)
	}
	if shouldWarnBeforeReplacing(empty) {
		t.Error("an empty leftover directory needs no warning")
	}

	full := filepath.Join(base, "full")
	if err := os.MkdirAll(full, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(full, "keep.txt"), []byte("user data"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !shouldWarnBeforeReplacing(full) {
		t.Error("a real directory holding files must trigger a warning")
	}
}
