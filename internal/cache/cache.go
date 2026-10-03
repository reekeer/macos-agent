package cache

import (
	"io/fs"
	"os"
	"path/filepath"
	"sort"
)

type entry struct {
	path string
	size int64
	mod  int64
}

func Size(dir string) int64 {
	var total int64
	filepath.WalkDir(dir, func(_ string, d fs.DirEntry, err error) error {
		if err == nil && d.Type().IsRegular() {
			if info, err := d.Info(); err == nil {
				total += info.Size()
			}
		}
		return nil
	})
	return total
}

func Trim(dir string, budget int64) (int64, error) {
	var files []entry
	var total int64
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || !d.Type().IsRegular() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		files = append(files, entry{path: path, size: info.Size(), mod: info.ModTime().UnixNano()})
		total += info.Size()
		return nil
	})
	if err != nil {
		return 0, err
	}
	if total <= budget {
		return 0, nil
	}
	sort.Slice(files, func(i, j int) bool { return files[i].mod < files[j].mod })
	var freed int64
	for _, f := range files {
		if total-freed <= budget {
			break
		}
		if os.Remove(f.path) == nil {
			freed += f.size
		}
	}
	pruneEmpty(dir)
	return freed, nil
}

func pruneEmpty(root string) {
	var dirs []string
	filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err == nil && d.IsDir() && path != root {
			dirs = append(dirs, path)
		}
		return nil
	})
	sort.Slice(dirs, func(i, j int) bool { return len(dirs[i]) > len(dirs[j]) })
	for _, d := range dirs {
		os.Remove(d)
	}
}
