package config

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MOMIEDEJIYI/ployenv/internal/platform"
)

// resolveInChild runs the test binary in its resolve mode with a controlled
// environment and returns what RootInfo decided. A child is required because the
// choice is made once per process.
func resolveInChild(t *testing.T, env map[string]string) (root, source string, writable bool) {
	t.Helper()
	root, source, writable, _ = runResolveChild(t, env)
	return root, source, writable
}

// rootUsableInChild returns the error message the child's RootUsable produced,
// or "" when the root was usable.
func rootUsableInChild(t *testing.T, env map[string]string) error {
	t.Helper()
	_, _, _, errText := runResolveChild(t, env)
	if errText == "" || errText == "ok" {
		return nil
	}
	return errors.New(errText)
}

func runResolveChild(t *testing.T, env map[string]string) (root, source string, writable bool, usable string) {
	t.Helper()
	return runChild(t, os.Args[0], env)
}

// runResolveChildInRoDir copies the test binary into a directory it cannot write
// to, so the portable "beside the executable" rule is out of reach and only the
// platform default remains.
func runResolveChildInRoDir(t *testing.T) (root, source string, writable bool, usable string) {
	t.Helper()
	dir := t.TempDir()
	exe := filepath.Join(dir, "resolve-test"+platform.ExeSuffix())
	data, err := os.ReadFile(os.Args[0])
	if err != nil {
		t.Fatalf("reading the test binary: %v", err)
	}
	if err := os.WriteFile(exe, data, 0o755); err != nil {
		t.Fatalf("copying the test binary: %v", err)
	}
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatalf("making the directory read-only: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })
	return runChild(t, exe, map[string]string{
		"HOME": "", "XDG_DATA_HOME": "", "USERPROFILE": "", "LOCALAPPDATA": "",
	})
}

func runChild(t *testing.T, exe string, env map[string]string) (root, source string, writable bool, usable string) {
	t.Helper()
	cmd := exec.Command(exe)
	childEnv := []string{"PLOYENV_TEST_RESOLVE=1"}
	for _, kv := range os.Environ() {
		name := strings.SplitN(kv, "=", 2)[0]
		if _, override := env[name]; override || name == "PLOYENV_ROOT" {
			continue
		}
		childEnv = append(childEnv, kv)
	}
	for name, value := range env {
		if value == "" {
			continue
		}
		childEnv = append(childEnv, name+"="+value)
	}
	cmd.Env = childEnv
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("child failed: %v\n%s", err, out)
	}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		name, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		switch name {
		case "root":
			root = value
		case "source":
			source = value
		case "writable":
			writable = value == "true"
		case "usable":
			usable = value
		}
	}
	if source == "" {
		t.Fatalf("child reported %q", out)
	}
	return root, source, writable, usable
}

func TestRootComesFromTheEnvironmentWhenSet(t *testing.T) {
	dir := t.TempDir()
	root, source, writable := resolveInChild(t, map[string]string{"PLOYENV_ROOT": dir})

	if root != dir {
		t.Errorf("root = %q, want %q", root, dir)
	}
	if source != string(RootFromEnv) {
		t.Errorf("source = %q, want %q", source, RootFromEnv)
	}
	if !writable {
		t.Error("a fresh temporary directory is writable")
	}
}

func TestRootFromEnvironmentIsCleanedAndTrimmed(t *testing.T) {
	dir := t.TempDir()
	// The value is used verbatim apart from cleaning, so "…/." and a trailing
	// separator must land on the same directory.
	messy := filepath.Join(dir, ".", "sub") + string(filepath.Separator)
	root, source, _ := resolveInChild(t, map[string]string{"PLOYENV_ROOT": messy})
	if root != filepath.Join(dir, "sub") {
		t.Errorf("root = %q, want the cleaned %q", root, filepath.Join(dir, "sub"))
	}
	if source != string(RootFromEnv) {
		t.Errorf("source = %q", source)
	}
}

