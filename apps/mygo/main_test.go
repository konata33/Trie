package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Run from source, the window opens a copy of the library, never the real
// one; built as Trie.app, it opens the real one unless told another.
func TestDataDir(t *testing.T) {
	home := t.TempDir()
	real := filepath.Join(home, "Library", "Application Support", "Trie")
	if err := os.MkdirAll(real, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(home, "link")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	for _, refused := range []string{"", "relative/dir", real, real + "/", strings.ToLower(real), filepath.Join(real, "sub"), link,
		filepath.Join(real, "..", "Trie")} {
		if _, err := dataDir(refused, home, false); err == nil {
			t.Errorf("%q was taken", refused)
		}
	}
	copy := filepath.Join(home, "copy")
	for _, release := range []bool{false, true} {
		if dir, err := dataDir(copy+"/", home, release); err != nil || dir != copy {
			t.Errorf("a copy, release %v: %q, %v", release, dir, err)
		}
	}
	if dir, err := dataDir("", home, true); err != nil || dir != real {
		t.Errorf("Trie.app: %q, %v", dir, err)
	}
	if _, err := dataDir(real, home, true); err == nil {
		t.Error("TRIE_DATA_DIR named the real library and was taken")
	}
}
