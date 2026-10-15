package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// I-32: the project scope must be usable in full - set, change and CLEAR - and
// the scope is stated on the command line instead of guessed. I-31: a flag the
// command does not accept is refused rather than silently changing the other
// scope.
func TestUseProjectFlagHasAShortAlias(t *testing.T) {
	resetSandbox(t)
	makeVersion(t, "node", "20.18.0")
	proj := t.TempDir()
	chdir(t, proj)

	out := mustRunCLI(t, "use", "node@20.18.0", "-p")
	if !strings.Contains(out, "Updated project file") {
		t.Errorf("use -p printed:\n%s", out)
	}
	data, err := os.ReadFile(filepath.Join(proj, ".ployenv"))
	if err != nil {
		t.Fatalf("reading .ployenv: %v", err)
	}
	if !strings.Contains(string(data), "node=20.18.0") {
		t.Errorf(".ployenv =\n%s", data)
	}
	// -p is the project scope, so the global default stays empty.
	if got := readConfig(t).Defaults["node"]; got != "" {
		t.Errorf("use -p changed the global default: %q", got)
	}
}

func TestUseGlobalFlagStatesTheDefaultScope(t *testing.T) {
	resetSandbox(t)
	makeVersion(t, "node", "20.18.0")
	proj := t.TempDir()
	chdir(t, proj)

	if out := mustRunCLI(t, "use", "node@20.18.0", "-g"); !strings.Contains(out, "Global default set") {
		t.Errorf("use -g printed:\n%s", out)
	}
	if got := readConfig(t).Defaults["node"]; got != "20.18.0" {
		t.Errorf("global default = %q, want 20.18.0", got)
	}
	if _, err := os.Stat(filepath.Join(proj, ".ployenv")); !os.IsNotExist(err) {
		t.Errorf("use -g must not write a project file (stat err = %v)", err)
	}
	// The two scopes are mutually exclusive, and saying both must fail.
	if _, code := runCLI(t, "use", "node@20.18.0", "-p", "-g"); code == 0 {
		t.Error("use -p -g must fail")
	}
}

func TestUseClearWordDropsTheGlobalDefault(t *testing.T) {
	resetSandbox(t)
	makeVersion(t, "node", "20.18.0")
	mustRunCLI(t, "use", "node@20.18.0")

	out := mustRunCLI(t, "use", "node@none")
	if !strings.Contains(out, "Global default cleared") {
		t.Errorf("use node@none printed:\n%s", out)
	}
	if got := readConfig(t).Defaults["node"]; got != "" {
		t.Errorf("the global default survived: %q", got)
	}
	if out := mustRunCLI(t, "use", "node@none"); !strings.Contains(out, "already unset") {
		t.Errorf("clearing twice printed:\n%s", out)
	}
}

func TestUseClearWordKeepsOtherProjectLines(t *testing.T) {
	resetSandbox(t)
	makeVersion(t, "node", "20.18.0")
	makeVersion(t, "java", "21.0.0")
	proj := t.TempDir()
	chdir(t, proj)
	if err := os.WriteFile(filepath.Join(proj, ".ployenv"), []byte("java=21.0.0\nnode=20.18.0\n# keep me\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	out := mustRunCLI(t, "use", "node@none", "-p")
	if !strings.Contains(out, "node removed") {
		t.Errorf("use node@none -p printed:\n%s", out)
	}
	data, err := os.ReadFile(filepath.Join(proj, ".ployenv"))
	if err != nil {
		t.Fatalf("reading .ployenv: %v", err)
	}
	text := string(data)
	if strings.Contains(text, "node=") {
		t.Errorf("the node entry survived:\n%s", text)
	}
	if !strings.Contains(text, "java=21.0.0") || !strings.Contains(text, "# keep me") {
		t.Errorf("other lines must survive:\n%s", text)
	}
}

func TestUseClearWordRemovesAnEmptiedProjectFile(t *testing.T) {
	resetSandbox(t)
	makeVersion(t, "node", "20.18.0")
	proj := t.TempDir()
	chdir(t, proj)
	mustRunCLI(t, "use", "node@20.18.0", "-p")

	mustRunCLI(t, "use", "node@none", "-p")
	if _, err := os.Stat(filepath.Join(proj, ".ployenv")); !os.IsNotExist(err) {
		t.Errorf("an emptied .ployenv must be removed (stat err = %v)", err)
	}
	// Clearing what is not there is reported, not an error.
	if out := mustRunCLI(t, "use", "node@none", "-p"); !strings.Contains(out, "nothing to clear") {
		t.Errorf("use node@none -p printed:\n%s", out)
	}
}

func TestUnknownFlagsAreRefused(t *testing.T) {
	resetSandbox(t)
	// The I-31 trap: `--project` on `config` used to be ignored, so the GLOBAL
	// default changed while the caller believed the project file did.
	if _, code := runCLI(t, "config", "--set-default", "node", "none", "--project"); code == 0 {
		t.Error("config --project must fail")
	}
	if got := readConfig(t).Defaults["node"]; got != "" {
		t.Errorf("the refused command still wrote a global default: %q", got)
	}
	for _, args := range [][]string{
		{"list", "--project"},
		{"use", "node@20.18.0", "--nope"},
		{"current", "--all"},
		{"cache", "--clean", "--force"},
	} {
		if _, code := runCLI(t, args...); code == 0 {
			t.Errorf("ployenv %v must fail", args)
		}
	}
	// The flags a command really takes keep working.
	if _, code := runCLI(t, "list", "--json"); code != 0 {
		t.Error("list --json must still work")
	}
	if _, code := runCLI(t, "cache", "--clean"); code != 0 {
		t.Error("cache --clean must still work")
	}
}
