package archive

import (
	"archive/tar"
	"compress/gzip"
	"os"
	"path/filepath"
	"testing"
)

type tarEntry struct {
	name     string
	typeflag byte
	linkname string
	body     string
}

func writeTarGz(t *testing.T, path string, entries []tarEntry) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)
	for _, e := range entries {
		hdr := &tar.Header{Name: e.name, Typeflag: e.typeflag, Mode: 0o644}
		if e.typeflag == tar.TypeSymlink {
			hdr.Linkname = e.linkname
		}
		if e.body != "" {
			hdr.Size = int64(len(e.body))
		}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatal(err)
		}
		if e.body != "" {
			if _, err := tw.Write([]byte(e.body)); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
}

// BUG-008: the literal path check does not stop a symlink whose *target* leaves
// the extraction root; a later entry can then be written through it.
func TestExtractTarRejectsEscapingSymlink(t *testing.T) {
	for _, target := range []string{
		`..\..\..\outside`,
		"../../../outside",
		"/etc/passwd",
		`C:\Windows\System32`,
	} {
		dir := t.TempDir()
		arc := filepath.Join(dir, "evil.tar.gz")
		writeTarGz(t, arc, []tarEntry{
			{name: "link", typeflag: tar.TypeSymlink, linkname: target},
			{name: "link/payload.txt", typeflag: tar.TypeReg, body: "pwned"},
		})
		dest := filepath.Join(dir, "out")
		if err := os.MkdirAll(dest, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := Extract(arc, dest); err == nil {
			t.Fatalf("symlink to %q was accepted, want an error", target)
		}
	}
}

func TestExtractTarAcceptsInternalSymlink(t *testing.T) {
	dir := t.TempDir()
	arc := filepath.Join(dir, "ok.tar.gz")
	writeTarGz(t, arc, []tarEntry{
		{name: "bin/tool", typeflag: tar.TypeReg, body: "binary"},
		{name: "bin/alias", typeflag: tar.TypeSymlink, linkname: "tool"},
	})
	dest := filepath.Join(dir, "out")
	if err := Extract(arc, dest); err != nil {
		t.Fatalf("a symlink inside the archive must be allowed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dest, "bin", "tool")); err != nil {
		t.Fatalf("regular entry was not extracted: %v", err)
	}
}

func TestExtractRejectsZipSlip(t *testing.T) {
	dir := t.TempDir()
	arc := filepath.Join(dir, "evil.tar.gz")
	writeTarGz(t, arc, []tarEntry{
		{name: "../escaped.txt", typeflag: tar.TypeReg, body: "pwned"},
	})
	dest := filepath.Join(dir, "out")
	if err := os.MkdirAll(dest, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := Extract(arc, dest); err == nil {
		t.Fatal("a path outside the destination was accepted, want an error")
	}
	if _, err := os.Stat(filepath.Join(dir, "escaped.txt")); !os.IsNotExist(err) {
		t.Fatalf("the file escaped the destination (stat err=%v)", err)
	}
}
