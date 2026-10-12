package cli

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// captureStdout runs f with os.Stdout redirected and returns everything written.
func captureStdout(t *testing.T, f func()) string {
	t.Helper()
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w
	done := make(chan string, 1)
	go func() {
		b, _ := io.ReadAll(r)
		done <- string(b)
	}()

	f()

	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	os.Stdout = old
	return <-done
}

// runJSON executes a command and returns stdout, failing on a non-zero exit.
func runJSON(t *testing.T, args ...string) string {
	t.Helper()
	var code int
	out := captureStdout(t, func() { code = Run(args) })
	if code != 0 {
		t.Fatalf("ployenv %s exited with %d", strings.Join(args, " "), code)
	}
	return out
}

// BUG-016: `list --json` and `doctor --json` printed their human report and
// ignored the flag, so the machine-interface contract (FR-18) only held for
// some commands.
//
// Everything lives in one test on purpose: the data root is resolved once per
// process (config.RootInfo uses sync.Once), so a second t.Setenv would silently
// keep pointing at the first root. The root deliberately contains non-ASCII
// characters, which the contract says must come back escaped.
func TestJSONContract(t *testing.T) {
	root := filepath.Join(t.TempDir(), "中文目录🚀")
	t.Setenv("PLOYENV_ROOT", root)

	for _, v := range []string{"20.0.0", "21.0.0"} {
		if err := os.MkdirAll(filepath.Join(root, "versions", "node", v), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	t.Run("every advertised --json is valid pure ASCII", func(t *testing.T) {
		for _, args := range [][]string{
			{"list", "--json"},
			{"list", "node", "--json"},
			{"doctor", "--json"},
			{"current", "--json"},
			{"support", "--json"},
			{"config", "--json"},
			{"cache", "--json"},
		} {
			t.Run(strings.Join(args, " "), func(t *testing.T) {
				out := runJSON(t, args...)
				if strings.TrimSpace(out) == "" {
					t.Fatal("no output")
				}
				if !json.Valid([]byte(out)) {
					t.Fatalf("output is not valid JSON:\n%s", out)
				}
				for i := 0; i < len(out); i++ {
					if out[i] > 0x7F {
						t.Fatalf("byte %#x at offset %d is not ASCII:\n%s", out[i], i, out)
					}
				}
			})
		}
	})

	t.Run("list reports one row per installed version", func(t *testing.T) {
		out := runJSON(t, "list", "--json")

		var rows []struct {
			Lang    string `json:"lang"`
			Version string `json:"version"`
			Current bool   `json:"current"`
			Dir     string `json:"dir"`
		}
		if err := json.Unmarshal([]byte(out), &rows); err != nil {
			t.Fatalf("decoding list --json: %v\n%s", err, out)
		}
		if len(rows) != 2 {
			t.Fatalf("got %d rows, want 2:\n%s", len(rows), out)
		}
		for _, r := range rows {
			if r.Lang != "node" {
				t.Errorf("lang = %q, want node", r.Lang)
			}
			if !strings.HasPrefix(r.Dir, root) {
				t.Errorf("dir = %q, want it under %s", r.Dir, root)
			}
		}
	})

	t.Run("list of a language with nothing installed is an empty array", func(t *testing.T) {
		out := runJSON(t, "list", "python", "--json")
		if strings.TrimSpace(out) != "[]" {
			t.Fatalf("got %q, want []", strings.TrimSpace(out))
		}
	})

	t.Run("doctor reports paths and state", func(t *testing.T) {
		out := runJSON(t, "doctor", "--json")

		var report struct {
			Version       string `json:"version"`
			OS            string `json:"os"`
			Arch          string `json:"arch"`
			Exe           string `json:"exe"`
			DataDir       string `json:"data_dir"`
			DataSource    string `json:"data_source"`
			Writable      bool   `json:"writable"`
			Config        string `json:"config"`
			ConfigPresent bool   `json:"config_present"`
			Versions      struct {
				Path    string `json:"path"`
				Present bool   `json:"present"`
			} `json:"versions"`
		}
		if err := json.Unmarshal([]byte(out), &report); err != nil {
			t.Fatalf("decoding doctor --json: %v\n%s", err, out)
		}
		if report.Version == "" || report.OS == "" || report.Arch == "" || report.Exe == "" {
			t.Errorf("version/os/arch/exe must be filled in: %s", out)
		}
		if !strings.HasPrefix(report.DataDir, root) {
			t.Errorf("data_dir = %q, want it under %s", report.DataDir, root)
		}
		if report.DataSource == "" {
			t.Error("data_source must say why this directory was chosen")
		}
		if !report.Writable {
			t.Error("a temporary root should be writable")
		}
		if !strings.HasPrefix(report.Versions.Path, root) {
			t.Errorf("versions.path = %q, want it under %s", report.Versions.Path, root)
		}
		if !report.Versions.Present {
			t.Error("versions/ exists in the fixture, so present should be true")
		}
		if !strings.HasPrefix(report.Config, root) {
			t.Errorf("config = %q, want it under %s", report.Config, root)
		}
	})
}
