package cli

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/MOMIEDEJIYI/ployenv/internal/config"
	"github.com/MOMIEDEJIYI/ployenv/internal/runtime"
)

// TestMain pins the data directory for the whole test binary. config resolves it
// once per process (RootInfo uses sync.Once), so every test in the package has
// to agree on one root; this replaces the per-test t.Setenv that silently stops
// working the moment an earlier test resolved it first.
//
// The name is deliberately not ASCII: the machine interface has to escape such
// paths (FR-18) and the JSON contract test checks exactly that.
func TestMain(m *testing.M) {
	base, err := os.MkdirTemp("", "ployenv-cli-test-")
	if err != nil {
		panic(err)
	}
	root := filepath.Join(base, "中文目录🚀")
	if err := os.MkdirAll(root, 0o755); err != nil {
		os.RemoveAll(base)
		panic(err)
	}
	if err := os.Setenv("PLOYENV_ROOT", root); err != nil {
		os.RemoveAll(base)
		panic(err)
	}
	// Run the suite from a scratch directory: the product resolves a project file
	// by walking up from the current directory, so a .ployenv / .tool-versions in
	// any ancestor of the checkout would leak into these tests (I-34). The scratch
	// dir only has %TEMP% above it.
	if err := os.Chdir(base); err != nil {
		os.RemoveAll(base)
		panic(err)
	}
	code := m.Run()
	// Windows cannot delete the process working directory, so step back out
	// of it before cleanup (otherwise every test binary run leaks a temp dir).
	_ = os.Chdir(os.TempDir())
	os.RemoveAll(base)
	os.Exit(code)
}

// sandbox returns the pinned data directory.
func sandbox(t *testing.T) string {
	t.Helper()
	root, _, _ := config.RootInfo()
	if root == "" {
		t.Fatal("the data root must be pinned by TestMain")
	}
	return root
}

// resetSandbox empties the data directory so a test starts from a known state
// no matter what ran before it.
func resetSandbox(t *testing.T) string {
	t.Helper()
	root := sandbox(t)
	if err := os.RemoveAll(root); err != nil {
		t.Fatalf("clearing the sandbox: %v", err)
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatalf("recreating the sandbox: %v", err)
	}
	return root
}

// makeVersion creates a version directory that looks installed: the primary tool
// is placed where the runtime's own layout expects it.
func makeVersion(t *testing.T, lang, ver string) string {
	t.Helper()
	spec := runtime.Lookup(lang)
	if spec == nil {
		t.Fatalf("runtime %q is not registered", lang)
	}
	dir := config.VersionDir(lang, ver)
	tool := filepath.Join(dir, spec.ToolPath(spec.Primary, ver))
	if err := os.MkdirAll(filepath.Dir(tool), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(tool, []byte("fake tool\n"), 0o755); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	return dir
}

// runCLI executes ployenv and returns stdout plus the exit code.
func runCLI(t *testing.T, args ...string) (string, int) {
	t.Helper()
	var code int
	out := captureStdout(t, func() { code = Run(args) })
	return out, code
}

// mustRunCLI fails the test when the command exits non-zero, and returns stdout.
func mustRunCLI(t *testing.T, args ...string) string {
	t.Helper()
	out, code := runCLI(t, args...)
	if code != 0 {
		t.Fatalf("ployenv %v exited with %d, output:\n%s", args, code, out)
	}
	return out
}

// writeSandboxConfig writes config.json the way a user would have it.
func writeSandboxConfig(t *testing.T, body string) {
	t.Helper()
	path := config.ConfigPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("writing %s: %v", path, err)
	}
}

// chdir moves the process into dir for the rest of the test: project pins are
// read from the working directory. The restore is registered after any TempDir
// cleanup, so the directory is still there when the working directory leaves it
// (which Windows requires).
func chdir(t *testing.T, dir string) {
	t.Helper()
	old, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("Chdir(%s): %v", dir, err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(old); err != nil {
			t.Errorf("restoring the working directory: %v", err)
		}
	})
}
