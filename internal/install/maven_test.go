package install

import (
	"crypto/sha512"
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
	"github.com/MOMIEDEJIYI/ployenv/internal/fsutil"
	"github.com/MOMIEDEJIYI/ployenv/internal/platform"
	"github.com/MOMIEDEJIYI/ployenv/internal/runtime"
)

// mavenTestMetadata is the shape Maven Central serves. <latest>/<release> point
// at a release candidate on purpose: the version asked for below must still win.
const mavenTestMetadata = `<?xml version="1.0" encoding="UTF-8"?>
<metadata>
  <versioning>
    <latest>4.0.0-rc-7</latest>
    <release>4.0.0-rc-7</release>
    <versions><version>3.10.0</version></versions>
  </versioning>
</metadata>`

func mavenTestArchiveName(ver string) string {
	if platform.IsWindows() {
		return "apache-maven-" + ver + "-bin.zip"
	}
	return "apache-maven-" + ver + "-bin.tar.gz"
}

// mavenSource serves a real Maven archive plus its .sha512 sidecar, and points the
// configured mirror at itself. A wrong sidecar is how a corrupted or tampered
// download looks to the installer, and it is the only integrity check Maven
// publishes (Central answers 404 for .sha256).
func mavenSource(t *testing.T, advertiseTheRealDigest bool) string {
	t.Helper()
	const ver = "3.10.0"
	file := mavenTestArchiveName(ver)

	archive := filepath.Join(t.TempDir(), file)
	writeArchive(t, archive, map[string]string{
		"apache-maven-" + ver + "/bin/mvn":                   "#!/bin/sh\n",
		"apache-maven-" + ver + "/bin/mvn.cmd":               "@echo off\r\n",
		"apache-maven-" + ver + "/conf/settings.xml":         "<settings/>\n",
		"apache-maven-" + ver + "/lib/maven-core-3.10.0.jar": "jar\n",
	})
	body, err := os.ReadFile(archive)
	if err != nil {
		t.Fatalf("reading the archive: %v", err)
	}

	digest := sha512.Sum512(body)
	published := hex.EncodeToString(digest[:])
	if !advertiseTheRealDigest {
		published = strings.Repeat("0", 128)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/maven-metadata.xml":
			io.WriteString(w, mavenTestMetadata)
		case strings.HasSuffix(r.URL.Path, "/binaries/"+file+".sha512"):
			fmt.Fprintf(w, "%s  %s\n", published, file)
		case strings.HasSuffix(r.URL.Path, "/binaries/"+file):
			http.ServeFile(w, r, archive)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)

	writeConfigFile(t, fmt.Sprintf(`{"mirrors":{"maven":%q}}`, srv.URL))
	return srv.URL
}

func TestEnsureVerifiesTheMavenSHA512Sidecar(t *testing.T) {
	resetSandbox(t)
	mavenSource(t, true)

	got, already, err := Ensure(runtime.Lookup("maven"), "3.10.0", Options{})
	if err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	if already || got != "3.10.0" {
		t.Fatalf("Ensure = (%q, %v), want (3.10.0, false)", got, already)
	}
	dir := config.VersionDir("maven", "3.10.0")
	if !fsutil.DirExists(dir) {
		t.Fatalf("%s must exist after a successful install", dir)
	}
	// The wrapper directory is gone, so bin/ sits at the version root.
	if fsutil.DirExists(filepath.Join(dir, "apache-maven-3.10.0")) {
		t.Error("the archive's top-level directory must not survive the flatten")
	}
	if !runtime.Lookup("maven").HasBinary(dir, got) {
		t.Errorf("%s must hold the mvn wrapper", dir)
	}
}

func TestEnsureRejectsATamperedMavenSHA512(t *testing.T) {
	resetSandbox(t)
	mavenSource(t, false)

	_, _, err := Ensure(runtime.Lookup("maven"), "3.10.0", Options{})
	if err == nil {
		t.Fatal("a SHA-512 mismatch must fail the install")
	}
	if !strings.Contains(err.Error(), "SHA-512 mismatch") {
		t.Errorf("error = %v, want it to name the SHA-512 check", err)
	}
	if fsutil.DirExists(config.VersionDir("maven", "3.10.0")) {
		t.Error("nothing may be registered when the download was rejected")
	}
	if _, serr := os.Stat(filepath.Join(config.CacheDir(), mavenTestArchiveName("3.10.0"))); !os.IsNotExist(serr) {
		t.Error("the unverified archive must be removed")
	}
}

// The index advertises a release candidate everywhere; asking for latest must
// still land on the newest stable 3.x.
func TestEnsureResolvesMavenLatestToAStableRelease(t *testing.T) {
	resetSandbox(t)
	mavenSource(t, true)

	got, _, err := Ensure(runtime.Lookup("maven"), "latest", Options{})
	if err != nil {
		t.Fatalf("Ensure(latest): %v", err)
	}
	if got != "3.10.0" {
		t.Errorf("latest resolved to %q, want 3.10.0", got)
	}
}
