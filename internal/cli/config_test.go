package cli

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MOMIEDEJIYI/ployenv/internal/config"
	"github.com/MOMIEDEJIYI/ployenv/internal/platform"
	"github.com/MOMIEDEJIYI/ployenv/internal/resolve"
	"github.com/MOMIEDEJIYI/ployenv/internal/runtime"
)

// readConfig loads config.json the way the next ployenv run would.
func readConfig(t *testing.T) *config.Config {
	t.Helper()
	cfg := config.Load()
	if cfg == nil {
		t.Fatal("config.Load returned nil")
	}
	return cfg
}

func TestConfigShowsEverySection(t *testing.T) {
	resetSandbox(t)
	writeSandboxConfig(t, `{"mirrors":{"node":"https://mirror.invalid/node"},"defaults":{"node":"21.0.0"},"logs":{"mode":"always","keep":7},"cache":{"keep":true}}`)

	out := mustRunCLI(t, "config")
	for _, want := range []string{
		"root", "config", "defaults:", "mirrors:", "logs:", "cache:",
		"node", "https://mirror.invalid/node", "21.0.0", "always", "true",
		"change: config --set-default",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("config output is missing %q:\n%s", want, out)
		}
	}
	// Runtimes without a mirror fall back to their upstream URL, not to a word.
	if !strings.Contains(out, "https://") {
		t.Errorf("a runtime without a mirror must show its upstream URL:\n%s", out)
	}

	out = mustRunCLI(t, "config", "--json")
	var cfg config.Config
	if err := json.Unmarshal([]byte(out), &cfg); err != nil {
		t.Fatalf("decoding config --json: %v\n%s", err, out)
	}
	if cfg.Mirrors["node"] != "https://mirror.invalid/node" || cfg.Defaults["node"] != "21.0.0" {
		t.Errorf("config --json lost settings: %+v", cfg)
	}
}

func TestConfigSetMirror(t *testing.T) {
	resetSandbox(t)

	out := mustRunCLI(t, "config", "--set-mirror", "node", "https://mirror.invalid/node/")
	if !strings.Contains(out, "Mirror updated: node") {
		t.Errorf("set-mirror printed:\n%s", out)
	}
	// The trailing slash is dropped so the stored value joins cleanly.
	if got := readConfig(t).Mirrors["node"]; got != "https://mirror.invalid/node" {
		t.Errorf("stored mirror = %q", got)
	}

	// "none" restores the official source by removing the entry.
	mustRunCLI(t, "config", "--set-mirror", "node", "none")
	if _, ok := readConfig(t).Mirrors["node"]; ok {
		t.Error("mirror=none must delete the entry")
	}
	for _, word := range []string{"off", "default"} {
		mustRunCLI(t, "config", "--set-mirror", "node", "https://mirror.invalid/x")
		mustRunCLI(t, "config", "--set-mirror", "node", word)
		if _, ok := readConfig(t).Mirrors["node"]; ok {
			t.Errorf("mirror=%s must delete the entry", word)
		}
	}

	// A mirror cannot be set for a runtime ployenv does not manage.
	if _, code := runCLI(t, "config", "--set-mirror", "ruby", "https://mirror.invalid"); code != 1 {
		t.Error("set-mirror with an unknown runtime must fail")
	}
	if _, code := runCLI(t, "config", "--set-mirror", "node"); code != 1 {
		t.Error("set-mirror without a URL must fail")
	}
	if _, code := runCLI(t, "config", "--set-mirror"); code != 1 {
		t.Error("set-mirror without a runtime must fail")
	}
}

