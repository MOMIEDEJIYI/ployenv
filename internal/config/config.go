// Package config loads and stores ployenv's config.json and answers all
// path questions (root, versions, cache, logs).
package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/MOMIEDEJIYI/ployenv/internal/fsutil"
	"github.com/MOMIEDEJIYI/ployenv/internal/platform"
	"github.com/MOMIEDEJIYI/ployenv/internal/version"
)

// Config mirrors config.json. It is safe to edit by hand.
type Config struct {
	// Version is reserved for future configuration migrations; nothing reads it
	// yet (BUG-014).
	Version  int               `json:"version,omitempty"`
	Root     string            `json:"root,omitempty"`
	Defaults map[string]string `json:"defaults"`
	Mirrors  map[string]string `json:"mirrors,omitempty"`
	Logs     *LogsConfig       `json:"logs,omitempty"`
	Cache    *CacheConfig      `json:"cache,omitempty"`
	// Runtimes is reserved for user-supplied runtime definitions (FR-11). It is
	// parsed and re-saved but deliberately has no effect yet: the built-in Go
	// Specs are the only definitions in force, and ployenv must not advertise
	// otherwise (BUG-014).
	Runtimes map[string]json.RawMessage `json:"runtimes,omitempty"`
}

// LogsConfig controls diagnostics file logging (FR-13).
type LogsConfig struct {
	Mode string `json:"mode,omitempty"` // off | on-error | always
	Dir  string `json:"dir,omitempty"`  // relative to root
	Keep int    `json:"keep,omitempty"` // max files to retain
}

// CacheConfig controls downloaded-archive retention (FR-17).
type CacheConfig struct {
	// Keep retains the downloaded archive after a successful install. The
	// default (false) deletes it so a portable install folder stays small.
	Keep bool `json:"keep,omitempty"`
}

// DefaultRoot is where a machine without an installed copy keeps its data.
func DefaultRoot() string {
	if platform.IsWindows() {
		if lad := strings.TrimSpace(os.Getenv("LOCALAPPDATA")); lad != "" {
			return filepath.Join(lad, "Programs", "PloyEnv")
		}
		if home := strings.TrimSpace(os.Getenv("USERPROFILE")); home != "" {
			return filepath.Join(home, "PloyEnv")
		}
	}
	// XDG first, as the manual promises (D-3).
	if xdg := strings.TrimSpace(os.Getenv("XDG_DATA_HOME")); xdg != "" {
		return filepath.Join(xdg, "ployenv")
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ".ployenv"
	}
	return filepath.Join(home, ".local", "share", "ployenv")
}

// ExePath is the absolute path of the running ployenv binary.
func ExePath() string {
	self, err := os.Executable()
	if err != nil {
		return ""
	}
	if resolved, err := filepath.EvalSymlinks(self); err == nil {
		self = resolved
	}
	return self
}

// ExeDir is the folder holding the running ployenv binary.
func ExeDir() string {
	p := ExePath()
	if p == "" {
		return ""
	}
	return filepath.Dir(p)
}

// RootSource explains why RootDir resolved the way it did.
type RootSource string

const (
	RootFromEnv     RootSource = "environment variable PLOYENV_ROOT"
	RootFromExeDir  RootSource = "next to the executable (portable)"
	RootFromDefault RootSource = "user default directory (fallback)"
)

var (
	rootOnce  sync.Once
	rootDir   string
	rootSrc   RootSource
	rootWrite bool
)

func resolveRoot() {
	rootOnce.Do(func() {
		if v := strings.TrimSpace(os.Getenv("PLOYENV_ROOT")); v != "" {
			rootDir, rootSrc = filepath.Clean(v), RootFromEnv
		} else if dir := ExeDir(); dir != "" && dirWritable(dir) {
			// Portable by default: the data lives beside the binary, so the
			// whole folder can be moved, copied or backed up as one unit.
			rootDir, rootSrc = dir, RootFromExeDir
		} else {
			rootDir, rootSrc = DefaultRoot(), RootFromDefault
		}
		rootWrite = dirWritable(rootDir)
	})
}

// RootInfo returns the data directory, why it was chosen, and whether it is
// writable. It is computed once per process.
func RootInfo() (string, RootSource, bool) {
	resolveRoot()
	return rootDir, rootSrc, rootWrite
}

