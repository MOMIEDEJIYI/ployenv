package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestMain does two jobs.
//
//  1. It pins the data root for the whole test binary: RootInfo resolves it once
//     per process (sync.Once), which is exactly what one of the tests is about.
//  2. It provides a child mode that prints the resolution result and exits. A
//     fresh process is the only way to run resolveRoot with a different
//     environment, so every branch of "where does the data live" is asserted by
//     re-executing this binary.
func TestMain(m *testing.M) {
	if os.Getenv("PLOYENV_TEST_RESOLVE") != "" {
		root, src, writable := RootInfo()
		fmt.Printf("root=%s\nsource=%s\nwritable=%t\n", root, src, writable)
		// RootUsable is what the CLI gates on (FR-809), so the child reports it
		// too: a fresh process is the only way to exercise it without the
		// once-per-process pin.
		if err := RootUsable(); err != nil {
			// Flattened onto one line: the message is multi-line by design, and
			// the test-side parser reads one key per line.
			fmt.Printf("usable=%s\n", strings.ReplaceAll(err.Error(), "\n", " | "))
		} else {
			fmt.Println("usable=ok")
		}
		os.Exit(0)
	}

	base, err := os.MkdirTemp("", "ployenv-config-test-")
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

// resetRoot empties the pinned data directory so a test starts from a known
// state, whatever ran before it.
func resetRoot(t *testing.T) string {
	t.Helper()
	root := RootDir()
	if root == "" {
		t.Fatal("the data root must be pinned by TestMain")
	}
	if err := os.RemoveAll(root); err != nil {
		t.Fatalf("clearing the data root: %v", err)
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatalf("recreating the data root: %v", err)
	}
	return root
}

// writeConfigFile writes raw bytes to config.json, valid or not.
func writeConfigFile(t *testing.T, body string) {
	t.Helper()
	if err := os.MkdirAll(RootDir(), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(ConfigPath(), []byte(body), 0o644); err != nil {
		t.Fatalf("writing config.json: %v", err)
	}
}
