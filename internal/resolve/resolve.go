// Package resolve decides which version of each runtime is active, following
// the simplified two-level rule (FR-14): project file first, then the global
// default. There is no implicit "only installed version" fallback.
package resolve

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/MOMIEDEJIYI/ployenv/internal/config"
	"github.com/MOMIEDEJIYI/ployenv/internal/fsutil"
	"github.com/MOMIEDEJIYI/ployenv/internal/runtime"
	"github.com/MOMIEDEJIYI/ployenv/internal/version"
)

// Source identifies where a version came from.
type Source int

const (
	SourceNone Source = iota
	SourceEnv
	SourceProject
	SourceGlobal
)

// Label is the human-readable label shown to the user.
func (s Source) Label() string {
	switch s {
	case SourceEnv:
		return "env"
	case SourceProject:
		return "project"
	case SourceGlobal:
		return "global"
	}
	return "unset"
}

// Result is the resolution outcome for one runtime.
type Result struct {
	Lang    string
	Version string // resolved installed version ("" when not configured)
	Request string // the raw request from the source
	Source  Source
	Detail  string // file path, env var name, or "" for the global default
	Dir     string
	Err     error // set when declared but not installed
}

// Configured reports whether the runtime has any version configured.
func (r Result) Configured() bool { return r.Source != SourceNone }

// EnvKey is the override environment variable for a runtime.
func EnvKey(lang string) string { return "PLOYENV_" + strings.ToUpper(lang) + "_VERSION" }

// For resolves one runtime.
func For(spec *runtime.Spec) Result {
	res := Result{Lang: spec.Name}

	if v := strings.TrimSpace(os.Getenv(EnvKey(spec.Name))); v != "" {
		res.Request, res.Source, res.Detail = v, SourceEnv, EnvKey(spec.Name)
		finish(&res, spec)
		return res
	}
	if v, src := findProject(spec); v != "" {
		res.Request, res.Source, res.Detail = v, SourceProject, src
		finish(&res, spec)
		return res
	}
	if v := strings.TrimSpace(config.Load().Defaults[spec.Name]); v != "" {
		res.Request, res.Source, res.Detail = v, SourceGlobal, ""
		finish(&res, spec)
		return res
	}
	return res
}

// All resolves every built-in runtime.
func All() []Result {
	specs := runtime.All()
	out := make([]Result, 0, len(specs))
	for _, s := range specs {
		out = append(out, For(s))
	}
	return out
}

func finish(res *Result, spec *runtime.Spec) {
	m := MatchInstalled(spec.Name, res.Request)
	if m == "" {
		where := res.Detail
		if where == "" {
			where = "the global default"
		}
		res.Err = fmt.Errorf("%s requires %s@%s, but it is not installed (run: ployenv install %s@%s)",
			where, spec.Name, res.Request, spec.Name, res.Request)
		return
	}
	res.Version = m
	res.Dir = config.VersionDir(spec.Name, m)
}

// MatchInstalled returns the highest installed version satisfying request.
//
// It is the single matching rule for both resolution and `use`, so `latest`
// means the same thing everywhere (FR-14, BUG-004).
func MatchInstalled(lang, request string) string {
	installed := config.InstalledVersions(lang)
	request = strings.TrimSpace(request)
	if request == "" || request == "latest" || request == "all" {
		return version.Highest(installed)
	}
	var candidates []string
	for _, v := range installed {
		if version.Matches(request, v) {
			candidates = append(candidates, v)
		}
	}
	return version.Highest(candidates)
}

// ------------------------------------------------------------ project files

// projectFileName is the one project file ployenv reads. A project pins its
// versions in exactly one .ployenv, so there is never a question of which of
// two files wins (I-11: the optional .ployenv.local was removed for that
// reason).
const projectFileName = ".ployenv"

func findProject(spec *runtime.Spec) (value, source string) {
	dir, err := os.Getwd()
	if err != nil {
		return "", ""
	}
	return findProjectFrom(dir, spec)
}

