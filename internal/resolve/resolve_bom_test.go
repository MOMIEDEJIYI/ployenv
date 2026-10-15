package resolve

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/MOMIEDEJIYI/ployenv/internal/runtime"
)

// I-28: a UTF-8 BOM in front of a project file used to hide the first line
// completely -- strings.TrimSpace does not treat U+FEFF as space (it is Cf, not
// White_Space), so the key was looked up as "\ufeffnode" and the line was
// skipped with no warning. Editors on Windows write that BOM by default.
func TestProjectFilesTolerateALeadingBOM(t *testing.T) {
	const bom = "\ufeff"
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

	cases := []struct {
		name string
		file string
		body string
		want string
	}{
		{"ployenv", ".ployenv", bom + "node=22.11.0\n", "22.11.0"},
		{"ployenv with the BOM on a line of its own", ".ployenv", bom + "\nnode=22.11.0\n", "22.11.0"},
		{"tool-versions", ".tool-versions", bom + "nodejs 22.11.0\n", "22.11.0"},
		{"the .nvmrc style file", ".nvmrc", bom + "22.11.0\n", "22.11.0"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			write(dir, tc.file, tc.body)
			got, src := findProjectFrom(dir, spec)
			if got != tc.want {
				t.Fatalf("value = %q, want %q (source %q)", got, tc.want, src)
			}
			if filepath.Base(src) != tc.file {
				t.Fatalf("source = %q, want %q", src, tc.file)
			}
		})
	}

	// go.mod is read through the same helper. The toolchain directive is
	// asserted rather than the `go` directive because only the former is
	// reported without an installed Go version -- the test must not depend on
	// what happens to be installed on the machine.
	t.Run("go.mod first line", func(t *testing.T) {
		dir := t.TempDir()
		write(dir, "go.mod", bom+"toolchain go1.22.5\n")
		got, src := goModVersion(dir)
		if got != "1.22.5" {
			t.Fatalf("toolchain = %q, want 1.22.5 (source %q)", got, src)
		}
	})
}
