package config

import (
	"fmt"
	"os"
	"path/filepath"
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
	code := m.Run()
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
