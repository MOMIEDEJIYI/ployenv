// Package runtime defines what a managed runtime is and holds the built-in
// definitions for node, java, go and python. Adding a runtime means adding a
// Spec here (or, later, supplying one from config).
package runtime

import (
	"path/filepath"
	"strings"

	"github.com/MOMIEDEJIYI/ployenv/internal/config"
	"github.com/MOMIEDEJIYI/ployenv/internal/fsutil"
	"github.com/MOMIEDEJIYI/ployenv/internal/platform"
)

// Release is one installable version advertised by an upstream index.
type Release struct {
	Version string
	LTS     string
	Note    string
}

// Layout tells the installer how to normalise an unpacked archive.
type Layout int

const (
	// LayoutNone leaves the extracted tree as-is.
	LayoutNone Layout = iota
	// LayoutFlatten moves the contents of a single top-level folder up.
	LayoutFlatten
	// LayoutNuGet flattens a NuGet package's tools/ folder and drops metadata.
	LayoutNuGet
	// LayoutMacJDK normalises a macOS JDK bundle: Adoptium's darwin archive
	// nests the actual JDK home two levels deep, as <wrapper>/Contents/Home,
	// so a plain flatten would leave Contents/ where bin/ is expected.
	LayoutMacJDK
	// LayoutRust merges the per-component directories of a Rust distribution
	// (rustc-<ver>-<triple>/, cargo-<ver>-<triple>/, rust-std-<triple>/) into
	// one version directory. Each component carries its own bin/ and share/, so
	// this is a directory merge, not a flatten.
	LayoutRust
)

// Part is one archive of a multi-archive runtime. Rust publishes rustc, cargo
// and rust-std as separate tarballs, each with its own checksum sidecar, so a
// single File on Plan cannot describe an install.
type Part struct {
	File   string
	URLFor func(base string) string
	SHA256 string // optional integrity check, read from the .sha256 sidecar
}

// Plan describes how to obtain one exact version.
type Plan struct {
	Exact  string   // exact version to record
	File   string   // cache file name
	Bases  []string // ordered download bases (mirror first, then official)
	URLFor func(base string) string
	SHA256 string // optional integrity check
	// SHA1 is the optional integrity check for upstreams that publish only a
	// SHA-1 (the Android SDK repository indexes no stronger digest for
	// platform-tools).
	SHA1 string
	// SHA512 is the optional integrity check for upstreams that publish only a
	// SHA-512 sidecar (Maven Central: .sha256 answers 404).
	SHA512 string
	// Parts describes a runtime that ships several archives. When it is set,
	// install fetches and verifies every part and unpacks them all into the
	// same version directory; Layout then runs once, on the merged tree.
	Parts  []Part
	Layout Layout
}

// Spec is a runtime definition.
type Spec struct {
	Name    string
	Title   string
	Kind    string // "language" (services reserved)
	Aliases []string

	// Official returns the upstream/default source URL, shown by config and
	// runtimes when no mirror is configured.
	Official func() string

	ExeTools []string // tools dispatched as direct binaries
	CmdTools []string // tools dispatched as shell wrappers
	Primary  string   // tool used by "which" and the install sanity check

	EnvVar   string // JAVA_HOME / GOROOT
	ToolPath func(tool, version string) string
	Env      func(dir string) []string
	// PathDirs returns the directories prepended to PATH when this version is
	// active, so the tools are directly runnable (e.g. <dir>, <dir>/bin).
	PathDirs func(dir string) []string
	Verify   func(dir string) (string, error)
	List     func(filter string) ([]Release, error)
	Plan     func(version string) (Plan, error)
}

// Tools returns every shim/tool name this runtime exposes.
func (s *Spec) Tools() []string {
	out := append([]string{}, s.ExeTools...)
	out = append(out, s.CmdTools...)
	return out
}

// BinaryPath returns the absolute path of the primary tool inside dir.
func (s *Spec) BinaryPath(dir, ver string) string {
	if s.ToolPath == nil {
		return ""
	}
	rel := s.ToolPath(s.Primary, ver)
	if rel == "" {
		return ""
	}
	return filepath.Join(dir, rel)
}

// HasBinary reports whether a version directory contains its primary tool.
func (s *Spec) HasBinary(dir, ver string) bool {
	p := s.BinaryPath(dir, ver)
	return p != "" && fsutil.FileExists(p)
}

// EnvEntries returns extra environment entries for a version directory.
func (s *Spec) EnvEntries(dir string) []string {
	if s.Env != nil {
		return s.Env(dir)
	}
	if s.EnvVar != "" {
		return []string{s.EnvVar + "=" + dir}
	}
	return nil
}

// OfficialURL returns the upstream source URL for display.
func (s *Spec) OfficialURL() string {
	if s.Official != nil {
		return s.Official()
	}
	return ""
}

// PathEntries returns the PATH additions for a version directory.
func (s *Spec) PathEntries(dir string) []string {
	if s.PathDirs != nil {
		return s.PathDirs(dir)
	}
	return []string{dir}
}

// downloadBases returns the ordered download roots: the configured/upstream
// base first, then well-known mirrors when they differ.
func downloadBases(lang, official string, mirrors ...string) []string {
	primary := config.MirrorFor(lang, official)
	bases := []string{primary}
	for _, m := range mirrors {
		m = strings.TrimRight(m, "/")
		if m != "" && m != primary {
			bases = append(bases, m)
		}
	}
	return bases
}

// exeName / scriptExt are small local aliases so provider files read cleanly.
func exeName(base string) string { return platform.ExeName(base) }
func scriptExt() string          { return platform.ScriptExt() }
