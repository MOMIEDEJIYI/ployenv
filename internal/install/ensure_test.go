package install

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MOMIEDEJIYI/ployenv/internal/config"
	"github.com/MOMIEDEJIYI/ployenv/internal/fsutil"
	"github.com/MOMIEDEJIYI/ployenv/internal/platform"
	"github.com/MOMIEDEJIYI/ployenv/internal/runtime"
)

func goSpec(t *testing.T) *runtime.Spec {
	t.Helper()
	spec := runtime.Lookup("go")
	if spec == nil {
		t.Fatal("the go runtime must be registered")
	}
	return spec
}

// The whole install path runs offline: the index and the archive come from a
// local server the configured mirror points at, and the archive is a real one,
// so download, checksum, extraction and the layout are all exercised.
func TestEnsureInstallsFromTheConfiguredMirror(t *testing.T) {
	resetSandbox(t)
	src := goSourceWithTool(t, "1.27.2")
	spec := goSpec(t)

	got, already, err := Ensure(spec, "1.27.2", Options{})
	if err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	if already {
		t.Error("a fresh install must not report 'already present'")
	}
	if got != "1.27.2" {
		t.Fatalf("version = %q, want 1.27.2", got)
	}

	tool := filepath.Join(config.VersionDir("go", "1.27.2"), "bin", goToolName())
	data, err := os.ReadFile(tool)
	if err != nil {
		t.Fatalf("the tool must be extracted and flattened into place: %v", err)
	}
	if string(data) != "binary content" {
		t.Errorf("tool contents = %q", data)
	}
	// The wrapper directory itself is gone: only its contents moved up.
	if fsutil.DirExists(filepath.Join(config.VersionDir("go", "1.27.2"), "go")) {
		t.Error("the archive's top-level directory must not survive the flatten")
	}

	// Archives are dropped after a successful install unless cache.keep is set.
	if _, err := os.Stat(filepath.Join(config.CacheDir(), src.file)); !os.IsNotExist(err) {
		t.Errorf("the archive must be removed after a successful install (err=%v)", err)
	}

	// Installing again is a no-op that says so (BUG-017) and never downloads.
	got, already, err = Ensure(spec, "1.27.2", Options{})
	if err != nil {
		t.Fatalf("second Ensure: %v", err)
	}
	if !already || got != "1.27.2" {
		t.Errorf("second Ensure = (%q, %v), want (1.27.2, true)", got, already)
	}
	if _, err := os.Stat(filepath.Join(config.CacheDir(), src.file)); !os.IsNotExist(err) {
		t.Error("the second call must not download anything")
	}
}

func TestEnsureKeepsTheArchiveWhenConfigured(t *testing.T) {
	resetSandbox(t)
	src := goSourceWithTool(t, "1.26.4")
	// cache.keep=true keeps the download around for a later reinstall.
	writeConfigFile(t, fmt.Sprintf(`{"mirrors":{"go":%q},"cache":{"keep":true}}`, src.url))

	if _, _, err := Ensure(goSpec(t), "1.26.4", Options{}); err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	if _, err := os.Stat(cachePath("1.26.4")); err != nil {
		t.Errorf("cache.keep=true must retain the archive: %v", err)
	}

	// A forced reinstall reuses the cached archive instead of downloading it
	// again; the server is not asked a second time because the file is there.
	if _, already, err := Ensure(goSpec(t), "1.26.4", Options{Force: true}); err != nil || already {
		t.Fatalf("forced Ensure = (%v, %v), want a fresh install", already, err)
	}
}

