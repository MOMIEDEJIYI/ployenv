// Package logx provides leveled console logging, an in-memory diagnostics
// capture that can be flushed to a log file on error, and a download progress
// display. All output goes to stderr so stdout stays clean for --json.
package logx

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Level controls how much is printed to the console.
type Level int

const (
	LevelQuiet Level = iota
	LevelNormal
	LevelVerbose
	LevelDebug
)

// FileMode controls when diagnostics are written to a log file.
type FileMode string

const (
	FileOff     FileMode = "off"
	FileOnError FileMode = "on-error"
	FileAlways  FileMode = "always"
)

const (
	colorReset  = "\033[0m"
	colorGray   = "\033[90m"
	colorRed    = "\033[31m"
	colorYellow = "\033[33m"
	colorGreen  = "\033[32m"
)

type logger struct {
	mu        sync.Mutex
	level     Level
	out       io.Writer
	color     bool
	capture   strings.Builder
	logDir    string
	fileMode  FileMode
	keepFiles int
	errSeen   bool
}

var std = &logger{level: LevelNormal, out: os.Stderr, fileMode: FileOnError, keepFiles: 20}

// Configure sets the console level, colour, and file-logging policy.
func Configure(level Level, color bool, logDir string, mode FileMode, keep int) {
	std.mu.Lock()
	defer std.mu.Unlock()
	std.level = level
	std.color = color
	std.logDir = logDir
	if mode != "" {
		std.fileMode = mode
	}
	if keep > 0 {
		std.keepFiles = keep
	}
}

// SetOutput redirects console output (used by tests).
func SetOutput(w io.Writer) {
	std.mu.Lock()
	defer std.mu.Unlock()
	std.out = w
}

func (l *logger) paint(color, s string) string {
	if l.color {
		return color + s + colorReset
	}
	return s
}

// line records every message for the on-error diagnostics file, then prints it
// when the console level allows.
func (l *logger) line(level Level, color, msg string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	fmt.Fprintf(&l.capture, "%s %s\n", time.Now().Format("2006-01-02 15:04:05.000"), msg)
	if level <= l.level && l.out != nil {
		fmt.Fprintln(l.out, l.paint(color, msg))
	}
}

// Infof prints at the normal level.
func Infof(format string, a ...any) { std.line(LevelNormal, "", fmt.Sprintf(format, a...)) }

// Stepf prints a normal-level step prefixed with "->".
func Stepf(format string, a ...any) {
	std.line(LevelNormal, colorGreen, "-> "+fmt.Sprintf(format, a...))
}

// Warnf prints at the normal level in yellow.
func Warnf(format string, a ...any) {
	std.line(LevelNormal, colorYellow, "warning: "+fmt.Sprintf(format, a...))
}

// Verbosef prints only with -v or --debug.
func Verbosef(format string, a ...any) {
	std.line(LevelVerbose, colorGray, fmt.Sprintf(format, a...))
}

// Debugf prints only with --debug.
func Debugf(format string, a ...any) {
	std.line(LevelDebug, colorGray, "debug: "+fmt.Sprintf(format, a...))
}

// Errorf records and prints an error, and remembers that one occurred.
func Errorf(format string, a ...any) {
	std.mu.Lock()
	std.errSeen = true
	std.mu.Unlock()
	std.line(LevelQuiet-1, colorRed, "error: "+fmt.Sprintf(format, a...))
}

// Capture appends an arbitrary diagnostics line (e.g. an HTTP attempt) that
// should appear in the log file even when not printed to the console.
func Capture(format string, a ...any) {
	std.mu.Lock()
	defer std.mu.Unlock()
	fmt.Fprintf(&std.capture, "%s %s\n", time.Now().Format("2006-01-02 15:04:05.000"), fmt.Sprintf(format, a...))
}

// Noted reports whether an error has been logged.
func Noted() bool {
	std.mu.Lock()
	defer std.mu.Unlock()
	return std.errSeen
}

// Flush writes captured diagnostics to a log file when the policy requires it
// (always, or on error). It never fails the caller.
func Flush(rootName string) {
	std.mu.Lock()
	defer std.mu.Unlock()
	if std.logDir == "" || std.fileMode == FileOff {
		return
	}
	if std.fileMode == FileOnError && !std.errSeen {
		return
	}
	if std.capture.Len() == 0 {
		return
	}
	if err := os.MkdirAll(std.logDir, 0o755); err != nil {
		return
	}
	name := fmt.Sprintf("ployenv-%s-%d.log", time.Now().Format("20060102-150405"), os.Getpid())
	path := filepath.Join(std.logDir, name)
	header := fmt.Sprintf("ployenv diagnostics | %s | pid=%d | root=%s\n", time.Now().Format(time.RFC3339), os.Getpid(), rootName)
	_ = os.WriteFile(path, []byte(header+std.capture.String()), 0o644)
	pruneLogs(std.logDir, std.keepFiles)
}

func pruneLogs(dir string, keep int) {
	if keep <= 0 {
		return
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	var files []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasPrefix(e.Name(), "ployenv-") && strings.HasSuffix(e.Name(), ".log") {
			files = append(files, e.Name())
		}
	}
	if len(files) <= keep {
		return
	}
	// Names embed a timestamp, so lexical order is chronological.
	for len(files) > keep {
		oldest := files[0]
		for _, f := range files {
			if f < oldest {
				oldest = f
			}
		}
		_ = os.Remove(filepath.Join(dir, oldest))
		idx := 0
		for i, f := range files {
			if f == oldest {
				idx = i
				break
			}
		}
		files = append(files[:idx], files[idx+1:]...)
	}
}
