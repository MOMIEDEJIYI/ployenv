package install

import (
	"archive/tar"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MOMIEDEJIYI/ployenv/internal/config"
	"github.com/MOMIEDEJIYI/ployenv/internal/fsutil"
	"github.com/MOMIEDEJIYI/ployenv/internal/platform"
	"github.com/MOMIEDEJIYI/ployenv/internal/runtime"
)

const rustTestVersion = "1.99.1"

func rustTestTriple() string {
	switch platform.OS() {
	case "windows":
		if platform.Arch() == "arm64" {
			return "aarch64-pc-windows-msvc"
		}
		return "x86_64-pc-windows-msvc"
	case "darwin":
		if platform.Arch() == "arm64" {
			return "aarch64-apple-darwin"
		}
		return "x86_64-apple-darwin"
	default:
		if platform.Arch() == "arm64" {
			return "aarch64-unknown-linux-gnu"
		}
		return "x86_64-unknown-linux-gnu"
	}
}

// writeRustTarball builds one component the way rust-installer does: everything
// under a single <component>-<version>-<triple>/ directory, together with the
// installer's own metadata files.
func writeRustTarball(t *testing.T, dst, top string, files map[string]string) {
	t.Helper()
	f, err := os.Create(dst)
	if err != nil {
		t.Fatalf("creating %s: %v", dst, err)
	}
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)
	for name, body := range files {
		hdr := &tar.Header{Name: top + "/" + name, Mode: 0o755, Size: int64(len(body))}
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
	if err := f.Close(); err != nil {
		t.Fatalf("closing %s: %v", dst, err)
	}
}

// rustSource serves the stable manifest plus the three component archives and
// their .sha256 sidecars, and points the rust mirror at itself.
func rustSource(t *testing.T, tamperCargo bool) string {
	t.Helper()
	triple := rustTestTriple()
	exe := ""
	if platform.IsWindows() {
		exe = ".exe"
	}
	work := t.TempDir()
	manifest := fmt.Sprintf("manifest-version = \"2\"\ndate = \"2026-10-13\"\n\n[pkg.rustc]\nversion = \"%s (58cf8f9c3 2026-10-13)\"\n", rustTestVersion)

	// The real archives nest the payload one level below the wrapper:
	// <component>-<version>-<triple>/<component>/bin/..., with rust-installer's
	// metadata beside it. A fixture that skips that level would pass a layout
	// that cannot install the real thing (2026-10-21 review).
	bodies := map[string]map[string]string{
		"rustc": {
			"rustc/bin/rustc" + exe:           "rustc\n",
			"rustc/bin/rustdoc" + exe:         "rustdoc\n",
			"rustc/lib/rustlib/components":    "components\n",
			"rustc/share/doc/rust/html/index": "docs\n",
			"install.sh":                      "#!/bin/sh\n",
			"components":                      "rustc\n",
			"rust-installer-version":          "3\n",
			"version":                         "1.99.1\n",
		},
		"cargo": {
			"cargo/bin/cargo" + exe:  "cargo\n",
			"cargo/manifest.in":      "manifest\n",
			"install.sh":             "#!/bin/sh\n",
			"components":             "cargo\n",
			"rust-installer-version": "3\n",
		},
		"rust-std": {
			"rust-std-" + triple + "/lib/rustlib/" + triple + "/lib/libstd.rlib": "rlib\n",
			"install.sh":             "#!/bin/sh\n",
			"components":             "rust-std\n",
			"rust-installer-version": "3\n",
		},
	}

	shas := map[string]string{}
	for comp, files := range bodies {
		top := fmt.Sprintf("%s-%s-%s", comp, rustTestVersion, triple)
		name := top + ".tar.gz"
		path := filepath.Join(work, name)
		writeRustTarball(t, path, top, files)
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("reading %s: %v", name, err)
		}
		sum := sha256.Sum256(data)
		shas[name] = hex.EncodeToString(sum[:])
	}
	if tamperCargo {
		cargo := fmt.Sprintf("cargo-%s-%s.tar.gz", rustTestVersion, triple)
		shas[cargo] = strings.Repeat("0", 64)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		base := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/"), ".sha256")
		switch {
		case r.URL.Path == "/channel-rust-stable.toml":
			fmt.Fprint(w, manifest)
		case shas[base] != "" && strings.HasSuffix(r.URL.Path, ".sha256"):
			fmt.Fprintf(w, "%s  %s\n", shas[base], base)
		case shas[base] != "":
			http.ServeFile(w, r, filepath.Join(work, base))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)

	writeConfigFile(t, fmt.Sprintf(`{"mirrors":{"rust":%q}}`, srv.URL))
	return srv.URL
}

