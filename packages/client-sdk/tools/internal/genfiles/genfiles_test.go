package genfiles

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func checkStale(t *testing.T, files map[string][]byte, dir string, want ...string) {
	t.Helper()
	stale, err := Compare(files, dir, ".py")
	if err != nil || !slices.Equal(stale, want) {
		t.Fatalf("got %v, %v; want %v", stale, err, want)
	}
}

func TestCompareAndWrite(t *testing.T) {
	files := map[string][]byte{"a.py": []byte("a"), "model/b.py": []byte("b"), "model/c.py": []byte("c"), "empty.py": {}}
	dir := filepath.Join(t.TempDir(), "generated")

	// A missing folder has no files, and Compare does not make it.
	checkStale(t, files, dir, "a.py", "empty.py", "model/b.py", "model/c.py")
	if _, err := os.Stat(dir); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("Compare made the folder: %v", err)
	}
	if err := Write(files, dir, ".py"); err != nil {
		t.Fatal(err)
	}
	checkStale(t, files, dir)

	for _, name := range []string{"a.py", "model/old.py", "notes.txt"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("old"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Remove(filepath.Join(dir, "model/c.py")); err != nil {
		t.Fatal(err)
	}
	checkStale(t, files, dir, "a.py", "model/c.py", "model/old.py")

	// Write removes the extra file, and keeps a file with another extension.
	if err := Write(files, dir, ".py"); err != nil {
		t.Fatal(err)
	}
	checkStale(t, files, dir)
	if _, err := os.Stat(filepath.Join(dir, "model/old.py")); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("model/old.py is still there: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "notes.txt")); err != nil {
		t.Errorf("Write removed notes.txt: %v", err)
	}
}