// findProjectFrom walks up from start looking for the project file; the nearest
// directory that defines the requested key wins.
func findProjectFrom(start string, spec *runtime.Spec) (value, source string) {
	dir := start
	for depth := 0; depth < 32; depth++ {
		p := filepath.Join(dir, projectFileName)
		if data, err := fsutil.ReadTextFile(p); err == nil {
			if v := parseKeyValue(string(data), spec); v != "" {
				return v, p
			}
		}
		tf := filepath.Join(dir, ".tool-versions")
		if data, err := fsutil.ReadTextFile(tf); err == nil {
			if v := parseToolVersions(string(data), spec); v != "" {
				return v, tf
			}
		}
		for _, name := range compatFiles(spec.Name) {
			p := filepath.Join(dir, name)
			if data, err := fsutil.ReadTextFile(p); err == nil {
				if v := firstVersionToken(string(data)); v != "" {
					return v, p
				}
			}
		}
		if spec.Name == "go" {
			if v, src := goModVersion(dir); v != "" {
				return v, src
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return "", ""
}

func compatFiles(lang string) []string {
	switch lang {
	case "node":
		return []string{".nvmrc", ".node-version"}
	case "java":
		return []string{".java-version"}
	case "python":
		return []string{".python-version"}
	case "go":
		return []string{".go-version"}
	}
	return nil
}

var (
	goModDirective       = regexp.MustCompile(`(?m)^go\s+(\d+(?:\.\d+){1,3})`)
	goToolchainDirective = regexp.MustCompile(`(?m)^toolchain\s+go(\d+(?:\.\d+){1,3})`)
	versionToken         = regexp.MustCompile(`\d+(?:\.\d+){0,3}`)
)

func goModVersion(dir string) (string, string) {
	p := filepath.Join(dir, "go.mod")
	data, err := fsutil.ReadTextFile(p)
	if err != nil {
		return "", ""
	}
	content := string(data)
	if m := goToolchainDirective.FindStringSubmatch(content); m != nil {
		return m[1], p + " (toolchain directive)"
	}
	if m := goModDirective.FindStringSubmatch(content); m != nil {
		if MatchInstalled("go", m[1]) != "" {
			return m[1], p + " (go directive)"
		}
	}
	return "", ""
}

func stripComment(line string) string {
	if i := strings.Index(line, "#"); i >= 0 {
		line = line[:i]
	}
	if i := strings.Index(line, "//"); i >= 0 {
		line = line[:i]
	}
	return line
}

func trimValue(v string) string { return strings.Trim(strings.TrimSpace(v), "'\"") }

// parseKeyValue reads lines like "node=22.11.0" or "java 21".
func parseKeyValue(content string, spec *runtime.Spec) string {
	sc := bufio.NewScanner(strings.NewReader(content))
	for sc.Scan() {
		line := strings.TrimSpace(stripComment(sc.Text()))
		if line == "" {
			continue
		}
		key, value := "", ""
		if i := strings.IndexAny(line, "=:"); i > 0 {
			key, value = line[:i], line[i+1:]
		} else if fields := strings.Fields(line); len(fields) >= 2 {
			key, value = fields[0], fields[1]
		}
		if key == "" {
			continue
		}
		if s := runtime.Lookup(key); s != nil && s.Name == spec.Name {
			if v := trimValue(value); v != "" {
				return v
			}
		}
	}
	return ""
}

var toolVersionAliases = map[string]string{
	"nodejs": "node", "node": "node",
	"golang": "go", "go": "go",
	"java": "java", "python": "python", "python3": "python",
}

func parseToolVersions(content string, spec *runtime.Spec) string {
	sc := bufio.NewScanner(strings.NewReader(content))
	for sc.Scan() {
		line := strings.TrimSpace(stripComment(sc.Text()))
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		if name, ok := toolVersionAliases[fields[0]]; ok && name == spec.Name {
			return trimValue(fields[len(fields)-1])
		}
	}
	return ""
}

func firstVersionToken(content string) string {
	line := strings.TrimSpace(stripComment(content))
	if line == "" {
		return ""
	}
	return versionToken.FindString(line)
}
