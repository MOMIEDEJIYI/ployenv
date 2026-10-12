// Package platform isolates all operating-system specific details so the rest
// of ployenv stays portable (Windows first, Linux/macOS supported later).
package platform

import (
	"os"
	"runtime"
)

// OS returns the operating system name (runtime.GOOS).
func OS() string { return runtime.GOOS }

// Arch returns the CPU architecture (runtime.GOARCH).
func Arch() string { return runtime.GOARCH }

// IsWindows reports whether ployenv runs on Windows.
func IsWindows() bool { return runtime.GOOS == "windows" }

// ExeSuffix is ".exe" on Windows, empty elsewhere.
func ExeSuffix() string {
	if IsWindows() {
		return ".exe"
	}
	return ""
}

// ExeName appends the platform executable suffix to base.
func ExeName(base string) string { return base + ExeSuffix() }

// ScriptExt is the shell wrapper extension (".cmd" on Windows).
func ScriptExt() string {
	if IsWindows() {
		return ".cmd"
	}
	return ""
}

// PathSep is the OS list separator for PATH-like variables.
func PathSep() string {
	if IsWindows() {
		return ";"
	}
	return ":"
}

// IsTerminal reports whether f is attached to a character device (a TTY).
// A non-UTF-8 console (e.g. Windows code page 936) is still a TTY.
func IsTerminal(f *os.File) bool {
	fi, err := f.Stat()
	if err != nil {
		return false
	}
	return fi.Mode()&os.ModeCharDevice != 0
}
