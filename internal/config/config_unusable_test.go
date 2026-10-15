package config

import (
	"io"
	"os"
	"strings"
	"testing"
)

// captureStderr runs f with os.Stderr redirected to a pipe and returns what was
// written to it.
func captureStderr(t *testing.T, f func()) string {
	t.Helper()
	old := os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stderr = w
	done := make(chan string, 1)
	go func() {
		b, _ := io.ReadAll(r)
		done <- string(b)
	}()

	f()

	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	os.Stderr = old
	return <-done
}

// resetConfigWarning clears the once-per-process guard, so a test can observe
// the first warning again.
func resetConfigWarning() {
	configWarnMu.Lock()
	configWarned = false
	configWarnMu.Unlock()
}

// I-30: a config.json that will not parse used to vanish without a word, and the
// next Save then wrote an empty configuration over a file the user could still
// have repaired.
func TestLoadWarnsOnceAboutAnUnusableConfig(t *testing.T) {
	resetRoot(t)
	resetConfigWarning()
	writeConfigFile(t, "{ this is not json")

	var cfg *Config
	out := captureStderr(t, func() {
		cfg = Load()
		Load() // Load runs on many paths; the second call must stay quiet
	})

	if cfg == nil || len(cfg.Defaults) != 0 || len(cfg.Mirrors) != 0 {
		t.Fatalf("a broken file must fall back to an empty config, got %+v", cfg)
	}
	if got := strings.Count(out, "warning:"); got != 1 {
		t.Errorf("want exactly one warning per process, got %d:\n%s", got, out)
	}
	for _, want := range []string{ConfigPath(), "not a usable ployenv config"} {
		if !strings.Contains(out, want) {
			t.Errorf("the warning must mention %q, got:\n%s", want, out)
		}
	}
}

func TestSaveRefusesToOverwriteAnUnusableConfig(t *testing.T) {
	resetRoot(t)
	resetConfigWarning()
	body := "{ this is not json\n"
	writeConfigFile(t, body)

	var cfg *Config
	captureStderr(t, func() { cfg = Load() })
	cfg.Defaults["node"] = "22.11.0"

	err := Save(cfg)
	if err == nil {
		t.Fatal("Save must refuse to overwrite a config.json it cannot parse")
	}
	for _, want := range []string{ConfigPath(), "refusing to overwrite"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q must mention %q", err, want)
		}
	}
	data, rerr := os.ReadFile(ConfigPath())
	if rerr != nil {
		t.Fatal(rerr)
	}
	if string(data) != body {
		t.Errorf("the file must be untouched:\nwant %q\ngot  %q", body, data)
	}
}

// I-28 strips a leading BOM before parsing, so a BOM-prefixed file is valid and
// must not be reported or refused (I-30's guard must not fire on it).
func TestABOMIsNotReportedAsBroken(t *testing.T) {
	resetRoot(t)
	resetConfigWarning()
	writeConfigFile(t, "\ufeff{\"defaults\":{\"node\":\"22.11.0\"}}")

	var cfg *Config
	out := captureStderr(t, func() { cfg = Load() })
	if out != "" {
		t.Errorf("a BOM-prefixed valid file must not warn, got:\n%s", out)
	}
	if got := cfg.Defaults["node"]; got != "22.11.0" {
		t.Fatalf("Defaults[node] = %q, want 22.11.0", got)
	}

	cfg.Defaults["go"] = "1.22.12"
	if err := Save(cfg); err != nil {
		t.Fatalf("saving over a BOM-prefixed valid file must be allowed: %v", err)
	}
	if got := Load().Defaults["go"]; got != "1.22.12" {
		t.Errorf("Defaults[go] = %q, want the value just saved", got)
	}
	if data, _ := os.ReadFile(ConfigPath()); len(data) >= 3 && data[0] == 0xEF {
		t.Error("Save rewrites the file, so the BOM should not survive it")
	}
}

func TestAFirstRunIsSilentAndWritable(t *testing.T) {
	resetRoot(t)
	resetConfigWarning()

	// resetRoot clears the data root, so there is no config.json yet.
	out := captureStderr(t, func() { Load() })
	if out != "" {
		t.Errorf("a missing config.json is the first-run path, not a problem:\n%s", out)
	}

	cfg := Load()
	cfg.Defaults["node"] = "22.11.0"
	if err := Save(cfg); err != nil {
		t.Fatalf("the first Save must be allowed: %v", err)
	}
}
