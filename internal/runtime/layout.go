package runtime

import "github.com/MOMIEDEJIYI/ployenv/internal/platform"

// hostOS is a seam: where a runtime's tools live inside a version directory
// differs per platform, and a runner can only ever execute one of them, so every
// mapping below must be assertable for all of them from any host (the same
// reasoning as netx.retrySleep). Production never reassigns it.
var (
	hostOS   = platform.OS
	hostArch = platform.Arch
)

// exeFor and scriptFor are the OS-parameterised forms of platform.ExeName and
// platform.ScriptExt: taking the OS as an argument is what makes both mappings
// testable instead of only the running one. They agree with the platform
// package for the host OS, which a test asserts.
func exeFor(osName, base string) string {
	if osName == "windows" {
		return base + ".exe"
	}
	return base
}

func scriptFor(osName, base string) string {
	if osName == "windows" {
		return base + ".cmd"
	}
	return base
}

// toolDirFor is the directory that holds a runtime's tools inside a version
// directory on osName; "" means the version directory itself. The Windows
// layouts keep the tools at the root (NuGet python, the node zip), while the
// Unix-likes keep them in bin/.
func toolDirFor(osName string) string {
	if osName == "windows" {
		return ""
	}
	return "bin"
}

// toolDir is toolDirFor for the host platform.
func toolDir() string { return toolDirFor(hostOS()) }