func TestEnsureMergesTheRustComponents(t *testing.T) {
	resetSandbox(t)
	rustSource(t, false)

	got, already, err := Ensure(runtime.Lookup("rust"), rustTestVersion, Options{})
	if err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	if already || got != rustTestVersion {
		t.Fatalf("Ensure = (%q, %v), want (%s, false)", got, already, rustTestVersion)
	}
	dir := config.VersionDir("rust", rustTestVersion)
	exe := ""
	if platform.IsWindows() {
		exe = ".exe"
	}

	// The merge is the point: rustc and cargo both carry bin/, and a flatten that
	// kept the first directory would lose cargo entirely.
	for _, tool := range []string{"rustc" + exe, "rustdoc" + exe, "cargo" + exe} {
		if !fsutil.FileExists(filepath.Join(dir, "bin", tool)) {
			t.Errorf("%s is missing from %s", tool, filepath.Join(dir, "bin"))
		}
	}
	if !fsutil.FileExists(filepath.Join(dir, "lib", "rustlib", rustTestTriple(), "lib", "libstd.rlib")) {
		t.Error("the rust-std library is missing after the merge")
	}
	if !fsutil.DirExists(filepath.Join(dir, "share", "doc", "rust", "html")) {
		t.Error("the rustc share tree is missing after the merge")
	}
	for _, gone := range []string{"rustc-", "cargo-", "rust-std-"} {
		entries, err := os.ReadDir(dir)
		if err != nil {
			t.Fatalf("reading %s: %v", dir, err)
		}
		for _, e := range entries {
			if strings.HasPrefix(e.Name(), gone) {
				t.Errorf("component directory %q survived the merge", e.Name())
			}
		}
	}
	for _, meta := range []string{"install.sh", "components", "manifest.in", "rust-installer-version", "version", "cargo"} {
		if fsutil.FileExists(filepath.Join(dir, meta)) {
			t.Errorf("installer metadata %q must not survive", meta)
		}
	}
	if !runtime.Lookup("rust").HasBinary(dir, got) {
		t.Errorf("%s must hold rustc under bin/", dir)
	}
}

func TestEnsureRejectsATamperedRustComponent(t *testing.T) {
	resetSandbox(t)
	rustSource(t, true)

	_, _, err := Ensure(runtime.Lookup("rust"), rustTestVersion, Options{})
	if err == nil {
		t.Fatal("a checksum mismatch in one component must fail the install")
	}
	if !strings.Contains(err.Error(), "checksum mismatch") {
		t.Errorf("error = %v, want it to name the checksum check", err)
	}
	if fsutil.DirExists(config.VersionDir("rust", rustTestVersion)) {
		t.Error("nothing may be registered when a component was rejected")
	}
	// The archive that failed verification is dropped; the components that were
	// already verified stay cached, because re-downloading 130 MB to retry would
	// be a punishment for a corrupt download the user did not cause.
	entries, err := os.ReadDir(config.CacheDir())
	if err != nil && !os.IsNotExist(err) {
		t.Fatalf("reading the cache: %v", err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "cargo-") {
			t.Errorf("cache kept the rejected archive %q", e.Name())
		}
	}
}
