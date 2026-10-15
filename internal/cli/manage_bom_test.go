package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// I-28: with a BOM in front of the first line the key was not recognised, so
// `use --project` appended a second entry instead of replacing the existing one.
// A duplicate is worse than the original bug: the file reads back the stale
// value that comes first, so `use --project` would look like it did nothing.
func TestUseProjectReplacesAKeyBehindABOM(t *testing.T) {
	resetSandbox(t)
	makeVersion(t, "java", "21.0.0")
	proj := t.TempDir()
	chdir(t, proj)

	original := "\ufeffjava=20.0.0\n# keep me\n"
	if err := os.WriteFile(filepath.Join(proj, ".ployenv"), []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}

	if out := mustRunCLI(t, "use", "java@21", "--project"); !strings.Contains(out, "Updated project file") {
		t.Errorf("use --project printed:\n%s", out)
	}

	data, err := os.ReadFile(filepath.Join(proj, ".ployenv"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	if got := strings.Count(text, "java="); got != 1 {
		t.Errorf("the key must appear exactly once, got %d:\n%s", got, text)
	}
	if !strings.Contains(text, "java=21.0.0") {
		t.Errorf("the entry was not replaced:\n%s", text)
	}
	if !strings.Contains(text, "# keep me") {
		t.Errorf("other lines must survive:\n%s", text)
	}
	if strings.HasPrefix(text, "\ufeff") {
		t.Errorf("the rewritten file must not start with a BOM:\n%q", text)
	}
}
