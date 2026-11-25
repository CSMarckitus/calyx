// Package genfiles writes the generated files of an output folder and compares them with it.
// Only the files with one extension, for example ".py", belong to the generator. The paths of
// the files are relative to the folder, with "/" as the separator.
package genfiles

import (
	"bytes"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
)

// Write writes the files to dir and removes the other files with the extension ext there, for
// example the class of a schema that backend.json no longer has.
func Write(files map[string][]byte, dir, ext string) error {
	old, err := existingFiles(dir, ext)
	if err != nil {
		return err
	}
	for _, name := range old {
		if _, ok := files[name]; !ok {
			if err := os.Remove(filepath.Join(dir, name)); err != nil {
				return err
			}
		}
	}
	for name, content := range files {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(path, content, 0o644); err != nil {
			return err
		}
	}
	return nil
}

// Compare returns, in sorted order, the names of the files that differ from the files in dir,
// that are missing from dir, or that are in dir with the extension ext but not generated.
func Compare(files map[string][]byte, dir, ext string) ([]string, error) {
	old, err := existingFiles(dir, ext)
	if err != nil {
		return nil, err
	}
	var stale []string
	for _, name := range old {
		if _, ok := files[name]; !ok {
			stale = append(stale, name)
		}
	}
	for name, want := range files {
		got, err := os.ReadFile(filepath.Join(dir, name))
		if errors.Is(err, fs.ErrNotExist) || (err == nil && !bytes.Equal(got, want)) {
			stale = append(stale, name)
		} else if err != nil {
			return nil, err
		}
	}
	slices.Sort(stale)
	return stale, nil
}

// existingFiles returns the paths of the files with the extension ext in dir. A missing dir
// has no files.
func existingFiles(dir, ext string) ([]string, error) {
	var names []string
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if errors.Is(err, fs.ErrNotExist) && path == dir {
			return filepath.SkipDir
		}
		if err != nil {
			return err
		}
		if !d.IsDir() && filepath.Ext(path) == ext {
			rel, err := filepath.Rel(dir, path)
			if err != nil {
				return err
			}
			names = append(names, filepath.ToSlash(rel))
		}
		return nil
	})
	return names, err
}