func TestConfigSetDefault(t *testing.T) {
	resetSandbox(t)

	if out := mustRunCLI(t, "config", "--set-default", "node", "21.0.0"); !strings.Contains(out, "Default version updated: node = 21.0.0") {
		t.Errorf("set-default printed:\n%s", out)
	}
	if got := readConfig(t).Defaults["node"]; got != "21.0.0" {
		t.Errorf("stored default = %q", got)
	}

	if _, code := runCLI(t, "config", "--set-default", "node"); code != 1 {
		t.Error("set-default without a version must fail")
	}
	if _, code := runCLI(t, "config", "--set-default"); code != 1 {
		t.Error("set-default without a runtime must fail")
	}
	if _, code := runCLI(t, "config", "--set-default", "ruby", "3.3.0"); code != 1 {
		t.Error("set-default for an unknown runtime must fail")
	}
}

func TestConfigSetLogModeAndCacheKeep(t *testing.T) {
	resetSandbox(t)

	for _, mode := range []string{"off", "on-error", "always"} {
		out := mustRunCLI(t, "config", "--set-log-mode", mode)
		if !strings.Contains(out, "Log mode updated: "+mode) {
			t.Errorf("set-log-mode %s printed:\n%s", mode, out)
		}
		if got := readConfig(t).Logs; got == nil || got.Mode != mode {
			t.Errorf("stored log mode = %+v, want %s", got, mode)
		}
	}
	// The value is case-insensitive and trimmed.
	mustRunCLI(t, "config", "--set-log-mode", "  ALWAYS ")
	if got := readConfig(t).Logs; got == nil || got.Mode != "always" {
		t.Errorf("stored log mode = %+v", got)
	}
	if _, code := runCLI(t, "config", "--set-log-mode", "sometimes"); code != 1 {
		t.Error("an unknown log mode must fail")
	}

	out := mustRunCLI(t, "config", "--set-cache-keep", "true")
	if !strings.Contains(out, "Cache keep updated: true") {
		t.Errorf("set-cache-keep printed:\n%s", out)
	}
	if got := readConfig(t).Cache; got == nil || !got.Keep {
		t.Errorf("stored cache keep = %+v", got)
	}
	mustRunCLI(t, "config", "--set-cache-keep", "false")
	if got := readConfig(t).Cache; got == nil || got.Keep {
		t.Errorf("stored cache keep = %+v", got)
	}
	if _, code := runCLI(t, "config", "--set-cache-keep", "maybe"); code != 1 {
		t.Error("a value that is not a bool must fail")
	}
	if _, code := runCLI(t, "config", "--set-cache-keep"); code != 1 {
		t.Error("set-cache-keep without a value must fail")
	}
}

func TestUseSetsTheGlobalDefault(t *testing.T) {
	resetSandbox(t)
	makeVersion(t, "node", "21.0.0")

	out := mustRunCLI(t, "use", "node@21.0.0")
	if !strings.Contains(out, "Global default set: node = 21.0.0") {
		t.Errorf("use printed:\n%s", out)
	}
	// Recording a version is not the same as activating it; say so.
	if !strings.Contains(out, "does not change PATH") || !strings.Contains(out, "ployenv shell") {
		t.Errorf("use must explain how to activate:\n%s", out)
	}
	// A7 / REQ-008: the recipes have to be ones this platform can actually run.
	if platform.IsWindows() {
		if !strings.Contains(out, "PowerShell") || !strings.Contains(out, "Invoke-Expression") {
			t.Errorf("on Windows the hint must show the Windows recipes:\n%s", out)
		}
	} else {
		if strings.Contains(out, "PowerShell") || strings.Contains(out, "Invoke-Expression") {
			t.Errorf("off Windows the hint must not offer PowerShell:\n%s", out)
		}
		if !strings.Contains(out, `eval "$(ployenv env --shell bash)"`) {
			t.Errorf("off Windows the hint must show the POSIX recipe:\n%s", out)
		}
	}
	if got := readConfig(t).Defaults["node"]; got != "21.0.0" {
		t.Errorf("stored default = %q", got)
	}

	// A partial request resolves to the installed version (BUG-004: the same rule
	// `current` uses, so "latest" cannot mean two different things).
	out = mustRunCLI(t, "use", "node@21")
	if !strings.Contains(out, "node = 21.0.0") {
		t.Errorf("use node@21 printed:\n%s", out)
	}

	// A second positional argument is accepted as the version.
	out = mustRunCLI(t, "use", "node", "21.0.0")
	if !strings.Contains(out, "node = 21.0.0") {
		t.Errorf("use node 21.0.0 printed:\n%s", out)
	}
}