func TestRootFallsBackToTheExecutableDirectory(t *testing.T) {
	// The test binary lives in a writable build directory, so the portable
	// "beside the executable" rule is the one that applies - unless this runner
	// is root or the binary sits in a system bin directory, in which case the
	// rule is deliberately off (A5) and there is nothing to assert here.
	self, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable: %v", err)
	}
	if !portableAllowedFor(platform.OS(), filepath.Dir(self), platform.IsRoot()) {
		t.Skipf("the portable rule does not apply to %s on this runner", filepath.Dir(self))
	}
	root, source, writable := resolveInChild(t, map[string]string{"PLOYENV_ROOT": ""})
	// Compare the directories themselves: Windows may hand out either the long
	// or the 8.3 short form of the same folder (CI runners do), and EvalSymlinks
	// can also rewrite the path on macOS.
	want, err := os.Stat(filepath.Dir(self))
	if err != nil {
		t.Fatalf("stat %q: %v", filepath.Dir(self), err)
	}
	got, err := os.Stat(root)
	if err != nil {
		t.Fatalf("stat %q: %v", root, err)
	}
	if !os.SameFile(want, got) {
		t.Errorf("root = %q, want the executable's directory %q", root, filepath.Dir(self))
	}
	if source != string(RootFromExeDir) {
		t.Errorf("source = %q, want %q", source, RootFromExeDir)
	}
	if !writable {
		t.Error("the build directory is writable")
	}
}

func TestRootSourceExplainsItself(t *testing.T) {
	// The strings are user-visible (doctor prints them), so they are asserted.
	for _, tc := range []struct {
		src  RootSource
		want string
	}{
		{RootFromEnv, "PLOYENV_ROOT"},
		{RootFromExeDir, "portable"},
		{RootFromDefault, "fallback"},
	} {
		if !strings.Contains(string(tc.src), tc.want) {
			t.Errorf("%q should mention %q", tc.src, tc.want)
		}
	}
}

func TestPathBuildersLiveUnderTheRoot(t *testing.T) {
	resetRoot(t)
	root := RootDir()

	for name, got := range map[string]string{
		"versions":     VersionsDir(),
		"cache":        CacheDir(),
		"state":        StateDir(),
		"config":       ConfigPath(),
		"langVersions": LangVersionsDir("node"),
		"version":      VersionDir("node", "21.0.0"),
	} {
		if !strings.HasPrefix(got, root) {
			t.Errorf("%s = %q, want it under %q", name, got, root)
		}
	}
	if LangVersionsDir("node") != filepath.Join(VersionsDir(), "node") {
		t.Error("LangVersionsDir must be <versions>/<lang>")
	}
	if VersionDir("node", "21.0.0") != filepath.Join(LangVersionsDir("node"), "21.0.0") {
		t.Error("VersionDir must be <versions>/<lang>/<version>")
	}
	if ConfigPath() != filepath.Join(root, "config.json") {
		t.Error("ConfigPath must be <root>/config.json")
	}
}

func TestLogsDirHonoursTheConfiguration(t *testing.T) {
	resetRoot(t)
	root := RootDir()

	if got := LogsDir(); got != filepath.Join(root, "logs") {
		t.Errorf("LogsDir without a config = %q", got)
	}

	writeConfigFile(t, `{"logs":{"dir":"diagnostics"}}`)
	if got := LogsDir(); got != filepath.Join(root, "diagnostics") {
		t.Errorf("a relative logs dir is inside the root, got %q", got)
	}

	abs := t.TempDir()
	writeConfigFile(t, `{"logs":{"dir":`+strconv(abs)+`}}`)
	if got := LogsDir(); got != abs {
		t.Errorf("an absolute logs dir is used as-is, got %q", got)
	}

	// A blank value means the default.
	writeConfigFile(t, `{"logs":{"dir":"   "}}`)
	if got := LogsDir(); got != filepath.Join(root, "logs") {
		t.Errorf("a blank logs dir must fall back to <root>/logs, got %q", got)
	}
}

func strconv(s string) string {
	data, _ := json.Marshal(s)
	return string(data)
}

func TestLoadFillsInDefaults(t *testing.T) {
	resetRoot(t)

	cfg := Load()
	if cfg == nil {
		t.Fatal("Load returned nil")
	}
	if cfg.Defaults == nil || cfg.Mirrors == nil {
		t.Error("the maps must be usable, not nil")
	}
	if cfg.Root != RootDir() {
		t.Errorf("Root = %q, want %q", cfg.Root, RootDir())
	}
}

