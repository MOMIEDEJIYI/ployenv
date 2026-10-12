// Package envx turns resolved versions into a session environment and can
// print it (for the user to eval) or spawn a child shell with it applied
// (FR-6). It never touches the global system environment.
package envx

import (
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/MOMIEDEJIYI/ployenv/internal/jsonx"
	"github.com/MOMIEDEJIYI/ployenv/internal/platform"
	"github.com/MOMIEDEJIYI/ployenv/internal/resolve"
	"github.com/MOMIEDEJIYI/ployenv/internal/runtime"
)

// Entry is the environment delta for a set of resolved runtimes.
type Entry struct {
	PathDirs []string
	Vars     [][2]string
}

// Build collects the PATH directories and variables for resolved runtimes.
func Build(results []resolve.Result) Entry {
	var e Entry
	for _, r := range results {
		if r.Version == "" || r.Err != nil {
			continue
		}
		spec := runtime.Lookup(r.Lang)
		if spec == nil {
			continue
		}
		e.PathDirs = append(e.PathDirs, spec.PathEntries(r.Dir)...)
		for _, kv := range spec.EnvEntries(r.Dir) {
			if i := strings.Index(kv, "="); i > 0 {
				e.Vars = append(e.Vars, [2]string{kv[:i], kv[i+1:]})
			}
		}
	}
	return e
}

// Empty reports whether nothing would be applied.
func (e Entry) Empty() bool { return len(e.PathDirs) == 0 && len(e.Vars) == 0 }

// Environ returns the current environment with the entry applied.
func (e Entry) Environ() []string {
	base := map[string]string{}
	var order []string
	for _, kv := range os.Environ() {
		i := strings.Index(kv, "=")
		if i < 0 {
			continue
		}
		k, v := kv[:i], kv[i+1:]
		if _, seen := base[k]; !seen {
			order = append(order, k)
		}
		base[k] = v
	}

	set := func(name, value string) {
		if key, ok := findKey(base, name); ok {
			base[key] = value
			return
		}
		order = append(order, name)
		base[name] = value
	}

	if len(e.PathDirs) > 0 {
		key, _ := findKey(base, "PATH")
		old := base[key]
		joined := strings.Join(e.PathDirs, platform.PathSep())
		if old != "" {
			joined = joined + platform.PathSep() + old
		}
		set("PATH", joined)
	}
	for _, kv := range e.Vars {
		set(kv[0], kv[1])
	}

	out := make([]string, 0, len(order))
	for _, k := range order {
		out = append(out, k+"="+base[k])
	}
	return out
}

func findKey(base map[string]string, name string) (string, bool) {
	if _, ok := base[name]; ok {
		return name, true
	}
	if platform.IsWindows() {
		for k := range base {
			if strings.EqualFold(k, name) {
				return k, true
			}
		}
	}
	return "", false
}

// Payload is the machine-readable form of an entry.
type Payload struct {
	PathDirs []string          `json:"path"`
	Vars     map[string]string `json:"vars"`
}

// JSONValue renders the entry for the shared JSON emitter, so `env --json` and
// `env --shell json` cannot drift apart (FR-18).
func (e Entry) JSONValue() Payload {
	dirs := e.PathDirs
	if dirs == nil {
		dirs = []string{}
	}
	vars := map[string]string{}
	for _, kv := range e.Vars {
		vars[kv[0]] = kv[1]
	}
	return Payload{PathDirs: dirs, Vars: vars}
}

