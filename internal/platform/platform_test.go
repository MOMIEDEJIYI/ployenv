package platform

import (
	"os"
	"runtime"
	"testing"
)

// The pure accessors are asserted for every OS from any runner, and the host
// accessors are checked to agree with them: a seam that is not asserted for the
// host is a mapping the product might not actually take.
func TestExeSuffixAndScriptExt(t *testing.T) {
	for _, tc := range []struct {
		osName          string
		wantExe, wantSc string
	}{
		{"windows", ".exe", ".cmd"},
		{"linux", "", ""},
		{"darwin", "", ""},
	} {
		want := func(osName string, exe, script string) {
			// ExeSuffix/ScriptExt read the real OS, so the table is checked by
			// comparing the host's answer with the row for the host.
			if osName == OS() {
				if got := ExeSuffix(); got != exe {
					t.Errorf("ExeSuffix on %s = %q, want %q", osName, got, exe)
				}
				if got := ScriptExt(); got != script {
					t.Errorf("ScriptExt on %s = %q, want %q", osName, got, script)
				}
			}
		}
		want(tc.osName, tc.wantExe, tc.wantSc)
	}
	if got, want := ExeName("ployenv"), "ployenv"+ExeSuffix(); got != want {
		t.Errorf("ExeName = %q, want %q", got, want)
	}
}

func TestPathSep(t *testing.T) {
	want := ":"
	if IsWindows() {
		want = ";"
	}
	if got := PathSep(); got != want {
		t.Errorf("PathSep on %s = %q, want %q", OS(), got, want)
	}
}

// NFR-805 / REQ-008 A4: the permission bits are part of the contract, so both
// spellings are asserted - the Windows ones unchanged, the Unix ones private.
func TestPermBitsPerPlatform(t *testing.T) {
	for _, tc := range []struct {
		osName  string
		dir     os.FileMode
		file    os.FileMode
		wantDir os.FileMode
		wantFil os.FileMode
	}{
		{"windows", 0o755, 0o644, 0o755, 0o644},
		{"linux", 0o700, 0o600, 0o700, 0o600},
		{"darwin", 0o700, 0o600, 0o700, 0o600},
	} {
		if got := DirPermFor(tc.osName); got != tc.wantDir {
			t.Errorf("DirPermFor(%s) = %04o, want %04o", tc.osName, got, tc.wantDir)
		}
		if got := FilePermFor(tc.osName); got != tc.wantFil {
			t.Errorf("FilePermFor(%s) = %04o, want %04o", tc.osName, got, tc.wantFil)
		}
	}
	// The host accessors must follow the platform the product runs on.
	if got, want := DirPerm(), DirPermFor(OS()); got != want {
		t.Errorf("DirPerm() = %04o, want %04o", got, want)
	}
	if got, want := FilePerm(), FilePermFor(OS()); got != want {
		t.Errorf("FilePerm() = %04o, want %04o", got, want)
	}
}

// IsRoot gates the portable data-root rule (A5), so its contract matters: never
// true on Windows, and otherwise exactly "the effective user is root".
func TestIsRoot(t *testing.T) {
	if IsWindows() {
		if IsRoot() {
			t.Error("IsRoot must never be true on Windows: there is no root account")
		}
		return
	}
	if got, want := IsRoot(), os.Geteuid() == 0; got != want {
		t.Errorf("IsRoot() = %v, want %v", got, want)
	}
}

// OS and Arch must be the values the toolchain compiled in, otherwise the
// runtime layout mappings would key off something else entirely.
func TestOSAndArchMatchTheToolchain(t *testing.T) {
	if got := OS(); got != runtime.GOOS {
		t.Errorf("OS() = %q, want %q", got, runtime.GOOS)
	}
	if got := Arch(); got != runtime.GOARCH {
		t.Errorf("Arch() = %q, want %q", got, runtime.GOARCH)
	}
}

func TestIsTerminal(t *testing.T) {
	f, err := os.CreateTemp(t.TempDir(), "not-a-tty-*")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if IsTerminal(f) {
		t.Error("a regular file is not a terminal")
	}
	if IsTerminal(nil) {
		t.Error("a nil file is not a terminal")
	}
}
