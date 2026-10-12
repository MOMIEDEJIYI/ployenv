package cli

import (
	"strings"
	"testing"
)

// BUG-002: value() used to collapse the literal value "true" to "", so
// `config --set-cache-keep true` was silently ignored. These tests pin the
// switch/value distinction down.
func TestSplitFlagsSeparatesPositionalsFromFlags(t *testing.T) {
	tests := []struct {
		name      string
		args      []string
		wantPos   []string
		wantFlags map[string]string
	}{
		{
			name:      "value-taking flag keeps its value",
			args:      []string{"config", "--set-cache-keep", "true"},
			wantPos:   []string{"config"},
			wantFlags: map[string]string{"set-cache-keep": "true"},
		},
		{
			name:      "switch does not consume the next token",
			args:      []string{"install", "node@20", "--force"},
			wantPos:   []string{"install", "node@20"},
			wantFlags: map[string]string{"force": "true"},
		},
		{
			name:      "key=value form",
			args:      []string{"--set-log-mode=always"},
			wantFlags: map[string]string{"set-log-mode": "always"},
		},
		{
			name:      "value-taking flag without a value is present but empty",
			args:      []string{"--set-mirror"},
			wantFlags: map[string]string{"set-mirror": ""},
		},
		{
			name:      "switch before a positional",
			args:      []string{"uninstall", "--all", "node"},
			wantPos:   []string{"uninstall", "node"},
			wantFlags: map[string]string{"all": "true"},
		},
		{
			name:    "lone dash stays positional",
			args:    []string{"-"},
			wantPos: []string{"-"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pos, flags := splitFlags(tt.args)
			if strings.Join(pos, ",") != strings.Join(tt.wantPos, ",") {
				t.Fatalf("positionals = %v, want %v", pos, tt.wantPos)
			}
			if len(flags) != len(tt.wantFlags) {
				t.Fatalf("flags = %v, want %v", flags, tt.wantFlags)
			}
			for name, want := range tt.wantFlags {
				if got := flags[name]; got != want {
					t.Errorf("flags[%q] = %q, want %q", name, got, want)
				}
			}
		})
	}
}

func TestValueKeepsLiteralTrueForValuedFlags(t *testing.T) {
	_, flags := splitFlags([]string{"--set-cache-keep", "true"})
	if got := value(flags, "set-cache-keep"); got != "true" {
		t.Fatalf("value(set-cache-keep) = %q, want %q", got, "true")
	}
}

func TestValueIsEmptyForSwitches(t *testing.T) {
	_, flags := splitFlags([]string{"--force"})
	if got := value(flags, "force"); got != "" {
		t.Fatalf("value(force) = %q, want empty", got)
	}
}

func TestValuelessFlagIsPresentButEmpty(t *testing.T) {
	_, flags := splitFlags([]string{"--set-mirror"})
	if got := value(flags, "set-mirror"); got != "" {
		t.Fatalf("value(set-mirror) = %q, want empty", got)
	}
	if !has(flags, "set-mirror") {
		t.Fatal("a valueless flag must still count as present")
	}
}

// BUG-020: --dry-run was parsed and then ignored, so "install node@22
// --dry-run" downloaded and installed for real. A flag that does nothing must
// abort instead of pretending the run was dry.
func TestRejectUnimplementedFlags(t *testing.T) {
	for _, args := range [][]string{
		{"install", "node@22", "--dry-run"},
		{"--dry-run"},
		{"install", "node@22", "--dry-run=true"},
	} {
		if err := rejectUnimplementedFlags(args); err == nil {
			t.Errorf("rejectUnimplementedFlags(%q) = nil, want an error", args)
		}
	}
	for _, args := range [][]string{
		nil,
		{},
		{"install", "node@22"},
		{"install", "node@22", "--from", "D:\\node"},
		{"uninstall", "node@22", "--all"},
		{"list", "-dry-run"},
	} {
		if err := rejectUnimplementedFlags(args); err != nil {
			t.Errorf("rejectUnimplementedFlags(%q) = %v, want nil", args, err)
		}
	}
}

func TestParseBoolFlag(t *testing.T) {
	for _, s := range []string{"true", "TRUE", " true ", "1", "yes", "on", "false", "0", "no", "off"} {
		if _, err := parseBoolFlag("--flag", s); err != nil {
			t.Errorf("parseBoolFlag(%q) returned %v, want nil", s, err)
		}
	}
	for _, s := range []string{"", "maybe", "2", "tru", "yesno"} {
		if _, err := parseBoolFlag("--flag", s); err == nil {
			t.Errorf("parseBoolFlag(%q) returned nil, want an error", s)
		}
	}

	got, err := parseBoolFlag("--flag", "true")
	if err != nil || !got {
		t.Fatalf("parseBoolFlag(true) = %v, %v; want true, nil", got, err)
	}
	got, err = parseBoolFlag("--flag", "off")
	if err != nil || got {
		t.Fatalf("parseBoolFlag(off) = %v, %v; want false, nil", got, err)
	}
}
