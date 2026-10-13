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

// DirPermFor and FilePermFor are the OS-parameterised permission bits for the
// directories and files ployenv creates under its data root (NFR-805). Off
// Windows the data root follows the private 0700 convention of XDG_DATA_HOME;
// on Windows the bits are decorative, so the previous values are kept verbatim
// to leave that platform untouched. Taking the OS as an argument is what makes
// both mappings assertable from any runner.
func DirPermFor(osName string) os.FileMode {
	if osName == "windows" {
		return 0o755
	}
	return 0o700
}

func FilePermFor(osName string) os.FileMode {
	if osName == "windows" {
		return 0o644
	}
	return 0o600
}

// DirPerm is DirPermFor for the host platform.
func DirPerm() os.FileMode { return DirPermFor(OS()) }

// FilePerm is FilePermFor for the host platform.
func FilePerm() os.FileMode { return FilePermFor(OS()) }

// IsTerminal reports whether f is attached to a character device (a TTY).
// A non-UTF-8 console (e.g. Windows code page 936) is still a TTY.
func IsTerminal(f *os.File) bool {
	fi, err := f.Stat()
	if err != nil {
		return false
	}
	return fi.Mode()&os.ModeCharDevice != 0
}

// IsRoot reports whether the process runs with superuser privileges. On Windows
// os.Geteuid returns -1, so this is always false there, which matches the fact
// that Windows has no equivalent of the Unix root account.
func IsRoot() bool { return os.Geteuid() == 0 }
