package resolve

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/MOMIEDEJIYI/ployenv/internal/runtime"
)

func TestParseKeyValue(t *testing.T) {
	spec := runtime.Lookup("node")
	if spec == nil {
		t.Fatal("node spec missing")
	}
	cases := map[string]string{
		"node=22.11.0\n":          "22.11.0",
		"java=21\nnode=18.20.0\n": "18.20.0",
		"node: 20.9.0\n":          "20.9.0",
		"node 18.0.0\n":           "18.0.0",
		"# node=1.0.0\n":          "",
		"java=21\n":               "",
	}
	for in, want := range cases {
		if got := parseKeyValue(in, spec); got != want {
			t.Errorf("parseKeyValue(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestParseToolVersions(t *testing.T) {
	spec := runtime.Lookup("python")
	if spec == nil {
		t.Fatal("python spec missing")
	}
	got := parseToolVersions("nodejs 22.11.0\npython 3.12.8\n", spec)
	if got != "3.12.8" {
		t.Errorf("parseToolVersions = %q, want 3.12.8", got)
	}
}

func TestFirstVersionToken(t *testing.T) {
	if got := firstVersionToken("v20.9.0\n"); got != "20.9.0" {
		t.Errorf("firstVersionToken = %q, want 20.9.0", got)
	}
	if got := firstVersionToken("# just a comment\n"); got != "" {
		t.Errorf("firstVersionToken on comment = %q, want empty", got)
	}
}

// I-11: a project pins versions in exactly one .ployenv, so the optional
// .ployenv.local is no longer read at all -- there is never a question of which
// of two files wins.
func TestProjectFilePrecedence(t *testing.T) {
	spec := runtime.Lookup("node")
	if spec == nil {
		t.Fatal("node spec missing")
	}
	write := func(dir, name, body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	t.Run("ployenv is read", func(t *testing.T) {
		dir := t.TempDir()
		write(dir, ".ployenv", "node=22.11.0\n")
		got, src := findProjectFrom(dir, spec)
		if got != "22.11.0" {
			t.Fatalf("value = %q, want 22.11.0", got)
		}
		if filepath.Base(src) != ".ployenv" {
			t.Fatalf("source = %q, want the .ployenv file", src)
		}
	})

	t.Run("ployenv.local is ignored", func(t *testing.T) {
		dir := t.TempDir()
		write(dir, ".ployenv.local", "node=20.9.0\n")
		if got, src := findProjectFrom(dir, spec); got != "" || src != "" {
			t.Fatalf("value = %q source = %q, want empty (.ployenv.local must not be read)", got, src)
		}
	})

	t.Run("ployenv.local cannot override ployenv", func(t *testing.T) {
		dir := t.TempDir()
		write(dir, ".ployenv", "node=22.11.0\n")
		write(dir, ".ployenv.local", "node=20.9.0\n")
		got, src := findProjectFrom(dir, spec)
		if got != "22.11.0" {
			t.Fatalf("value = %q, want 22.11.0 (only .ployenv counts)", got)
		}
		if filepath.Base(src) != ".ployenv" {
			t.Fatalf("source = %q, want the .ployenv file", src)
		}
	})

	t.Run("nearest directory wins", func(t *testing.T) {
		parent := t.TempDir()
		write(parent, ".ployenv", "node=22.11.0\n")
		child := filepath.Join(parent, "child")
		if err := os.MkdirAll(child, 0o755); err != nil {
			t.Fatal(err)
		}
		write(child, ".ployenv", "node=18.20.0\n")
		if got, _ := findProjectFrom(child, spec); got != "18.20.0" {
			t.Fatalf("value = %q, want 18.20.0 (the child directory wins)", got)
		}
	})

	t.Run("a parent still supplies keys the child omits", func(t *testing.T) {
		java := runtime.Lookup("java")
		if java == nil {
			t.Fatal("java spec missing")
		}
		parent := t.TempDir()
		write(parent, ".ployenv", "java=21\n")
		child := filepath.Join(parent, "child")
		if err := os.MkdirAll(child, 0o755); err != nil {
			t.Fatal(err)
		}
		write(child, ".ployenv", "node=18.20.0\n")
		if got, _ := findProjectFrom(child, java); got != "21" {
			t.Fatalf("java value = %q, want 21 (from the parent .ployenv)", got)
		}
	})

	t.Run("key omitted everywhere", func(t *testing.T) {
		dir := t.TempDir()
		write(dir, ".ployenv", "java=21\n")
		if got, src := findProjectFrom(dir, spec); got != "" || src != "" {
			t.Fatalf("value = %q source = %q, want empty", got, src)
		}
	})
}
