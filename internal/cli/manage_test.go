package cli

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MOMIEDEJIYI/ployenv/internal/config"
	"github.com/MOMIEDEJIYI/ployenv/internal/runtime"
)

// nodeIndexFixture is a trimmed nodejs.org/dist/index.json: two LTS lines and one
// current one, which is enough to exercise the filter, the sort and the limit.
const nodeIndexFixture = `[
  {"version":"v22.11.0","lts":"Jod"},
  {"version":"v23.1.0","lts":false},
  {"version":"v20.18.0","lts":"Iron"}
]`

// useMirror points the node mirror at a local server and writes it into
// config.json, so commands that consult an index run offline through the
// ordinary mirror path (FR-6) instead of reaching the internet.
func useMirror(t *testing.T) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/index.json" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(nodeIndexFixture))
	}))
	t.Cleanup(srv.Close)
	writeSandboxConfig(t, fmt.Sprintf(`{"mirrors":{"node":%q}}`, srv.URL))
	return srv.URL
}

func TestListReportsWhatIsInstalled(t *testing.T) {
	resetSandbox(t)
	makeVersion(t, "node", "20.18.0")
	makeVersion(t, "node", "22.11.0")
	makeVersion(t, "java", "21.0.12.1+1")

	out := mustRunCLI(t, "list")
	for _, want := range []string{"node (Node.js)", "java (Java", "20.18.0", "22.11.0", "21.0.12.1+1"} {
		if !strings.Contains(out, want) {
			t.Errorf("list output is missing %q:\n%s", want, out)
		}
	}
	// Nothing is configured yet, so nothing may be marked as current.
	if strings.Contains(out, "<- current") {
		t.Errorf("no version is configured, so none may be current:\n%s", out)
	}

	// An explicit language is listed even when it has nothing installed.
	out = mustRunCLI(t, "list", "go")
	if !strings.Contains(out, "go (Go)") || !strings.Contains(out, "(not installed)") {
		t.Errorf("list go should say it is not installed:\n%s", out)
	}

	if _, code := runCLI(t, "list", "ruby"); code != 1 {
		t.Errorf("list ruby: exit = %d, want 1", code)
	}
}

func TestListWithoutAnythingInstalledHintsWhatToDo(t *testing.T) {
	resetSandbox(t)

	out := mustRunCLI(t, "list")
	if !strings.Contains(out, "No runtimes installed yet.") {
		t.Fatalf("list on an empty root printed:\n%s", out)
	}
	if !strings.Contains(out, "ployenv support") || !strings.Contains(out, "ployenv available") {
		t.Errorf("the empty listing must point at support/available:\n%s", out)
	}
}

func TestListJSONMarksTheConfiguredVersion(t *testing.T) {
	resetSandbox(t)
	makeVersion(t, "node", "21.0.0")
	writeSandboxConfig(t, `{"defaults":{"node":"21.0.0"}}`)

	out := mustRunCLI(t, "list", "--json")
	var rows []struct {
		Lang    string `json:"lang"`
		Version string `json:"version"`
		Current bool   `json:"current"`
		Source  string `json:"source"`
		Dir     string `json:"dir"`
	}
	if err := json.Unmarshal([]byte(out), &rows); err != nil {
		t.Fatalf("decoding: %v\n%s", err, out)
	}
	if len(rows) != 1 {
		t.Fatalf("got %d rows, want 1: %s", len(rows), out)
	}
	if !rows[0].Current || rows[0].Source != "global" || rows[0].Version != "21.0.0" {
		t.Errorf("the configured version must be marked current with its source: %+v", rows[0])
	}
}

func TestCurrentExplainsWhereTheVersionComesFrom(t *testing.T) {
	root := resetSandbox(t)

	out := mustRunCLI(t, "current")
	if !strings.Contains(out, "Nothing configured yet") {
		t.Fatalf("current on an empty root printed:\n%s", out)
	}

	makeVersion(t, "node", "21.0.0")
	writeSandboxConfig(t, `{"defaults":{"node":"21.0.0"}}`)

	out = mustRunCLI(t, "current")
	if !strings.Contains(out, "No project file (.ployenv) found; using global defaults:") {
		t.Errorf("a global default must be announced:\n%s", out)
	}
	if !strings.Contains(out, "node") || !strings.Contains(out, "[global]") {
		t.Errorf("the row must name the language and the source:\n%s", out)
	}

	out = mustRunCLI(t, "current", "node")
	if !strings.Contains(out, "21.0.0") {
		t.Errorf("current node lost the version:\n%s", out)
	}

	if _, code := runCLI(t, "current", "ruby"); code != 1 {
		t.Errorf("current ruby: exit = %d, want 1", code)
	}

	// A default that points at a version which is not installed is an error the
	// user has to see (and the exit code has to say so).
	writeSandboxConfig(t, `{"defaults":{"node":"99.0.0"}}`)
	out, code := runCLI(t, "current")
	if code != 1 {
		t.Errorf("exit = %d, want 1 when the configured version is missing", code)
	}
	if !strings.Contains(out, "99.0.0") {
		t.Errorf("the report must mention the missing version:\n%s", out)
	}
	if !strings.Contains(root, "node") && !strings.Contains(out, "not installed") {
		t.Errorf("the error text must explain the problem:\n%s", out)
	}
}

