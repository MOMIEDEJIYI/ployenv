package runtime

import "strings"

var all = []*Spec{nodeSpec, javaSpec, goSpec, pythonSpec, mavenSpec, adbSpec}

var byName = map[string]*Spec{}

func init() {
	for _, s := range all {
		byName[s.Name] = s
		for _, a := range s.Aliases {
			byName[a] = s
		}
	}
}

// Lookup finds a runtime by name or alias (case-insensitive).
func Lookup(name string) *Spec {
	return byName[strings.ToLower(strings.TrimSpace(name))]
}

// All returns every built-in runtime.
func All() []*Spec { return all }

// Names returns the canonical runtime names.
func Names() []string {
	out := make([]string, 0, len(all))
	for _, s := range all {
		out = append(out, s.Name)
	}
	return out
}

// NamesText returns a comma-separated list of runtime names for messages.
func NamesText() string { return strings.Join(Names(), ", ") }