func TestLoadSurvivesABrokenConfigFile(t *testing.T) {
	resetRoot(t)
	resetConfigWarning()
	writeConfigFile(t, "{ this is not json")

	// Ignoring the file is deliberate; saying so is part of the contract, and it
	// is reported once per process because Load runs on many paths (I-30). The
	// file itself is never overwritten - see TestSaveRefusesToOverwriteAnUnusableConfig.
	var cfg *Config
	captureStderr(t, func() { cfg = Load() })
	if cfg == nil {
		t.Fatal("a broken config file must not take ployenv down")
	}
	if len(cfg.Defaults) != 0 || len(cfg.Mirrors) != 0 {
		t.Errorf("a broken file must fall back to defaults, got %+v", cfg)
	}
	if cfg.Root != RootDir() {
		t.Errorf("Root = %q", cfg.Root)
	}
}

func TestLoadNormalisesNullMaps(t *testing.T) {
	resetRoot(t)
	writeConfigFile(t, `{"defaults":null,"mirrors":null}`)

	cfg := Load()
	if cfg.Defaults == nil || cfg.Mirrors == nil {
		t.Fatalf("null maps must become empty maps: %+v", cfg)
	}
}

func TestSaveAndLoadRoundTrip(t *testing.T) {
	resetRoot(t)
	writeConfigFile(t, `{}`)

	cfg := Load()
	cfg.Defaults["node"] = "21.0.0"
	cfg.Mirrors["node"] = "https://mirror.invalid/node"
	cfg.Logs = &LogsConfig{Mode: "always", Dir: "diag", Keep: 5}
	cfg.Cache = &CacheConfig{Keep: true}
	// Reserved fields are parsed and re-saved (BUG-014): dropping them would
	// silently delete a user's settings on the next write.
	cfg.Runtimes = map[string]json.RawMessage{"custom": json.RawMessage(`{"name":"custom"}`)}
	if err := Save(cfg); err != nil {
		t.Fatalf("Save: %v", err)
	}

	data, err := os.ReadFile(ConfigPath())
	if err != nil {
		t.Fatalf("reading config.json: %v", err)
	}
	if !strings.HasSuffix(string(data), "\n") {
		t.Error("config.json must end with a newline so hand-editing stays pleasant")
	}

	back := Load()
	if back.Defaults["node"] != "21.0.0" || back.Mirrors["node"] != "https://mirror.invalid/node" {
		t.Errorf("defaults/mirrors did not round-trip: %+v", back)
	}
	if back.Logs == nil || back.Logs.Mode != "always" || back.Logs.Keep != 5 {
		t.Errorf("logs did not round-trip: %+v", back.Logs)
	}
	if back.Cache == nil || !back.Cache.Keep {
		t.Errorf("cache did not round-trip: %+v", back.Cache)
	}
	if _, ok := back.Runtimes["custom"]; !ok {
		t.Errorf("the reserved runtimes field must survive: %+v", back.Runtimes)
	}
}