func TestEnsureRejectsAChecksumMismatch(t *testing.T) {
	resetSandbox(t)
	// The index advertises a digest the archive does not have.
	newGoSourceFromFilesAdvertised(t, "1.25.0", strings.Repeat("a", 64))

	_, _, err := Ensure(goSpec(t), "1.25.0", Options{})
	if err == nil {
		t.Fatal("a checksum mismatch must fail the install")
	}
	if !strings.Contains(err.Error(), "checksum mismatch") {
		t.Errorf("error = %v, want it to mention the checksum", err)
	}
	// The unverified archive is deleted and nothing is registered.
	if _, serr := os.Stat(cachePath("1.25.0")); !os.IsNotExist(serr) {
		t.Error("the archive must be removed after a failed verification")
	}
	if fsutil.DirExists(config.VersionDir("go", "1.25.0")) {
		t.Error("nothing may be registered when the download was rejected")
	}
}

func TestEnsureRollsBackWhenTheArchiveCannotBeUsed(t *testing.T) {
	t.Run("unreadable archive", func(t *testing.T) {
		resetSandbox(t)
		newGoSource(t, "1.24.0", []byte("this is not an archive at all"), "")

		if _, _, err := Ensure(goSpec(t), "1.24.0", Options{}); err == nil {
			t.Fatal("extracting garbage must fail")
		}
		if fsutil.DirExists(config.VersionDir("go", "1.24.0")) {
			t.Error("a failed extraction must not leave a half-installed version behind")
		}
	})

	t.Run("archive without the tool", func(t *testing.T) {
		resetSandbox(t)
		newGoSourceFromFiles(t, "1.23.0", map[string]string{"go/README.md": "docs"})

		_, _, err := Ensure(goSpec(t), "1.23.0", Options{})
		if err == nil {
			t.Fatal("an archive that does not contain the tool must fail")
		}
		if !strings.Contains(err.Error(), "not found after extraction") {
			t.Errorf("error = %v, want the 'not found after extraction' message", err)
		}
		if fsutil.DirExists(config.VersionDir("go", "1.23.0")) {
			t.Error("the rollback must remove the destination directory")
		}
	})
}

// The post-install self-check only warns: the files are in place, and a tool that
// refuses to run is worth reporting but not worth deleting a finished install.
func TestEnsureRejectsAToolWithoutTheExecuteBit(t *testing.T) {
	// FR-808 / REQ-008 A2: the reverse assertion. An archive whose tool carries
	// no execute bit must fail the install and roll back, rather than leave a
	// version directory that reports success and breaks on first use.
	if platform.IsWindows() {
		t.Skip("execute bits do not exist on Windows")
	}
	resetSandbox(t)

	path := filepath.Join(t.TempDir(), goArchiveName("1.26.9"))
	writeArchiveMode(t, path, map[string]string{"go/bin/" + goToolName(): "binary content"}, 0o644)
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading the archive: %v", err)
	}
	newGoSource(t, "1.26.9", body, "")

	if _, _, err := Ensure(goSpec(t), "1.26.9", Options{}); err == nil {
		t.Fatal("an archive whose tool is not executable must fail the install")
	} else if !strings.Contains(err.Error(), "not executable") {
		t.Errorf("the error must name the problem, got: %v", err)
	}
	if fsutil.DirExists(config.VersionDir("go", "1.26.9")) {
		t.Error("the half-installed version directory must be rolled back")
	}
}

