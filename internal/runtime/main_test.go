package runtime

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/MOMIEDEJIYI/ployenv/internal/config"
)

// TestMain does two jobs.
//
//  1. Helper mode: a provider verifies a tool by running it (captureOutput), and
//     these tests audit that by copying this test binary into a version
//     directory. A child started with PLOYENV_HELPER_OUT set prints exactly that
//     text and exits, so the real run-and-parse path is exercised without a mock.
//  2. Portable root: the data directory is pinned to a scratch directory before
//     anything resolves it, so a test that configures a mirror can never touch
//     the machine's own ployenv data. config.RootDir caches its choice for the
//     whole process, so this is the only place it can be set.
func TestMain(m *testing.M) {
	if out := os.Getenv("PLOYENV_HELPER_OUT"); out != "" {
		fmt.Print(out)
		os.Exit(0)
	}
	root, err := os.MkdirTemp("", "ployenv-runtime-test-")
	if err != nil {
		fmt.Fprintln(os.Stderr, "runtime tests: temp root:", err)
		os.Exit(1)
	}
	if err := os.Setenv("PLOYENV_ROOT", root); err != nil {
		fmt.Fprintln(os.Stderr, "runtime tests: PLOYENV_ROOT:", err)
		os.RemoveAll(root)
		os.Exit(1)
	}
	// Run from the scratch root so an ancestor project file cannot leak in (I-34).
	if err := os.Chdir(root); err != nil {
		fmt.Fprintln(os.Stderr, "runtime tests: chdir:", err)
		os.RemoveAll(root)
		os.Exit(1)
	}
	code := m.Run()
	// Windows cannot delete the process working directory, so step back out
	// of it before cleanup (otherwise every test binary run leaks a temp dir).
	_ = os.Chdir(os.TempDir())
	os.RemoveAll(root)
	os.Exit(code)
}

// helperOutput makes every child process started from here print out and exit.
// It is what turns this test binary into a stand-in for go/node/java/python.
func helperOutput(t *testing.T, out string) {
	t.Helper()
	old, had := os.LookupEnv("PLOYENV_HELPER_OUT")
	if err := os.Setenv("PLOYENV_HELPER_OUT", out); err != nil {
		t.Fatalf("Setenv: %v", err)
	}
	t.Cleanup(func() {
		if had {
			os.Setenv("PLOYENV_HELPER_OUT", old)
			return
		}
		os.Unsetenv("PLOYENV_HELPER_OUT")
	})
}

// fakeTool copies this test binary to dst so a Verify function can run it as if
// it were the managed tool.
func fakeTool(t *testing.T, dst string) {
	t.Helper()
	self, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable: %v", err)
	}
	data, err := os.ReadFile(self)
	if err != nil {
		t.Fatalf("read %s: %v", self, err)
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		t.Fatalf("MkdirAll %s: %v", filepath.Dir(dst), err)
	}
	// 0o755 matters on the Unix-likes: without the exec bit the child cannot
	// start and every Verify test would fail with a permission error.
	if err := os.WriteFile(dst, data, 0o755); err != nil {
		t.Fatalf("write %s: %v", dst, err)
	}
}

// writeConfig writes the config.json a user would have, so mirror lookups (FR-6)
// can point at a local server instead of the internet.
func writeConfig(t *testing.T, mirrors map[string]string) {
	t.Helper()
	data, err := json.Marshal(map[string]any{"mirrors": mirrors})
	if err != nil {
		t.Fatalf("marshal config: %v", err)
	}
	path := config.ConfigPath()
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	t.Cleanup(func() { os.Remove(path) })
}

// server starts a test server whose handler is torn down with the test.
func server(t *testing.T, h http.HandlerFunc) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return srv
}

// staticServer answers each path in routes with its body and 404s the rest.
func staticServer(t *testing.T, routes map[string]string) *httptest.Server {
	t.Helper()
	return server(t, func(w http.ResponseWriter, r *http.Request) {
		body, ok := routes[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, body)
	})
}

// override points a package-level endpoint variable at a test server and
// restores it afterwards (the providers keep those URLs in variables so the
// built-in behavior stays exactly as shipped - same values, never reassigned in
// production).
func override(t *testing.T, target *string, url string) {
	t.Helper()
	old := *target
	*target = url
	t.Cleanup(func() { *target = old })
}

// withOS fakes the platform for the duration of a test, so every mapping that
// differs per OS can be asserted for all of them from a single host. A runner
// can only ever execute one platform's code path, which is exactly how a layout
// that is wrong on the other one used to slip through (BUG-021).
func withOS(t *testing.T, osName string) {
	t.Helper()
	old := hostOS
	hostOS = func() string { return osName }
	t.Cleanup(func() { hostOS = old })
}

// withArch is the architecture counterpart of withOS.
func withArch(t *testing.T, arch string) {
	t.Helper()
	old := hostArch
	hostArch = func() string { return arch }
	t.Cleanup(func() { hostArch = old })
}

func equalStrings(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}