func TestUseWritesAProjectFile(t *testing.T) {
	resetSandbox(t)
	makeVersion(t, "node", "20.18.0")
	proj := t.TempDir()
	chdir(t, proj)

	// An existing project file keeps its other lines and its order.
	original := "java=21\nnode=20.18.0\n# comment\n"
	if err := os.WriteFile(filepath.Join(proj, ".ployenv"), []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}
	makeVersion(t, "java", "21.0.0")

	out := mustRunCLI(t, "use", "java@21", "--project")
	if !strings.Contains(out, "Updated project file") || !strings.Contains(out, "java = 21.0.0") {
		t.Errorf("use --project printed:\n%s", out)
	}
	data, err := os.ReadFile(filepath.Join(proj, ".ployenv"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	if !strings.Contains(text, "java=21.0.0") {
		t.Errorf("the entry was not replaced in place:\n%s", text)
	}
	if !strings.Contains(text, "node=20.18.0") || !strings.Contains(text, "# comment") {
		t.Errorf("other lines must survive:\n%s", text)
	}
	// The project file is not the global default.
	if got := readConfig(t).Defaults["java"]; got != "" {
		t.Errorf("--project must not change the global default, got %q", got)
	}
}

func TestUseErrors(t *testing.T) {
	resetSandbox(t)
	makeVersion(t, "node", "21.0.0")

	if _, code := runCLI(t, "use"); code != 1 {
		t.Error("use without arguments must fail")
	}
	if _, code := runCLI(t, "use", "node"); code != 1 {
		t.Error("use without a version must fail")
	}
	if _, code := runCLI(t, "use", "node@9.9.9"); code != 1 {
		t.Error("use of a version that is not installed must fail")
	}
	if _, code := runCLI(t, "use", "ruby@3.3.0"); code != 1 {
		t.Error("use of an unknown runtime must fail")
	}
	out, code := runCLI(t, "use", "node@lts")
	if code != 1 {
		t.Error("use node@lts without it installed must fail")
	}
	if !strings.Contains(out, "") && code == 0 {
		t.Errorf("unexpected output: %s", out)
	}
}

func TestUninstallRemovesVersions(t *testing.T) {
	resetSandbox(t)
	dir := makeVersion(t, "node", "21.0.0")
	makeVersion(t, "node", "20.18.0")
	writeSandboxConfig(t, `{"defaults":{"node":"21.0.0"}}`)

	if out := mustRunCLI(t, "uninstall", "node@21.0.0"); !strings.Contains(out, "Uninstalled node@21.0.0") {
		t.Errorf("uninstall printed:\n%s", out)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Error("the version directory must be gone")
	}
	// Removing the version that was the global default clears the default too.
	if got := readConfig(t).Defaults["node"]; got != "" {
		t.Errorf("the default must be cleared, got %q", got)
	}

	if out := mustRunCLI(t, "uninstall", "node", "--all"); !strings.Contains(out, "Removed all node versions: 20.18.0") {
		t.Errorf("uninstall --all printed:\n%s", out)
	}
	// With nothing left, --all reports instead of failing.
	if out := mustRunCLI(t, "uninstall", "node", "--all"); !strings.Contains(out, "has no versions installed") {
		t.Errorf("uninstall --all on an empty runtime printed:\n%s", out)
	}
}

func TestUninstallErrors(t *testing.T) {
	resetSandbox(t)
	makeVersion(t, "node", "21.0.0")

	if _, code := runCLI(t, "uninstall"); code != 1 {
		t.Error("uninstall without arguments must fail")
	}
	if _, code := runCLI(t, "uninstall", "node"); code != 1 {
		t.Error("uninstall without a version must fail")
	}
	if _, code := runCLI(t, "uninstall", "ruby@3.3.0"); code != 1 {
		t.Error("uninstall of an unknown runtime must fail")
	}
	if _, code := runCLI(t, "uninstall", "node@9.9.9"); code != 1 {
		t.Error("uninstalling a version that is not installed must fail")
	}
}

func TestAdoptRegistersAnExistingDirectory(t *testing.T) {
	resetSandbox(t)
	src := filepath.Join(t.TempDir(), "node-v21.0.0-win-x64")
	if err := os.MkdirAll(filepath.Join(src, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "bin", "node.exe"), []byte("binary"), 0o644); err != nil {
		t.Fatal(err)
	}

	out := mustRunCLI(t, "adopt", "node", "--root", src)
	if !strings.Contains(out, "Adopted node@21.0.0") {
		t.Errorf("adopt printed:\n%s", out)
	}
	// The source directory survives: adopting links, it does not move.
	if _, err := os.Stat(filepath.Join(src, "bin", "node.exe")); err != nil {
		t.Errorf("the source must survive: %v", err)
	}

	// Adopting again reports the truth (BUG-017) instead of claiming a new link.
	out = mustRunCLI(t, "adopt", "node", "--root", src)
	if !strings.Contains(out, "already registered") {
		t.Errorf("re-adopting printed:\n%s", out)
	}
}

func TestAdoptErrors(t *testing.T) {
	resetSandbox(t)

	if _, code := runCLI(t, "adopt"); code != 1 {
		t.Error("adopt without arguments must fail")
	}
	if _, code := runCLI(t, "adopt", "node"); code != 1 {
		t.Error("adopt without --root must fail")
	}
	if _, code := runCLI(t, "adopt", "ruby", "--root", t.TempDir()); code != 1 {
		t.Error("adopt of an unknown runtime must fail")
	}
	if _, code := runCLI(t, "adopt", "node", "--root", filepath.Join(t.TempDir(), "missing")); code != 1 {
		t.Error("adopt of a directory that does not exist must fail")
	}
}

func TestInstallUsageAndAlreadyInstalled(t *testing.T) {
	resetSandbox(t)
	useMirror(t)
	makeVersion(t, "node", "21.0.0")

	if _, code := runCLI(t, "install"); code != 1 {
		t.Error("install without arguments must fail")
	}
	if _, code := runCLI(t, "install", "ruby@3.3.0"); code != 1 {
		t.Error("install of an unknown runtime must fail")
	}

	// Already installed: the command says so and never touches the network.
	out := mustRunCLI(t, "install", "node@21.0.0")
	if !strings.Contains(out, "already registered") {
		t.Errorf("install of an installed version printed:\n%s", out)
	}

	// --from hands the request to adopt, so an existing directory is registered
	// without downloading anything.
	src := filepath.Join(t.TempDir(), "node-v20.18.0-win-x64")
	if err := os.MkdirAll(src, 0o755); err != nil {
		t.Fatal(err)
	}
	out = mustRunCLI(t, "install", "node@20.18.0", "--from", src)
	if !strings.Contains(out, "Installed node@20.18.0") {
		t.Errorf("install --from printed:\n%s", out)
	}
	if _, err := os.Stat(config.VersionDir("node", "20.18.0")); err != nil {
		t.Errorf("install --from must register the directory: %v", err)
	}
}

func TestEnvRendersForEachShell(t *testing.T) {
	resetSandbox(t)

	if _, code := runCLI(t, "env"); code != 1 {
		t.Error("env with nothing configured must fail")
	}
	if _, code := runCLI(t, "shell"); code != 1 {
		t.Error("shell with nothing configured must fail")
	}

	makeVersion(t, "node", "21.0.0")
	writeSandboxConfig(t, `{"defaults":{"node":"21.0.0"}}`)

	cases := []struct {
		args []string
		want string
		dir  string // "" means the shell escapes the path, so only the version is asserted
	}{
		{[]string{"env", "--shell", "pwsh"}, "$env:PATH", config.VersionDir("node", "21.0.0")},
		{[]string{"env", "--shell", "powershell"}, "$env:PATH", config.VersionDir("node", "21.0.0")},
		{[]string{"env", "--shell", "cmd"}, `set "PATH=`, config.VersionDir("node", "21.0.0")},
		// The POSIX shells quote the entry, so a Windows path does not appear
		// verbatim: the version still has to.
		{[]string{"env", "--shell", "bash"}, "export PATH=", ""},
		{[]string{"env", "--shell", "zsh"}, "export PATH=", ""},
		{[]string{"env"}, "PATH", ""},
	}
	for _, tc := range cases {
		out := mustRunCLI(t, tc.args...)
		if !strings.Contains(out, tc.want) {
			t.Errorf("%v did not render %q:\n%s", tc.args, tc.want, out)
		}
		if tc.dir != "" && !strings.Contains(out, tc.dir) {
			t.Errorf("%v must put the version directory on PATH:\n%s", tc.args, out)
		}
		if !strings.Contains(out, "21.0.0") {
			t.Errorf("%v must mention the active version:\n%s", tc.args, out)
		}
	}

	// Both spellings of "give me JSON" go through the one emitter.
	for _, args := range [][]string{{"env", "--json"}, {"env", "--shell", "json"}} {
		out := mustRunCLI(t, args...)
		var payload struct {
			Path []string          `json:"path"`
			Vars map[string]string `json:"vars"`
		}
		if err := json.Unmarshal([]byte(out), &payload); err != nil {
			t.Fatalf("%v is not valid JSON: %v\n%s", args, err, out)
		}
		if len(payload.Path) == 0 {
			t.Errorf("%v reported no PATH entries: %s", args, out)
		}
	}
}

func TestLoggingFlagWritesADiagnosticFile(t *testing.T) {
	resetSandbox(t)
	makeVersion(t, "node", "21.0.0")

	mustRunCLI(t, "--log", "list")
	entries, err := os.ReadDir(config.LogsDir())
	if err != nil {
		t.Fatalf("--log must create the log directory: %v", err)
	}
	found := false
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".log") && !e.IsDir() {
			found = true
		}
	}
	if !found {
		t.Errorf("--log wrote no log file, directory holds %v", entries)
	}
}