func TestSaveFillsInNilMaps(t *testing.T) {
	resetRoot(t)
	writeConfigFile(t, `{}`)

	if err := Save(&Config{}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	back := Load()
	if back.Defaults == nil || back.Mirrors == nil {
		t.Fatalf("Save must write usable maps: %+v", back)
	}
}

// An installed folder has to stay movable, so the root is only recorded when it
// is somewhere other than beside the executable.
func TestSaveOmitsTheRootWhenItIsTheExecutableDirectory(t *testing.T) {
	resetRoot(t)

	if err := Save(&Config{Defaults: map[string]string{}, Root: ExeDir()}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	fromRoot := Load()
	if fromRoot.Root != RootDir() {
		t.Errorf("Root = %q", fromRoot.Root)
	}
	// The file was written next to the binary (the exe directory), so read it
	// there to check what was actually stored.
	data, err := os.ReadFile(filepath.Join(ExeDir(), "config.json"))
	if err != nil {
		t.Skipf("the executable directory is not writable for this test: %v", err)
	}
	defer os.Remove(filepath.Join(ExeDir(), "config.json"))

	var stored struct {
		Root string `json:"root"`
	}
	if err := json.Unmarshal(data, &stored); err != nil {
		t.Fatalf("decoding the written file: %v", err)
	}
	if stored.Root != "" {
		t.Errorf("stored root = %q, want it empty so the folder stays portable", stored.Root)
	}
}

func TestSaveRecordsAnExplicitRoot(t *testing.T) {
	resetRoot(t)
	other := filepath.Join(t.TempDir(), "别处")

	if err := Save(&Config{Defaults: map[string]string{}, Root: other}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(other, "config.json"))
	if err != nil {
		t.Fatalf("Save must write into the configured root: %v", err)
	}
	var stored struct {
		Root     string            `json:"root"`
		Defaults map[string]string `json:"defaults"`
	}
	if err := json.Unmarshal(data, &stored); err != nil {
		t.Fatalf("decoding: %v", err)
	}
	if stored.Root != other {
		t.Errorf("stored root = %q, want %q", stored.Root, other)
	}
}

func TestSaveReportsAnUnusableRoot(t *testing.T) {
	resetRoot(t)
	file := filepath.Join(RootDir(), "not-a-directory")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := Save(&Config{Defaults: map[string]string{}, Root: file}); err == nil {
		t.Error("saving into a path that is a file must fail")
	}
}

func TestEnsureRootCreatesTheLayout(t *testing.T) {
	root := resetRoot(t)

	if err := EnsureRoot(); err != nil {
		t.Fatalf("EnsureRoot: %v", err)
	}
	for _, dir := range []string{root, VersionsDir(), CacheDir()} {
		info, err := os.Stat(dir)
		if err != nil {
			t.Fatalf("%s must exist: %v", dir, err)
		}
		if !info.IsDir() {
			t.Errorf("%s must be a directory", dir)
		}
	}
	// Calling it twice is fine, which is what every command does.
	if err := EnsureRoot(); err != nil {
		t.Errorf("EnsureRoot must be idempotent: %v", err)
	}
}

func TestCacheKeepFollowsTheConfig(t *testing.T) {
	resetRoot(t)
	writeConfigFile(t, `{}`)
	if CacheKeep() {
		t.Error("the default must be to delete the archive")
	}
	writeConfigFile(t, `{"cache":{"keep":true}}`)
	if !CacheKeep() {
		t.Error("cache.keep=true must be honoured")
	}
}

func TestMirrorLookup(t *testing.T) {
	resetRoot(t)
	writeConfigFile(t, `{}`)

	if got := MirrorFor("node", "https://nodejs.org/dist/"); got != "https://nodejs.org/dist" {
		t.Errorf("MirrorFor without a mirror = %q, want the trimmed fallback", got)
	}
	if got := Mirror("node"); got != "" {
		t.Errorf("Mirror without a mirror = %q, want empty", got)
	}

	writeConfigFile(t, `{"mirrors":{"node":"  https://mirror.invalid/node/  "}}`)
	// MirrorFor strips the trailing slash so callers can join paths; Mirror is
	// the raw trimmed value and leaves that to its caller.
	if got := MirrorFor("node", "https://nodejs.org/dist"); got != "https://mirror.invalid/node" {
		t.Errorf("MirrorFor = %q, want the trailing slash stripped", got)
	}
	if got := Mirror("node"); got != "https://mirror.invalid/node/" {
		t.Errorf("Mirror = %q, want the value only trimmed of surrounding space", got)
	}
	if got := Mirror("java"); got != "" {
		t.Errorf("Mirror(java) = %q, want empty", got)
	}
	if got := MirrorFor("java", "https://api.adoptium.net/v3/"); got != "https://api.adoptium.net/v3" {
		t.Errorf("MirrorFor = %q, want the fallback trimmed of its trailing slash", got)
	}
}

func TestInstalledVersionsAreSortedNumerically(t *testing.T) {
	resetRoot(t)
	for _, v := range []string{"9.0.0", "10.0.0", "2.0.0"} {
		if err := os.MkdirAll(VersionDir("node", v), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	// A dot directory is bookkeeping, not a version.
	if err := os.MkdirAll(filepath.Join(LangVersionsDir("node"), ".trash"), 0o755); err != nil {
		t.Fatal(err)
	}

	got := InstalledVersions("node")
	want := []string{"2.0.0", "9.0.0", "10.0.0"}
	if len(got) != len(want) {
		t.Fatalf("InstalledVersions = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("InstalledVersions = %v, want %v (numeric, ascending)", got, want)
		}
	}

	if got := InstalledVersions("ruby"); got != nil {
		t.Errorf("an unknown runtime has nothing installed, got %v", got)
	}
	if got := InstalledVersions("java"); len(got) != 0 {
		t.Errorf("a runtime with no versions = %v", got)
	}
}

func TestDirWritable(t *testing.T) {
	root := resetRoot(t)
	if !dirWritable(root) {
		t.Error("a fresh directory is writable")
	}
	if dirWritable("") {
		t.Error("an empty path is not writable")
	}
	file := filepath.Join(root, "file")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if dirWritable(file) {
		t.Error("a path that is a file is not a writable directory")
	}
	if dirWritable(filepath.Join(file, "under-a-file")) {
		t.Error("a directory under a file cannot be created")
	}
}

func TestExePathAndExeDir(t *testing.T) {
	path := ExePath()
	if path == "" {
		t.Fatal("ExePath must resolve for a running program")
	}
	if !filepath.IsAbs(path) {
		t.Errorf("ExePath = %q, want an absolute path", path)
	}
	if ExeDir() != filepath.Dir(path) {
		t.Errorf("ExeDir = %q, want %q", ExeDir(), filepath.Dir(path))
	}
}

func TestDefaultRootPerEnvironment(t *testing.T) {
	if platform.IsWindows() {
		lad := filepath.Join(t.TempDir(), "AppData")
		t.Setenv("LOCALAPPDATA", lad)
		if got := DefaultRoot(); got != filepath.Join(lad, "Programs", "PloyEnv") {
			t.Errorf("with LOCALAPPDATA, DefaultRoot = %q", got)
		}

		prof := filepath.Join(t.TempDir(), "profile")
		t.Setenv("LOCALAPPDATA", "")
		t.Setenv("USERPROFILE", prof)
		if got := DefaultRoot(); got != filepath.Join(prof, "PloyEnv") {
			t.Errorf("with only USERPROFILE, DefaultRoot = %q", got)
		}
	}

	xdg := filepath.Join(t.TempDir(), "xdg")
	t.Setenv("LOCALAPPDATA", "")
	t.Setenv("USERPROFILE", "")
	t.Setenv("XDG_DATA_HOME", xdg)
	if got := DefaultRoot(); got != filepath.Join(xdg, "ployenv") {
		t.Errorf("with XDG_DATA_HOME, DefaultRoot = %q", got)
	}

	// Without any of the overrides the platform's home directory is used, and a
	// machine without a home falls back to the working directory. Windows only
	// ever consults USERPROFILE (not HOME), so clearing both reaches the same
	// place on either platform.
	t.Setenv("XDG_DATA_HOME", "")
	t.Setenv("USERPROFILE", "")
	t.Setenv("HOME", "")
	if got := DefaultRoot(); got != "" {
		t.Errorf("without any home, DefaultRoot = %q, want \"\" (FR-809: no relative fallback)", got)
	}
}

// FR-809: no environment can make the data directory a *relative* path. With
// HOME, XDG_DATA_HOME and the Windows equivalents all absent the root is either
// the portable one beside the executable (writable here) or nothing at all - and
// never ".ployenv", which is what used to scatter versions into whatever
// directory ployenv happened to be started from.
// A5 / REQ-008: the portable rule must not turn a system bin directory into a
// data directory just because root happens to be able to write there. Every
// combination is asserted from any runner.
func TestPortableAllowedFor(t *testing.T) {
	for _, tc := range []struct {
		osName, exeDir string
		isRoot         bool
		want           bool
	}{
		// Windows keeps the behaviour it was built around, root or not.
		{"windows", `C:\tools`, false, true},
		{"windows", `C:\tools`, true, true},
		{"windows", `/usr/local/bin`, true, true},
		// The Unix-likes: a user directory is fine ...
		{"linux", "/home/me/.local/bin", false, true},
		{"darwin", "/Users/me/bin", false, true},
		{"linux", "/tmp/go-build123/b001", false, true},
		// ... a system bin directory is not ...
		{"linux", "/usr/local/bin", false, false},
		{"linux", "/usr/bin", false, false},
		{"linux", "/usr/sbin", false, false},
		{"linux", "/bin", false, false},
		{"darwin", "/usr/local/bin", false, false},
		{"darwin", "/opt/homebrew/bin", false, true},
		// ... and neither is anything at all when running as root.
		{"linux", "/home/me/bin", true, false},
		{"darwin", "/Users/me/bin", true, false},
		// A trailing separator must not sneak past the comparison. (The
		// exeDir argument is a *host-form* path - it comes from ExeDir() - so
		// only the host's own spelling is meaningful here; a Windows-shaped
		// path would be one literal filename on the Unix-likes.)
		{"linux", "/usr/local/bin/", false, false},
		{"linux", "/usr/local/bin/../bin", false, false},
	} {
		if got := portableAllowedFor(tc.osName, tc.exeDir, tc.isRoot); got != tc.want {
			t.Errorf("portableAllowedFor(%q, %q, root=%v) = %v, want %v",
				tc.osName, tc.exeDir, tc.isRoot, got, tc.want)
		}
	}
}

func TestRootNeverFallsBackToARelativePath(t *testing.T) {
	root, source, _ := resolveInChild(t, map[string]string{
		"HOME": "", "XDG_DATA_HOME": "", "USERPROFILE": "", "LOCALAPPDATA": "",
	})
	if root != "" && !filepath.IsAbs(root) {
		t.Errorf("root = %q, want an absolute path or none at all", root)
	}
	if root != "" && source != string(RootFromExeDir) {
		t.Errorf("source = %q, want the portable rule when it applies", source)
	}
	if root == "" && source != string(RootFromDefault) {
		t.Errorf("source = %q, want the default-directory label when nothing resolved", source)
	}
}

// The rule itself, both directions: an undetermined directory must fail with a
// message naming the variable that fixes it, and any determined directory must
// pass - including a relative-looking one, because deciding what is acceptable
// is the resolver's job, not this check's.
func TestRootUsableFor(t *testing.T) {
	if err := rootUsableFor("/var/lib/ployenv"); err != nil {
		t.Errorf("a resolved root must be usable, got: %v", err)
	}
	err := rootUsableFor("")
	if err == nil {
		t.Fatal("an empty root must be rejected")
	}
	if !errors.Is(err, ErrRootUnavailable) {
		t.Errorf("the error must be ErrRootUnavailable, got: %v", err)
	}
	for _, want := range []string{"PLOYENV_ROOT", "XDG_DATA_HOME", "HOME"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the error must mention %s, got: %v", want, err)
		}
	}
}

// A resolvable data directory must not report the error. Without PLOYENV_ROOT
// the child still resolves one - the portable directory beside the executable is
// writable under `go test` - which is exactly the point: RootUsable only objects
// when nothing resolved at all.
func TestRootUsableAcceptsAResolvableRoot(t *testing.T) {
	if err := rootUsableInChild(t, nil); err != nil {
		t.Errorf("a pinned PLOYENV_ROOT must be usable, got: %v", err)
	}
}

// FR-809, the literal scenario DSH asked for: with HOME and XDG_DATA_HOME gone
// *and* no writable directory beside the executable, the run must stop with an
// error naming PLOYENV_ROOT instead of guessing a relative path. The child is
// copied into a read-only directory so the portable rule cannot apply - on
// Windows the mode bits do not gate writes, so that half is skipped there and
// exercised by the ubuntu runner.
func TestRootUnavailableChildErrors(t *testing.T) {
	if platform.IsWindows() {
		t.Skip("directory mode bits do not prevent writes on Windows")
	}
	_, _, _, usable := runResolveChildInRoDir(t)
	if usable == "" || usable == "ok" {
		t.Fatalf("with no home and an unwritable exe directory the child must report an error, got %q", usable)
	}
	for _, want := range []string{"PLOYENV_ROOT", "XDG_DATA_HOME"} {
		if !strings.Contains(usable, want) {
			t.Errorf("the child's error must mention %s, got: %s", want, usable)
		}
	}
}
