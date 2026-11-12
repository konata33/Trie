package reader

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
)

var (
	fixtureOnce     sync.Once
	fixtureArticles []*Article
	fixtureErr      error
)

// fixture loads the exported articles, or skips the test where they are
// not: TRIE_FIXTURE, else .context/mygo/fixtures/articles.json.
func fixture(t testing.TB) []*Article {
	t.Helper()
	path := os.Getenv("TRIE_FIXTURE")
	if path == "" {
		path = filepath.Join("..", "..", "..", "..", ".context", "mygo", "fixtures", "articles.json")
	}
	if _, err := os.Stat(path); err != nil {
		t.Skipf("no fixture at %s", path)
	}
	fixtureOnce.Do(func() { fixtureArticles, fixtureErr = LoadFixture(path) })
	if fixtureErr != nil {
		t.Fatal(fixtureErr)
	}
	return fixtureArticles
}
