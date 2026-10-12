package envx

import (
	"os"
	"strings"
	"testing"

	"github.com/MOMIEDEJIYI/ployenv/internal/platform"
)

// Literal Windows paths on purpose: Script() is pure string rendering, and the
// escaping bugs it must not reintroduce (I-07/I-08) are Windows-shaped, so the
// assertions should hold on every runner, not only on windows-latest.
func sampleEntry() Entry {
	return Entry{
		PathDirs: []string{`C:\PloyEnv\versions\node\22.23.3`, `C:\PloyEnv\versions\go\1.27.2\bin`},
		Vars:     [][2]string{{"GOROOT", `C:\PloyEnv\versions\go\1.27.2`}},
	}
}

// I-07: the pwsh script was rendered with Go's %q verb, which doubles every
// backslash. PowerShell does not treat a backslash as an escape character, so
// the doubled form reached $env:PATH verbatim (and a '"' would have produced a
// syntax error).
func TestScriptPWSHKeepsBackslashesIntact(t *testing.T) {
	e := sampleEntry()
	out, err := e.Script("pwsh")
	if err != nil {
		t.Fatalf("Script(pwsh) returned an error: %v", err)
	}
	if strings.Contains(out, `\\`) {
		t.Fatalf("Script(pwsh) doubled a backslash:\n%s", out)
	}
	wantPath := quotePS(strings.Join(e.PathDirs, platform.PathSep()) + platform.PathSep())
	if !strings.Contains(out, "$env:PATH = "+wantPath+" + $env:PATH") {
		t.Errorf("Script(pwsh) PATH line is wrong:\ngot  %s\nwant $env:PATH = %s + $env:PATH", out, wantPath)
	}
	if !strings.Contains(out, `$env:GOROOT = 'C:\PloyEnv\versions\go\1.27.2'`) {
		t.Errorf("Script(pwsh) variable line is wrong:\n%s", out)
	}
}

// A single quote is the only character a PowerShell single quoted literal cares
// about; a dollar sign must stay literal inside it.
func TestScriptPWSHQuotesValue(t *testing.T) {
	e := Entry{Vars: [][2]string{{"X", `C:\a'b$c`}}}
	out, err := e.Script("pwsh")
	if err != nil {
		t.Fatalf("Script(pwsh) returned an error: %v", err)
	}
	if !strings.Contains(out, `$env:X = 'C:\a''b$c'`) {
		t.Errorf("Script(pwsh) quoting is wrong:\n%s", out)
	}
}

// I-08: the separator is a property of the target shell. On Windows the POSIX
// shells (Git Bash, MSYS, WSL) still use ':' -- ';' made them treat the whole
// list as a single directory.
func TestPathSeparatorPerShell(t *testing.T) {
	cases := map[string]string{
		"":           ":",
		"bash":       ":",
		"sh":         ":",
		"zsh":        ":",
		"bash.exe":   ":",
		"BASH":       ":",
		"pwsh":       platform.PathSep(),
		"powershell": platform.PathSep(),
		"PowerShell": platform.PathSep(),
		"cmd":        ";",
		"cmd.exe":    ";",
		"CMD.EXE":    ";",
	}
	for shell, want := range cases {
		if got := pathSeparator(shell); got != want {
			t.Errorf("pathSeparator(%q) = %q, want %q", shell, got, want)
		}
	}
	e := sampleEntry()
	for _, shell := range []string{"bash", "sh", "zsh", "weird-shell"} {
		out, err := e.Script(shell)
		if err != nil {
			t.Fatalf("Script(%q) returned an error: %v", shell, err)
		}
		if !strings.Contains(out, quotePOSIX(strings.Join(e.PathDirs, ":")+":")) {
			t.Errorf("Script(%q) must join with ':' (not the platform separator):\n%s", shell, out)
		}
	}
}

func TestScriptPOSIXKeepsDollarExpansion(t *testing.T) {
	e := sampleEntry()
	out, err := e.Script("bash")
	if err != nil {
		t.Fatalf("Script(bash) returned an error: %v", err)
	}
	if !strings.Contains(out, quotePOSIX(strings.Join(e.PathDirs, ":")+":")+"$PATH") {
		t.Errorf("Script(bash) must keep $PATH outside the literal:\n%s", out)
	}
	if strings.Contains(out, `\$PATH`) {
		t.Errorf("Script(bash) escaped $PATH, so it will not expand:\n%s", out)
	}
}

// Go's %q leaves '$' and '`' alone; inside a double quoted shell string both
// would still be interpreted.
func TestScriptPOSIXEscapesDollarAndBacktick(t *testing.T) {
	e := Entry{Vars: [][2]string{{"X", "a$b`c-d\"e\\f"}}}
	out, err := e.Script("bash")
	if err != nil {
		t.Fatalf("Script(bash) returned an error: %v", err)
	}
	wantValue := `a` + `\$` + `b` + `\` + "`" + `c-d` + `\"` + `e` + `\\` + `f`
	if !strings.Contains(out, `export X="`+wantValue+`"`) {
		t.Errorf("Script(bash) escaping is wrong:\ngot  %s\nwant export X=%q", out, wantValue)
	}
}

