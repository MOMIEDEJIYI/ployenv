package logx

import (
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/MOMIEDEJIYI/ployenv/internal/fsutil"
)

// Progress renders a download progress bar on a TTY and percentage text lines
// when stderr is redirected. It never writes to stdout.
type Progress struct {
	mu      sync.Mutex
	label   string
	total   int64
	written int64
	start   time.Time
	last    time.Time
	lastPct int
	tty     bool
}

// NewProgress starts a progress display for label with an optional total size.
func NewProgress(label string, total int64) *Progress {
	return &Progress{
		label:   label,
		total:   total,
		start:   time.Now(),
		lastPct: -1,
		tty:     IsTerminal(os.Stderr),
	}
}

// IsTerminal reports whether stderr is a TTY (re-exported for callers).
func IsTerminal(f *os.File) bool {
	fi, err := f.Stat()
	if err != nil {
		return false
	}
	return fi.Mode()&os.ModeCharDevice != 0
}

// Add records n newly downloaded bytes and repaints as needed.
func (p *Progress) Add(n int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.written += int64(n)
	if p.label == "" {
		return
	}
	if p.tty {
		if time.Since(p.last) < 400*time.Millisecond {
			return
		}
		p.last = time.Now()
		p.paintBar()
		return
	}
	// Non-TTY: emit a plain percentage line roughly every 10%.
	if p.total > 0 {
		pct := int(p.written * 100 / p.total)
		if pct/10 > p.lastPct/10 || p.lastPct < 0 {
			p.lastPct = pct
			fmt.Fprintf(os.Stderr, "  %s %d%%\n", p.label, pct)
		}
	} else if time.Since(p.last) > 3*time.Second {
		p.last = time.Now()
		fmt.Fprintf(os.Stderr, "  %s %s\n", p.label, fsutil.HumanBytes(p.written))
	}
}

// Done finishes the display with a summary line.
func (p *Progress) Done() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.label == "" {
		return
	}
	elapsed := time.Since(p.start).Round(time.Second)
	if p.tty {
		line := fmt.Sprintf("-> %s done %s (took %s)", p.label, fsutil.HumanBytes(p.written), elapsed)
		fmt.Fprintf(os.Stderr, "\r%s%s\n", line, strings.Repeat(" ", 12))
	} else {
		fmt.Fprintf(os.Stderr, "  %s done %s (took %s)\n", p.label, fsutil.HumanBytes(p.written), elapsed)
	}
}

func (p *Progress) paintBar() {
	speed := float64(p.written) / max(time.Since(p.start).Seconds(), 0.001)
	if p.total > 0 {
		fmt.Fprintf(os.Stderr, "\r  %s %5.1f%%  %s / %s  %s/s   ",
			p.label, float64(p.written)*100/float64(p.total),
			fsutil.HumanBytes(p.written), fsutil.HumanBytes(p.total), fsutil.HumanBytes(int64(speed)))
	} else {
		fmt.Fprintf(os.Stderr, "\r  %s %s  %s/s   ", p.label, fsutil.HumanBytes(p.written), fsutil.HumanBytes(int64(speed)))
	}
}

func max(a, b float64) float64 {
	if a > b {
		return a
	}
	return b
}
