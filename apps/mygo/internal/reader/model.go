// Package reader shows an article's cleaned body (library.ArticleContent)
// as native MyGo elements.
package reader

import (
	"encoding/json"
	"fmt"
	"os"
)

// Article is one article of the fixture: what the reader shows above the
// body, and its content, decoded when it is opened.
type Article struct {
	ID          string
	Title       string
	URL         string
	Authors     []string
	PublishedAt string
	ModifiedAt  string
	SourceName  string
	content     string
}

// Content is ArticleContent, the fields the reader uses.
type Content struct {
	Status          string      `json:"status"`
	HTML            string      `json:"html"`
	Message         string      `json:"message"`
	FeedVersion     string      `json:"feedVersion"`
	FetchedAt       string      `json:"fetchedAt"`
	FormatVersion   int         `json:"formatVersion"`
	CodeBlocks      []CodeBlock `json:"codeBlocks"`
	HighlightStyles []string    `json:"highlightStyles"`
	Document        *Document   `json:"document"`
}

// CodeBlock is one of content.codeBlocks: the code of a pre, as copied.
type CodeBlock struct {
	ID       string `json:"id"`
	Language string `json:"language"`
	Code     string `json:"code"`
}

// Document is the stored reader document (library.IsReaderDocument): the
// body as a hast tree whose blocks carry reader unit IDs.
type Document struct {
	Version         int                    `json:"version"`
	ContentRevision string                 `json:"contentRevision"`
	Root            node                   `json:"root"`
	Units           []Unit                 `json:"units"`
	CodeTokens      map[string][]codeToken `json:"codeTokens"`
}

// Unit is a ReaderUnit: a block a clip or a reading position points at.
type Unit struct {
	ID     string `json:"id"`
	Kind   string `json:"kind"`
	Text   string `json:"text"`
	Anchor string `json:"anchor"`
}

type codeToken struct {
	Text      string `json:"text"`
	ClassName string `json:"className"`
}

// node is a hast node, and what parseHTML makes of the HTML of articles
// stored without a document.
type node struct {
	Type       string         `json:"type"`
	Tag        string         `json:"tagName"`
	Value      string         `json:"value"`
	Properties map[string]any `json:"properties"`
	Children   []*node        `json:"children"`
}

func (n *node) prop(name string) string {
	switch v := n.Properties[name].(type) {
	case string:
		return v
	case float64:
		return fmt.Sprint(v)
	case []any:
		if len(v) > 0 {
			s, _ := v[0].(string)
			return s
		}
	}
	return ""
}

func (n *node) num(name string) int {
	var v int
	fmt.Sscan(n.prop(name), &v)
	return v
}

// LoadFixture reads the exported articles (.context/mygo/fixtures/
// articles.json): an array of {id, article, source, content}, each a JSON
// string. Contents stay encoded until Content asks for one.
func LoadFixture(path string) ([]*Article, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var rows []struct{ ID, Article, Source, Content string }
	if err := json.Unmarshal(data, &rows); err != nil {
		return nil, err
	}
	out := make([]*Article, 0, len(rows))
	for _, r := range rows {
		a := &Article{ID: r.ID, content: r.Content}
		var meta struct {
			Title, URL, PublishedAt, ModifiedAt string
			Authors                             []string
		}
		var source struct{ Title string }
		if err := json.Unmarshal([]byte(r.Article), &meta); err != nil {
			return nil, fmt.Errorf("article %s: %w", r.ID, err)
		}
		json.Unmarshal([]byte(r.Source), &source)
		a.Title, a.URL, a.Authors, a.PublishedAt, a.ModifiedAt, a.SourceName = meta.Title, meta.URL, meta.Authors, meta.PublishedAt, meta.ModifiedAt, source.Title
		out = append(out, a)
	}
	return out, nil
}

// Content decodes the article's content.
func (a *Article) Content() (*Content, error) {
	c, err := FromJSON([]byte(a.content))
	if err != nil {
		return nil, fmt.Errorf("content of %s: %w", a.ID, err)
	}
	return c, nil
}

// FromJSON decodes an ArticleContent, as the library stores and gives it.
func FromJSON(data []byte) (*Content, error) {
	var c Content
	if err := json.Unmarshal(data, &c); err != nil {
		return nil, err
	}
	if c.Document != nil && (c.Document.Version != 1 || c.Document.Root.Type != "root") {
		c.Document = nil // as isReaderDocument refuses it: the HTML stands in
	}
	return &c, nil
}
