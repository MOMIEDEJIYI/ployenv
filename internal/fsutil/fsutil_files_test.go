package fsutil

import (
	"os"
	"path/filepath"
	"testing"
)

func TestExistsHelpers(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "file.txt")
	if err := os.WriteFile(file, []byte("data"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	missing := filepath.Join(dir, "missing")

	if !FileExists(file) {
		t.Error("FileExists(file) must be true")
	}
	if FileExists(dir) {
		t.Error("FileExists(dir) must be false: a directory is not a file")
	}
	if FileExists(missing) {
		t.Error("FileExists(missing) must be false")
	}

	if !DirExists(dir) {
		t.Error("DirExists(dir) must be true")
	}
	if DirExists(file) {
		t.Error("DirExists(file) must be false: a file is not a directory")
	}
	if DirExists(missing) {
		t.Error("DirExists(missing) must be false")
	}
}

func TestHumanBytes(t *testing.T) {
	cases := []struct {
		n    int64
		want string
	}{
		{0, "0 B"},
		{1, "1 B"},
		{1023, "1023 B"},
		{1024, "1.0 KB"},
		{1536, "1.5 KB"},
		{1024 * 1024, "1.0 MB"},
		{3 * 1024 * 1024, "3.0 MB"},
		{1024 * 1024 * 1024, "1.0 GB"},
		{1024 * 1024 * 1024 * 1024, "1.0 TB"},
		{1024 * 1024 * 1024 * 1024 * 1024, "1.0 PB"},
	}
	for _, tc := range cases {
		if got := HumanBytes(tc.n); got != tc.want {
			t.Errorf("HumanBytes(%d) = %q, want %q", tc.n, got, tc.want)
		}
	}
}

func TestCopyFileCopiesBytesAndCreatesParents(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src.bin")
	payload := []byte("ployenv 0.3.3\n")
	if err := os.WriteFile(src, payload, 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	// The destination directory does not exist: CopyFile creates it.
	dst := filepath.Join(dir, "nested", "deeper", "dst.bin")
	if err := CopyFile(src, dst); err != nil {
		t.Fatalf("CopyFile: %v", err)
	}
	got, err := os.ReadFile(dst)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(got) != string(payload) {
		t.Fatalf("copied %q, want %q", got, payload)
	}

	if err := CopyFile(filepath.Join(dir, "missing.bin"), filepath.Join(dir, "out.bin")); err == nil {
		t.Error("CopyFile must fail when the source does not exist")
	}
	if err := CopyFile(src, dir); err == nil {
		t.Error("CopyFile must fail when the destination is a directory")
	}
}

func TestMoveContentsUpFlattensASingleWrapperDirectory(t *testing.T) {
	dir := t.TempDir()
	inner := filepath.Join(dir, "wrapper")
	if err := os.MkdirAll(filepath.Join(inner, "nested"), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(filepath.Join(inner, "tool.exe.txt"), []byte("tool"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if err := os.WriteFile(filepath.Join(inner, "nested", "deep.txt"), []byte("deep"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	if err := MoveContentsUp(dir); err != nil {
		t.Fatalf("MoveContentsUp: %v", err)
	}
	if !FileExists(filepath.Join(dir, "tool.exe.txt")) {
		t.Error("the wrapper's files must move up one level")
	}
	if !FileExists(filepath.Join(dir, "nested", "deep.txt")) {
		t.Error("directories must move up with their contents")
	}
	if DirExists(inner) {
		t.Error("the empty wrapper directory must be removed")
	}
}

func TestMoveContentsUpKeepsEntriesThatAlreadyExist(t *testing.T) {
	dir := t.TempDir()
	inner := filepath.Join(dir, "wrapper")
	if err := os.MkdirAll(inner, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	// The wrapper contains an entry with the wrapper's own name. Flattening it
	// would replace the wrapper itself, so that entry is left behind (and goes
	// away with the wrapper), while everything else still moves up.
	if err := os.WriteFile(filepath.Join(inner, "wrapper"), []byte("collides"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if err := os.WriteFile(filepath.Join(inner, "extra.txt"), []byte("extra"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	if err := MoveContentsUp(dir); err != nil {
		t.Fatalf("MoveContentsUp: %v", err)
	}
	if !FileExists(filepath.Join(dir, "extra.txt")) {
		t.Error("files without a collision must still move up")
	}
	if DirExists(inner) {
		t.Error("the wrapper directory must be removed once its contents moved")
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	if len(entries) != 1 || entries[0].Name() != "extra.txt" {
		t.Fatalf("dir holds %v, want only the moved file", entries)
	}
}

func TestMoveContentsUpIgnoresAnythingElse(t *testing.T) {
	// Two entries: not a wrapper directory, so nothing moves.
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "inner"), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "loose.txt"), []byte("loose"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if err := MoveContentsUp(dir); err != nil {
		t.Fatalf("MoveContentsUp: %v", err)
	}
	if !DirExists(filepath.Join(dir, "inner")) || !FileExists(filepath.Join(dir, "loose.txt")) {
		t.Error("a directory with two entries must be left untouched")
	}

	// A single file is not a wrapper directory either.
	flat := t.TempDir()
	if err := os.WriteFile(filepath.Join(flat, "only.txt"), []byte("only"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if err := MoveContentsUp(flat); err != nil {
		t.Fatalf("MoveContentsUp: %v", err)
	}
	if !FileExists(filepath.Join(flat, "only.txt")) {
		t.Error("a single file must be left untouched")
	}

	if err := MoveContentsUp(filepath.Join(flat, "missing")); err == nil {
		t.Error("MoveContentsUp must fail when the directory does not exist")
	}
}
