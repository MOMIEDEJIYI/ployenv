package install

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MOMIEDEJIYI/ployenv/internal/config"
	"github.com/MOMIEDEJIYI/ployenv/internal/platform"
)

// TestMain pins the data directory for the whole test binary: config resolves it
// once per process (RootInfo uses sync.Once), so the tests have to agree on one
// root. The path is deliberately not ASCII, the same shape a user with a
// non-English user name has.
func TestMain(m *testing.M) {
	base, err := os.MkdirTemp("", "ployenv-install-test-")
	if err != nil {
		panic(err)
	}
	root := filepath.Join(base, "中文目录🚀")
	if err := os.MkdirAll(root, 0o755); err != nil {
		os.RemoveAll(base)
		panic(err)
	}
	if err := os.Setenv("PLOYENV_ROOT", root); err != nil {
		os.RemoveAll(base)
		panic(err)
	}
	code := m.Run()
	os.RemoveAll(base)
	os.Exit(code)
}

// resetSandbox empties the data directory so a test starts from a known state.
func resetSandbox(t *testing.T) string {
	t.Helper()
	root, _, _ := config.RootInfo()
	if root == "" {
		t.Fatal("the data root must be pinned by TestMain")
	}
	if err := os.RemoveAll(root); err != nil {
		t.Fatalf("clearing the sandbox: %v", err)
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatalf("recreating the sandbox: %v", err)
	}
	return root
}

// writeConfigFile writes config.json the way a user would have it.
func writeConfigFile(t *testing.T, body string) {
	t.Helper()
	path := config.ConfigPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("writing %s: %v", path, err)
	}
}

// goArchiveName is the archive the Go plan looks for on this platform.
func goArchiveName(ver string) string {
	ext := "tar.gz"
	if platform.IsWindows() {
		ext = "zip"
	}
	return fmt.Sprintf("go%s.%s-%s.%s", ver, platform.OS(), platform.Arch(), ext)
}

// goIndexJSON advertises one release with one archive for this platform.
func goIndexJSON(ver, file, sha string) string {
	return fmt.Sprintf(
		`[{"version":"go%s","stable":true,"files":[{"filename":%q,"os":%q,"arch":%q,"kind":"archive","sha256":%q}]}]`,
		ver, file, platform.OS(), platform.Arch(), sha)
}

// writeArchive builds a real archive (zip on Windows, tar.gz elsewhere, which is
// exactly what the Go plan expects) holding files, with the entry names given.
func writeArchive(t *testing.T, dst string, files map[string]string) {
	t.Helper()
	writeArchiveMode(t, dst, files, 0o755)
}

// writeArchiveMode is writeArchive with an explicit Unix file mode, so a test can
// build an archive whose tool lost its execute bit (FR-808). A zip cannot carry
// the mode the way a tar does, so the parameter only affects the tar branch.
func writeArchiveMode(t *testing.T, dst string, files map[string]string, mode int64) {
	t.Helper()
	f, err := os.Create(dst)
	if err != nil {
		t.Fatalf("creating %s: %v", dst, err)
	}
	if platform.IsWindows() {
		zw := zip.NewWriter(f)
		for name, body := range files {
			w, err := zw.Create(name)
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
	} else {
		gz := gzip.NewWriter(f)
		tw := tar.NewWriter(gz)
		for name, body := range files {
			hdr := &tar.Header{Name: name, Mode: mode, Size: int64(len(body))}
			if err := tw.WriteHeader(hdr); err != nil {
				t.Fatalf("tar header %s: %v", name, err)
			}
			if _, err := tw.Write([]byte(body)); err != nil {
				t.Fatalf("tar write %s: %v", name, err)
			}
		}
		if err := tw.Close(); err != nil {
			t.Fatalf("closing the tar: %v", err)
		}
		if err := gz.Close(); err != nil {
			t.Fatalf("closing the gzip: %v", err)
		}
	}
	if err := f.Close(); err != nil {
		t.Fatalf("closing %s: %v", dst, err)
	}
}

func sha256Of(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// goToolName is the Go binary's name on this platform.
func goToolName() string { return platform.ExeName("go") }

// goSource is a local package source: a Go index plus the archive it advertises,
// served from one test server that the configured mirror points at (FR-6).
type goSource struct {
	url     string
	file    string
	sha     string
	archive string
}

// newGoSource serves body as the archive for one Go version and points the
// configured mirror at itself, so an install runs without touching the network.
// advertised may differ from the real digest, which is how the checksum failure
// is exercised.
func newGoSource(t *testing.T, ver string, body []byte, advertised string) *goSource {
	t.Helper()
	file := goArchiveName(ver)
	sha := sha256.Sum256(body)
	real := hex.EncodeToString(sha[:])
	if advertised == "" {
		advertised = real
	}

	archive := filepath.Join(t.TempDir(), file)
	if err := os.WriteFile(archive, body, 0o644); err != nil {
		t.Fatalf("writing the archive: %v", err)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.RawQuery, "mode=json") {
			w.Header().Set("Content-Type", "application/json")
			io.WriteString(w, goIndexJSON(ver, file, advertised))
			return
		}
		if strings.HasSuffix(r.URL.Path, "/"+file) {
			http.ServeFile(w, r, archive)
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(srv.Close)

	writeConfigFile(t, fmt.Sprintf(`{"mirrors":{"go":%q}}`, srv.URL))
	return &goSource{url: srv.URL, file: file, sha: real, archive: archive}
}

// newGoSourceFromFiles builds a real archive holding files and serves it.
func newGoSourceFromFiles(t *testing.T, ver string, files map[string]string) *goSource {
	t.Helper()
	path := filepath.Join(t.TempDir(), goArchiveName(ver))
	writeArchive(t, path, files)
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading the archive: %v", err)
	}
	return newGoSource(t, ver, body, "")
}

// goSourceWithTool is the archive an install of a working runtime expects: the
// tool sits one directory down, which the flatten layout has to move up.
func goSourceWithTool(t *testing.T, ver string) *goSource {
	return newGoSourceFromFiles(t, ver, map[string]string{
		"go/bin/" + goToolName(): "binary content",
		"go/README.md":           "docs",
	})
}

// newGoSourceWithToolOnly serves a single, perfectly good release: enough for the
// resolution tests, which never download anything.
func newGoSourceWithToolOnly(t *testing.T, ver string) *goSource {
	return goSourceWithTool(t, ver)
}

// newGoSourceFromFilesAdvertised serves a real archive while advertising a digest
// it does not have, which is how a corrupted download looks to the installer.
func newGoSourceFromFilesAdvertised(t *testing.T, ver, advertised string) *goSource {
	t.Helper()
	path := filepath.Join(t.TempDir(), goArchiveName(ver))
	writeArchive(t, path, map[string]string{"go/bin/" + goToolName(): "binary content"})
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading the archive: %v", err)
	}
	return newGoSource(t, ver, body, advertised)
}

func cachePath(ver string) string {
	return filepath.Join(config.CacheDir(), goArchiveName(ver))
}
