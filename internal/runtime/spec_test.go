package runtime

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSpecToolListDoesNotAliasTheSpec(t *testing.T) {
	sp := &Spec{ExeTools: []string{"exe1", "exe2"}, CmdTools: []string{"wrap1"}}
	got := sp.Tools()
	if !equalStrings(got, []string{"exe1", "exe2", "wrap1"}) {
		t.Fatalf("Tools() = %v, want [exe1 exe2 wrap1]", got)
	}
	got[0] = "clobbered"
	if sp.ExeTools[0] != "exe1" {
		t.Fatal("Tools() must return a copy: the caller's edits leaked into the spec")
	}
}

func TestSpecBinaryPathAndHasBinary(t *testing.T) {
	dir := t.TempDir()
	tool := exeName("demo")
	sp := &Spec{
		Primary: "demo",
		// A real ToolPath answers only for the tools the runtime exposes.
		ToolPath: func(name, _ string) string {
			if name != "demo" {
				return ""
			}
			return filepath.Join("bin", tool)
		},
	}

	want := filepath.Join(dir, "bin", tool)
	if got := sp.BinaryPath(dir, "1.0.0"); got != want {
		t.Fatalf("BinaryPath = %q, want %q", got, want)
	}
	if sp.HasBinary(dir, "1.0.0") {
		t.Fatal("HasBinary must be false while nothing is installed")
	}
	if err := os.MkdirAll(filepath.Dir(want), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(want, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if !sp.HasBinary(dir, "1.0.0") {
		t.Fatal("HasBinary must be true once the primary tool exists")
	}

	// An unknown tool has no path, and a spec without ToolPath cannot have one.
	unknown := &Spec{Primary: "other", ToolPath: func(string, string) string { return "" }}
	if got := unknown.BinaryPath(dir, "1.0.0"); got != "" {
		t.Fatalf("BinaryPath for an unknown tool = %q, want empty", got)
	}
	if unknown.HasBinary(dir, "1.0.0") {
		t.Fatal("HasBinary must be false when ToolPath yields no path")
	}
	none := &Spec{Primary: "demo"}
	if got := none.BinaryPath(dir, "1.0.0"); got != "" {
		t.Fatalf("BinaryPath without ToolPath = %q, want empty", got)
	}
	if none.HasBinary(dir, "1.0.0") {
		t.Fatal("HasBinary must be false without ToolPath")
	}
}

func TestSpecEnvAndPathEntries(t *testing.T) {
	// EnvVar is the simple form: one NAME=dir entry.
	simple := &Spec{EnvVar: "DEMO_HOME"}
	if got := simple.EnvEntries(`/data/demo`); !equalStrings(got, []string{`DEMO_HOME=/data/demo`}) {
		t.Fatalf("EnvEntries = %v, want [DEMO_HOME=/data/demo]", got)
	}

	// A runtime that needs more (the Windows python layout) supplies Env, which
	// wins over EnvVar.
	rich := &Spec{
		EnvVar: "IGNORED",
		Env:    func(dir string) []string { return []string{"A=" + dir, "B=" + filepath.Join(dir, "bin")} },
	}
	want := []string{"A=/data/rich", "B=" + filepath.Join("/data/rich", "bin")}
	if got := rich.EnvEntries("/data/rich"); !equalStrings(got, want) {
		t.Fatalf("EnvEntries = %v, want %v", got, want)
	}

	if got := (&Spec{}).EnvEntries("/data/none"); got != nil {
		t.Fatalf("EnvEntries without EnvVar/Env = %v, want nil", got)
	}

	// PathDirs defaults to the version directory itself.
	if got := (&Spec{}).PathEntries("/data/plain"); !equalStrings(got, []string{"/data/plain"}) {
		t.Fatalf("PathEntries = %v, want [/data/plain]", got)
	}
	nested := &Spec{PathDirs: func(dir string) []string { return []string{dir, filepath.Join(dir, "bin")} }}
	if got := nested.PathEntries("/data/nested"); !equalStrings(got, []string{"/data/nested", filepath.Join("/data/nested", "bin")}) {
		t.Fatalf("PathEntries = %v", got)
	}
}

func TestSpecOfficialURL(t *testing.T) {
	if got := (&Spec{}).OfficialURL(); got != "" {
		t.Fatalf("OfficialURL without Official = %q, want empty", got)
	}
	sp := &Spec{Official: func() string { return "https://example.invalid/dl" }}
	if got := sp.OfficialURL(); got != "https://example.invalid/dl" {
		t.Fatalf("OfficialURL = %q", got)
	}
}

func TestDownloadBasesPrefersMirrorAndDropsDuplicates(t *testing.T) {
	// Without a configured mirror the official source comes first and the
	// well-known mirrors follow, in order, without duplicates or empty entries.
	got := downloadBases("go", "https://go.dev/dl/", "https://m1.invalid/", "", "https://go.dev/dl", "https://m2.invalid")
	want := []string{"https://go.dev/dl", "https://m1.invalid", "https://m2.invalid"}
	if !equalStrings(got, want) {
		t.Fatalf("downloadBases = %v, want %v", got, want)
	}

	// A configured mirror replaces the official base and is not repeated when it
	// is also listed among the fallbacks.
	writeConfig(t, map[string]string{"go": "https://mirror.invalid/golang/"})
	got = downloadBases("go", "https://go.dev/dl", "https://mirror.invalid/golang", "https://m2.invalid")
	want = []string{"https://mirror.invalid/golang", "https://m2.invalid"}
	if !equalStrings(got, want) {
		t.Fatalf("with a mirror, downloadBases = %v, want %v", got, want)
	}
}

func TestBuiltInSpecsAreComplete(t *testing.T) {
	specs := All()
	if len(specs) != 7 {
		t.Fatalf("All() returned %d runtimes, want 7", len(specs))
	}
	for _, sp := range specs {
		if sp.Name == "" || sp.Title == "" || sp.Kind == "" || sp.Primary == "" {
			t.Errorf("%v: name/title/kind/primary must be set", sp)
		}
		if sp.List == nil || sp.Plan == nil || sp.Verify == nil || sp.ToolPath == nil {
			t.Errorf("%s: List/Plan/Verify/ToolPath must be wired", sp.Name)
		}
		if len(sp.Tools()) == 0 {
			t.Errorf("%s: exposes no tools", sp.Name)
		}
		if sp.OfficialURL() == "" {
			t.Errorf("%s: has no official source URL", sp.Name)
		}
		if got := sp.ToolPath(sp.Primary, "1.0.0"); got == "" {
			t.Errorf("%s: no path for its primary tool %q", sp.Name, sp.Primary)
		}
		if got := sp.ToolPath("definitely-not-a-tool", "1.0.0"); got != "" {
			t.Errorf("%s: unknown tool must have no path, got %q", sp.Name, got)
		}
		base := filepath.Join(string(filepath.Separator), "data", sp.Name)
		dirs := sp.PathEntries(base)
		if len(dirs) == 0 {
			t.Errorf("%s: PathEntries is empty", sp.Name)
		}
		for _, d := range dirs {
			if !strings.HasPrefix(d, base) {
				t.Errorf("%s: PathEntries returned %q, outside the version directory", sp.Name, d)
			}
		}
		// Every tool a runtime advertises must resolve to a path, otherwise the
		// shim layer publishes a tool that can never be launched.
		for _, tool := range sp.Tools() {
			if sp.ToolPath(tool, "1.0.0") == "" {
				t.Errorf("%s: tool %q has no path", sp.Name, tool)
			}
		}
	}
}

func TestRegistryLookupByNamesAndAliases(t *testing.T) {
	if got := NamesText(); got != strings.Join(Names(), ", ") {
		t.Fatalf("NamesText = %q", got)
	}
	if want := []string{"node", "java", "go", "python", "maven", "adb", "rust"}; !equalStrings(Names(), want) {
		t.Fatalf("Names = %v, want %v", Names(), want)
	}
	for _, sp := range All() {
		if got := Lookup(sp.Name); got != sp {
			t.Errorf("Lookup(%q) = %v, want the spec itself", sp.Name, got)
		}
		// Names and aliases are matched case-insensitively and trimmed.
		if got := Lookup("  " + strings.ToUpper(sp.Name) + " "); got != sp {
			t.Errorf("Lookup(%q) did not resolve case-insensitively", sp.Name)
		}
		for _, alias := range sp.Aliases {
			if got := Lookup(alias); got != sp {
				t.Errorf("Lookup(alias %q) = %v, want %s", alias, got, sp.Name)
			}
		}
	}
	for _, missing := range []string{"", "   ", "golang2", "kotlin", "nodejs2"} {
		if got := Lookup(missing); got != nil {
			t.Errorf("Lookup(%q) = %v, want nil", missing, got)
		}
	}
}