func TestAssertExecutable(t *testing.T) {
	// A unit-level companion to the end-to-end test above: the rule itself, both
	// directions, so a regression is pinpointed rather than merely detected.
	spec := goSpec(t)
	dir := t.TempDir()
	bin := filepath.Join(dir, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(bin, goToolName())

	if err := os.WriteFile(p, []byte("tool"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := assertExecutable(spec, dir, "1.27.2"); err != nil {
		t.Errorf("an executable tool must pass on every platform: %v", err)
	}

	if err := os.Chmod(p, 0o644); err != nil {
		t.Fatal(err)
	}
	err := assertExecutable(spec, dir, "1.27.2")
	if platform.IsWindows() {
		if err != nil {
			t.Errorf("the check must be a no-op on Windows, got: %v", err)
		}
		return
	}
	if err == nil {
		t.Fatal("a tool without any execute bit must be rejected")
	}
	if !strings.Contains(err.Error(), "not executable") || !strings.Contains(err.Error(), "0644") {
		t.Errorf("the error must name the problem and the mode, got: %v", err)
	}
}

// The rule behind FR-808, asserted for every OS from any runner: only the
// Unix-likes care, and only about any of the three execute bits.
func TestExecutableFor(t *testing.T) {
	for _, tc := range []struct {
		osName string
		mode   os.FileMode
		want   bool
	}{
		{"windows", 0o644, true},
		{"windows", 0o000, true},
		{"linux", 0o755, true},
		{"linux", 0o700, true},
		{"linux", 0o644, false},
		{"linux", 0o000, false},
		{"darwin", 0o755, true},
		{"darwin", 0o644, false},
	} {
		if got := executableFor(tc.osName, tc.mode); got != tc.want {
			t.Errorf("executableFor(%s, %04o) = %v, want %v", tc.osName, tc.mode, got, tc.want)
		}
	}
	// The host accessor must follow the platform, otherwise the branch the
	// product actually runs stays untested.
	dir := t.TempDir()
	p := filepath.Join(dir, "tool")
	if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := executableFor(platform.OS(), st.Mode()), platform.IsWindows(); got != want {
		t.Errorf("on %s a 0644 file = %v, want %v", platform.OS(), got, want)
	}
}

func TestEnsureWarnsWhenTheSelfCheckFails(t *testing.T) {
	resetSandbox(t)
	// The "tool" is a text file: it exists, so HasBinary is satisfied, but it
	// cannot be executed.
	goSourceWithTool(t, "1.22.0")

	if _, _, err := Ensure(goSpec(t), "1.22.0", Options{}); err != nil {
		t.Fatalf("a failing self-check must not fail the install: %v", err)
	}
	if !fsutil.FileExists(filepath.Join(config.VersionDir("go", "1.22.0"), "bin", goToolName())) {
		t.Error("the install must survive a failing self-check")
	}
}

func TestEnsureAdoptsWhenFromIsGiven(t *testing.T) {
	resetSandbox(t)
	src := filepath.Join(t.TempDir(), "go-1.21.0")
	if err := os.MkdirAll(filepath.Join(src, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "bin", goToolName()), []byte("tool"), 0o644); err != nil {
		t.Fatal(err)
	}

	got, already, err := Ensure(goSpec(t), "1.21.0", Options{From: src})
	if err != nil {
		t.Fatalf("Ensure --from: %v", err)
	}
	if already || got != "1.21.0" {
		t.Errorf("Ensure --from = (%q, %v)", got, already)
	}
	if !fsutil.FileExists(filepath.Join(config.VersionDir("go", "1.21.0"), "bin", goToolName())) {
		t.Error("the adopted directory must be reachable through the version directory")
	}
}

func TestEnsureReportsAPlanItCannotSatisfy(t *testing.T) {
	resetSandbox(t)
	newGoSourceWithToolOnly(t, "1.27.2")

	// The version is exact, so it is not looked up, but no plan exists for it.
	_, _, err := Ensure(goSpec(t), "9.9.9", Options{})
	if err == nil {
		t.Fatal("a version the index does not offer must fail")
	}
	if !strings.Contains(err.Error(), "9.9.9") {
		t.Errorf("error = %v, want it to name the version", err)
	}
}

func TestResolveExact(t *testing.T) {
	resetSandbox(t)
	newGoSourceWithToolOnly(t, "1.27.2")
	spec := goSpec(t)

	for _, tc := range []struct {
		request string
		want    string
	}{
		{"1.27.2", "1.27.2"}, // exact, no index consultation needed
		{"1.27", "1.27.2"},   // a prefix resolves to the newest matching release
		{"latest", "1.27.2"}, // a channel keyword lists everything and picks the newest
		{"", "1.27.2"},
		{"9.9.9", "9.9.9"}, // not in the index, but an exact request is still honoured
	} {
		got, err := resolveExact(spec, tc.request)
		if err != nil {
			t.Errorf("resolveExact(%q): %v", tc.request, err)
			continue
		}
		if got != tc.want {
			t.Errorf("resolveExact(%q) = %q, want %q", tc.request, got, tc.want)
		}
	}

	// A request that is neither exact nor matching must be reported, not guessed.
	_, err := resolveExact(spec, "9.9")
	if err == nil {
		t.Fatal("an unsatisfiable filter must fail")
	}
	if !strings.Contains(err.Error(), "no version matching") {
		t.Errorf("error = %v", err)
	}
}

func TestUninstallRemovesTheVersionDirectory(t *testing.T) {
	resetSandbox(t)
	dir := filepath.Join(config.VersionDir("go", "1.20.0"))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeConfigFile(t, `{"defaults":{"go":"1.20.0"}}`)

	if err := Uninstall("go", "1.20.0"); err != nil {
		t.Fatalf("Uninstall: %v", err)
	}
	if fsutil.DirExists(dir) {
		t.Error("the version directory must be gone")
	}
	// Removing the version that was the default clears the default too.
	if got := config.Load().Defaults["go"]; got != "" {
		t.Errorf("the default must be cleared, got %q", got)
	}
	// A default for another version is left alone.
	if err := os.MkdirAll(config.VersionDir("go", "1.19.0"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeConfigFile(t, `{"defaults":{"go":"1.19.0"}}`)
	if err := Uninstall("go", "1.20.0"); err == nil {
		t.Error("uninstalling something that is not there must fail")
	}
	if got := config.Load().Defaults["go"]; got != "1.19.0" {
		t.Errorf("an unrelated default must survive, got %q", got)
	}
}

func TestUninstallErrors(t *testing.T) {
	resetSandbox(t)

	if err := Uninstall("ruby", "3.3.0"); err == nil {
		t.Error("an unknown runtime must be rejected")
	}
	if err := Uninstall("go", "1.2.3"); err == nil {
		t.Error("a version that is not installed must be rejected")
	}
	// The path guard: nothing outside the versions directory may be deleted,
	// whatever the caller passes as a version.
	err := Uninstall("go", filepath.Join("..", "..", "outside"))
	if err == nil {
		t.Fatal("a version that escapes the versions directory must be refused")
	}
	if !strings.Contains(err.Error(), "refusing to delete") {
		t.Errorf("error = %v, want the refusal message", err)
	}
}

// An adopted version is a link: uninstalling it must drop the link and leave the
// user's own directory untouched.
func TestUninstallKeepsTheSourceOfAnAdoptedVersion(t *testing.T) {
	resetSandbox(t)
	src := filepath.Join(t.TempDir(), "go-1.18.0")
	if err := os.MkdirAll(filepath.Join(src, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "bin", goToolName()), []byte("tool"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := Adopt(goSpec(t), "1.18.0", src, false); err != nil {
		t.Fatalf("Adopt: %v", err)
	}

	if err := Uninstall("go", "1.18.0"); err != nil {
		t.Fatalf("Uninstall: %v", err)
	}
	if !fsutil.DirExists(src) {
		t.Error("the adopted source directory must survive")
	}
	if err := os.WriteFile(filepath.Join(src, "bin", "still-here"), []byte("x"), 0o644); err != nil {
		t.Errorf("the source must still be writable: %v", err)
	}
}

func TestApplyLayout(t *testing.T) {
	t.Run("none leaves the tree alone", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.MkdirAll(filepath.Join(dir, "wrapper"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "wrapper", "tool"), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := applyLayout(runtime.LayoutNone, dir); err != nil {
			t.Fatalf("applyLayout: %v", err)
		}
		if !fsutil.FileExists(filepath.Join(dir, "wrapper", "tool")) {
			t.Error("LayoutNone must not move anything")
		}
	})

	t.Run("flatten lifts a single wrapper", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.MkdirAll(filepath.Join(dir, "wrapper", "bin"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "wrapper", "bin", "tool"), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := applyLayout(runtime.LayoutFlatten, dir); err != nil {
			t.Fatalf("applyLayout: %v", err)
		}
		if !fsutil.FileExists(filepath.Join(dir, "bin", "tool")) {
			t.Error("LayoutFlatten must move the contents up")
		}
		if fsutil.DirExists(filepath.Join(dir, "wrapper")) {
			t.Error("the wrapper must be removed")
		}
	})

	// A1 / REQ-008: Adoptium's macOS archive is a bundle. The real JDK home sits
	// at <wrapper>/Contents/Home, so a single flatten leaves Contents/ where
	// bin/ is expected and the install used to roll back with "not found after
	// extraction" on every macOS machine.
	t.Run("mac-jdk flatten descends into Contents/Home", func(t *testing.T) {
		dir := t.TempDir()
		home := filepath.Join(dir, "jdk-21.0.12.1+1", "Contents", "Home")
		if err := os.MkdirAll(filepath.Join(home, "bin"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(filepath.Join(home, "lib"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(filepath.Join(home, "..", "MacOS"), 0o755); err != nil {
			t.Fatal(err)
		}
		// The tool is created under the name the spec will look for, so the
		// assertion holds on every runner rather than only on the Unix-likes.
		if err := os.WriteFile(filepath.Join(home, "bin", platform.ExeName("java")), []byte("binary"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := applyLayout(runtime.LayoutMacJDK, dir); err != nil {
			t.Fatalf("applyLayout: %v", err)
		}
		if !fsutil.FileExists(filepath.Join(dir, "bin", platform.ExeName("java"))) {
			t.Error("after the mac-jdk layout the JDK home must sit at the version root")
		}
		if fsutil.DirExists(filepath.Join(dir, "Contents")) {
			t.Error("the Contents/ level must be gone")
		}
		if fsutil.DirExists(filepath.Join(dir, "jdk-21.0.12.1+1")) {
			t.Error("the tarball wrapper must be removed")
		}

		// The point of the whole exercise: the spec must now find the tool, so
		// the install neither rolls back nor warns.
		spec := runtime.Lookup("java")
		if spec == nil {
			t.Fatal("java spec missing")
		}
		if !spec.HasBinary(dir, "21.0.12.1+1") {
			t.Error("HasBinary must find bin/java after the mac-jdk layout")
		}
	})

	t.Run("mac-jdk layout still handles a flat archive", func(t *testing.T) {
		// A non-bundle archive (or a future Adoptium layout change) must not be
		// damaged: with no Contents/ the plain flatten stands.
		dir := t.TempDir()
		if err := os.MkdirAll(filepath.Join(dir, "wrapper", "bin"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "wrapper", "bin", "java"), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := applyLayout(runtime.LayoutMacJDK, dir); err != nil {
			t.Fatalf("applyLayout: %v", err)
		}
		if !fsutil.FileExists(filepath.Join(dir, "bin", "java")) {
			t.Error("a flat archive must end up at bin/java as well")
		}
	})

	t.Run("mac-jdk layout keeps an already-normalised tree", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.MkdirAll(filepath.Join(dir, "bin"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "bin", "java"), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "release"), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := applyLayout(runtime.LayoutMacJDK, dir); err != nil {
			t.Fatalf("applyLayout: %v", err)
		}
		if !fsutil.FileExists(filepath.Join(dir, "bin", "java")) {
			t.Error("an already-normalised tree must survive untouched")
		}
	})

	t.Run("nuget keeps tools and drops metadata", func(t *testing.T) {
		dir := t.TempDir()
		for _, p := range []string{"tools/python.exe", "tools/Scripts/pip.exe", "package/one", "_rels/two", "images/three"} {
			full := filepath.Join(dir, filepath.FromSlash(p))
			if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(full, []byte("x"), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		for _, p := range []string{"python.nuspec", "[Content_Types].xml", ".signature.p7s"} {
			if err := os.WriteFile(filepath.Join(dir, p), []byte("x"), 0o644); err != nil {
				t.Fatal(err)
			}
		}

		if err := applyLayout(runtime.LayoutNuGet, dir); err != nil {
			t.Fatalf("applyLayout: %v", err)
		}
		if !fsutil.FileExists(filepath.Join(dir, "python.exe")) {
			t.Error("tools/ contents must move up")
		}
		if !fsutil.FileExists(filepath.Join(dir, "Scripts", "pip.exe")) {
			t.Error("subdirectories must move up with their contents")
		}
		for _, gone := range []string{"tools", "package", "_rels", "images", "python.nuspec", "[Content_Types].xml", ".signature.p7s"} {
			if fsutil.DirExists(filepath.Join(dir, gone)) || fsutil.FileExists(filepath.Join(dir, gone)) {
				t.Errorf("%s must be dropped", gone)
			}
		}
	})

	t.Run("nuget without a tools directory is a no-op", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "keep"), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := applyLayout(runtime.LayoutNuGet, dir); err != nil {
			t.Fatalf("applyLayout: %v", err)
		}
		if !fsutil.FileExists(filepath.Join(dir, "keep")) {
			t.Error("nothing may be removed when there is no tools/ directory")
		}
	})

	t.Run("nuget does not overwrite what is already there", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "python.exe"), []byte("installed"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(filepath.Join(dir, "tools"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "tools", "python.exe"), []byte("from-archive"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := applyLayout(runtime.LayoutNuGet, dir); err != nil {
			t.Fatalf("applyLayout: %v", err)
		}
		data, err := os.ReadFile(filepath.Join(dir, "python.exe"))
		if err != nil {
			t.Fatal(err)
		}
		if string(data) != "installed" {
			t.Errorf("python.exe = %q, want the existing file to win", data)
		}
	})
}

func TestSha256File(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "f")
	if err := os.WriteFile(path, []byte("abc"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Well-known digest for "abc", so the helper is checked against something
	// other than itself.
	got, err := sha256File(path)
	if err != nil {
		t.Fatalf("sha256File: %v", err)
	}
	if got != "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad" {
		t.Errorf("sha256File = %q", got)
	}
	if _, err := sha256File(filepath.Join(dir, "missing")); err == nil {
		t.Error("a missing file must be an error")
	}
}

func TestWarnHelpers(t *testing.T) {
	// An empty leftover directory is not worth warning about.
	empty := t.TempDir()
	if shouldWarnBeforeReplacing(empty) {
		t.Error("an empty directory holds no data to lose")
	}
	// One with files is.
	if err := os.WriteFile(filepath.Join(empty, "file"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !shouldWarnBeforeReplacing(empty) {
		t.Error("a directory with files must warn before it is replaced")
	}
	// A missing path cannot be replaced at all.
	if shouldWarnBeforeReplacing(filepath.Join(empty, "missing")) {
		t.Error("a path that does not exist must not warn")
	}
	if shouldWarnBeforeReplacing(filepath.Join(empty, "file")) {
		t.Error("a file is not a directory")
	}

	// warnIfReplacingUserData must not panic on any of these.
	warnIfReplacingUserData(empty)
	warnIfReplacingUserData(filepath.Join(empty, "missing"))
}

func TestErrorsAreWrappedNotSwallowed(t *testing.T) {
	// resolveExact must not invent a version when the request cannot be satisfied.
	resetSandbox(t)
	newGoSourceWithToolOnly(t, "1.27.2")
	spec := goSpec(t)

	_, err := resolveExact(spec, "definitely-not-a-version")
	if err == nil {
		t.Fatal("a request that names nothing must fail")
	}
	// Notes: the message has to carry the runtime name so a user knows what to fix.
	if !strings.Contains(err.Error(), spec.Name) {
		t.Errorf("error = %v, want it to name the runtime", err)
	}
	// And it must not be one of the sentinel errors this package invents for a
	// successful no-op.
	if errors.Is(err, os.ErrNotExist) {
		t.Errorf("error = %v", err)
	}
}
