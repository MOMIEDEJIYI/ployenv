package cli

import (
	"strings"
	"testing"

	"github.com/MOMIEDEJIYI/ployenv/internal/runtime"
)

func TestRunWithoutArgumentsPrintsHelp(t *testing.T) {
	resetSandbox(t)

	out, code := runCLI(t)
	if code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	for _, want := range []string{"usage: ployenv <command>", "ployenv install", "ployenv available"} {
		if !strings.Contains(out, want) {
			t.Errorf("help is missing %q:\n%s", want, out)
		}
	}
}

func TestHelpAndVersionAliases(t *testing.T) {
	resetSandbox(t)

	for _, alias := range []string{"help", "-h", "-help", "--help", "-?"} {
		out, code := runCLI(t, alias)
		if code != 0 {
			t.Errorf("%s: exit = %d, want 0", alias, code)
		}
		if !strings.Contains(out, "usage: ployenv <command>") {
			t.Errorf("%s did not print the help text:\n%s", alias, out)
		}
	}

	for _, alias := range []string{"version", "-v", "--version"} {
		out, code := runCLI(t, alias)
		if code != 0 {
			t.Errorf("%s: exit = %d, want 0", alias, code)
		}
		if !strings.Contains(out, "ployenv "+Version) {
			t.Errorf("%s printed %q, want the version string", alias, strings.TrimSpace(out))
		}
		// The platform is part of the version line so bug reports carry it.
		if !strings.Contains(out, "(") || !strings.Contains(out, "/") {
			t.Errorf("%s printed %q, want os/arch in parentheses", alias, strings.TrimSpace(out))
		}
	}
}

func TestHelpAndVersionNeverTouchTheDataDirectory(t *testing.T) {
	root := resetSandbox(t)

	for _, args := range [][]string{{"help"}, {"version"}} {
		out, code := runCLI(t, args...)
		if code != 0 {
			t.Fatalf("%v: exit = %d", args, code)
		}
		if strings.Contains(out, root) {
			t.Errorf("%v mentioned the data directory; pure help/version must not", args)
		}
	}
}

func TestUnknownCommandFailsWithUsageHint(t *testing.T) {
	resetSandbox(t)

	out, code := runCLI(t, "definitely-not-a-command")
	if code != 1 {
		t.Fatalf("exit = %d, want 1", code)
	}
	if strings.TrimSpace(out) != "" {
		t.Errorf("an unknown command must not print to stdout, got:\n%s", out)
	}
}

// BUG-020: a flag the parser knows but nothing implements must fail loudly
// instead of being ignored while the command runs for real.
func TestUnimplementedFlagIsRejectedBeforeDispatch(t *testing.T) {
	resetSandbox(t)

	if _, code := runCLI(t, "list", "--dry-run"); code != 1 {
		t.Fatalf("exit = %d, want 1 for --dry-run", code)
	}
}

func TestParseGlobalsSeparatesFlagsFromArguments(t *testing.T) {
	args := []string{"--json", "list", "-q", "node", "--verbose", "--debug", "--log", "--no-color", "-y", "--yes"}
	g := parseGlobals(&args)

	if !g.json || !g.quiet || !g.verbose || !g.debug || !g.forceLog || !g.noColor || !g.yes {
		t.Fatalf("not every flag was picked up: %+v", g)
	}
	if len(args) != 2 || args[0] != "list" || args[1] != "node" {
		t.Fatalf("remaining arguments = %v, want [list node]", args)
	}
}

func TestParseGlobalsLeavesOtherFlagsAlone(t *testing.T) {
	args := []string{"available", "node", "--limit", "5", "--all"}
	g := parseGlobals(&args)

	if g.json || g.verbose || g.quiet || g.debug || g.forceLog || g.noColor || g.yes {
		t.Fatalf("command flags must not set globals: %+v", g)
	}
	if strings.Join(args, " ") != "available node --limit 5 --all" {
		t.Fatalf("arguments = %v", args)
	}
}

// Every documented alias has to reach the same command, otherwise a script that
// uses `ls` or `list-remote` silently does something else.
func TestCommandAliasesDispatchToTheSameCommand(t *testing.T) {
	resetSandbox(t)
	makeVersion(t, "node", "21.0.0")

	aliases := []struct {
		args []string
		want string
	}{
		{[]string{"support"}, "ployenv manages"},
		{[]string{"supported"}, "ployenv manages"},
		{[]string{"runtimes"}, "ployenv manages"},
		{[]string{"langs"}, "ployenv manages"},
		{[]string{"envs"}, "ployenv manages"},
	}
	for _, tc := range aliases {
		out, code := runCLI(t, tc.args...)
		if code != 0 {
			t.Fatalf("%v: exit = %d", tc.args, code)
		}
		if !strings.Contains(out, tc.want) {
			t.Errorf("%v printed %q, want it to contain %q", tc.args, out, tc.want)
		}
	}

	listAliases := [][]string{{"list"}, {"ls"}}
	var first string
	for i, args := range listAliases {
		out, code := runCLI(t, args...)
		if code != 0 {
			t.Fatalf("%v: exit = %d", args, code)
		}
		if i == 0 {
			first = out
			continue
		}
		if out != first {
			t.Errorf("%v and %v printed different output:\n%s\n---\n%s", listAliases[0], args, first, out)
		}
	}
}

// A command that is not part of the dispatch switch but looks like one must not
// be mistaken for a runtime name either.
func TestSpecOrFailRejectsUnknownRuntimes(t *testing.T) {
	if _, err := specOrFail("node"); err != nil {
		t.Fatalf("node must be a known runtime: %v", err)
	}
	// Aliases resolve to the same spec.
	for _, alias := range []string{"nodejs", "golang", "jdk", "py"} {
		if _, err := specOrFail(alias); err != nil {
			t.Errorf("alias %q must resolve: %v", alias, err)
		}
	}
	_, err := specOrFail("ruby")
	if err == nil {
		t.Fatal("ruby is not managed, so this must fail")
	}
	if !strings.Contains(err.Error(), "supported:") {
		t.Errorf("the error must list the supported runtimes, got %v", err)
	}
}

func TestFindToolMapsEveryAdvertisedTool(t *testing.T) {
	for _, spec := range runtime.All() {
		for _, tool := range spec.Tools() {
			if got := findTool(tool); got != spec {
				t.Errorf("findTool(%q) = %v, want %s", tool, got, spec.Name)
			}
		}
		if got := findTool(spec.Primary); got != spec {
			t.Errorf("findTool(%q) = %v, want %s", spec.Primary, got, spec.Name)
		}
	}
	for _, unknown := range []string{"", "definitely-not-a-tool", "npmx"} {
		if got := findTool(unknown); got != nil {
			t.Errorf("findTool(%q) = %v, want nil", unknown, got)
		}
	}
}
