package netx

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestPartialNameAndIsPartial(t *testing.T) {
	dest := filepath.Join("cache", "node-22.11.0.zip")
	got := partialName(dest)
	if !strings.HasPrefix(got, dest+".part.") {
		t.Errorf("partialName(%q) = %q, want a %q prefix", dest, got, dest+".part.")
	}
	if !strings.Contains(got, "."+itoa(os.Getpid())) {
		t.Errorf("partialName(%q) = %q, want it to carry the process id (BUG-013)", dest, got)
	}
	for name, want := range map[string]bool{
		"x.zip.part":    true,
		"x.zip.part.42": true,
		"x.zip":         false,
		"part.zip":      false,
		"":              false,
	} {
		if got := isPartial(name); got != want {
			t.Errorf("isPartial(%q) = %v, want %v", name, got, want)
		}
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

func TestPurgePartialsOnlyRemovesStaleFiles(t *testing.T) {
	dir := t.TempDir()
	old := filepath.Join(dir, "old.zip.part")
	oldPID := filepath.Join(dir, "old.zip.part.1234")
	fresh := filepath.Join(dir, "fresh.zip.part")
	regular := filepath.Join(dir, "keep.zip")
	subdir := filepath.Join(dir, "dir.part")
	for _, p := range []string{old, oldPID, fresh, regular} {
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Mkdir(subdir, 0o755); err != nil {
		t.Fatal(err)
	}
	twoHoursAgo := time.Now().Add(-2 * time.Hour)
	for _, p := range []string{old, oldPID, subdir} {
		if err := os.Chtimes(p, twoHoursAgo, twoHoursAgo); err != nil {
			t.Fatal(err)
		}
	}

	PurgePartials(dir)

	for _, gone := range []string{old, oldPID} {
		if _, err := os.Stat(gone); !os.IsNotExist(err) {
			t.Errorf("%s should have been purged (err=%v)", filepath.Base(gone), err)
		}
	}
	for _, kept := range []string{fresh, regular, subdir} {
		if _, err := os.Stat(kept); err != nil {
			t.Errorf("%s must be kept, but stat failed: %v", filepath.Base(kept), err)
		}
	}
}

func TestPurgePartialsIgnoresMissingDirectory(t *testing.T) {
	PurgePartials(filepath.Join(t.TempDir(), "does-not-exist"))
}

func TestDownloadWritesFileAndLeavesNoPartial(t *testing.T) {
	body := strings.Repeat("ployenv-", 1000)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()

	dest := filepath.Join(t.TempDir(), "nested", "dir", "payload.zip")
	if err := Download([]string{srv.URL}, func(base string) string { return base + "/f.zip" }, dest, "payload"); err != nil {
		t.Fatalf("Download returned an error: %v", err)
	}
	got, err := os.ReadFile(dest)
	if err != nil {
		t.Fatalf("the destination file was not created: %v", err)
	}
	if string(got) != body {
		t.Errorf("downloaded %d bytes, want %d", len(got), len(body))
	}
	if left := partialFiles(t, filepath.Dir(dest)); len(left) != 0 {
		t.Errorf("partial files were left behind: %v", left)
	}
}

func TestDownloadFallsBackToTheNextBase(t *testing.T) {
	var first, second int32
	mirror := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&first, 1)
		w.WriteHeader(http.StatusNotFound)
	}))
	defer mirror.Close()
	official := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&second, 1)
		_, _ = w.Write([]byte("from the official source"))
	}))
	defer official.Close()

	dest := filepath.Join(t.TempDir(), "payload.zip")
	err := Download([]string{mirror.URL, official.URL}, func(base string) string { return base + "/f.zip" }, dest, "payload")
	if err != nil {
		t.Fatalf("Download should have used the second base: %v", err)
	}
	if atomic.LoadInt32(&first) == 0 || atomic.LoadInt32(&second) == 0 {
		t.Errorf("expected both bases to be tried, got mirror=%d official=%d", first, second)
	}
	if got, _ := os.ReadFile(dest); string(got) != "from the official source" {
		t.Errorf("destination holds %q", got)
	}
}

func TestDownloadRejectsEmptyBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK) // 200 with no bytes at all
	}))
	defer srv.Close()

	dir := t.TempDir()
	dest := filepath.Join(dir, "empty.zip")
	err := Download([]string{srv.URL}, func(base string) string { return base + "/f.zip" }, dest, "payload")
	if err == nil {
		t.Fatal("an empty download must be an error, not a silent success")
	}
	if !strings.Contains(err.Error(), "empty") {
		t.Errorf("the error should say the download is empty: %v", err)
	}
	if _, serr := os.Stat(dest); !os.IsNotExist(serr) {
		t.Errorf("no destination file may survive a failed download (stat err=%v)", serr)
	}
	if left := partialFiles(t, dir); len(left) != 0 {
		t.Errorf("partial files were left behind: %v", left)
	}
}