// Script renders the entry for a shell, suitable for eval / Invoke-Expression.
func (e Entry) Script(shell string) (string, error) {
	sep := pathSeparator(shell)
	switch strings.ToLower(strings.TrimSpace(shell)) {
	case "json":
		return jsonx.Encode(e.JSONValue())
	case "pwsh", "powershell":
		var b strings.Builder
		for _, kv := range e.Vars {
			fmt.Fprintf(&b, "$env:%s = %s\n", kv[0], quotePS(kv[1]))
		}
		if len(e.PathDirs) > 0 {
			fmt.Fprintf(&b, "$env:PATH = %s + $env:PATH\n", quotePS(strings.Join(e.PathDirs, sep)+sep))
		}
		return b.String(), nil
	case "cmd", "cmd.exe":
		var b strings.Builder
		for _, kv := range e.Vars {
			fmt.Fprintf(&b, "set \"%s=%s\"\n", kv[0], kv[1])
		}
		if len(e.PathDirs) > 0 {
			fmt.Fprintf(&b, "set \"PATH=%s%s%%PATH%%\"\n", strings.Join(e.PathDirs, sep), sep)
		}
		return b.String(), nil
	default: // bash / sh / zsh
		var b strings.Builder
		for _, kv := range e.Vars {
			fmt.Fprintf(&b, "export %s=%s\n", kv[0], quotePOSIX(kv[1]))
		}
		if len(e.PathDirs) > 0 {
			// $PATH must stay unquoted so the shell expands it.
			fmt.Fprintf(&b, "export PATH=%s$PATH\n", quotePOSIX(strings.Join(e.PathDirs, sep)+sep))
		}
		return b.String(), nil
	}
}

// pathSeparator returns the PATH separator of the *target shell*, which is not
// always the separator of the host platform: on Windows the POSIX shells
// (Git Bash, MSYS, WSL) still want ':' (I-08).
func pathSeparator(shell string) string {
	switch strings.ToLower(strings.TrimSpace(shell)) {
	case "cmd", "cmd.exe":
		return ";"
	case "pwsh", "powershell":
		return platform.PathSep()
	default: // bash / sh / zsh
		return ":"
	}
}

// quotePS renders s as a single-quoted PowerShell literal. Backslashes are left
// alone (PowerShell escapes with a backtick, never with a backslash), so Go's
// %q verb -- which doubles every backslash and would also turn a quote into a
// backslash-escape PowerShell does not understand -- must not be used (I-07).
func quotePS(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}

// quotePOSIX renders s as a double-quoted POSIX shell literal. Go's %q escapes
// backslashes and quotes, but NOT '$' or '`', which a double-quoted shell string
// would still expand (I-07).
func quotePOSIX(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `"`, `\"`, "$", `\$`, "`", "\\`")
	return `"` + r.Replace(s) + `"`
}

// Shell spawns an interactive child shell with the entry applied, optionally
// setting the window title so an activated terminal is recognisable (the
// conda-style "you are inside this project" cue, without any new concept).
func Shell(shell string, e Entry, title string) error {
	bin, args := shellCommand(shell, title)
	env := e.Environ()
	if title != "" {
		env = append(env, "PLOYENV_ACTIVE="+title)
	}
	cmd := exec.Command(bin, args...)
	cmd.Env = env
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func shellCommand(shell, title string) (string, []string) {
	switch strings.ToLower(strings.TrimSpace(shell)) {
	case "", "default":
		if platform.IsWindows() {
			bin := os.Getenv("COMSPEC")
			if bin == "" {
				bin = "cmd.exe"
			}
			return cmdWithTitle(bin, title)
		}
		if s := os.Getenv("SHELL"); s != "" {
			return s, nil
		}
		return "sh", nil
	case "cmd", "cmd.exe":
		return cmdWithTitle("cmd.exe", title)
	case "pwsh":
		return pwshWithTitle("pwsh", title)
	case "powershell":
		return pwshWithTitle("powershell", title)
	case "bash":
		return "bash", nil
	case "sh":
		return "sh", nil
	}
	return shell, nil
}

// cmdWithTitle runs cmd with `/k title ...` so the window shows the activation.
func cmdWithTitle(bin, title string) (string, []string) {
	if title == "" {
		return bin, nil
	}
	return bin, []string{"/k", "title " + cmdSanitize(title)}
}

// pwshWithTitle starts PowerShell without exiting and sets the window title.
func pwshWithTitle(bin, title string) (string, []string) {
	if title == "" {
		return bin, nil
	}
	script := fmt.Sprintf("$Host.UI.RawUI.WindowTitle = '%s'", strings.ReplaceAll(title, "'", "''"))
	return bin, []string{"-NoExit", "-Command", script}
}

// cmdSanitize neutralises characters cmd.exe would treat as operators inside
// the `title` argument.
func cmdSanitize(s string) string {
	replacer := strings.NewReplacer("&", " ", "|", " ", "<", " ", ">", " ", "^", " ", "\"", " ")
	return replacer.Replace(s)
}
