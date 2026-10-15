package fsutil

import (
	"os"
	"path/filepath"
	"testing"
)

// I-28: editors on Windows put a BOM in front of text files, and neither
// strings.TrimSpace nor json.Unmarshal tolerates one. Every text file ployenv
// parses is read through ReadTextFile, so the rule is asserted here once.
func TestReadTextFileDropsALeadingBOM(t *testing.T) {
	dir := t.TempDir()
	write := func(name string, body []byte) string {
		t.Helper()
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, body, 0o644); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}
		return p
	}
	bom := []byte{0xEF, 0xBB, 0xBF}

	t.Run("leading BOM is removed", func(t *testing.T) {
		p := write("bom.env", append(append([]byte{}, bom...), []byte("node=22.11.0\n")...))
		got, err := ReadTextFile(p)
		if err != nil {
			t.Fatalf("ReadTextFile: %v", err)
		}
		if string(got) != "node=22.11.0\n" {
			t.Errorf("content = %q, want %q", got, "node=22.11.0\n")
		}
	})

	t.Run("a file without a BOM is unchanged", func(t *testing.T) {
		p := write("plain.env", []byte("node=22.11.0\n"))
		got, err := ReadTextFile(p)
		if err != nil {
			t.Fatalf("ReadTextFile: %v", err)
		}
		if string(got) != "node=22.11.0\n" {
			t.Errorf("content = %q", got)
		}
	})

	t.Run("only the first BOM goes", func(t *testing.T) {
		// A BOM further in is data, not a mark on the file: dropping it would
		// change a value that legitimately contains one.
		body := append(append(append([]byte{}, bom...), []byte("a=\"")...), append(bom, []byte("x\"\n")...)...)
		p := write("inner.env", body)
		got, err := ReadTextFile(p)
		if err != nil {
			t.Fatalf("ReadTextFile: %v", err)
		}
		want := "a=\"" + string(bom) + "x\"\n"
		if string(got) != want {
			t.Errorf("content = %q, want %q", got, want)
		}
	})

	t.Run("a short file is not sliced", func(t *testing.T) {
		// Two of the three BOM bytes is not a BOM.
		p := write("short.env", []byte{0xEF, 0xBB})
		got, err := ReadTextFile(p)
		if err != nil {
			t.Fatalf("ReadTextFile: %v", err)
		}
		if len(got) != 2 {
			t.Errorf("content = %v, want the two bytes back", got)
		}
	})

	t.Run("an empty file stays empty", func(t *testing.T) {
		p := write("empty.env", nil)
		got, err := ReadTextFile(p)
		if err != nil {
			t.Fatalf("ReadTextFile: %v", err)
		}
		if len(got) != 0 {
			t.Errorf("content = %q, want empty", got)
		}
	})

	t.Run("a missing file reports the error", func(t *testing.T) {
		if _, err := ReadTextFile(filepath.Join(dir, "nope")); err == nil {
			t.Error("a missing file must return an error")
		}
	})
}
