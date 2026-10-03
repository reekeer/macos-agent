package cache

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestTrimRemovesOldestFirst(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	for i, name := range []string{"a/old", "b/mid", "new"} {
		p := filepath.Join(dir, name)
		os.MkdirAll(filepath.Dir(p), 0o755)
		os.WriteFile(p, make([]byte, 100), 0o644)
		os.Chtimes(p, now, now.Add(time.Duration(i)*time.Hour))
	}
	freed, err := Trim(dir, 150)
	if err != nil || freed != 200 {
		t.Fatalf("freed=%d err=%v", freed, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "new")); err != nil {
		t.Fatal("newest file must stay")
	}
	if _, err := os.Stat(filepath.Join(dir, "a")); !os.IsNotExist(err) {
		t.Fatal("empty dirs must be removed")
	}
}