func TestDownloadReportsEveryBaseFailing(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer srv.Close()
	// Keep the fallback hermetic: no configured curl and none on PATH, so the
	// error that surfaces is the HTTP one from downloadFile rather than curl's.
	t.Setenv("PLOYENV_CURL", filepath.Join(t.TempDir(), "no-such-curl"))
	t.Setenv("PATH", t.TempDir())

	err := Download([]string{srv.URL}, func(base string) string { return base + "/f.zip" }, filepath.Join(t.TempDir(), "f.zip"), "payload")
	if err == nil {
		t.Fatal("expected an error once every source failed")
	}
	if !strings.Contains(err.Error(), "HTTP 403") {
		t.Errorf("the last error should surface the HTTP status: %v", err)
	}
}

// The curl fallback exists for networks that only allow allow-listed clients.
// A fake curl (the same trick the plugin contract tests use for a fake CLI)
// keeps the test hermetic: it writes the payload to the -o path it is handed.
func TestDownloadFallsBackToCurl(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("the fake curl shim below is a .cmd file (Windows only)")
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer srv.Close()

	dir := t.TempDir()
	fake := filepath.Join(dir, "fake-curl.cmd")
	script := "@echo off\r\n" +
		"set OUT=\r\n" +
		":loop\r\n" +
		"if \"%~1\"==\"\" goto done\r\n" +
		"if \"%~1\"==\"-o\" set OUT=%~2\r\n" +
		"shift\r\n" +
		"goto loop\r\n" +
		":done\r\n" +
		"if \"%OUT%\"==\"\" exit /b 2\r\n" +
		"echo curl-payload> \"%OUT%\"\r\n" +
		"exit /b 0\r\n"
	if err := os.WriteFile(fake, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PLOYENV_CURL", fake)

	dest := filepath.Join(dir, "curl.zip")
	err := Download([]string{srv.URL}, func(base string) string { return base + "/f.zip" }, dest, "payload")
	if err != nil {
		t.Fatalf("Download should have fallen back to curl: %v", err)
	}
	got, rerr := os.ReadFile(dest)
	if rerr != nil {
		t.Fatalf("curl fallback did not produce the destination: %v", rerr)
	}
	if !strings.Contains(string(got), "curl-payload") {
		t.Errorf("destination holds %q, want the fake curl payload", got)
	}
	if left := partialFiles(t, dir); len(left) != 0 {
		t.Errorf("partial files were left behind: %v", left)
	}
}

func TestDownloadViaCurlRejectsEmptyFile(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("the fake curl shim below is a .cmd file (Windows only)")
	}
	dir := t.TempDir()
	fake := filepath.Join(dir, "empty-curl.cmd")
	script := "@echo off\r\n" +
		"set OUT=\r\n" +
		":loop\r\n" +
		"if \"%~1\"==\"\" goto done\r\n" +
		"if \"%~1\"==\"-o\" set OUT=%~2\r\n" +
		"shift\r\n" +
		"goto loop\r\n" +
		":done\r\n" +
		"type nul > \"%OUT%\"\r\n" +
		"exit /b 0\r\n"
	if err := os.WriteFile(fake, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PLOYENV_CURL", fake)

	dest := filepath.Join(dir, "empty.zip")
	err := downloadViaCurl(fake, "https://example.invalid/f.zip", dest, "payload")
	if err == nil {
		t.Fatal("an empty curl download must be an error")
	}
	if !strings.Contains(err.Error(), "empty") {
		t.Errorf("the error should say the download is empty: %v", err)
	}
	if left := partialFiles(t, dir); len(left) != 0 {
		t.Errorf("partial files were left behind: %v", left)
	}
}

func TestFindCurlPrefersTheConfiguredPath(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "my-curl.exe")
	if err := os.WriteFile(exe, []byte("x"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PLOYENV_CURL", exe)
	if got := findCurl(); got != exe {
		t.Errorf("findCurl() = %q, want the configured %q", got, exe)
	}
	// a configured path that does not exist must not be used
	t.Setenv("PLOYENV_CURL", filepath.Join(dir, "absent.exe"))
	got := findCurl()
	if got == filepath.Join(dir, "absent.exe") {
		t.Error("findCurl returned a configured path that does not exist")
	}
}

func partialFiles(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("cannot read %s: %v", dir, err)
	}
	var out []string
	for _, e := range entries {
		if !e.IsDir() && isPartial(e.Name()) {
			out = append(out, e.Name())
		}
	}
	return out
}