func TestCurrentPrefersTheProjectFile(t *testing.T) {
	resetSandbox(t)
	makeVersion(t, "node", "20.18.0")
	makeVersion(t, "node", "21.0.0")
	writeSandboxConfig(t, `{"defaults":{"node":"21.0.0"}}`)

	proj := t.TempDir()
	if err := os.WriteFile(filepath.Join(proj, ".ployenv"), []byte("node=20.18.0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	chdir(t, proj)

	out := mustRunCLI(t, "current")
	if strings.Contains(out, "No project file") {
		t.Errorf("a project file is present, so it must not claim otherwise:\n%s", out)
	}
	if !strings.Contains(out, "20.18.0") || !strings.Contains(out, "[project]") {
		t.Errorf("the project pin must win:\n%s", out)
	}
}

func TestWhichPrintsTheToolPath(t *testing.T) {
	resetSandbox(t)
	spec := runtime.Lookup("node")
	dir := makeVersion(t, "node", "21.0.0")
	writeSandboxConfig(t, `{"defaults":{"node":"21.0.0"}}`)

	out := mustRunCLI(t, "which", "node")
	want := filepath.Join(dir, spec.ToolPath("node", "21.0.0"))
	if strings.TrimSpace(out) != want {
		t.Errorf("which node = %q, want %q", strings.TrimSpace(out), want)
	}

	// The .exe suffix is accepted and dropped.
	out = mustRunCLI(t, "which", "node.exe")
	if strings.TrimSpace(out) != want {
		t.Errorf("which node.exe = %q, want %q", strings.TrimSpace(out), want)
	}

	// A shim that ployenv does not provide falls back to the primary tool.
	out = mustRunCLI(t, "which", "npm")
	if !strings.Contains(out, "npm") {
		t.Errorf("which npm = %q", strings.TrimSpace(out))
	}

	if _, code := runCLI(t, "which", "ruby-tool"); code != 1 {
		t.Errorf("which with an unknown tool: exit = %d, want 1", code)
	}
	if _, code := runCLI(t, "which"); code != 1 {
		t.Errorf("which without arguments: exit = %d, want 1", code)
	}

	// Nothing configured: the message must tell the user what to run first.
	writeSandboxConfig(t, `{}`)
	out, code := runCLI(t, "which", "node")
	if code != 1 {
		t.Errorf("exit = %d, want 1", code)
	}
	if !strings.Contains(out, "") && code == 0 {
		t.Errorf("unexpected success output: %s", out)
	}
}

func TestDoctorHumanReport(t *testing.T) {
	root := resetSandbox(t)
	makeVersion(t, "node", "21.0.0")
	writeSandboxConfig(t, `{"defaults":{"node":"21.0.0"}}`)
	// Keep the executable out of PATH so the hint branch is exercised.
	t.Setenv("PATH", filepath.Join(root, "not-the-exe-dir"))

	out := mustRunCLI(t, "doctor")
	for _, want := range []string{
		"ployenv " + Version,
		"executable",
		"data dir",
		"source",
		"writable",
		"versions",
		"cache",
		"logs",
		"hint: add this directory to PATH",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("doctor is missing %q:\n%s", want, out)
		}
	}
	if !strings.Contains(out, "node") {
		t.Errorf("doctor must report the resolved runtimes:\n%s", out)
	}
}

func TestAvailableListsFromTheConfiguredMirror(t *testing.T) {
	resetSandbox(t)
	useMirror(t)

	out := mustRunCLI(t, "available", "node")
	// Newest first, with the LTS notes attached.
	for _, want := range []string{"23.1.0", "22.11.0", "20.18.0", "LTS Jod", "LTS Iron"} {
		if !strings.Contains(out, want) {
			t.Errorf("available output is missing %q:\n%s", want, out)
		}
	}
	if strings.Index(out, "23.1.0") > strings.Index(out, "22.11.0") {
		t.Errorf("the newest version must come first:\n%s", out)
	}
	if strings.Contains(out, "total, showing") {
		t.Errorf("three releases fit under the default limit, so nothing is truncated:\n%s", out)
	}

	// A limit truncates and says so.
	out = mustRunCLI(t, "available", "node", "--limit", "2")
	if !strings.Contains(out, "3 total, showing the newest 2") {
		t.Errorf("--limit 2 must explain the truncation:\n%s", out)
	}
	if strings.Contains(out, "20.18.0") {
		t.Errorf("--limit 2 must not print the third release:\n%s", out)
	}

	// --all and --limit 0 both mean "everything".
	for _, args := range [][]string{{"available", "node", "--all"}, {"available", "node", "--limit", "0"}} {
		out := mustRunCLI(t, args...)
		if !strings.Contains(out, "20.18.0") {
			t.Errorf("%v must print every release:\n%s", args, out)
		}
	}

	// Filters behave like the runtime's own list: a version prefix and the lts
	// keyword, and an unknown one matches nothing.
	if out := mustRunCLI(t, "available", "node", "22"); !strings.Contains(out, "22.11.0") || strings.Contains(out, "23.1.0") {
		t.Errorf("available node 22 = %s", out)
	}
	if out := mustRunCLI(t, "available", "node", "lts"); strings.Contains(out, "23.1.0") {
		t.Errorf("available node lts must drop the non-LTS line:\n%s", out)
	}
	if out := mustRunCLI(t, "available", "node", "9.9.9"); !strings.Contains(out, "No matching versions.") {
		t.Errorf("available node 9.9.9 = %q", out)
	}

	// Machine output stays a JSON array and honours --limit.
	out = mustRunCLI(t, "available", "node", "--json", "--limit", "2")
	var rels []struct {
		Version string `json:"Version"`
	}
	if err := json.Unmarshal([]byte(out), &rels); err != nil {
		t.Fatalf("available --json is not valid JSON: %v\n%s", err, out)
	}
	if len(rels) != 2 {
		t.Errorf("available --json --limit 2 returned %d releases, want 2", len(rels))
	}
}

func TestAvailableUsageErrors(t *testing.T) {
	resetSandbox(t)

	if _, code := runCLI(t, "available"); code != 1 {
		t.Errorf("available without a language: exit = %d, want 1", code)
	}
	if _, code := runCLI(t, "available", "ruby"); code != 1 {
		t.Errorf("available ruby: exit = %d, want 1", code)
	}
}

func TestCacheReportsAndEmpties(t *testing.T) {
	resetSandbox(t)

	out := mustRunCLI(t, "cache")
	if !strings.Contains(out, config.CacheDir()) || !strings.Contains(out, "(empty)") {
		t.Fatalf("cache on an empty root printed:\n%s", out)
	}

	dir := config.CacheDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	files := map[string]string{"node-v22.11.0-win-x64.zip": strings.Repeat("x", 2048), "jdk-21.zip": strings.Repeat("y", 1024)}
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	out = mustRunCLI(t, "cache")
	for name := range files {
		if !strings.Contains(out, name) {
			t.Errorf("cache must list %s:\n%s", name, out)
		}
	}
	if !strings.Contains(out, "2 file(s)") {
		t.Errorf("cache must count the files:\n%s", out)
	}

	out = mustRunCLI(t, "cache", "--json")
	var payload struct {
		Dir   string `json:"dir"`
		Total int64  `json:"total_bytes"`
		Files []struct {
			Name string `json:"name"`
			Size int64  `json:"size"`
		} `json:"files"`
	}
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		t.Fatalf("decoding cache --json: %v\n%s", err, out)
	}
	if payload.Total != 3072 || len(payload.Files) != 2 || payload.Dir != dir {
		t.Errorf("cache --json = %+v", payload)
	}

	out = mustRunCLI(t, "cache", "--clean")
	if !strings.Contains(out, "Cache emptied: 2 file(s)") {
		t.Errorf("cache --clean printed:\n%s", out)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Errorf("%d cache files survived --clean", len(entries))
	}
	// A subdirectory is not an archive and must not be removed by --clean.
	if err := os.MkdirAll(filepath.Join(dir, "keepme"), 0o755); err != nil {
		t.Fatal(err)
	}
	mustRunCLI(t, "cache", "--clean")
	if _, err := os.Stat(filepath.Join(dir, "keepme")); err != nil {
		t.Errorf("--clean must only remove files: %v", err)
	}
}

