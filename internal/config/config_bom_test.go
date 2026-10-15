package config

import "testing"

// I-28: a BOM made json.Unmarshal fail, and Load treats a failed unmarshal as
// "there is no config" -- so a BOM in front of perfectly valid JSON silently
// threw the whole file away and every default came back unset. Unlike a
// genuinely broken file (TestLoadSurvivesABrokenConfigFile) a BOM must load.
func TestLoadAcceptsALeadingBOM(t *testing.T) {
	resetRoot(t)
	writeConfigFile(t, "\ufeff{\"defaults\":{\"node\":\"22.11.0\"},\"mirrors\":{\"node\":\"https://example.invalid\"}}")

	cfg := Load()
	if got := cfg.Defaults["node"]; got != "22.11.0" {
		t.Errorf("Defaults[node] = %q, want 22.11.0 (a BOM must not hide the file)", got)
	}
	if got := cfg.Mirrors["node"]; got != "https://example.invalid" {
		t.Errorf("Mirrors[node] = %q, want the value from the file", got)
	}
	if cfg.Root != RootDir() {
		t.Errorf("Root = %q, want %q", cfg.Root, RootDir())
	}
}
