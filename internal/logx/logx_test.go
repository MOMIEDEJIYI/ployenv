package logx

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// resetLogger puts the shared logger back to a known state. The package keeps
// one process-wide logger on purpose, so tests have to reset it explicitly
// instead of relying on the order they run in.
func resetLogger(t *testing.T) *bytes.Buffer {
	t.Helper()
	buf := &bytes.Buffer{}
	std.mu.Lock()
	std.level = LevelNormal
	std.out = buf
	std.color = false
	std.logDir = ""
	std.fileMode = FileOnError
	std.keepFiles = 20
	std.errSeen = false
	std.capture.Reset()
	std.mu.Unlock()
	t.Cleanup(func() {
		std.mu.Lock()
		std.out = os.Stderr
		std.color = false
		std.capture.Reset()
		std.errSeen = false
		std.mu.Unlock()
	})
	return buf
}

func TestLevelsGateTheConsole(t *testing.T) {
	cases := []struct {
		level       Level
		wantInfo    bool
		wantVerbose bool
		wantDebug   bool
	}{
		{LevelQuiet, false, false, false},
		{LevelNormal, true, false, false},
		{LevelVerbose, true, true, false},
		{LevelDebug, true, true, true},
	}
	for _, tc := range cases {
		buf := resetLogger(t)
		Configure(tc.level, false, "", FileOnError, 20)

		Infof("info")
		Verbosef("verbose")
		Debugf("debug")

		out := buf.String()
		if got := strings.Contains(out, "info"); got != tc.wantInfo {
			t.Errorf("level %d: info printed = %v, want %v (%q)", tc.level, got, tc.wantInfo, out)
		}
		if got := strings.Contains(out, "verbose"); got != tc.wantVerbose {
			t.Errorf("level %d: verbose printed = %v, want %v (%q)", tc.level, got, tc.wantVerbose, out)
		}
		if got := strings.Contains(out, "debug"); got != tc.wantDebug {
			t.Errorf("level %d: debug printed = %v, want %v (%q)", tc.level, got, tc.wantDebug, out)
		}
	}
}

// An error is the one thing -q must not hide: a silent failure is worse than a
// noisy one.
func TestErrorsAreAlwaysPrintedAndRemembered(t *testing.T) {
	buf := resetLogger(t)
	Configure(LevelQuiet, false, "", FileOnError, 20)

	if Noted() {
		t.Fatal("nothing has been logged yet")
	}
	Errorf("boom %d", 1)

	out := buf.String()
	if !strings.Contains(out, "error: boom 1") {
		t.Errorf("the error must reach the console even when quiet: %q", out)
	}
	if !Noted() {
		t.Error("Noted must report that an error was logged")
	}
}