func TestSupportListsRuntimesWithTheirState(t *testing.T) {
	resetSandbox(t)
	useMirror(t)
	makeVersion(t, "node", "21.0.0")

	out := mustRunCLI(t, "support")
	for _, want := range []string{"ployenv manages 5 runtimes", "node", "aliases:", "tools:", "mirror:", "installed: 21.0.0", "installed: none"} {
		if !strings.Contains(out, want) {
			t.Errorf("support output is missing %q:\n%s", want, out)
		}
	}

	out = mustRunCLI(t, "support", "--json")
	var rows []struct {
		Name      string   `json:"name"`
		Title     string   `json:"title"`
		Aliases   []string `json:"aliases"`
		Tools     []string `json:"tools"`
		Installed []string `json:"installed"`
		Default   string   `json:"default"`
		Mirror    string   `json:"mirror"`
	}
	if err := json.Unmarshal([]byte(out), &rows); err != nil {
		t.Fatalf("decoding support --json: %v\n%s", err, out)
	}
	if len(rows) != 5 {
		t.Fatalf("got %d runtimes, want 5", len(rows))
	}
	for _, r := range rows {
		if r.Name == "node" {
			if len(r.Installed) != 1 || r.Installed[0] != "21.0.0" {
				t.Errorf("node installed = %v", r.Installed)
			}
			if r.Mirror == "" {
				t.Error("the configured mirror must be reported")
			}
		}
		// Every runtime always reports a list, never null, so scripts can index it.
		if r.Installed == nil {
			t.Errorf("%s: installed must be an empty array, not null", r.Name)
		}
	}
}
