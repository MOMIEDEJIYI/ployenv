package runtime

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// runtimeTable keeps the CI end-to-end matrix and the registry in step. Every
// runtime named there is one CI job; a runtime missing from the table is simply
// never exercised, and nothing else would notice.
const runtimeTable = "../../ci/e2e-runtimes.tsv"

// suiteDir is where the test binary started, which is this package's directory.
// It is captured at package initialisation, before TestMain moves the suite into
// its sandbox, so the table stays reachable from tests (I-34).
var suiteDir = func() string {
	wd, err := os.Getwd()
	if err != nil {
		return ""
	}
	return wd
}()

type e2eRow struct {
	name     string
	version  string
	tool     string
	args     string
	expect   string
	requires string
}

func readRuntimeTable(t *testing.T) []e2eRow {
	t.Helper()
	if suiteDir == "" {
		t.Fatal("cannot determine the checkout root: the process has no working directory")
	}
	path := filepath.Join(suiteDir, runtimeTable)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v\nEvery runtime needs a row there, or CI never runs it", path, err)
	}
	var rows []e2eRow
	for i, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSuffix(line, "\r")
		if strings.TrimSpace(line) == "" || strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		f := strings.Split(line, "\t")
		if len(f) != 6 {
			t.Fatalf("%s:%d: %d tab-separated fields, want 6 (name, version, tool, args, expect, requires)", runtimeTable, i+1, len(f))
		}
		rows = append(rows, e2eRow{f[0], f[1], f[2], f[3], f[4], f[5]})
	}
	if len(rows) == 0 {
		t.Fatalf("%s lists no runtimes", runtimeTable)
	}
	return rows
}

func hasTool(spec *Spec, tool string) bool {
	for _, name := range spec.Tools() {
		if name == tool {
			return true
		}
	}
	return false
}

func TestE2ETableRowsResolveToTheRegistry(t *testing.T) {
	seen := map[string]bool{}
	for _, row := range readRuntimeTable(t) {
		spec := Lookup(row.name)
		if spec == nil {
			t.Errorf("%s: %q is not a registered runtime", runtimeTable, row.name)
			continue
		}
		if spec.Name != row.name {
			t.Errorf("%s: %q is an alias of %q; the canonical name keeps the matrix readable", runtimeTable, row.name, spec.Name)
		}
		if seen[row.name] {
			t.Errorf("%s: %q appears twice; each runtime gets one job", runtimeTable, row.name)
		}
		seen[row.name] = true

		if row.version == "" {
			t.Errorf("%s: %q has no version", runtimeTable, row.name)
		}
		if !hasTool(spec, row.tool) {
			t.Errorf("%s: %q is not one of %v's tools (%v)", runtimeTable, row.tool, row.name, spec.Tools())
		}
		if row.requires != "-" {
			lang, _, _ := strings.Cut(row.requires, "@")
			if Lookup(lang) == nil {
				t.Errorf("%s: %q requires %q, which is not a registered runtime", runtimeTable, row.name, row.requires)
			}
		}
		if _, err := regexp.Compile(row.expect); err != nil {
			t.Errorf("%s: %q has an uncompilable expect %q: %v", runtimeTable, row.name, row.expect, err)
		}
	}
}

// The reverse direction is the one that rots silently: a new runtime with no row
// is never exercised, yet the suite and the gates stay green.
func TestEveryRuntimeHasAnE2ERow(t *testing.T) {
	seen := map[string]bool{}
	for _, row := range readRuntimeTable(t) {
		seen[row.name] = true
	}
	for _, spec := range All() {
		if !seen[spec.Name] {
			t.Errorf("%s has no row in %s: add one (name, version, tool, args, expect, requires) so CI exercises it",
				spec.Name, runtimeTable)
		}
	}
}
