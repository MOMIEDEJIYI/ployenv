package install

import (
	"archive/zip"
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"net/http"
	"net/http/httptest"

	"github.com/MOMIEDEJIYI/ployenv/internal/config"
	"github.com/MOMIEDEJIYI/ployenv/internal/fsutil"
	"github.com/MOMIEDEJIYI/ployenv/internal/platform"
	"github.com/MOMIEDEJIYI/ployenv/internal/runtime"
)

const adbTestVersion = "37.0.1"

// adbTestTarget mirrors the repository's two platform names: <host-os> in the
// index and the archive suffix in the file name.
func adbTestTarget() (archiveOS, hostOS string) {
	switch platform.OS() {
	case "windows":
		return "win", "windows"
	case "darwin":
		return "darwin", "macosx"
	default:
		return "linux", "linux"
	}
}

func adbTestArchiveName() string {
	archiveOS, _ := adbTestTarget()
	return "platform-tools_r" + adbTestVersion + "-" + archiveOS + ".zip"
}

func adbTestRepository(url, sum string) string {
	_, hostOS := adbTestTarget()
	return fmt.Sprintf(`<?xml version="1.0" encoding="utf-8"?>
<sdk:sdk-repository xmlns:sdk="http://schemas.android.com/sdk/android/repo/repository2/03">
  <remotePackage path="platform-tools">
    <revision>
      <major>37</major>
      <minor>0</minor>
      <micro>1</micro>
    </revision>
    <archives>
      <archive>
        <complete>
          <size>1</size>
          <checksum type="sha1">%s</checksum>
          <url>%s</url>
        </complete>
        <host-os>%s</host-os>
      </archive>
    </archives>
  </remotePackage>
</sdk:sdk-repository>`, sum, url, hostOS)
}

// writeADBZip builds the .zip platform-tools really ships on every platform -
// a single folder whose tools are executable.
func writeADBZip(t *testing.T, dst string, files map[string]string) {
	t.Helper()
	f, err := os.Create(dst)
	if err != nil {
		t.Fatalf("creating %s: %v", dst, err)
	}
	zw := zip.NewWriter(f)
	for name, body := range files {
		hdr := &zip.FileHeader{Name: name, Method: zip.Deflate}
		hdr.SetMode(0o755)
		w, err := zw.CreateHeader(hdr)
		if err != nil {
			t.Fatalf("zip entry %s: %v", name, err)
		}
		if _, err := w.Write([]byte(body)); err != nil {
			t.Fatalf("zip write %s: %v", name, err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("closing the zip: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("closing %s: %v", dst, err)
	}
}

// adbSource serves a platform-tools archive plus the repository index that
// carries its SHA-1, and points the adb mirror at itself.
func adbSource(t *testing.T, advertiseTheRealDigest bool) string {
	t.Helper()
	name := adbTestArchiveName()
	exe := "adb"
	if platform.IsWindows() {
		exe = "adb.exe"
	}
	archive := filepath.Join(t.TempDir(), name)
	writeADBZip(t, archive, map[string]string{
		"platform-tools/" + exe:            "binary\n",
		"platform-tools/AdbWinApi.dll":     "dll\n",
		"platform-tools/source.properties": "Pkg.UserSrc=false\nPkg.Revision=" + adbTestVersion + "\n",
		"platform-tools/package.xml":       "<package/>\n",
	})

	body, err := os.ReadFile(archive)
	if err != nil {
		t.Fatalf("reading the archive: %v", err)
	}
	digest := sha1.Sum(body)
	published := hex.EncodeToString(digest[:])
	if !advertiseTheRealDigest {
		published = strings.Repeat("0", 40)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repository2-3.xml":
			fmt.Fprint(w, adbTestRepository(name, published))
		case "/" + name:
			http.ServeFile(w, r, archive)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)

	writeConfigFile(t, fmt.Sprintf(`{"mirrors":{"adb":%q}}`, srv.URL))
	return srv.URL
}

func TestEnsureVerifiesTheADBSHA1(t *testing.T) {
	resetSandbox(t)
	adbSource(t, true)

	got, already, err := Ensure(runtime.Lookup("adb"), adbTestVersion, Options{})
	if err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	if already || got != adbTestVersion {
		t.Fatalf("Ensure = (%q, %v), want (%s, false)", got, already, adbTestVersion)
	}
	dir := config.VersionDir("adb", adbTestVersion)
	if !fsutil.DirExists(dir) {
		t.Fatalf("%s must exist after a successful install", dir)
	}
	if fsutil.DirExists(filepath.Join(dir, "platform-tools")) {
		t.Error("the archive's top-level directory must not survive the flatten")
	}
	if !runtime.Lookup("adb").HasBinary(dir, got) {
		t.Errorf("%s must hold adb at the version root", dir)
	}
}

func TestEnsureRejectsATamperedADBSHA1(t *testing.T) {
	resetSandbox(t)
	adbSource(t, false)

	_, _, err := Ensure(runtime.Lookup("adb"), adbTestVersion, Options{})
	if err == nil {
		t.Fatal("a SHA-1 mismatch must fail the install")
	}
	if !strings.Contains(err.Error(), "SHA-1 mismatch") {
		t.Errorf("error = %v, want it to name the SHA-1 check", err)
	}
	if fsutil.DirExists(config.VersionDir("adb", adbTestVersion)) {
		t.Error("nothing may be registered when the download was rejected")
	}
	if _, serr := os.Stat(filepath.Join(config.CacheDir(), adbTestArchiveName())); !os.IsNotExist(serr) {
		t.Error("the unverified archive must be removed")
	}
}

// The repository carries one revision, so `latest` has only one answer.
func TestEnsureResolvesADBLatest(t *testing.T) {
	resetSandbox(t)
	adbSource(t, true)

	got, _, err := Ensure(runtime.Lookup("adb"), "latest", Options{})
	if err != nil {
		t.Fatalf("Ensure(latest): %v", err)
	}
	if got != adbTestVersion {
		t.Errorf("adb@latest resolved to %q, want %s", got, adbTestVersion)
	}
}
