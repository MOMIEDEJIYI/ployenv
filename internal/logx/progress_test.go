package logx

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// captureStderr runs f with os.Stderr redirected and returns everything written.
// Progress writes straight to the real stderr, so this is the only way to see it.
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

// A progress bar for a command that never named itself must stay quiet: the
// download still happens, it just does not spam the log.
func TestProgressWithoutALabelIsSilent(t *testing.T) {
	out := captureStderr(t, func() {
		p := NewProgress("", 0)
		p.Add(1024)
		p.Done()
	})
	if out != "" {
		t.Errorf("a label-less progress printed %q", out)
	}
}

// When stderr is redirected (CI, a pipe) the bar is useless, so a percentage is
// emitted when the download crosses into a new 10% band - roughly ten lines for
// a whole run, not one per chunk.
func TestProgressNonTTYEmitsPercentages(t *testing.T) {
	out := captureStderr(t, func() {
		p := NewProgress("Downloading node", 1000)
		p.Add(10)  // 1%: the first update is always reported
		p.Add(10)  // 2%: still the same band (1/10 == 0)
		p.Add(50)  // 7%: still the same band
		p.Add(100) // 17%: a new band, reported
		p.Done()
	})

	if !strings.Contains(out, "Downloading node 1%") {
		t.Errorf("the first update must be reported: %q", out)
	}
	if !strings.Contains(out, "Downloading node 17%") {
		t.Errorf("a new band must be reported: %q", out)
	}
	if strings.Contains(out, "node 7%") {
		t.Errorf("a percentage inside the same band must stay quiet: %q", out)
	}
	if got := strings.Count(out, "%"); got != 2 {
		t.Errorf("want exactly two percentage lines, got %d in %q", got, out)
	}
	if !strings.Contains(out, "done") || !strings.Contains(out, "took") {
		t.Errorf("Done must summarise: %q", out)
	}
}

func TestProgressNonTTYWithUnknownTotalReportsBytes(t *testing.T) {
	// The first update of an unknown-size download is reported (there is no
	// percentage to throttle on) ...
	out := captureStderr(t, func() {
		p := NewProgress("Fetching", 0)
		p.Add(4096)
	})
	if !strings.Contains(out, "Fetching") || !strings.Contains(out, "4.0 KB") {
		t.Errorf("the first update must report the running total: %q", out)
	}

	// ... and then the display settles down to one line every few seconds.
	out = captureStderr(t, func() {
		p := NewProgress("Fetching", 0)
		p.last = time.Now()
		p.Add(4096)
	})
	if strings.Contains(out, "Fetching") {
		t.Errorf("an update inside the throttle window must be skipped: %q", out)
	}

	out = captureStderr(t, func() {
		p := NewProgress("Fetching", 0)
		p.last = time.Now().Add(-5 * time.Second)
		p.Add(4096)
	})
	if !strings.Contains(out, "Fetching") {
		t.Errorf("a later update must be reported: %q", out)
	}
}

// On a TTY the bar is repainted in place, at most every 400 ms.
func TestProgressTTYPaintsAndThrottles(t *testing.T) {
	p := NewProgress("Downloading go", 2048)
	p.tty = true

	out := captureStderr(t, func() {
		p.last = time.Now()
		p.Add(1024) // throttled
	})
	if out != "" {
		t.Errorf("a repaint inside the throttle window must be skipped: %q", out)
	}

	out = captureStderr(t, func() {
		p.last = time.Now().Add(-time.Second)
		p.Add(1024)
	})
	if !strings.Contains(out, "\r") || !strings.Contains(out, "100.0%") {
		t.Errorf("the bar must be painted in place with a percentage: %q", out)
	}
	if !strings.Contains(out, "2.0 KB") {
		t.Errorf("the bar must show the total: %q", out)
	}

	out = captureStderr(t, func() { p.Done() })
	if !strings.Contains(out, "\r") || !strings.Contains(out, "done") {
		t.Errorf("the TTY summary must clear the bar: %q", out)
	}
}

func TestProgressTTYWithUnknownTotalPaintsBytes(t *testing.T) {
	p := NewProgress("Fetching", 0)
	p.tty = true
	p.last = time.Now().Add(-time.Second)

	out := captureStderr(t, func() { p.Add(2048) })
	if !strings.Contains(out, "2.0 KB") || !strings.Contains(out, "/s") {
		t.Errorf("without a total the bar still shows bytes and speed: %q", out)
	}
}

func TestIsTerminalOnARegularFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "out")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	if IsTerminal(f) {
		t.Error("a regular file is not a terminal")
	}
	// A file that no longer exists cannot be stat'ed, which must not panic.
	if IsTerminal(f) && false {
		t.Error("unreachable")
	}
	closed, err := os.Create(filepath.Join(t.TempDir(), "gone"))
	if err != nil {
		t.Fatal(err)
	}
	name := closed.Name()
	closed.Close()
	os.Remove(name)
	if IsTerminal(closed) {
		t.Error("a closed file is not a terminal")
	}
}

func TestMax(t *testing.T) {
	if max(2, 1) != 2 {
		t.Error("max(2,1) = 2")
	}
	if max(1, 2) != 2 {
		t.Error("max(1,2) = 2")
	}
	if max(1, 1) != 1 {
		t.Error("max(1,1) = 1")
	}
}