func TestScriptCMDShape(t *testing.T) {
	e := sampleEntry()
	out, err := e.Script("cmd")
	if err != nil {
		t.Fatalf("Script(cmd) returned an error: %v", err)
	}
	want := `set "PATH=` + strings.Join(e.PathDirs, ";") + `;%PATH%"`
	if !strings.Contains(out, want) {
		t.Errorf("Script(cmd) is wrong:\ngot  %s\nwant %s", out, want)
	}
	if !strings.Contains(out, `set "GOROOT=C:\PloyEnv\versions\go\1.27.2"`) {
		t.Errorf("Script(cmd) should set the variables:\n%s", out)
	}
	if strings.Contains(out, `\\`) {
		t.Errorf("Script(cmd) doubled a backslash:\n%s", out)
	}
}

// cmd expands %VAR% even inside `set "K=V"`, so a literal percent has to be doubled
// (the script is meant to be saved and `call`ed, where %% collapses back to %). I-13.
func TestScriptCMDPercentIsDoubled(t *testing.T) {
	e := Entry{
		Vars:     [][2]string{{"PLOYENV_TEST_PERCENT", `100%done and %PATH% untouched`}},
		PathDirs: []string{`C:\PloyEnv\100%data`},
	}
	out, err := e.Script("cmd")
	if err != nil {
		t.Fatalf("Script(cmd) returned an error: %v", err)
	}
	if !strings.Contains(out, `set "PLOYENV_TEST_PERCENT=100%%done and %%PATH%% untouched"`) {
		t.Errorf("Script(cmd) did not double the literal percents:\n%s", out)
	}
	if !strings.Contains(out, `set "PATH=C:\PloyEnv\100%%data;%PATH%"`) {
		t.Errorf("Script(cmd) did not double a percent inside a PATH entry (and must keep the trailing %%PATH%% expandable):\n%s", out)
	}
}

// A double quote cannot be expressed inside `set "K=V"`: it would end the quoted
// value and turn the remainder into commands, so it is refused with an actionable
// message instead (I-13, owner picked option 1).
func TestScriptCMDRejectsDoubleQuote(t *testing.T) {
	for _, tc := range []struct {
		name string
		e    Entry
	}{
		{"value", Entry{Vars: [][2]string{{"PLOYENV_TEST_QUOTE", `a"b`}}}},
		{"path dir", Entry{PathDirs: []string{`C:\PloyEnv\a"b`}}},
	} {
		out, err := tc.e.Script("cmd")
		if err == nil {
			t.Fatalf("%s: expected an error for a double quote, got:\n%s", tc.name, out)
		}
		if !strings.Contains(err.Error(), `double quote`) || !strings.Contains(err.Error(), "--shell") {
			t.Errorf("%s: the error should name the problem and an alternative shell, got: %v", tc.name, err)
		}
		if out != "" {
			t.Errorf("%s: nothing may be emitted when the value cannot be expressed, got:\n%s", tc.name, out)
		}
	}
	// the other shells must still work with the same value
	if _, err := (Entry{Vars: [][2]string{{"X", `a"b`}}}).Script("pwsh"); err != nil {
		t.Errorf("pwsh must not be affected by the cmd limitation: %v", err)
	}
}

func TestScriptJSONMatchesPayload(t *testing.T) {
	e := sampleEntry()
	out, err := e.Script("json")
	if err != nil {
		t.Fatalf("Script(json) returned an error: %v", err)
	}
	// jsonx escapes backslashes, so the JSON form deliberately differs from the
	// shell forms (the documented 0.2.0 contract, FR-18).
	for _, dir := range e.PathDirs {
		if !strings.Contains(out, strings.ReplaceAll(dir, `\`, `\\`)) {
			t.Errorf("Script(json) does not contain %q:\n%s", dir, out)
		}
	}
	if !strings.Contains(out, `"path"`) || !strings.Contains(out, `"vars"`) {
		t.Errorf("Script(json) is missing the path/vars keys:\n%s", out)
	}
}

// Environ is what `ployenv shell` hands to the child process: managed
// directories first, the previous PATH kept.
func TestEnvironPrependsPathAndSetsVars(t *testing.T) {
	old, had := os.LookupEnv("PATH")
	if err := os.Setenv("PATH", `C:\windows;C:\windows\system32`); err != nil {
		t.Fatalf("Setenv: %v", err)
	}
	defer func() {
		if had {
			os.Setenv("PATH", old)
		} else {
			os.Unsetenv("PATH")
		}
	}()

	e := sampleEntry()
	env := e.Environ()

	pathValue := ""
	for _, kv := range env {
		if i := strings.Index(kv, "="); i > 0 && strings.EqualFold(kv[:i], "PATH") {
			pathValue = kv[i+1:]
		}
	}
	if pathValue == "" {
		t.Fatalf("no PATH entry in Environ(): %v", env)
	}
	wantPrefix := strings.Join(e.PathDirs, platform.PathSep()) + platform.PathSep()
	if !strings.HasPrefix(pathValue, wantPrefix) {
		t.Errorf("PATH = %q, want prefix %q", pathValue, wantPrefix)
	}
	if !strings.Contains(pathValue, `C:\windows`) {
		t.Errorf("PATH lost the previous value: %q", pathValue)
	}

	found := false
	for _, kv := range env {
		if kv == "GOROOT="+e.Vars[0][1] {
			found = true
		}
	}
	if !found {
		t.Errorf("Environ() did not set GOROOT: %v", env)
	}
}
