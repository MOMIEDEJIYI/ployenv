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

// splitFlags separates positional arguments from "--key value" / "--key=value"
// flags. Flags listed in boolFlags are parsed as booleans.
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
			flags[name[:eq]] = name[eq+1:]
			continue
		}
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