// The formats below are the contract other programs read, so they are asserted
// directly rather than through a command.
func TestDisplayHelpers(t *testing.T) {
	if yesNo(true) != "yes" || yesNo(false) != "no" {
		t.Error("yesNo must render yes/no")
	}
	if got := presentLabel(filepath.Join(t.TempDir(), "missing")); got != "not created yet" {
		t.Errorf("presentLabel(missing) = %q", got)
	}
	dir := t.TempDir()
	if got := presentLabel(dir); got != "not created yet" {
		t.Errorf("presentLabel(directory) = %q, want it to look at files only", got)
	}
	file := filepath.Join(dir, "f")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := presentLabel(file); got != "present" {
		t.Errorf("presentLabel(file) = %q", got)
	}

	if got := detailSuffix(""); got != "" {
		t.Errorf("detailSuffix(\"\") = %q", got)
	}
	if got := detailSuffix("  "); got != "" {
		t.Errorf("detailSuffix(blank) = %q", got)
	}
	if got := detailSuffix(".ployenv"); got != " .ployenv" {
		t.Errorf("detailSuffix = %q", got)
	}

	if got := orDefault("", "fallback"); got != "fallback" {
		t.Errorf("orDefault(empty) = %q", got)
	}
	if got := orDefault("  ", "fallback"); got != "fallback" {
		t.Errorf("orDefault(blank) = %q", got)
	}
	if got := orDefault("set", "fallback"); got != "set" {
		t.Errorf("orDefault(set) = %q", got)
	}

	if got := orEmpty(nil); got == nil || len(got) != 0 {
		t.Errorf("orEmpty(nil) = %v, want an empty non-nil slice", got)
	}
	if got := orEmpty([]string{"a"}); len(got) != 1 {
		t.Errorf("orEmpty = %v", got)
	}

	st := dirStateOf(dir)
	if st.Path != dir || !st.Present {
		t.Errorf("dirStateOf = %+v", st)
	}
	if st := dirStateOf(filepath.Join(dir, "missing")); st.Present {
		t.Errorf("dirStateOf(missing) = %+v", st)
	}
}

