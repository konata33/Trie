package reader

// Sketch is a block of the body as a coarse picture of the page draws it (the
// app's capture proof, DESIGN.md Capture 显影): what it is, how deep in lists
// and quotes it sits, and its words, the stretches that link or are code apart.
type Sketch struct {
	// Kind is Paragraph, Heading, Caption, Item (a list item's text), Quote (a
	// quote's text), Code, Picture, Table or Rule.
	Kind  Kind
	Level int // a heading's level, 1–6
	Depth int // the lists and quotes it is within
	Runs  []SketchRun
}

// SketchRun is a stretch of a block's words in one kind.
type SketchRun struct {
	Text       string
	Link, Mono bool
}

// Sketch is the body's first blocks as Sketches, at most n: lists, quotes and
// figures opened up into what they hold. A code block's one run is its code.
func (b *Body) Sketch(n int) []Sketch {
	var out []Sketch
	var walk func(bs []*block, as Kind, depth int)
	walk = func(bs []*block, as Kind, depth int) {
		for _, bl := range bs {
			if len(out) >= n {
				return
			}
			switch bl.kind {
			case List:
				walk(bl.children, Item, depth+1)
			case Item:
				walk(bl.children, Item, depth)
			case Quote:
				walk(bl.children, Quote, depth+1)
			case Figure:
				walk(bl.children, Paragraph, depth)
			case Code:
				s := Sketch{Kind: Code, Depth: depth}
				if bl.code != nil {
					s.Runs = []SketchRun{{Text: bl.code.code, Mono: true}}
				}
				out = append(out, s)
			case Picture, Table, Rule:
				out = append(out, Sketch{Kind: bl.kind, Depth: depth})
			default:
				kind := bl.kind
				if kind == Paragraph {
					kind = as
				}
				s := Sketch{Kind: kind, Level: bl.level, Depth: depth}
				for _, r := range bl.runs {
					if r.style&glyph == 0 && r.text != "" {
						s.Runs = append(s.Runs, SketchRun{Text: r.text, Link: r.href != "", Mono: r.style&mono != 0})
					}
				}
				if len(s.Runs) > 0 {
					out = append(out, s)
				}
			}
		}
	}
	if b != nil {
		walk(b.blocks, Paragraph, 0)
	}
	return out
}