func TestMessagePrefixes(t *testing.T) {
	buf := resetLogger(t)
	Configure(LevelDebug, false, "", FileOnError, 20)

	Stepf("step")
	Warnf("careful")
	Debugf("detail")
	Errorf("bad")
	Infof("plain")

	out := buf.String()
	for _, want := range []string{"-> step", "warning: careful", "debug: detail", "error: bad"} {
		if !strings.Contains(out, want) {
			t.Errorf("output is missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "warning: warning:") {
		t.Errorf("the prefix must not be doubled:\n%s", out)
	}
	if !strings.Contains(out, "\nplain\n") {
		t.Errorf("a plain message carries no prefix:\n%s", out)
	}
}

func TestColorWrapsTheMessage(t *testing.T) {
	buf := resetLogger(t)
	Configure(LevelNormal, true, "", FileOnError, 20)

	Stepf("green")
	if !strings.Contains(buf.String(), colorGreen+"-> green"+colorReset) {
		t.Errorf("a coloured step was not wrapped: %q", buf.String())
	}

	buf.Reset()
	Warnf("yellow")
	if !strings.Contains(buf.String(), colorYellow) {
		t.Errorf("a warning was not coloured: %q", buf.String())
	}

	// Without colour the message is untouched, which is what a redirected
	// console or NO_COLOR must get.
	buf.Reset()
	Configure(LevelNormal, false, "", FileOnError, 20)
	Stepf("plain")
	if strings.Contains(buf.String(), "\033[") {
		t.Errorf("no escape codes may be emitted without colour: %q", buf.String())
	}
}

// Capture exists for lines that belong in the diagnostics file but would be
// noise on the console (every HTTP attempt, for instance).
func TestCaptureIsRecordedButNotPrinted(t *testing.T) {
	buf := resetLogger(t)
	Configure(LevelDebug, false, "", FileAlways, 20)

	Capture("GET %s", "https://example.invalid/x")
	if strings.Contains(buf.String(), "example.invalid") {
		t.Errorf("Capture must not print: %q", buf.String())
	}
	if !strings.Contains(std.capture.String(), "GET https://example.invalid/x") {
		t.Errorf("Capture must record the line: %q", std.capture.String())
	}
	// Every recorded line carries a timestamp so a log file can be read back.
	if !strings.Contains(std.capture.String(), "20") || !strings.Contains(std.capture.String(), ":") {
		t.Errorf("the captured line has no timestamp: %q", std.capture.String())
	}
}

func TestFlushPolicies(t *testing.T) {
	t.Run("off writes nothing", func(t *testing.T) {
		dir := t.TempDir()
		resetLogger(t)
		Configure(LevelNormal, false, dir, FileOff, 20)
		Errorf("something went wrong")
		Flush("/root")
		if entries := logFiles(t, dir); len(entries) != 0 {
			t.Errorf("FileOff wrote %v", entries)
		}
	})

	t.Run("on-error without an error writes nothing", func(t *testing.T) {
		dir := t.TempDir()
		resetLogger(t)
		Configure(LevelNormal, false, dir, FileOnError, 20)
		Infof("all good")
		Flush("/root")
		if entries := logFiles(t, dir); len(entries) != 0 {
			t.Errorf("a successful run must not leave a diagnostics file: %v", entries)
		}
	})

	t.Run("on-error writes once an error was seen", func(t *testing.T) {
		dir := t.TempDir()
		resetLogger(t)
		Configure(LevelNormal, false, dir, FileOnError, 20)
		Infof("before")
		Errorf("failure")
		Flush("/root")

		entries := logFiles(t, dir)
		if len(entries) != 1 {
			t.Fatalf("want exactly one diagnostics file, got %v", entries)
		}
		body, err := os.ReadFile(filepath.Join(dir, entries[0]))
		if err != nil {
			t.Fatal(err)
		}
		text := string(body)
		if !strings.Contains(text, "ployenv diagnostics |") || !strings.Contains(text, "root=/root") {
			t.Errorf("the file needs a header naming the root: %q", text)
		}
		if !strings.Contains(text, "before") || !strings.Contains(text, "error: failure") {
			t.Errorf("every line must be in the file, including the successful ones: %q", text)
		}
		if !strings.Contains(text, "pid=") {
			t.Errorf("the header must name the process: %q", text)
		}
	})

	t.Run("always writes without an error", func(t *testing.T) {
		dir := t.TempDir()
		resetLogger(t)
		Configure(LevelNormal, false, dir, FileAlways, 20)
		Infof("just a normal line")
		Flush("/root")
		if entries := logFiles(t, dir); len(entries) == 0 {
			t.Error("FileAlways must write even when nothing failed")
		}
	})

	t.Run("no directory configured writes nothing", func(t *testing.T) {
		dir := t.TempDir()
		resetLogger(t)
		Configure(LevelNormal, false, "", FileAlways, 20)
		Infof("line")
		Flush("/root")
		if entries := logFiles(t, dir); len(entries) != 0 {
			t.Errorf("without a log directory nothing may be written: %v", entries)
		}
	})

	t.Run("nothing captured writes nothing", func(t *testing.T) {
		dir := t.TempDir()
		resetLogger(t)
		Configure(LevelNormal, false, dir, FileAlways, 20)
		Flush("/root")
		if entries := logFiles(t, dir); len(entries) != 0 {
			t.Errorf("an empty capture must not create a file: %v", entries)
		}
	})

	t.Run("an unusable directory is reported, not fatal", func(t *testing.T) {
		dir := t.TempDir()
		resetLogger(t)
		file := filepath.Join(dir, "blocker")
		if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		Configure(LevelNormal, false, filepath.Join(file, "logs"), FileAlways, 20)
		Infof("line")
		Flush("/root") // must not panic: diagnostics never fail a command
	})
}

func logFiles(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasPrefix(e.Name(), "ployenv-") && strings.HasSuffix(e.Name(), ".log") {
			out = append(out, e.Name())
		}
	}
	return out
}

func TestPruneLogsKeepsTheNewest(t *testing.T) {
	dir := t.TempDir()
	names := []string{
		"ployenv-20260101-000000-1.log",
		"ployenv-20260102-000000-1.log",
		"ployenv-20260103-000000-1.log",
		"ployenv-20260104-000000-1.log",
	}
	for _, n := range names {
		if err := os.WriteFile(filepath.Join(dir, n), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// A file that does not follow the naming scheme is none of our business.
	other := filepath.Join(dir, "notes.txt")
	if err := os.WriteFile(other, []byte("keep me"), 0o644); err != nil {
		t.Fatal(err)
	}
	// A directory must not be counted as a log file either.
	if err := os.MkdirAll(filepath.Join(dir, "ployenv-old.log"), 0o755); err != nil {
		t.Fatal(err)
	}

	pruneLogs(dir, 2)
	left := logFiles(t, dir)
	if len(left) != 2 {
		t.Fatalf("pruneLogs left %v, want the two newest", left)
	}
	for _, want := range []string{names[2], names[3]} {
		if _, err := os.Stat(filepath.Join(dir, want)); err != nil {
			t.Errorf("%s must be kept: %v", want, err)
		}
	}
	if _, err := os.Stat(other); err != nil {
		t.Errorf("unrelated files must survive: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "ployenv-old.log")); err != nil {
		t.Errorf("directories must survive: %v", err)
	}

	// keep<=0 means "do not prune", and a missing directory is not an error.
	pruneLogs(dir, 0)
	if len(logFiles(t, dir)) != 2 {
		t.Error("keep=0 must not remove anything")
	}
	pruneLogs(filepath.Join(dir, "missing"), 2)
}

func TestSetOutputRedirectsEverything(t *testing.T) {
	resetLogger(t)
	buf := &bytes.Buffer{}
	SetOutput(buf)
	Configure(LevelNormal, false, "", FileOnError, 20)

	Infof("redirected")
	if !strings.Contains(buf.String(), "redirected") {
		t.Errorf("SetOutput did not take effect: %q", buf.String())
	}
}