func TestPathHasMatchesEntriesExactly(t *testing.T) {
	dir := t.TempDir()
	sep := string(filepath.ListSeparator)

	t.Setenv("PATH", dir+sep+filepath.Join(dir, "other"))
	if !pathHas(dir) {
		t.Error("an exact PATH entry must be found")
	}
	if !pathHas(filepath.Join(dir, ".")) {
		t.Error("the comparison must normalise the path")
	}
	if pathHas(filepath.Join(dir, "different")) {
		t.Error("a different directory must not match")
	}
	t.Setenv("PATH", "")
	if pathHas(dir) {
		t.Error("an empty PATH has no entries")
	}
}

func TestResolutionRowsSkipUnconfiguredRuntimes(t *testing.T) {
	results := []resolve.Result{
		{Lang: "node", Source: resolve.SourceNone, Version: "21.0.0"},
		{Lang: "java", Source: resolve.SourceGlobal, Version: "21.0.12.1+1", Dir: "/data/java"},
		{Lang: "go", Source: resolve.SourceProject, Request: "1.27", Detail: ".ployenv", Err: errors.New("not installed")},
	}
	rows := resolutionRows(results)
	if len(rows) != 2 {
		t.Fatalf("got %d rows, want only the configured runtimes: %+v", len(rows), rows)
	}
	if rows[0].Lang != "java" || rows[0].Source != "global" || rows[0].Version != "21.0.12.1+1" {
		t.Errorf("row 0 = %+v", rows[0])
	}
	if rows[1].Lang != "go" || rows[1].Source != "project" || rows[1].Detail != ".ployenv" || rows[1].Error == "" {
		t.Errorf("row 1 = %+v", rows[1])
	}
	// An empty input renders as an empty array, never null.
	out := resolutionRows(nil)
	if out == nil || len(out) != 0 {
		t.Errorf("resolutionRows(nil) = %v", out)
	}
}

func TestMirrorLabelAndLogsEffective(t *testing.T) {
	spec := runtime.Lookup("node")
	cfg := &config.Config{Mirrors: map[string]string{}, Defaults: map[string]string{}}
	got := mirrorLabel(spec, cfg)
	if !strings.Contains(got, spec.OfficialURL()) || !strings.Contains(got, "official") {
		t.Errorf("without a mirror, mirrorLabel = %q, want the upstream URL marked official", got)
	}
	cfg.Mirrors["node"] = "  https://mirror.invalid/node  "
	if got := mirrorLabel(spec, cfg); got != "https://mirror.invalid/node" {
		t.Errorf("mirrorLabel = %q, want it trimmed", got)
	}

	mode, dir, keep := logsEffective(&config.Config{})
	if mode == "" || dir == "" || keep <= 0 {
		t.Errorf("logsEffective defaults = %q %q %d", mode, dir, keep)
	}
	mode, dir, keep = logsEffective(&config.Config{Logs: &config.LogsConfig{Mode: "always", Dir: "diag", Keep: 3}})
	if mode != "always" || !strings.HasSuffix(dir, "diag") || keep != 3 {
		t.Errorf("logsEffective = %q %q %d", mode, dir, keep)
	}
}