// RootDir resolves the data directory (see RootInfo).
func RootDir() string {
	d, _, _ := RootInfo()
	return d
}

// dirWritable reports whether a new file can be created in dir.
func dirWritable(dir string) bool {
	if dir == "" {
		return false
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return false
	}
	f, err := os.CreateTemp(dir, ".ployenv-wtest-*")
	if err != nil {
		return false
	}
	name := f.Name()
	f.Close()
	_ = os.Remove(name)
	return true
}

// ConfigPath is where config.json is read from and written to.
func ConfigPath() string { return filepath.Join(RootDir(), "config.json") }

// VersionsDir is <root>/versions.
func VersionsDir() string { return filepath.Join(RootDir(), "versions") }

// LangVersionsDir is <root>/versions/<lang>.
func LangVersionsDir(lang string) string { return filepath.Join(VersionsDir(), lang) }

// VersionDir is <root>/versions/<lang>/<version>.
func VersionDir(lang, ver string) string { return filepath.Join(LangVersionsDir(lang), ver) }

// CacheDir is <root>/cache.
func CacheDir() string { return filepath.Join(RootDir(), "cache") }

// CacheKeep reports whether downloaded archives are retained after a
// successful install. Default is false: the archive is deleted to save space.
func CacheKeep() bool {
	c := Load()
	return c.Cache != nil && c.Cache.Keep
}

// LogsDir is the diagnostics log directory.
func LogsDir() string {
	cfg := Load()
	sub := "logs"
	if cfg.Logs != nil && strings.TrimSpace(cfg.Logs.Dir) != "" {
		sub = cfg.Logs.Dir
	}
	if filepath.IsAbs(sub) {
		return sub
	}
	return filepath.Join(RootDir(), sub)
}

// StateDir is <root>/state, used for scratch files.
func StateDir() string { return filepath.Join(RootDir(), "state") }

// Load reads config.json, filling in defaults for missing fields.
func Load() *Config {
	cfg := &Config{Defaults: map[string]string{}, Mirrors: map[string]string{}}
	if data, err := os.ReadFile(ConfigPath()); err == nil {
		if json.Unmarshal(data, cfg) == nil {
			if cfg.Defaults == nil {
				cfg.Defaults = map[string]string{}
			}
			if cfg.Mirrors == nil {
				cfg.Mirrors = map[string]string{}
			}
			if cfg.Root == "" {
				cfg.Root = RootDir()
			}
			return cfg
		}
	}
	cfg.Root = RootDir()
	return cfg
}

// Save writes config.json, keeping an installed folder movable by leaving root
// empty when the data lives beside the binary.
func Save(cfg *Config) error {
	root := strings.TrimSpace(cfg.Root)
	if root == "" {
		root = RootDir()
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return err
	}
	stored := *cfg
	stored.Root = root
	if dir := ExeDir(); dir != "" && fsutil.SamePath(root, dir) {
		stored.Root = ""
	}
	if stored.Defaults == nil {
		stored.Defaults = map[string]string{}
	}
	if stored.Mirrors == nil {
		stored.Mirrors = map[string]string{}
	}
	data, err := json.MarshalIndent(stored, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(root, "config.json"), append(data, '\n'), 0o644)
}

// EnsureRoot creates the directory layout ployenv needs.
func EnsureRoot() error {
	for _, d := range []string{RootDir(), VersionsDir(), CacheDir()} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return err
		}
	}
	return nil
}

// MirrorFor returns the configured mirror for lang, or the official fallback.
func MirrorFor(lang, fallback string) string {
	if m := strings.TrimSpace(Load().Mirrors[lang]); m != "" {
		return strings.TrimRight(m, "/")
	}
	return strings.TrimRight(fallback, "/")
}

// Mirror returns the configured mirror for lang, or "" when none is set.
func Mirror(lang string) string { return strings.TrimSpace(Load().Mirrors[lang]) }

// InstalledVersions lists versions of lang found on disk, ascending.
func InstalledVersions(lang string) []string {
	entries, err := os.ReadDir(LangVersionsDir(lang))
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		if !strings.HasPrefix(e.Name(), ".") {
			out = append(out, e.Name())
		}
	}
	sort.Slice(out, func(i, j int) bool { return version.Compare(out[i], out[j]) < 0 })
	return out
}
