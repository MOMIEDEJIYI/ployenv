// Package cli implements ployenv's command line: argument parsing, command
// dispatch and output formatting. Human output goes to stdout, progress and
// diagnostics to stderr, so pipes and --json stay clean.
package cli

import (
	"fmt"
	"strings"
)

// boolFlags are flags that never take a value.
var boolFlags = map[string]bool{
	"force": true, "global": true, "project": true, "dry-run": true,
	"json": true, "verbose": true, "quiet": true, "debug": true,
	"log": true, "no-color": true, "help": true, "all": true, "yes": true,
	"clean": true,
}

// flagAliases maps the short spelling of a switch onto its long name, so `-p`
// and `--project` are the same thing. Normalisation happens inside splitFlags,
// before any command reads the flags.
var flagAliases = map[string]string{"p": "project", "g": "global"}

// commandFlags lists every flag a command accepts; an entry with no flags means
// the command takes none. rejectUnknownFlags runs before dispatch, because a
// silently ignored flag is how `ployenv config --set-default node none
// --project` used to change the GLOBAL default while the caller believed it was
// editing a project file (I-31).
var commandFlags = map[string][]string{
	"list": nil, "ls": nil,
	"available": {"limit", "all"}, "list-remote": {"limit", "all"}, "ls-remote": {"limit", "all"},
	"install": {"from", "force"}, "add": {"from", "force"},
	"adopt": {"root", "force"}, "import": {"root", "force"},
	"uninstall": {"all"}, "remove": {"all"}, "rm": {"all"},
	"use":     {"project", "global"},
	"current": nil, "which": nil,
	"support": nil, "supported": nil, "runtimes": nil, "langs": nil, "envs": nil,
	"cache":  {"clean"},
	"env":    {"shell"},
	"shell":  {"shell"},
	"config": {"set-mirror", "set-default", "set-log-mode", "set-cache-keep"},
	"doctor": nil,
}

// flagHints explains what the caller probably meant, so a refusal also points at
// the right command instead of only saying no.
var flagHints = map[string]string{
	"config/project": "project-level pins are written by `ployenv use <lang>@<version> -p`",
	"config/global":  "`ployenv config --set-default` already changes the global default",
}

// longFlag resolves a short alias to the long flag name.
func longFlag(name string) string {
	if long, ok := flagAliases[name]; ok {
		return long
	}
	return name
}

// wantsHelp reports whether args ask for usage, wherever they appear.
func wantsHelp(args []string) bool {
	for _, a := range args {
		switch a {
		case "-h", "-help", "--help", "-?", "help":
			return true
		}
	}
	return false
}

// rejectUnknownFlags fails when args carry a flag the command does not accept.
// `--help` is accepted everywhere because Run prints the usage for it.
func rejectUnknownFlags(cmd string, args []string) error {
	allowed, known := commandFlags[cmd]
	if !known {
		return nil // dispatch reports the unknown command itself
	}
	accepted := map[string]bool{"help": true, "h": true}
	for _, a := range allowed {
		accepted[a] = true
	}
	for _, a := range args {
		if !strings.HasPrefix(a, "-") || a == "-" {
			continue
		}
		name := strings.TrimLeft(a, "-")
		if eq := strings.Index(name, "="); eq >= 0 {
			name = name[:eq]
		}
		name = longFlag(name)
		if accepted[name] {
			continue
		}
		msg := fmt.Sprintf("unknown flag %s: `ployenv %s` takes no flags", a, cmd)
		if len(allowed) > 0 {
			msg = fmt.Sprintf("unknown flag %s: `ployenv %s` accepts --%s", a, cmd, strings.Join(allowed, ", --"))
		}
		if hint, ok := flagHints[cmd+"/"+name]; ok {
			msg += "; " + hint
		}
		return fmt.Errorf("%s", msg)
	}
	return nil
}

// splitFlags separates positional arguments from "--key value" / "--key=value"
// flags. Flags listed in boolFlags are parsed as booleans; short aliases are
// normalised to their long names.
func splitFlags(args []string) (pos []string, flags map[string]string) {
	flags = map[string]string{}
	for i := 0; i < len(args); i++ {
		a := args[i]
		if !strings.HasPrefix(a, "-") || a == "-" {
			pos = append(pos, a)
			continue
		}
		name := strings.TrimLeft(a, "-")
		if eq := strings.Index(name, "="); eq >= 0 {
			flags[longFlag(name[:eq])] = name[eq+1:]
			continue
		}
		name = longFlag(name)
		if boolFlags[name] {
			flags[name] = "true"
			continue
		}
		if i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") {
			flags[name] = args[i+1]
			i++
			continue
		}
		// A value-taking flag with no value is recorded as present but empty,
		// so callers can tell "missing value" from the literal value "true".
		flags[name] = ""
	}
	return pos, flags
}

// value returns the value of a value-taking flag. Boolean switches never carry
// one, so asking for their value yields the empty string.
//
// A flag that is not a switch keeps its value verbatim: --set-cache-keep true
// means the value "true", not a bare switch.
func value(flags map[string]string, name string) string {
	if boolFlags[name] {
		return ""
	}
	return flags[name]
}

func has(flags map[string]string, name string) bool {
	_, ok := flags[name]
	return ok
}

// unimplementedFlags are flags the parser knows but nothing implements yet.
// Ignoring one silently is worse than failing: install ignores --dry-run, so
// "install node@22 --dry-run" really downloads and installs (BUG-020).
var unimplementedFlags = map[string]string{
	"dry-run": "not implemented; it would download and install for real. Run without it, or pass --from <dir> to register an existing copy.",
}

// rejectUnimplementedFlags fails loudly when args carry a flag whose behaviour
// does not exist yet. Run calls it before dispatch, so every command is covered
// and the check stays next to the flag table it guards.
func rejectUnimplementedFlags(args []string) error {
	for _, a := range args {
		if !strings.HasPrefix(a, "--") {
			continue
		}
		name := strings.TrimPrefix(a, "--")
		if eq := strings.Index(name, "="); eq >= 0 {
			name = name[:eq]
		}
		if msg, ok := unimplementedFlags[name]; ok {
			return fmt.Errorf("--%s: %s", name, msg)
		}
	}
	return nil
}

// parseBoolFlag validates an on/off argument. A typo must fail loudly instead
// of silently meaning "false".
func parseBoolFlag(flag, v string) (bool, error) {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "true", "1", "yes", "on":
		return true, nil
	case "false", "0", "no", "off":
		return false, nil
	}
	return false, fmt.Errorf("%s must be true or false (got %q)", flag, v)
}

// splitAt splits "node@22" into ("node", "22"). A bare "22" is rejected later.
func splitAt(s string) (name, ver string) {
	if i := strings.Index(s, "@"); i >= 0 {
		return s[:i], s[i+1:]
	}
	return s, ""
}
