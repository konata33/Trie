package app

import (
	"fmt"
	"math"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/egoist/mygo/ui"

	"trie/internal/library"
	"trie/internal/locales"
	"trie/internal/motion"
	"trie/internal/reader"
	"trie/internal/theme"
)

// 摘录: whose words were kept, a page for
// each person, set like a magazine's pull quotes. Whoever was quoted last
// opens the page on a spread of their own; everyone else follows in
// columns. On each page their latest passage stands large, sized by its
// length, the others as opening lines. A passage clicked takes the stage:
// its words travel to the middle of the window as the page goes dark
// behind them, and its article's title opens the article at the passage.

// clipsState is what 摘录 keeps between frames.
type clipsState struct {
	scroll  ui.ScrollState
	query   string
	search  bool
	heights map[string]float32 // each person's page as last laid out, by scope and name
	terms   string             // the search the heights were kept for
	settled *clipsPlaced       // where everyone went, once composed from measured heights
	seen    map[string]bool    // the passages here when the page came, which do not rise
	arrived uint64             // the crossing the page came by
	copied  map[string]copyRun // the clip bar's copies, by passage
	washes  map[string]washRun // what was copied lighting up, by passage ("card" for the card's)
	open    string             // the passage on the stage (引文上台)
	stage   *clipStage         // the stage while it shows: coming, there or going back
	turns   uint64             // its turns to another passage, to cross them over
	turned  bool
	who     string // the person the stage showed, to cross them over when it changes
	whoRun  uint64
	dir     float32            // the way the last turn went along the dial: 1 on, -1 back
	tag     string             // the passage whose tick the pointer last named under the dial
	cardBox ui.Rect            // the stage's words as last laid out
	boxes   map[string]ui.Rect // the passages as last laid out (App.Press)
	steps   map[string]step    // the leads' type as the page last set them
	folding map[string]*fold   // passages deleted, folding shut before they go
	doomed  string             // a passage deleted on the stage, which folds once it is back
	note    clipNote
	copy    copyRun // the stage's last 复制引文
	back    string  // the passage an article was opened from, which takes the focus back
	focus   string  // the passage to take the focus in the next build
	order   []*library.Clip
	pics    map[string]map[string]reader.KeptPicture         // the passages' pictures, by passage and unit; nil while on their way
	waiting map[string][]func(map[string]reader.KeptPicture) // what waits for them
}

type clipsPlaced struct {
	scope  string
	placed map[string]int
}

// clipNote is the note being written on the card, and whether the pen in its
// foot takes the focus back (writing ended from the keys).
type clipNote struct {
	pen       bool
	id, draft string
	editing   bool
}

// copyRun is a copy as the clipboard took it: of which passage, when,
// whether it failed, and whether it came from the keys or under less
// motion (it says so at once).
type copyRun struct {
	id     string
	at     time.Time
	failed bool
	kbd    bool
}

// says reports whether run r of passage id still says how it went at now.
func (r copyRun) says(id string, now time.Time) bool {
	return r.id == id && !r.at.IsZero() && now.Sub(r.at) < motion.Copied
}

// fold is a deleted passage folding shut: from the height
// it had, since when.
type fold struct {
	at   time.Time
	from float32
	sent bool // folded shut, the delete is on its way
}

// washRun is a wash under way: since when, and whether only fading (less
// motion). It lights up what a copy took, once the clipboard has it, so the
// eye sees what went; not from the keys.
type washRun struct {
	at   time.Time
	fade bool
}

func (a *App) clipsSt() *clipsState {
	if a.clips == nil {
		a.clips = &clipsState{heights: map[string]float32{}, seen: map[string]bool{}, copied: map[string]copyRun{}, washes: map[string]washRun{},
			boxes: map[string]ui.Rect{}, steps: map[string]step{}, folding: map[string]*fold{}, pics: map[string]map[string]reader.KeptPicture{},
			waiting: map[string][]func(map[string]reader.KeptPicture){}}
	}
	return a.clips
}

// person is one whose words were kept: their passages, newest first.
type person struct {
	name  string
	icon  *siteIcon
	clips []*library.Clip
}

var (
	parenName = regexp.MustCompile(`\(([^()]+)\)\s*$`)
	bareEmail = regexp.MustCompile(`^[^\s@]+@[^\s@]+$`)
)

// clipAuthor is who wrote a passage: feeds often send "name@example.com
// (Name)"; only the person's name is kept.
func clipAuthor(cl *library.Clip) string {
	var names []string
	for _, au := range cl.Citation.Authors {
		name := strings.TrimSpace(au)
		if m := parenName.FindStringSubmatch(au); m != nil {
			name = strings.TrimSpace(m[1])
		}
		if name != "" && !bareEmail.MatchString(name) {
			names = append(names, name)
		}
	}
	if len(names) > 0 {
		return strings.Join(names, locales.ClipsAuthorSeparator())
	}
	return cl.Citation.SourceName
}

// clipMarkdown is the passage with its source, link and note, as copied
// for an agent; a Chinese title in 《》 as the page names it. Its pictures,
// saved, link to their files where they stood.
func clipMarkdown(cl *library.Clip, saved map[string]pasted) string {
	var quote []string
	words := func(s string) {
		if s = strings.TrimSpace(s); s == "" {
			return
		}
		if len(quote) > 0 {
			quote = append(quote, ">")
		}
		for _, line := range strings.Split(strings.ReplaceAll(s, "\r\n", "\n"), "\n") {
			if strings.TrimSpace(line) != "" {
				quote = append(quote, "> "+strings.TrimRightFunc(line, unicode.IsSpace))
			} else {
				quote = append(quote, ">")
			}
		}
	}
	runes, from := []rune(cl.Text), 0
	for _, p := range cl.Pictures {
		at := min(max(p.At, from), len(runes))
		words(string(runes[from:at]))
		if pic := saved[p.Unit]; pic.path != "" {
			if len(quote) > 0 {
				quote = append(quote, ">")
			}
			quote = append(quote, "> "+mdImage(pic))
		}
		from = at
	}
	words(string(runes[from:]))
	author, site := clipAuthor(cl), cl.Citation.SourceName
	by := author
	if site != "" && author != site {
		by = locales.ClipsAuthorAtSite(author, site)
	}
	date := ""
	if p := cl.Citation.PublishedAt; p != nil && len(*p) >= 10 && dateOnly((*p)[:10]) {
		date = ", " + (*p)[:10]
	}
	note := ""
	if n := strings.TrimSpace(cl.Note); n != "" {
		note = "\n\n" + locales.ClipsMarkdownNote(n)
	}
	link := ""
	if cl.Citation.URL != "" {
		link = "\n" + cl.Citation.URL
	}
	return strings.Join(quote, "\n") + "\n\n— " + by + ", " + book(clipTitle(cl)) + date + link + note + "\n"
}

func personOf(cl *library.Clip) string {
	if cl.Citation.SourceName != "" {
		return cl.Citation.SourceName
	}
	return clipAuthor(cl)
}

func clipTitle(cl *library.Clip) string {
	if cl.Citation.Title == "" {
		return locales.ClipsUntitled()
	}
	return cl.Citation.Title
}

var han = regexp.MustCompile(`\p{Han}`)

// book names a Chinese title in 《》, as a reference would; a Latin one
// stands as it is.
func book(title string) string {
	if han.MatchString(title) {
		return "《" + title + "》"
	}
	return title
}

// iconOfClip is the face of the source a passage's article shows under.
func (a *App) iconOfClip(cl *library.Clip) *siteIcon {
	art := a.article(cl.ArticleID)
	if art == nil {
		return nil
	}
	return a.iconOf(a.sourceOf(art))
}

// byPerson is a page per person, whoever was quoted most recently first.
func (a *App) byPerson(clips []*library.Clip) []*person {
	sorted := slices.Clone(clips)
	slices.SortStableFunc(sorted, func(x, y *library.Clip) int { return strings.Compare(y.CreatedAt, x.CreatedAt) })
	var out []*person
	by := map[string]*person{}
	for _, cl := range sorted {
		name := personOf(cl)
		p := by[name]
		if p == nil {
			p = &person{name: name, icon: a.iconOfClip(cl)}
			by[name] = p
			out = append(out, p)
		}
		p.clips = append(p.clips, cl)
	}
	return out
}

// termsOf are the words searched for: every one must appear somewhere in
// the passage, the note, the article's title or the person's name.
func termsOf(q string) []string { return strings.Fields(strings.ToLower(q)) }

func clipMatches(cl *library.Clip, terms []string) bool {
	text := strings.ToLower(strings.Join([]string{cl.Text, cl.Note, cl.Citation.Title, personOf(cl), clipAuthor(cl)}, "\n"))
	for _, t := range terms {
		if !strings.Contains(text, t) {
			return false
		}
	}
	return true
}

var (
	lineBreaks      = regexp.MustCompile(`\s*\n+\s*`)
	paragraphBreaks = regexp.MustCompile(`\n+`)
)

// excerpt is a passage's opening, a line to each of its paragraphs, so that a
// heading stays apart from the words under it; searching, it starts a little
// before the first hit, so that the hit is in view.
func excerpt(text string, terms []string) string {
	flat := []rune(strings.TrimSpace(lineBreaks.ReplaceAllString(text, "\n")))
	lower := []rune(strings.ToLower(string(flat)))
	at := -1
	for _, t := range terms {
		if i := runeIndex(lower, []rune(t)); i >= 0 && (at < 0 || i < at) {
			at = i
		}
	}
	if at > 48 {
		return "…" + string(flat[at-16:])
	}
	return string(flat)
}

func runeIndex(s, sub []rune) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if slices.Equal(s[i:i+len(sub)], sub) {
			return i
		}
	}
	return -1
}

// hits are text's spans, the words searched for marked.
func hits(text string, terms []string, base ui.Span) []ui.Span {
	if len(terms) == 0 {
		base.Text = text
		return []ui.Span{base}
	}
	rs, lower := []rune(text), []rune(strings.ToLower(text))
	var out []ui.Span
	start := 0
	for i := 0; i < len(rs); {
		n := 0
		for _, t := range terms {
			if tr := []rune(t); len(tr) > n && i+len(tr) <= len(lower) && slices.Equal(lower[i:i+len(tr)], tr) {
				n = len(tr)
			}
		}
		if n == 0 {
			i++
			continue
		}
		if start < i {
			s := base
			s.Text = string(rs[start:i])
			out = append(out, s)
		}
		s := base
		s.Text, s.Color, s.Background, s.Radius = string(rs[i:i+n]), theme.Text, theme.Hit, 2
		out = append(out, s)
		i += n
		start = i
	}
	if start < len(rs) {
		s := base
		s.Text = string(rs[start:])
		out = append(out, s)
	}
	return out
}

// reach is how long a passage reads: a Han character one, anything else
// half (Latin letters run about half as wide).
func reach(text string) float32 {
	var n float32
	for _, r := range text {
		if unicode.Is(unicode.Han, r) || r >= 0x3000 && r <= 0x303F || r >= 0xFF00 && r <= 0xFFEF {
			n++
		} else {
			n += 0.5
		}
	}
	return n
}

// Type follows length, as a magazine sets a pull quote: the fewer the
// words, the larger they stand; a long passage keeps the reading size.
type step uint8

const (
	stepXL step = iota
	stepL
	stepM
	stepS
)

var (
	stepSize = [4][2]float32{{32, 44}, {24, 36}, {20, 32}, {17, 32}} // size and line height
	stepMark = [4]float32{24, 20, 16, 16}
	stepUp   = [4]step{stepXL, stepXL, stepL, stepM}
)

func leadStep(text string, hero bool) step {
	n := reach(text)
	switch {
	case hero && n <= 30:
		return stepXL
	case hero && n <= 70, !hero && n <= 30:
		return stepL
	case hero && n <= 140, !hero && n <= 70:
		return stepM
	}
	return stepS
}

func cardStep(text string) step {
	switch n := reach(text); {
	case n <= 40:
		return stepL
	case n <= 100:
		return stepM
	}
	return stepS
}

// besideLead is how many of a spread's passages stand beside its lead.
// ponytail: estimated from text length, not measured; measure the laid-out
// lead if spreads start to look lopsided.
func besideLead(lead *library.Clip, others []*library.Clip, s step) int {
	size, line := stepSize[s][0], stepSize[s][1]
	room := 64 + min(6, ceil(reach(lead.Text)*size/480))*line + 36 + 24
	if strings.TrimSpace(lead.Note) != "" {
		room += 32
	}
	count := 0
	for _, cl := range others {
		room -= 33 + min(2, ceil(reach(cl.Text)*15/340))*24
		if strings.TrimSpace(cl.Note) != "" {
			room -= 28
		}
		if count == 0 {
			room -= 20
		}
		if count > 0 && room < 0 {
			break
		}
		count++
	}
	return count
}

func ceil(v float32) float32 { return float32(int(v + 0.999999)) }

// clipsLayout is the page's composition: the person on the opening spread,
// and the columns under it.
type clipsLayout struct {
	hero   *person
	rest   [][]*person
	placed map[string]int
}

// arrange composes the page: whoever was quoted last
// on a spread of their own, everyone else into the column shortest so far,
// by their page as last laid out (by its passages before). People already
// placed stay in their column, so a page deleted closes up where it was,
// even a column's only one; a lone other person takes the whole width.
func arrange(people []*person, columns int, heightOf func(*person) (float32, bool), kept map[string]int) clipsLayout {
	hero, others := people[0], people[1:]
	type column struct {
		height float32
		people []*person
	}
	n := min(columns, len(others))
	if len(others) <= 1 {
		kept = nil
	}
	for _, p := range others {
		if was, ok := kept[p.name]; ok && was < columns {
			n = max(n, was+1)
		}
	}
	rest := make([]column, n)
	placed := map[string]int{}
	for _, p := range others {
		col, ok := kept[p.name]
		if !ok || col >= n {
			col = 0
			for i := range rest {
				if rest[i].height < rest[col].height {
					col = i
				}
			}
		}
		rest[col].people = append(rest[col].people, p)
		h, ok := heightOf(p)
		if !ok {
			h = 200 + float32(len(p.clips))*80
		}
		rest[col].height += h + 16
		placed[p.name] = col
	}
	out := clipsLayout{hero: hero, placed: placed}
	for _, c := range rest {
		out.rest = append(out.rest, c.people)
	}
	return out
}

// How a person's page is laid out: spread, the whole width with room, the
// lead on the left and as many passages beside it as its height allows,
// the rest under both, three across; stack, the whole width in a narrow
// window, the lead at a reading measure, the passages under it two across;
// card, one of several in a column, the passages under the lead in a list.
type shape uint8

const (
	spread shape = iota
	stack
	card
)

// clipsPage builds 摘录.
func (a *App) clipsPage(c *ui.Context, live bool) {
	cs := a.clipsSt()
	p := page{kind: "clips"}
	sc := ui.Scroll(c.Key("scroll")).TrackScroll(&cs.scroll).Grow(1).FillWidth().Scrollbars(false)
	view := sc.LayoutBounds()
	if live {
		cs.syncStage(c.Now(), a.still(c))
		clear(cs.steps)
		if s := cs.stage; s != nil && s.cut {
			s.cut = false
			c.DisableAnimations() // turned from the keys: what it turned from goes at once
		}
	}
	clips := make([]*library.Clip, len(a.lib.Clips))
	for i := range a.lib.Clips {
		clips[i] = &a.lib.Clips[i]
	}
	if live {
		// What is here as the page comes does not rise; what comes back later does.
		if cs.arrived != a.run {
			cs.arrived = a.run
			clear(cs.seen)
			for _, cl := range clips {
				cs.seen[cl.ID] = true
			}
		}
		a.scrolling = &cs.scroll
		// Deleted on the stage, it folds once back (the stage frames until then).
		if cs.doomed != "" && (a.kbd || cs.stage == nil || cs.stage.home(c.Now())) {
			a.foldClip(c, cs.doomed)
			cs.doomed = ""
		}
		// Folded shut, a passage goes (the notice offers 撤销), staying shut
		// until the library has let it go; if it does not, it opens again.
		for id, f := range cs.folding {
			gone := !slices.ContainsFunc(clips, func(cl *library.Clip) bool { return cl.ID == id })
			switch t := c.Now().Sub(f.at); {
			case gone || f.sent && t > motion.Fold+1200*time.Millisecond:
				delete(cs.folding, id)
			case !f.sent && t >= motion.Fold:
				f.sent = true
				a.deleteClip(id)
			}
		}
		if cs.back != "" && a.page == p && a.next == nil {
			cs.focus, cs.back = cs.back, ""
		}
	}
	terms := termsOf(cs.query)
	roomy := min(a.area.W, 1200)-64 >= 960
	columns := 2
	if roomy {
		columns = 3
	}
	scope := fmt.Sprint(columns, "\x00", strings.Join(terms, " "))
	if q := strings.Join(terms, " "); live && q != cs.terms {
		// Another search: the heights kept for the last one go (a width's own stay).
		for k := range cs.heights {
			if _, rest, _ := strings.Cut(k, "\x00"); strings.HasPrefix(rest, cs.terms+"\x00") {
				delete(cs.heights, k)
			}
		}
		cs.terms = q
	}
	var shown []*library.Clip
	for _, cl := range clips {
		if len(terms) == 0 || clipMatches(cl, terms) {
			shown = append(shown, cl)
		}
	}
	people := a.byPerson(shown)
	heightOf := func(pp *person) (float32, bool) { h, ok := cs.heights[scope+"\x00"+pp.name]; return h, ok }
	var layout *clipsLayout
	if len(people) > 0 {
		var kept map[string]int
		if cs.settled != nil && cs.settled.scope == scope {
			kept = cs.settled.placed
		}
		l := arrange(people, columns, heightOf, kept)
		layout = &l
		switch {
		case kept != nil:
			placed := map[string]int{}
			for k, v := range kept {
				placed[k] = v
			}
			for k, v := range l.placed {
				placed[k] = v
			}
			cs.settled = &clipsPlaced{scope, placed}
		case !slices.ContainsFunc(people[1:], func(pp *person) bool { _, ok := heightOf(pp); return !ok }):
			cs.settled = &clipsPlaced{scope, l.placed}
		}
	}
	// ← → in the card walk the page in reading order: the spread, then the columns left to right.
	cs.order = cs.order[:0]
	if layout != nil {
		for _, pp := range append([]*person{layout.hero}, slices.Concat(layout.rest...)...) {
			cs.order = append(cs.order, pp.clips...)
		}
	}
	full := stack
	if roomy {
		full = spread
	}
	sc.Children(func() {
		ui.Column(c).FillWidth().MaxWidth(1200).MinHeight(view.H).AlignSelf(ui.Center).Padding(0, 32, 140, 32).Children(func() {
			a.band(c, func() {}, func() {
				if len(clips) > 0 {
					a.searchField(c, &cs.query, &cs.search, locales.ClipsSearch(), locales.ClipsSearchPlaceholder())
				}
			})
			switch {
			case len(clips) == 0:
				a.emptyState(c, "clips", locales.ClipsEmptyTitle(), "", nil)
			case layout == nil:
				a.emptyState(c, "", "", locales.ClipsNoResults(strings.TrimSpace(cs.query)), nil)
			default:
				ui.Column(c.Key("people")).FillWidth().Role(ui.RoleRegion).Label(locales.ClipsByWriter()).Children(func() {
					a.personPage(c, p, layout.hero, true, full, terms, live)
					if len(layout.rest) == 0 {
						return
					}
					tracks := make([]ui.Track, len(layout.rest))
					for i := range tracks {
						tracks[i] = ui.Fr(1)
					}
					// With one other person, they get the whole width too, rather than a lone column.
					ui.Grid(c.Key("rest")).ColumnTracks(tracks...).GapX(16).Margin(16, 0, 0, 0).AlignItems(ui.Start).Children(func() {
						for i, col := range layout.rest {
							ui.Column(c.Key(i)).Gap(16).MinWidth(0).Children(func() {
								for _, pp := range col {
									sh := card
									if len(layout.rest) == 1 {
										sh = full
									}
									e := a.personPage(c, p, pp, false, sh, terms, live)
									if live {
										if h := e.LayoutBounds().H; h > 0 {
											key := scope + "\x00" + pp.name
											if old, ok := cs.heights[key]; !ok || old-h > 1 || h-old > 1 {
												cs.heights[key] = h
												c.Invalidate()
											}
										}
									}
								}
							})
						}
					})
				})
			}
		})
	})
	if live {
		a.clipsStage(c, p)
	}
}

// personPage is one person's page: their face and name, their latest
// passage set large, their other passages as opening lines; flat like
// every resting card, the halo of their avatar's colours at its top.
func (a *App) personPage(c *ui.Context, p page, pp *person, hero bool, sh shape, terms []string, live bool) ui.Element {
	lead, others := pp.clips[0], pp.clips[1:]
	wide := sh == spread && len(others) > 0
	// Across the whole width with a single passage, the words stand a step larger, like a poster.
	st := leadStep(lead.Text, hero)
	if sh != card && len(others) == 0 {
		st = stepUp[st]
	}
	beside := len(others)
	switch {
	case wide:
		beside = besideLead(lead, others, st)
	case sh == stack:
		beside = 0
	}
	// A single passage across a spread keeps its note in the margin beside it.
	poster := sh == spread && len(others) == 0 && strings.TrimSpace(lead.Note) != ""
	pad := float32(32)
	if hero {
		pad = 48
	}
	var page ui.Element
	if wide {
		page = ui.Grid(c.Key("person:"+pp.name)).ColumnTracks(ui.Fr(7), ui.Fr(5)).GapX(64).AlignItems(ui.Start)
	} else {
		page = ui.Column(c.Key("person:" + pp.name))
	}
	page.FillWidth().MinWidth(0).Padding(pad).Radius(10).ClipX().ClipY().Background(theme.Surface).InsetShadow(0, 0, 0, 1, theme.LineSoft).
		Role(ui.RoleArticle).Label(pp.name)
	if a.crossing(c, live) {
		page.Cache()
	}
	// While one of their passages is on the stage, the stage takes their face and name from here.
	cs := a.clipsSt()
	var s *clipStage
	for _, cl := range pp.clips {
		if s = cs.onStage(cl.ID, live); s != nil {
			break
		}
	}
	if s != nil {
		s.from.card = page.LayoutBounds()
	}
	land := motion.Land // closing up, without going past its place, as a page above folds away
	page.Transition(a.tr(c, ui.ElementTransition{Animation: &land, Position: true}))
	// A person's last passage takes their whole page with it.
	if len(pp.clips) == 1 {
		a.folded(c, page, lead.ID)
	}
	if !hero {
		a.focusIn(page)
	}
	row := func(cl *library.Clip, i int, below, last bool) {
		before := lead
		if i > 0 {
			before = others[i-1]
		}
		// The article is named once above each run of passages taken from it, and again at the top of a spread's side;
		// across the columns below a spread every passage names its own, so the lines stay level.
		article := ""
		if below || before.ArticleID != cl.ArticleID || wide && i == 0 {
			article = clipTitle(cl)
		}
		a.clipRow(c, p, cl, article, below, last, terms, live)
	}
	return page.Children(func() {
		a.aura(c, hero)
		first := ui.Column(c.Key("first")).MinWidth(0)
		if sh != card && !wide && !poster {
			first.MaxWidth(680)
		}
		first.Children(func() {
			gap, mb, size := float32(8), float32(24), float32(24)
			if hero {
				gap, mb, size = 12, 32, 32
			}
			head := ui.Row(c.Key("head")).Gap(gap).AlignItems(ui.Center).MinWidth(0).Margin(0, 0, mb, 0)
			if s != nil {
				s.from.head, s.from.face, s.from.gap = head.LayoutBounds(), size, gap
				if s.hides() {
					head.Opacity(0) // on the stage
				}
			}
			head.Children(func() {
				a.avatar(c, pp.name, pp.icon, size)
				ui.Text(c, pp.name).FontSize(theme.Item).LineHeight(20.0 / 15).FontWeight(600).TextColor(theme.Text).SingleLine().Ellipsis("…").
					Shrink(1).MinWidth(0).Role(ui.RoleHeading).Level(2)
			})
			a.clipLead(c, p, lead, hero, len(others) == 0, st, poster, terms, live)
		})
		if beside > 0 {
			l := ui.Column(c.Key("beside")).MinWidth(0).Role(ui.RoleList)
			if wide {
				l.Margin(-4, 0, 0, 0)
			} else {
				l.Margin(24, 0, 0, 0).BorderWidth(1, 0, 0, 0).BorderColor(theme.LineSoft)
			}
			l.Children(func() {
				for i, cl := range others[:beside] {
					row(cl, i, false, i == beside-1)
				}
			})
		}
		if beside < len(others) {
			cols := []ui.Track{ui.Fr(1), ui.Fr(1), ui.Fr(1)}
			if sh == stack {
				cols = cols[:2]
			}
			g := ui.Grid(c.Key("below")).ColumnTracks(cols...).GapX(48).AlignItems(ui.Start).Margin(32, 0, 0, 0).Role(ui.RoleList)
			if wide {
				g.ColumnSpan(2)
			}
			g.Children(func() {
				for i, cl := range others[beside:] {
					row(cl, beside+i, true, false)
				}
			})
		}
	})
}

// focusIn brings a page into focus as it scrolls in (Explore's 移焦 on
// scroll): from 0.2 and a 3 blur as it shows, clear once seven tenths of it
// is in; it follows the scroll, never a clock, and moves nothing.
func (a *App) focusIn(e ui.Element) {
	b := e.LayoutBounds()
	if b.H <= 0 {
		return
	}
	k := min(1, max(0, (a.area.Y+a.area.H-b.Y)/(0.7*b.H)))
	if k >= 1 {
		return
	}
	e.Opacity(0.2 + 0.8*k)
	if blur := 3 * (1 - k); blur > 0.05 {
		e.Blur(blur)
	}
}

// aura is a soft grey halo resting at the top of a person's page, the same
// for everyone, as Explore's covers glow (not their avatar's colours). The
// page or the card it rests in cuts it at its rounded corners: a clip of
// its own there would reach into them, and be drawn as a mask on the CPU
// again wherever the page moves.
func (a *App) aura(c *ui.Context, hero bool) {
	hues := grey
	ui.Box(c.Key("aura")).Absolute().Fill().ZIndex(-1).IgnoreInput(true).AccessHidden(true).Children(func() {
		near, far := [3]float32{-240, -120, 400}, [3]float32{-260, 180, 320}
		if hero {
			near, far = [3]float32{-320, -160, 560}, [3]float32{-340, 240, 440}
		}
		a.personHalo(c, "near", near, 0.3, hues[0])
		a.personHalo(c, "far", far, 0.18, hues[1])
	})
}

// personHalo is a disc of colour blurred by 34 (top and right of its box, and
// its size, in at), as alpha opaque, drawn as the radial gradient a blur
// leaves of a disc, rather than blurred on the GPU every frame. In the card
// its colour crosses over as it turns to someone else.
func (a *App) personHalo(c *ui.Context, key string, at [3]float32, alpha float32, color ui.Color) {
	const s = 34
	r := at[2] / 2
	reach := r + 3*s
	d := ui.Box(c.Key(key)).Absolute().Top(at[0]-3*s).Right(at[1]-3*s).Size(2*reach, 2*reach)
	col := d.AnimateColor("c", color, a.ease(motion.DialogIn))
	// A disc's edge blurred by a Gaussian: its colour to r−2σ, half at r, gone past r+3σ.
	var stops []ui.GradientStop
	for _, p := range [][2]float32{{-reach, 1}, {-2 * s, 1}, {-s, 0.84}, {0, 0.5}, {s, 0.16}, {2 * s, 0.02}, {3 * s, 0}} {
		stops = append(stops, ui.GradientStop{Color: col.Alpha(alpha * p[1]), At: max(0, (r+p[0])/reach)})
	}
	d.RadialGradient(ui.RadialGradient{Radius: reach, Stops: stops})
}

// avatar is a person's face; with none, the first letter of their name on
// a quiet disc, as the sidebar shows them.
func (a *App) avatar(c *ui.Context, name string, ic *siteIcon, size float32) ui.Element {
	if ic != nil {
		return a.face(c, ic, "avatar", size)
	}
	fs := float32(theme.Label)
	if size >= 32 {
		fs = theme.Item
	}
	return ui.Box(c.Key("avatar")).Size(size, size).Radius(size/2).Background(theme.Selected).InsetShadow(0, 0, 0, 1, theme.LineSoft).Center().Shrink(0).
		AccessHidden(true).Children(func() {
		ui.Text(c, initial(name)).FontSize(fs).LineHeight(1).FontWeight(600).TextColor(theme.Text2)
	})
}

// passageMove is how a passage moves as others fold away (foldClip),
// closing the gap on Land, and comes back: put back while the page shows,
// it rises 10 into place on RiseIn, neither going past its place. Not from
// the keys, nor under less motion.
func (a *App) passageMove(c *ui.Context, id string) ui.ElementTransition {
	land := motion.Land
	t := ui.ElementTransition{Animation: &land, Position: true}
	if !a.clipsSt().seen[id] {
		rise := motion.RiseIn
		t.Animation, t.Enter = &rise, &ui.Motion{Y: 10}
	}
	return a.tr(c, t)
}

// clipLead is a person's latest passage as a pull quote: the hand-drawn
// mark hanging in the margin, the article under it, the note under that.
func (a *App) clipLead(c *ui.Context, p page, cl *library.Clip, hero, alone bool, st step, poster bool, terms []string, live bool) {
	note := strings.TrimSpace(cl.Note)
	ex := excerpt(cl.Text, terms)
	spans := hits(ex, terms, ui.Span{})
	box := ui.Box(c.Key(cl.ID)).FillWidth()
	box.Transition(a.passageMove(c, cl.ID))
	if !alone {
		a.folded(c, box, cl.ID) // alone, its page folds instead
	}
	hover := box.Hovered()
	box.Children(func() {
		// It reaches 32 into the side, so that the hanging mark is inside it, and so is the focus ring (stretched, less its margin).
		b := ui.ButtonBase(c.Key("open")).Label(ex).Margin(0, 0, 0, -32).Padding(0, 0, 0, 32).Radius(6).FocusRing(false)
		a.passageButton(c, p, b, cl, live)
		if b.FocusVisible() {
			b.FocusRing(true).FocusRingOffset(8)
		}
		a.pressAt(b, 0.99, 0, 0.5)
		wash := a.washOf(c, cl.ID)
		s := a.clipsSt().onStage(cl.ID, live)
		if live {
			a.clipsSt().steps[cl.ID] = st
		}
		b.Children(func() {
			size := stepMark[st]
			ink, lit := theme.Muted, false
			if hover {
				ink, lit = theme.Text2, true
			}
			mark := a.icon(c, "clips", size, lit).Absolute().Top(8).Left(32 + markLeft(st)).TextColor(b.AnimateColor("mark", ink, a.ease(motion.Hover)))
			body := func() {
				ui.Column(c.Key("words")).MinWidth(0).Children(func() {
					lines := 6
					if !hero {
						lines = 5
					}
					sz := stepSize[st]
					fam, k := reader.PassageFace(cl.Text)
					q := washed(c, "quote", wash, func() ui.Element {
						return ui.RichText(c, spans...).Font(fam).FontSize(sz[0] * k).LineHeight(sz[1] / (sz[0] * k)).TextColor(theme.Text).
							LetterSpacing(track(st)).MaxLines(lines).Ellipsis("…").Pretty()
					})
					q.FillWidth()
					cite := washed(c, "cite", wash, func() ui.Element {
						return ui.RichText(c, hits(book(clipTitle(cl)), terms, ui.Span{})...).FontSize(theme.Body).LineHeight(20.0 / 13).TextColor(theme.Text2).
							SingleLine().Ellipsis("…")
					}).Margin(16, 0, 0, 0)
					if s != nil {
						s.from.quote, s.from.cite = q.LayoutBounds(), cite.LayoutBounds()
						s.from.look = quoteLook{text: ex, fam: fam, size: sz[0] * k, line: sz[1], track: track(st), lines: lines,
							mark: size, markX: markLeft(st), markY: 8}
						if !s.built {
							s.lit = lit // as it was clicked: the stage soon covers the pointer
						}
						if s.hides() {
							q.Opacity(0) // on the stage
							mark.Opacity(0)
							cite.Opacity(0)
						}
					}
					if note != "" && !poster {
						a.noteLine(c, note, terms, wash, 2).Margin(12, 0, 0, 0)
					}
				})
			}
			if !poster {
				body()
				return
			}
			// A lone passage across a spread: the words on the left, the note beside them, its first line on theirs.
			top := map[step]float32{stepXL: 12, stepL: 8}[st]
			if st != stepXL && st != stepL {
				top = 6
			}
			ui.Grid(c.Key("poster")).ColumnTracks(ui.Fr(7), ui.Fr(5)).GapX(64).AlignItems(ui.Start).Children(func() {
				body()
				ui.Box(c.Key("aside")).Padding(top, 0, 0, 0).MaxWidth(320).Children(func() { a.noteLine(c, note, terms, wash, 0) })
			})
		})
		a.clipTools(c, cl, hover, true)
	})
}

// clipRow is one of a person's other passages: the article (at the start
// of a run), the opening lines, the note.
func (a *App) clipRow(c *ui.Context, p page, cl *library.Clip, article string, below, last bool, terms []string, live bool) {
	note := strings.TrimSpace(cl.Note)
	ex := excerpt(cl.Text, terms)
	spans := hits(ex, terms, ui.Span{})
	box := ui.Box(c.Key(cl.ID)).FillWidth().MinWidth(0)
	switch {
	case below:
		box.BorderWidth(1, 0, 0, 0).BorderColor(theme.LineSoft)
	case !last:
		box.BorderWidth(0, 0, 1, 0).BorderColor(theme.LineSoft)
	}
	box.Transition(a.passageMove(c, cl.ID))
	a.folded(c, box, cl.ID)
	hover := box.Hovered()
	box.Children(func() {
		// Flat until pointed at; the ground reaches 12 past the words, so that they keep the page's edge.
		b := ui.ButtonBase(c.Key("open")).Label(ex).Margin(4, -12).Padding(12).Radius(6).FocusRing(false)
		a.passageButton(c, p, b, cl, live)
		ground := ui.Transparent
		if b.Hovered() {
			ground = theme.Hover
		}
		b.Background(b.AnimateColor("bg", ground, a.ease(motion.Hover)))
		if b.FocusVisible() {
			b.FocusRing(true).FocusRingOffset(-2)
		}
		a.give(b, 0.99)
		wash := a.washOf(c, cl.ID)
		b.Children(func() {
			ui.Column(c).MinWidth(0).FillWidth().Children(func() {
				if article != "" {
					ui.RichText(c, hits(article, terms, ui.Span{})...).FontSize(theme.Label).LineHeight(16.0/12).TextColor(theme.Muted).
						SingleLine().Ellipsis("…").Margin(0, 0, 4, 0)
				}
				fam, k := reader.PassageFace(cl.Text)
				words := washed(c, "excerpt", wash, func() ui.Element {
					return ui.RichText(c, spans...).Font(fam).FontSize(theme.Item * k).LineHeight(24 / (theme.Item * k)).TextColor(theme.Text).
						MaxLines(2).Ellipsis("…").Pretty()
				}).FillWidth()
				if s := a.clipsSt().onStage(cl.ID, live); s != nil {
					// The article named above a run stays: on the stage the passage's own comes with its words.
					s.from.quote, s.from.cite = words.LayoutBounds(), ui.Rect{}
					s.from.look = quoteLook{text: ex, fam: fam, size: theme.Item * k, line: 24, lines: 2}
					if s.hides() {
						words.Opacity(0) // on the stage
					}
				}
				if note != "" {
					a.noteLine(c, note, terms, wash, 1).Margin(8, 0, 0, 0)
				}
			})
		})
		a.clipTools(c, cl, hover, false)
	})
}

// passageButton puts a passage on the stage, and takes the focus or the
// ground back from the stage or an article it opened.
func (a *App) passageButton(c *ui.Context, p page, b ui.Element, cl *library.Clip, live bool) {
	cs := a.clipsSt()
	b.TabStop(true)
	r := b.LayoutBounds()
	if s := cs.onStage(cl.ID, live); s != nil {
		s.from.box = r
	}
	if live {
		cs.boxes[cl.ID] = r
		a.layInto(p, cl.ArticleID, r)
		if cs.focus == cl.ID {
			b.Focus()
			// A passage going moves the others (a neighbour can become the lead, built anew): the focus is
			// asked for again until it has gone.
			if cs.doomed == "" && len(cs.folding) == 0 && !a.busy {
				cs.focus = ""
			}
		}
	}
	if b.Hovered() {
		a.clipPictures(cl, nil) // read while the pointer rests, there as the card opens
	}
	if b.Clicked() && live {
		a.showClip(cl.ID)
	}
}

// pressAt is press with the scale's origin at (x, y) of e's box.
func (a *App) pressAt(e ui.Element, scale, x, y float32) {
	e.TransformOrigin(x, y)
	a.give(e, scale)
}

// noteLine is the reader's own line: a short amber stroke, then interface
// type in text-2, on lines lines (0 for as many as it needs).
func (a *App) noteLine(c *ui.Context, note string, terms []string, wash float32, lines int) ui.Element {
	return ui.Box(c.Key("note")).Padding(0, 0, 0, 20).MinWidth(0).Children(func() {
		ui.Box(c).Absolute().Top(10).Left(0).Size(12, 1).Background(theme.Accent).IgnoreInput(true)
		washed(c, "note", wash, func() ui.Element {
			t := ui.RichText(c, hits(note, terms, ui.Span{})...).FontSize(theme.Body).LineHeight(20.0 / 13).TextColor(theme.Text2)
			switch lines {
			case 1:
				t.SingleLine().Ellipsis("…")
			case 0:
				t.Pretty()
			default:
				t.MaxLines(lines).Ellipsis("…").Pretty()
			}
			return t
		})
	})
}

// clipTools is the reader's own clip bar at a passage's corner while it is
// pointed at, and while it says 已复制: copy it with its source (复制引文),
// or delete it (the notice brings it back). Pointer only: from the keys,
// both are on the stage.
func (a *App) clipTools(c *ui.Context, cl *library.Clip, hover, lead bool) {
	cs := a.clipsSt()
	now := c.Now()
	run, held := cs.copied[cl.ID]
	if held && run.says(cl.ID, now) {
		c.After(motion.Copied - now.Sub(run.at))
	} else if held {
		delete(cs.copied, cl.ID)
		held = false
	}
	bar := ui.Row(c.Key("tools")).Absolute().ZIndex(1).Padding(4).Gap(4).Radius(10).Background(theme.Elevated).AlignItems(ui.Center).
		InsetShadow(0, 0, 0, 1, theme.Line).Shadow(0, 0, 0, 1, theme.ShadowEdge).Shadow(0, 12, 32, 0, theme.ShadowFloat).Role(ui.RoleToolbar).Label(locales.ClipsActions())
	// It stays while the pointer is on it, above the passage's edge.
	show := hover || held || bar.Hovered()
	if lead {
		bar.Top(-28).Right(0)
	} else {
		bar.Top(-16).Right(-4)
	}
	k := bar.AnimateTo("show", map[bool]float32{true: 1, false: 0}[show], a.ease(motion.Hover))
	bar.Opacity(k)
	if s := 0.96 + 0.04*k; s < 1 && !a.still(c) {
		bar.TransformOrigin(1, 0).Scale(s, s)
	}
	if !show {
		bar.IgnoreInput(true)
		if k <= 0 {
			return // gone: nothing in it to build
		}
	}
	bar.Children(func() {
		label := locales.ClipsCopyQuote()
		switch {
		case held && run.failed:
			label = locales.ClipsCopyFailedTip()
		case held:
			label = locales.CommonCopied()
		}
		cp := a.toolButton(c, "copy", "copy", label, held && !run.failed, false)
		if cp.Clicked() {
			kbd, still := a.kbd, a.still(c)
			a.copyClip(c, cl, func(err error) {
				cs.copied[cl.ID] = copyRun{id: cl.ID, at: now, failed: err != nil}
				if err == nil && !kbd {
					cs.washes[cl.ID] = washRun{at: now, fade: still}
				}
			})
		}
		del := a.toolButton(c, "delete", "trash", locales.CommonDelete(), false, false)
		if del.Clicked() {
			a.foldClip(c, cl.ID)
		}
	})
}

// toolButton is a small IconButton of the clip bar: text-2, the hover
// ground; delete goes danger under the pointer; done, copy's dots flow
// into an amber check (DESIGN.md 点会走); on, it stays pressed in, on the
// selected ground with its accent lit, while what it opened is open.
func (a *App) toolButton(c *ui.Context, key, icon, label string, done, on bool) ui.Element {
	b := ui.ButtonBase(c.Key(key)).Label(label).Size(24, 24).Radius(6).Center().Shrink(0).FocusRing(false).TabStop(false)
	color, ground := theme.Text2, ui.Transparent
	if on {
		color, ground = theme.Text, theme.Selected
	}
	if b.Hovered() && !on {
		color, ground = theme.Text, theme.Hover
		if key == "delete" {
			color = theme.Danger
		}
	}
	fg := b.AnimateColor("fg", color, a.ease(motion.Hover))
	b.Background(b.AnimateColor("bg", ground, a.ease(motion.Hover))).TextColor(fg)
	if b.FocusVisible() {
		b.FocusRing(true)
	}
	b.Children(func() { a.dotIcon(c, b, icon, 16, iconLook{ink: fg, copied: done, lit: on}) })
	a.tip(c, b, label, false)
	return b
}

// --- Copying, deleting.

// washOf is how far id's wash has gone, from 0 to 1; -1 for none.
func (a *App) washOf(c *ui.Context, id string) float32 {
	cs := a.clipsSt()
	w, ok := cs.washes[id]
	if !ok {
		return -1
	}
	k := float32(c.Now().Sub(w.at)) / float32(motion.Wash)
	if k >= 1 {
		delete(cs.washes, id)
		return -1
	}
	c.AnimationFrame()
	if w.fade {
		return -1 - k // a fade: -1 to -2
	}
	return k
}

// washed builds a text that a wash runs over: the reader's clip colour
// drawn across its lines from the left (to 0.3 of the way), held, then
// lifted off to the right; fading in and out instead under less motion.
func washed(c *ui.Context, key string, k float32, text func() ui.Element) ui.Element {
	if k == -1 {
		return text()
	}
	return ui.Box(c.Key("wash:" + key)).MinWidth(0).Children(func() {
		under := text().Absolute().Left(0).Top(0).Right(0).TextColor(ui.Transparent).TextBackground(theme.AccentSoft).IgnoreInput(true).AccessHidden(true)
		if k < -1 { // fading
			f := -1 - k
			o := float32(1)
			switch {
			case f < 0.3:
				o = motion.Ease(f / 0.3)
			case f > 0.6:
				o = 1 - motion.Ease((f-0.6)/0.4)
			}
			under.Opacity(o)
		} else {
			from, to := float32(0), float32(1)
			switch {
			case k < 0.3:
				to = motion.Ease(k / 0.3)
			case k > 0.6:
				from = motion.Ease((k - 0.6) / 0.4)
			}
			black := theme.Darken
			under.MaskLinearGradient(ui.LinearGradient{Angle: 90, Stops: []ui.GradientStop{{Color: ui.Transparent, At: 0}, {Color: ui.Transparent, At: from},
				{Color: black, At: from}, {Color: black, At: to}, {Color: ui.Transparent, At: to}, {Color: ui.Transparent, At: 1}}})
		}
		text()
	})
}

// foldClip deletes a passage: it folds shut first, and the rest close up;
// from the keys, or under less motion, it goes at once.
func (a *App) foldClip(c *ui.Context, id string) {
	cs := a.clipsSt()
	if a.kbd || a.still(c) {
		a.deleteClip(id)
		return
	}
	if cs.folding[id] == nil {
		cs.folding[id] = &fold{at: c.Now()}
	}
}

// folded folds e, a passage, or the page of a person whose last passage
// it is, while passage id folds: its height to nothing, its opacity gone
// halfway (250 ms along the one curve).
func (a *App) folded(c *ui.Context, e ui.Element, id string) {
	f := a.clipsSt().folding[id]
	if f == nil {
		return
	}
	if f.from <= 0 {
		f.from = e.LayoutBounds().H
	}
	k := motion.Ease(min(1, float32(c.Now().Sub(f.at))/float32(motion.Fold)))
	e.Height(f.from * (1 - k)).ClipY().Opacity(max(0, 1-2*k))
	c.AnimationFrame()
}

// deleteClip lets go of a passage; the notice offers 撤销, which puts it
// back as it was.
func (a *App) deleteClip(id string) {
	var cl *library.Clip
	for i := range a.lib.Clips {
		if a.lib.Clips[i].ID == id {
			cl = &a.lib.Clips[i]
		}
	}
	if cl == nil {
		return
	}
	// Put back by 撤销, it rises into place as what comes back does.
	cs := a.clipsSt()
	delete(cs.seen, id)
	delete(cs.boxes, id)
	restore := library.AddClip{Clip: library.ClipInput{ID: cl.ID, ArticleID: cl.ArticleID, Text: cl.Text, Note: cl.Note, UnitID: cl.UnitID,
		ContentRevision: cl.ContentRevision, CreatedAt: cl.CreatedAt, Pictures: cl.Pictures}}
	a.mutate(library.DeleteClip{ID: id}, func() { a.notify(locales.ClipsDeleted(), nil, restore) })
}

// --- The stage (引文上台).

// The stage sets a passage stageScale times as large as the opening spread
// would, on a sheet of the cards' own paper. The window behind it stays in
// view but steps back: out of focus by stageBlur, black at stageDark over it,
// so that its words read as light, not as text; the card the words came from
// gets stageLift of that back while the pointer rests in the dark: the way
// back.
// What only the stage has rises ownRise into place out of ownSoft of blur.
// Around the sheet and its dial the stage keeps stageHit of its own, where
// a click is not one in the dark.
const (
	stageScale       = 1.25
	stageDark        = 0.6
	stageLift        = 0.16
	stageBlur        = 16
	ownRise, ownSoft = 4, 2
	stageHit         = 24
	// The sheet: its padding, wider at the left where the passage's mark hangs; the least room it keeps from the
	// window's edges.
	sheetPadL, sheetPadR, sheetPadY = 72, 56, 48
	sheetEdge                       = 48
	// The dial under it: its gap from the sheet, its size, the step between two ticks (two where the person changes),
	// and the room under it for the name of a tick pointed at.
	dialGap, dialW, dialH, tickStep = 16, 280, 32, 7
	dialTag                         = 28
)

// clipFrom is where a passage's shared parts stand in the list, in the
// window, as the page last laid them out: the person's page, the row of
// their face and name (the face's size, the gap after it), the words, and
// the article (none under a lead, where the article is named above a run).
type clipFrom struct {
	card, head, quote, cite ui.Rect
	box                     ui.Rect // the passage's own place, whose ground spreads into the sheet
	face, gap               float32
	look                    quoteLook
}

// quoteLook is how the list sets a passage's words: the text it shows and
// its face, size, line height, tracking and lines, and the mark hanging
// beside them, its size (0 for none) and its corner from theirs.
type quoteLook struct {
	text, fam          string
	size, line, track  float32
	lines              int
	mark, markX, markY float32
}

// clipStage is the passage on the stage, or on its way there or back: one
// value takes its shared parts from the list (0) to the stage (1) along the
// travel spring, and another brings in the stage's own parts (own).
type clipStage struct {
	id     string
	from   clipFrom
	at     time.Time // when it last set off, either way
	q0, s0 float32   // the two values as it did
	open   bool      // coming or there; else going back
	keyed  bool      // opened from the keys: it comes and goes at once
	still  bool      // less motion: it fades in and out where it stands
	built  bool      // laid out: the page hides what it took
	gone   bool      // back: the page shows its passage again
	leave  bool      // its article's title is pressed: it goes with its page
	lit    bool      // the pointer had lit its mark in the list as it was clicked
	focus  string    // the passage that takes the focus once it is back, if not its own
	cut    bool      // turned from the keys: the next frame shows the turn at once
	hit    ui.Rect   // the sheet and its dial with their own room around them, as last laid out
	cite   ui.Rect   // the article's line
	top    float32   // where the sheet stands, once it has been laid out (0 until then)
	tall   float32   // the height the sheet keeps while its dial is in hand: the most its passages needed meanwhile
	high   float32   // its height as last laid out
	spare  float32   // the room its passage left above its foot, as last laid out
	dial   ui.Rect   // the dial in the window, as last laid out
	turn   uint64    // the turn it last saw, and when
	turnAt time.Time
	roll   ui.ScrollState // the stage scrolling a sheet taller than the window
	sheet  ui.Rect        // the sheet as last drawn, which becomes the article's page
}

// onStage is the stage while it shows passage id, for the live page.
func (cs *clipsState) onStage(id string, live bool) *clipStage {
	if s := cs.stage; live && s != nil && s.id == id {
		return s
	}
	return nil
}

// syncStage settles the stage as a frame begins: gone once back, and
// turned round from where it is when it is wanted the other way.
func (cs *clipsState) syncStage(now time.Time, still bool) {
	s := cs.stage
	switch {
	case s == nil:
	case s.gone || s.keyed && cs.open != s.id:
		// Back (from the keys, at once): the page shows the passage again, which takes the focus back, or
		// a neighbour does if it is about to fold away.
		cs.stage, cs.focus = nil, s.id
		if s.focus != "" {
			cs.focus = s.focus
		}
	case s.at.IsZero():
		s.at, s.still = now, still
	case s.open != (cs.open == s.id):
		s.q0, s.s0 = s.values(now)
		s.at, s.open = now, !s.open
	}
}

// hides reports whether the page hides the parts the stage took: once the
// stage has them, and until they are back; under less motion, only while
// it stands, the passage showing again under it as it fades.
func (s *clipStage) hides() bool { return s.built && (s.open || !s.still) }

// values are, at now, how far the shared parts have come (q) and how far
// in the stage's own parts are (own): those come in once the shared parts
// are StageLand of the way there, and go as they set off back.
func (s *clipStage) values(now time.Time) (q, own float32) {
	var t time.Duration
	if !s.at.IsZero() {
		t = now.Sub(s.at)
	}
	part := func(t, d time.Duration) float32 { return min(1, max(0, float32(t)/float32(d))) }
	switch {
	case s.keyed && s.open:
		return 1, 1
	case s.keyed:
		return 0, 0
	case s.still && s.open:
		return 1, s.s0 + (1-s.s0)*motion.Ease(part(t, motion.StagePartsIn))
	case s.still:
		return 1, s.s0 * (1 - motion.Ease(part(t, motion.StagePartsOut)))
	case s.open:
		var land time.Duration
		if s.q0 < motion.StageLand {
			land = time.Duration(float32(motion.StageIn) * motion.TravelReach((motion.StageLand-s.q0)/(1-s.q0)))
		}
		return s.q0 + (1-s.q0)*motion.TravelEase(part(t, motion.StageIn)), s.s0 + (1-s.s0)*motion.Ease(part(t-land, motion.StagePartsIn))
	}
	return s.q0 * (1 - motion.TravelEase(part(t, motion.StageOut))), s.s0 * (1 - motion.Ease(part(t, motion.StagePartsOut)))
}

// dark is how far into the dark the page has gone: ahead of the shared
// parts as they lift off; under less motion at once as the stage comes,
// and with it as it goes.
func (s *clipStage) dark(q, own float32) float32 {
	if s.still && !s.keyed {
		if s.open {
			return 1
		}
		return own
	}
	return 1 - (1-q)*(1-q)
}

// home reports whether the stage is back at now.
func (s *clipStage) home(now time.Time) bool {
	switch {
	case s.open:
		return false
	case s.keyed:
		return true
	case s.still:
		return now.Sub(s.at) >= motion.StagePartsOut
	}
	return now.Sub(s.at) >= motion.StageOut
}

// showClip puts passage id on the stage; with the stage up, it turns to it.
func (a *App) showClip(id string) {
	cs := a.clipsSt()
	switch s := cs.stage; {
	case s != nil && s.open && s.id != id:
		of := func(id string) int {
			return slices.IndexFunc(cs.order, func(cl *library.Clip) bool { return cl.ID == id })
		}
		cs.dir = 1
		if of(id) < of(s.id) {
			cs.dir = -1
		}
		s.id, s.from, s.cut = id, clipFrom{}, a.kbd
		if s.roll.Y > 0 {
			s.roll.ScrollTo(0, 0, map[bool]ui.Animation{true: {}, false: ui.Smooth}[a.kbd]) // a passage reads from its start
		}
		cs.turns++
		cs.turned = true
		delete(cs.washes, "card") // what was copied stays with its own passage
	case s == nil || s.id != id:
		cs.stage = &clipStage{id: id, open: true, keyed: a.kbd}
		cs.turned = false
	}
	cs.open = id
	a.keepNote()
	cs.note = clipNote{}
}

// closeClip sends the passage on the stage back into the page, which takes
// the focus back once it is there (syncStage); leaving, the stage goes
// with its page instead (an article opening), as its last build said. A
// note being written is kept.
func (a *App) closeClip(c *ui.Context, leaving bool) {
	cs := a.clipsSt()
	if leaving {
		cs.stage = nil
	}
	a.keepNote()
	cs.open = ""
	c.Invalidate()
}

// keepNote saves the note being written in the card's margin, as leaving
// the field does, and stops writing it.
func (a *App) keepNote() {
	n := &a.clipsSt().note
	if !n.editing {
		return
	}
	n.editing = false
	for _, cl := range a.lib.Clips {
		if cl.ID == n.id {
			if next := strings.TrimSpace(n.draft); next != strings.TrimSpace(cl.Note) {
				a.mutate(library.UpdateClipNote{ID: cl.ID, Note: next}, nil)
			}
			return
		}
	}
}

// clipsStage builds the stage over the window while a passage is on it or
// on its way: the dark over the page, the card the words came from a way
// back in it, the sheet the words stand on in the middle of the window, and
// the dial under it. A click in the dark, or Esc, sends it back; ← → and
// the dial turn to the others.
func (a *App) clipsStage(c *ui.Context, p page) {
	cs := a.clipsSt()
	now := c.Now()
	cs.syncStage(now, a.still(c))
	s := cs.stage
	if s == nil {
		return
	}
	at := slices.IndexFunc(cs.order, func(cl *library.Clip) bool { return cl.ID == s.id })
	if at < 0 {
		// Its passage is gone: the neighbour it named takes the focus.
		cs.stage, cs.open, cs.focus = nil, "", s.focus
		return
	}
	q, own := s.values(now)
	if s.home(now) {
		s.gone = true // it stands where the page has it in this frame; from the next on, the page shows it
		c.Invalidate()
	} else if !s.open || q < 1 || own < 1 {
		c.AnimationFrame()
	}
	e := s.dark(q, own)
	w, h := c.Size()
	wq := min(600, w-2*sheetEdge-sheetPadL-sheetPadR)
	sw := wq + sheetPadL + sheetPadR
	var room float32
	if len(cs.order) > 1 {
		room = dialGap + dialH + dialTag
	}
	// Laid out where it stands in the last frame, so that what travels knows where it goes: its first frames lay it
	// out unseen, once to learn how tall it is, once where that puts it.
	placed := s.top > 0
	editing := cs.note.editing
	ui.Overlay(c, func() {
		// Modal until it is back: the page takes no clicks while the words go home.
		back := ui.Box(c.Key("clip-stage")).Absolute().Left(0).Top(0).Right(0).Bottom(0).ObserveInput(a.observe).Modal()
		px, py, in := back.PointerPosition()
		r := s.hit
		near := in && px >= r.X && px < r.X+r.W && py >= r.Y && py < r.Y+r.H // on the sheet or its dial, or close by
		d := s.dial
		onDial := in && d.W > 0 && px >= d.X-24 && px < d.X+d.W+24 && py >= d.Y-12 && py < d.Y+d.H+dialTag
		over := s.open && in && !near
		lift := back.AnimateTo("lift", map[bool]float32{true: 1, false: 0}[over], a.ease(motion.Hover))
		clicked, escaped := false, false // in the dark, through the scroll over it; Esc, with the focus on the stage
		back.Children(func() {
			dark := ui.Box(c.Key("dark")).Absolute().IgnoreInput(true).AccessHidden(true)
			if r := s.from.card; r.W > 0 {
				// The card is a window in the dark, which lifts while the pointer rests outside the stage: soft at its edge, as
				// the card under it is out of focus.
				dark.Left(r.X).Top(r.Y).Size(r.W, r.H).Radius(10).Background(theme.Darken.Alpha(e*(stageDark-stageLift*lift))).
					Shadow(0, 0, 0, w+h, theme.Darken.Alpha(e*stageDark))
				if lift > 0.01 {
					dark.Blur(stageBlur / 2 * lift)
				}
			} else {
				dark.Left(0).Top(0).Right(0).Bottom(0).Background(theme.Darken.Alpha(e * stageDark))
			}
			// A sheet taller than the window rides a scroll over the dark, its words all there. What it holds is laid out
			// in the scroll's own terms, ry below the window's.
			roll := ui.Scroll(c.Key("roll")).TrackScroll(&s.roll).Absolute().Left(0).Top(0).Right(0).Bottom(0).Scrollbars(false)
			ry := s.roll.Y
			if roll.Clicked() {
				clicked = true
			}
			// One Esc sends it back. With the focus on one of its controls, whose tip is a layer on top that would take
			// the first Esc, the stage around the focus takes it first; while a note is written, the note's own Esc
			// puts it away instead.
			if !editing && roll.Shortcut(0, ui.KeyEscape) {
				escaped = true
			}
			roll.Children(func() {
				ui.Box(c.Key("room")).Width(w).Height(max(h, s.top+s.high+room+24)).Children(func() {
					col := ui.Column(c.Key("stage")).Absolute().Left((w-sw)/2).Width(sw).
						Padding(sheetPadY, sheetPadR, sheetPadY, sheetPadL).ZIndex(3).
						Role(ui.RoleDialog).Label(locales.ClipsCard()).FontSize(theme.Body).LineHeight(1.5).TextColor(theme.Text) // over the dark
					col.Clicked() // a click on the stage is not one in the dark
					b := col.LayoutBounds()
					// Turned, the sheet keeps its size, as a book's page does, growing only for a longer passage: the dial stays
					// under the pointer turning it. Once the dial is let go of, the sheet draws in to fit its passage. (Not past
					// the window: a sheet that only scrolls keeps no room.)
					if cs.turns != s.turn {
						s.turn, s.turnAt = cs.turns, now
					}
					fits := b.H - s.spare
					wait := motion.SheetHold - now.Sub(s.turnAt)
					hold := onDial || wait > 0
					if hold {
						s.tall = max(s.tall, fits)
						if !onDial {
							c.After(wait)
						}
					} else {
						s.tall = fits
					}
					keep := min(s.tall, max(0, h-48-room))
					if a.kbd || a.still(c) || !placed {
						keep = col.Jump("keep", keep)
					} else {
						keep = col.AnimateTo("keep", keep, ui.Animation{Spring: motion.Follow.Spring})
					}
					col.MinHeight(keep)
					s.high = b.H
					if b.H > 0 {
						// While the dial is in hand the sheet stays where it stands, so that a turn moves only its foot; it rises
						// only as far as keeps it and its dial in the window, and no higher than 24 from its top. Let go of, it
						// settles in the middle of the window again with its size.
						if s.top == 0 || !hold {
							s.top = max(sheetEdge, (h-min(fits, b.H)-room)/2)
						}
						s.top = max(24, min(s.top, h-24-room-b.H))
					}
					top := s.top
					switch {
					case top == 0:
						top = h
					case a.kbd || a.still(c) || !placed:
						top = col.Jump("top", top)
					default:
						top = col.AnimateTo("top", top, ui.Animation{Spring: motion.Follow.Spring})
					}
					col.Top(top)
					sheet := ui.Rect{X: (w - sw) / 2, Y: top, W: sw, H: b.H}
					cs.cardBox = ui.Rect{X: sheet.X + sheetPadL, Y: sheet.Y + sheetPadY - ry, W: sw - sheetPadL - sheetPadR, H: max(0, b.H-2*sheetPadY)}
					switch {
					case s.still && !s.keyed:
						// Less motion: all of it comes into focus where it stands.
						col.Opacity(own)
						if bl := ownSoft * (1 - own); s.open && bl > 0.05 {
							col.Blur(bl)
						}
					case !placed:
						col.Opacity(0)
					}
					col.Children(func() { a.stageWords(c, p, s, cs.order[at], at, len(cs.order), q, own, wq) })
					hit := sheet
					if placed {
						g := a.stageSheet(c, s, sheet, ry, q, own, e)
						if len(cs.order) > 1 {
							d := a.clipDial(c, s, at, g, own)
							hit.H = d.Y + d.H - hit.Y
							s.dial = ui.Rect{X: d.X, Y: d.Y - ry, W: d.W, H: d.H}
						}
					}
					s.hit = ui.Rect{X: hit.X - stageHit, Y: hit.Y - ry - stageHit, W: hit.W + 2*stageHit, H: hit.H + 2*stageHit}
				})
			})
		})
		if s.open && !editing && ((clicked || back.Clicked()) && !near || escaped || back.OverlayShortcut(0, ui.KeyEscape)) {
			a.closeClip(c, false)
		}
		// Its article opening, it lets go with the page it stands on; back, it goes at once. (Given every
		// frame: one with an Exit that a frame leaves out counts as gone.)
		x := motion.Deep.Crossing()
		t := ui.ElementTransition{Colors: true, ExitDuration: -1, ExitEase: motion.Ease, Exit: &ui.Motion{Blur: motion.Defocus, Scale: x.To}}
		if s.leave || cs.stage != s {
			t.ExitDuration = x.Out
		}
		back.Transition(a.tr(c, t))
	})
	s.built = s.top > 0
}

// stageSheet lays the sheet the stage's words stand on, of the cards' own
// paper. It grows out of the passage's own place
// in the list as the words travel there (底色先动) and draws back into it as
// they go home; turned to a passage of another length, its foot follows on
// the follow spring. sheet is in the stage scroll's terms, ry below the
// window's; it returns where it is drawn, in the same terms.
func (a *App) stageSheet(c *ui.Context, s *clipStage, sheet ui.Rect, ry, q, own, e float32) ui.Rect {
	g := ui.Box(c.Key("sheet")).Absolute().ZIndex(2).Background(theme.Surface).InsetShadow(0, 0, 0, 1, theme.LineSoft).IgnoreInput(true).AccessHidden(true)
	r, radius := sheet, float32(12)
	from := s.from.box
	if h := s.from.head; s.from.cite.W > 0 && h.W > 0 {
		// A person's latest passage stands under their face and name: the sheet grows out of both, which travel on it.
		from = ui.Rect{X: min(from.X, h.X), Y: min(from.Y, h.Y), W: max(from.X+from.W, h.X+h.W) - min(from.X, h.X), H: from.Y + from.H - min(from.Y, h.Y)}
	}
	from.Y += ry // the list's place in the window, in the scroll's terms
	switch {
	case !s.keyed && !s.still && q < 1 && from.W > 0:
		r = ui.Rect{X: from.X + (sheet.X-from.X)*q, Y: from.Y + (sheet.Y-from.Y)*q, W: from.W + (sheet.W-from.W)*q, H: from.H + (sheet.H-from.H)*q}
		radius = 6 + 6*q
		g.Jump("y", r.Y)
		g.Jump("h", r.H)
	default:
		// Its place and height are the sheet's own, which move on their springs already (top, keep) or grow with what
		// it holds (a note opening): following them on a spring of its own, it would lag behind its foot.
		g.Jump("y", r.Y)
		g.Jump("h", r.H)
	}
	g.Left(r.X).Top(r.Y).Size(r.W, r.H).Radius(radius).Shadow(0, 24, 64, 0, theme.ShadowDialog.Alpha(float32(theme.ShadowDialog.A)/255*e))
	if own > 0.01 {
		// The cards' own grey halo at its top, as the person's card has it, coming in with the stage's own parts. (Clipped
		// only then: a sheet clipping as it grows would be masked anew each frame.)
		g.ClipX().ClipY().Children(func() {
			ui.Box(c.Key("halo")).Absolute().Fill().Opacity(own).Children(func() { a.aura(c, true) })
		})
	}
	if s.still && !s.keyed {
		g.Opacity(own)
	}
	s.sheet = ui.Rect{X: r.X, Y: r.Y - ry, W: r.W, H: r.H}
	return r
}

// clipDial is the scale under the sheet: a tick for each passage in the
// page's order, a longer one where another person's begin, read as the
// app's rulers read. The needle stands over the passage on the stage, and
// a turn slides it there (a scale too long for the dial slides under it
// instead). A tick pointed at
// lights, and whose words it holds shows under the scale; pressed, the
// stage turns to it. The ticks come in from the needle outward with the
// stage's own parts and draw back into it as they go. The arrows at its
// ends turn one at a time.
func (a *App) clipDial(c *ui.Context, s *clipStage, at int, sheet ui.Rect, own float32) ui.Rect {
	cs := a.clipsSt()
	n := len(cs.order)
	pos, first := make([]float32, n), make([]bool, n)
	for i, cl := range cs.order {
		first[i] = i == 0 || personOf(cl) != personOf(cs.order[i-1])
		if i > 0 {
			pos[i] = pos[i-1] + tickStep
			if first[i] {
				pos[i] += tickStep
			}
		}
	}
	// As wide as its ticks and a little room at either end, up to dialW: with few passages the arrows stay by them.
	sw := min(dialW-64, pos[n-1]+48)
	r := ui.Rect{X: sheet.X + (sheet.W-sw-64)/2, Y: sheet.Y + sheet.H + dialGap, W: sw + 64, H: dialH}
	hot := -1
	ui.Row(c.Key("dial")).Absolute().Left(r.X).Top(r.Y).Size(r.W, r.H).AlignItems(ui.Center).Gap(8).ZIndex(3).Children(func() {
		turn := func(key, icon, label, tip string, to int) {
			none := to < 0 || to >= n
			b := a.iconBtn(c, key, iconOpt{icon: icon, label: label, tip: tip, small: true, disabled: none})
			if own < 1 {
				b.Opacity(own * map[bool]float32{true: 0.4, false: 1}[none])
			}
			if b.Clicked() && !none {
				a.showClip(cs.order[to].ID)
			}
		}
		turn("prev", "back", locales.ClipsPrevious(), locales.ClipsPreviousTip(), at-1)
		scale := ui.ButtonBase(c.Key("scale")).Label(locales.ClipsPosition(at+1, n)).Width(sw).Height(dialH).FocusRing(false).TabStop(false)
		mid := sw / 2
		// A scale that fits stands still, centred, and the needle slides along it; a longer one slides under the needle,
		// which stays in the middle. Both on the follow spring; at once from the keys and under less motion.
		view := pos[n-1] / 2
		if pos[n-1] > 1.7*mid {
			view = pos[at]
		}
		move := func(key string, to float32) float32 {
			if a.kbd || a.still(c) {
				return scale.Jump(key, to)
			}
			return scale.AnimateTo(key, to, ui.Animation{Spring: motion.Follow.Spring})
		}
		off, needleAt := move("view", view), move("needle", pos[at])
		if px, _, over := scale.PointerPosition(); over && own > 0.99 && s.open {
			best := float32(tickStep) + 0.5 // the nearest tick, a step away at most: in a gap between two people, the first
			for i := range n {
				if d := float32(math.Abs(float64(mid + pos[i] - off - px))); d < best {
					best, hot = d, i
				}
			}
		}
		if hot == at {
			hot = -1 // the needle's own: nothing to name
		}
		if hot >= 0 && scale.Clicked() {
			a.showClip(cs.order[hot].ID)
		}
		lit := hot
		scale.Draw(func(pt *ui.Painter, b ui.Rect) {
			hair := 1 / pt.Scale()
			snap := func(v float32) float32 { return float32(math.Round(float64(v*pt.Scale()))) / pt.Scale() }
			foot := b.Y + b.H/2 + 7
			for i := range n {
				dx := pos[i] - off
				d := float32(math.Abs(float64(dx))) / mid
				if d >= 1 {
					continue
				}
				// Out from the needle as it comes in; fading toward the ends, where a longer scale runs on unseen.
				near := float32(math.Abs(float64(pos[i] - needleAt)))
				o := min(1, max(0, 1.6*own-0.6*near/mid)) * min(1, (1-d)/0.3)
				if o < 0.01 {
					continue
				}
				l, ink := float32(7), 0.3+0.45*lampLight(near, 56)
				if first[i] {
					l = 12
				}
				if i == lit {
					l, ink = l+4, 0.9
				}
				pt.Fill(ui.Rect{X: b.X + snap(mid+dx), Y: foot - l, W: hair, H: l}, theme.RulerInk.Alpha(ink*o), 0)
			}
			// The needle: brighter and a little heavier, fading in at its top, standing past the ticks' foot.
			needle := theme.Needle.Alpha(float32(theme.Needle.A) / 255 * own)
			pt.FillGradient(ui.Rect{X: b.X + snap(mid+needleAt-off), Y: foot - 20, W: snap(1), H: 24},
				ui.LinearGradient{Angle: 180, Stops: []ui.GradientStop{{Color: needle.Alpha(0), At: 0}, {Color: needle, At: 0.45}, {Color: needle, At: 1}}}, 0)
		})
		turn("next", "forward", locales.ClipsNext(), locales.ClipsNextTip(), at+1)
	})
	// Under the scale, whose words the tick pointed at holds, and how they open.
	if hot >= 0 {
		cs.tag = cs.order[hot].ID
	}
	tag := ui.Row(c.Key("tag")).Absolute().Left(r.X - 120).Top(r.Y + r.H).Width(r.W + 240).Height(dialTag).Justify(ui.Center).AlignItems(ui.Center).Gap(8).
		ZIndex(3).IgnoreInput(true).AccessHidden(true)
	k := tag.AnimateTo("on", map[bool]float32{true: 1, false: 0}[hot >= 0], a.ease(motion.Hover))
	if i := slices.IndexFunc(cs.order, func(cl *library.Clip) bool { return cl.ID == cs.tag }); i >= 0 && k > 0.01 {
		cl := cs.order[i]
		tag.Opacity(k).Children(func() {
			ui.Text(c, personOf(cl)).FontSize(theme.Label).LineHeight(16.0 / 12).FontWeight(500).TextColor(theme.Text2).NoWrap().Shrink(0)
			ui.Text(c, strings.ReplaceAll(excerpt(cl.Text, nil), "\n", " · ")).FontSize(theme.Label).LineHeight(16.0 / 12).TextColor(theme.Muted).SingleLine().Ellipsis("…").MaxWidth(300)
		})
	}
	return r
}

// stageWords are what stands on the sheet: who said it and the day it was
// written beside them; the words with their mark and the article under
// them, the note in the margin; the foot. Who said it, the words and the
// article come from the list on the shared value; the rest is the stage's
// own.
func (a *App) stageWords(c *ui.Context, p page, s *clipStage, cl *library.Clip, at, total int, q, own, wq float32) {
	cs := a.clipsSt()
	f := s.from
	travelling := !s.keyed && !s.still && q < 1 && f.quote.W > 0
	if s.still && !s.keyed {
		own = 1 // the stage fades as one (clipsStage)
	}
	// The stage's own parts come in where they stand, rising out of a little blur; going, they only fade.
	mine := func(e ui.Element, k float32) {
		if o := own * k; o < 1 {
			e.Opacity(o)
		}
		if own < 1 && s.open {
			e.Translate(0, ownRise*(1-own))
			if bl := ownSoft * (1 - own); bl > 0.05 {
				e.Blur(bl)
			}
		}
	}
	// Turned to another passage, what changes crosses over where it stands: the one going fades over SwapOut,
	// and once it is half gone the one coming fades in over SwapIn. At once from the keys. (Its clock runs from the opening on,
	// so that what does not change, the person turning to their own next passage, stays.)
	swap := func(e ui.Element, run uint64) float32 {
		k := e.Keyframes("swap", ramp(motion.SwapOut/2, motion.SwapIn), ui.KeyframeOptions{Run: run, NonMotion: true}) // once the one going is half gone
		if !cs.turned || a.kbd {
			return 1
		}
		return k
	}
	// What comes in drifts a little the way the dial slid as it fades in; what goes only fades where it stands.
	drift := func(e ui.Element, k float32) {
		if k < 1 && !a.still(c) {
			e.Translate(cs.dir*8*(1-k), 0)
		}
	}
	crossOut := func(e ui.Element) {
		// Given every frame: one with an Exit that a frame leaves out counts as gone, and leaves a copy.
		t := ui.ElementTransition{Colors: true, ExitDuration: motion.SwapOut, ExitEase: motion.Ease, Exit: &ui.Motion{}}
		if !s.open {
			t.ExitDuration = -1 // going back, nothing turns
		}
		e.Transition(a.tr(c, t))
	}
	// ← → turn to the neighbours, outside the note being written (which keeps its arrows).
	if s.open && at > 0 {
		prev := cs.order[at-1].ID
		c.OnShortcut(0, ui.KeyLeft, func() { a.kbd = true; a.showClip(prev) })
	}
	if s.open && at+1 < total {
		next := cs.order[at+1].ID
		c.OnShortcut(0, ui.KeyRight, func() { a.kbd = true; a.showClip(next) })
	}
	// The person crosses over with the words only when the stage turns to someone else.
	ic, who := a.iconOfClip(cl), personOf(cl)
	if cs.who != who {
		cs.who = who
		cs.whoRun++
	}
	head := ui.Row(c.Key("head")).Height(32).Gap(8).AlignItems(ui.Center)
	hb := head.LayoutBounds()
	head.Children(func() {
		face, gap := float32(32), float32(12)
		if travelling && f.face > 0 {
			face, gap = f.face+(32-f.face)*q, f.gap+(12-f.gap)*q
		}
		wr := ui.Row(c.Key("who:" + who)).Gap(gap).AlignItems(ui.Center).MinWidth(0).Shrink(1).Role(ui.RoleHeading).Level(2).Label(who)
		if k := swap(wr, cs.whoRun); k < 1 {
			wr.Opacity(k)
			drift(wr, k)
		}
		crossOut(wr)
		if travelling && f.head.W > 0 && hb.W > 0 {
			travel(wr, f.head, ui.Rect{X: hb.X, Y: hb.Y + (32-face)/2}, 1, q)
		}
		wr.Children(func() {
			a.avatar(c, who, ic, face)
			ui.Text(c, who).FontSize(theme.Item).LineHeight(20.0 / 15).FontWeight(600).TextColor(theme.Text).SingleLine().Ellipsis("…").Shrink(1).MinWidth(0)
			if d := dateText(cl.Citation.PublishedAt); d != "" {
				// When it was written sits by who wrote it, small and quiet.
				t := ui.Text(c.Key("date:"+cl.ID), d).FontSize(theme.Label).LineHeight(16.0 / 12).TextColor(theme.Muted).
					FontFeatures(theme.Numbers).NoWrap().Shrink(0)
				mine(t, swap(t, cs.turns))
				crossOut(t)
			}
		})
	})
	wash := a.washOf(c, "card")
	st := a.stageStep(cl)
	sz := stepSize[st]
	fam, k := reader.PassageFace(cl.Text)
	size, line := sz[0]*k*stageScale, sz[1]*stageScale
	// The mark lights as the words land, or stays lit if the pointer had lit it in the list; going back, it
	// dims as the list's will be.
	lit := s.open && (q >= motion.StageLand || s.lit)
	listIn, stageIn := motion.StageCross(q)
	body := ui.Column(c.Key("body:"+cl.ID)).Width(wq).Margin(36, 0, 0, 0)
	if k := swap(body, cs.turns); k < 1 {
		body.Opacity(k)
		drift(body, k)
	}
	crossOut(body)
	body.Children(func() {
		ui.Column(c.Key("quote")).MinWidth(0).Children(func() {
			words := ui.Column(c.Key("words")).MinWidth(0)
			if to := words.LayoutBounds(); travelling && to.W > 0 {
				travel(words, f.quote, to, f.look.size/size, q)
			}
			words.Children(func() {
				if travelling && listIn > 0 {
					// The list's words cross into the stage's under them as they lift off, while the two nearly coincide.
					a.listWords(c, f.look, size/f.look.size, f.quote.W, s.open && s.lit).Opacity(listIn)
				}
				stand := ui.Column(c.Key("stand")).MinWidth(0)
				if travelling && stageIn < 1 {
					stand.Opacity(stageIn)
				}
				stand.Children(func() {
					// The pictures the passage ran across stand where they were among its words, the card's own: they
					// come in with what only the card has.
					pics := a.clipPictures(cl, nil)
					_, wh := c.Size()
					shown, i, pic := 0, 0, false
					under := "" // the caption of the picture just shown, which the words after it may be
					for _, part := range passageParts(cl.Text, cl.Pictures) {
						gap := float32(0)
						if shown > 0 {
							gap = 16 * stageScale
						}
						if part.unit != "" {
							pc, ok := pics[part.unit]
							if !ok || pc.Img == nil || pc.W <= 0 || pc.H <= 0 {
								continue
							}
							if pic {
								gap = 8 // pictures one after another make a group
							}
							e := cardPicture(c, "pic:"+part.unit, pc, wq, cardTall*wh).Margin(gap, 0, 0, 0)
							e.Transition(a.tr(c, ui.ElementTransition{Duration: motion.SwapIn, Ease: motion.Ease, Enter: &ui.Motion{}}))
							mine(e, 1)
							shown, pic, under = shown+1, true, strings.Join(strings.Fields(pc.Caption), " ")
							continue
						}
						l := part.words
						if under != "" && strings.Contains(under, strings.Join(strings.Fields(l), " ")) {
							// Its picture's caption, set as the reader sets one: small and quiet, close under it.
							capSize := theme.Body * k * stageScale
							washed(c, fmt.Sprint("cap:", shown), wash, func() ui.Element {
								return ui.Text(c, l).Font(fam).FontSize(capSize).LineHeight(1.5).TextColor(theme.Muted).Pretty()
							}).FillWidth().Margin(8*stageScale, 0, 0, 0)
							shown, pic, under = shown+1, false, ""
							continue
						}
						under = ""
						lead := i == 0
						shown, i, pic = shown+1, i+1, false
						ui.Box(c.Key(i)).Margin(gap, 0, 0, 0).Children(func() {
							if lead {
								a.icon(c, "clips", stepMark[st]*stageScale, lit).Absolute().Top(8 * stageScale).Left(markLeft(st) * stageScale).TextColor(theme.Text2)
							}
							// Later paragraphs hang a Western opening quote the same way.
							first, _ := utf8.DecodeRuneInString(l)
							if !lead && strings.ContainsRune("“‘\"'", first) {
								ui.Text(c, string(first)).Absolute().Top(0).Left(-textWidthIn(string(first), fam, size)).Font(fam).FontSize(size).LineHeight(line / size).TextColor(theme.Text)
								l = l[utf8.RuneLen(first):]
							}
							washed(c, "p", wash, func() ui.Element {
								return ui.Text(c, l).Font(fam).FontSize(size).LineHeight(line / size).LetterSpacing(track(st) * stageScale).TextColor(theme.Text).Pretty()
							}).FillWidth()
						})
					}
				})
			})
			// The article, as a reference reads: 《title》. The title is the way back to it: it opens the article at this
			// passage. Its underline is the stage's own.
			title := clipTitle(cl)
			cite := ui.Row(c.Key("cite")).Margin(16, 0, 0, 0).AlignItems(ui.Start).FontSize(theme.Body).LineHeight(20.0 / 13).TextColor(theme.Text2)
			cb := cite.LayoutBounds()
			s.cite = cb
			if travelling && cb.W > 0 {
				from := f.cite
				if from.W <= 0 {
					// Under a lead the list names the article above a run: here it rides in under the words, coming with them.
					from = ui.Rect{X: f.quote.X, Y: f.quote.Y + f.quote.H + 16}
					cite.Opacity(stageIn)
				}
				travel(cite, from, cb, 1, q)
			}
			cite.Children(func() {
				hanTitle := han.MatchString(title)
				if hanTitle {
					ui.Text(c, "《")
				}
				link := ui.ButtonBase(c.Key("title")).Label(title).Radius(2).FocusRing(false).Shrink(1).MinWidth(0)
				s.leave = link.Pressed() // about to open the article: the stage will let go with its page
				if link.Clicked() {
					a.openClip(c, cl)
				}
				color, under := theme.Text2, theme.Faint
				if link.Hovered() {
					color, under = theme.Text, theme.Text2
				}
				if link.FocusVisible() {
					link.FocusRing(true)
				}
				link.Children(func() {
					fg, line := link.AnimateColor("fg", color, a.ease(motion.Hover)), link.AnimateColor("line", under, a.ease(motion.Hover))
					t := washed(c, "cite", wash, func() ui.Element {
						if own < 0.01 {
							// Not underlined yet: an underline gone clear is drawn in the text's colour.
							return ui.Text(c, title).FontSize(theme.Body).LineHeight(20.0 / 13).TextColor(fg).Pretty()
						}
						return citeTitle(c, title).TextColor(fg).DecorationColor(line.Alpha(own))
					})
					a.end(t, p, "title:"+cl.ArticleID)
				})
				a.tip(c, link, locales.ClipsShowInArticle(), false)
				if hanTitle {
					ui.Text(c, "》")
				}
			})
		})
		a.cardMargin(c, cl, wash, mine)
	})
	// A shorter passage than the sheet keeps leaves room above the foot.
	s.spare = ui.Box(c.Key("room")).Grow(1).LayoutBounds().H
	// The foot is the passage's own too: it crosses over with it.
	foot := ui.Row(c.Key("foot:"+cl.ID)).Gap(12).Margin(40, 0, 0, 0).AlignItems(ui.Center)
	fk := swap(foot, cs.turns)
	mine(foot, fk)
	drift(foot, fk)
	crossOut(foot)
	foot.Children(func() {
		ui.Text(c, locales.ClipsClippedOn(dateText(&cl.CreatedAt))).FontSize(theme.Label).LineHeight(16.0 / 12).TextColor(theme.Muted).FontFeatures(theme.Numbers)
		ui.Box(c).Grow(1)
		a.notePen(c, cl)
		del := a.toolButton(c, "delete", "trash", locales.CommonDelete(), false, false).Size(32, 32).TabStop(true)
		if del.Clicked() {
			a.closeClip(c, false)
			// Focus goes to a neighbouring passage once it is back, since this one folds away then.
			if at+1 < total {
				s.focus = cs.order[at+1].ID
			} else if at > 0 {
				s.focus = cs.order[at-1].ID
			}
			cs.doomed = cl.ID
		}
		a.handOff(c, cl)
	})
}

// travel takes e, laid out at to on the stage, to where the list has it,
// from, by 1 − q: its corner on that corner, and its size scale of its own
// at q 0.
func travel(e ui.Element, from, to ui.Rect, scale, q float32) {
	k := 1 - q
	e.TransformOrigin(0, 0)
	if scale != 1 {
		s := scale + (1-scale)*q
		e.Scale(s, s) // first: a scale after the move would scale the move too
	}
	e.Translate((from.X-to.X)*k, (from.Y-to.Y)*k)
}

// listWords draws a passage's words as the list sets them, width wide,
// scale times as large from their corner, the mark lit as the pointer
// lights it: what the stage's words cross over from as they lift off, and
// into as they land.
func (a *App) listWords(c *ui.Context, l quoteLook, scale, width float32, lit bool) ui.Element {
	return ui.Box(c.Key("list")).Absolute().Left(0).Top(0).Width(width).TransformOrigin(0, 0).Scale(scale, scale).IgnoreInput(true).AccessHidden(true).
		Children(func() {
			if l.mark > 0 {
				ink := theme.Muted
				if lit {
					ink = theme.Text2
				}
				a.icon(c, "clips", l.mark, lit).Absolute().Top(l.markY).Left(l.markX).TextColor(ink)
			}
			ui.Text(c, l.text).Font(l.fam).FontSize(l.size).LineHeight(l.line / l.size).LetterSpacing(l.track).TextColor(theme.Text).
				MaxLines(l.lines).Ellipsis("…").Pretty().FillWidth()
		})
}

// stageStep is the type the stage sets a passage in, stageScale times as
// large: the opening spread's for its length, or the page's if it sets it
// larger (a lone passage across the page).
func (a *App) stageStep(cl *library.Clip) step {
	st := leadStep(cl.Text, true)
	if s, ok := a.clipsSt().steps[cl.ID]; ok && s < st {
		st = s
	}
	return st
}

// track is the tracking of the larger steps.
func track(st step) float32 {
	switch st {
	case stepXL:
		return -0.01 * 32
	case stepL:
		return -0.005 * 24
	}
	return 0
}

// markLeft is where a lead's hanging mark stands, from the words' left edge.
func markLeft(st step) float32 {
	if st == stepXL {
		return -12 - stepMark[st]
	}
	return -8 - stepMark[st]
}

// citeTitle is the title in the stage's citation: ui/controls LinkButton's
// look, underlined.
func citeTitle(c *ui.Context, s string) ui.Element {
	return ui.Text(c, s).FontSize(theme.Body).LineHeight(20.0 / 13).TextColor(theme.Text2).Underline().UnderlineOffset(3).Pretty()
}

// cardMargin is the reader's own line under the article, after a short
// amber stroke as the list sets it: edited in place, saved on leaving; Esc
// puts the old words back. With no note it is not there at all: the pen in
// the foot (notePen) opens it, growing in under the article, and it folds
// away again if left empty. mine brings it in with the stage's own parts.
func (a *App) cardMargin(c *ui.Context, cl *library.Clip, wash float32, mine func(ui.Element, float32)) {
	cs := a.clipsSt()
	n := &cs.note
	note := strings.TrimSpace(cl.Note)
	editing := n.editing && n.id == cl.ID
	if note == "" && !editing {
		return
	}
	m := ui.Column(c.Key("margin")).MinWidth(0).AlignItems(ui.Start).Padding(20, 0, 0, 20).Role(ui.RoleRegion).Label(locales.ClipsNote())
	// Opened or let go of on the same passage, it opens its height and comes in, or folds away; with the passage it
	// comes and goes as the stage's own.
	m.Transition(a.tr(c, ui.ElementTransition{Duration: motion.SwapIn, Ease: motion.Ease, ExitDuration: motion.SwapOut, ExitEase: motion.Ease,
		Height: true, Enter: &ui.Motion{Collapse: true}, Exit: &ui.Motion{Collapse: true}}))
	mine(m, 1)
	m.Children(func() {
		ui.Box(c).Absolute().Top(20+10).Left(0).Size(12, 1).Background(theme.Accent).IgnoreInput(true)
		if editing {
			in := ui.TextAreaBase(c.Key("editor"), &n.draft).Label(locales.ClipsNote()).Placeholder(locales.ClipsNotePlaceholder()).MaxLength(4000).Lines(1, 40).
				FillWidth().FontSize(theme.Body).LineHeight(20.0 / 13).TextColor(theme.Text).PlaceholderColor(theme.Muted).CaretColor(theme.Accent).AutoFocus()
			if in.OverlayShortcut(0, ui.KeyEscape) {
				n.editing, n.pen = false, true // the old words back
			}
			if in.Shortcut(ui.Cmd, ui.KeyEnter) {
				a.keepNote()
				n.pen = true
			} else if in.FocusLeft() {
				a.keepNote()
			}
			return
		}
		b := ui.ButtonBase(c.Key("note")).Label(locales.ClipsNoteLabel(note)).FillWidth().FocusRing(false)
		if b.Clicked() {
			*n = clipNote{id: cl.ID, draft: cl.Note, editing: true}
		}
		color := theme.Text2
		if b.Hovered() {
			color = theme.Text
		}
		if b.FocusVisible() {
			b.FocusRing(true).FocusRingOffset(4)
		}
		b.Children(func() {
			washed(c, "note", wash, func() ui.Element {
				return ui.Text(c, note).FontSize(theme.Body).LineHeight(20.0 / 13).TextColor(b.AnimateColor("fg", color, a.ease(motion.Hover))).Balance()
			}).FillWidth()
		})
	})
}

// notePen is the pen in the card's foot, by the bin: it opens the note under
// the article, the words already there to go on with; pressed again while
// writing, it puts the note away (saved). Its name is its tooltip.
func (a *App) notePen(c *ui.Context, cl *library.Clip) {
	n := &a.clipsSt().note
	label := locales.ClipsAddNote()
	if strings.TrimSpace(cl.Note) != "" {
		label = locales.ClipsEditNote()
	}
	// While the note is written the pen stays pressed in, and pressing it leaves the focus in the field, which would
	// otherwise lose it and save as the press began, so that the click puts the note away rather than opening it anew.
	writing := n.id == cl.ID && n.editing
	pen := a.toolButton(c, "note", "note", label, false, writing).Size(32, 32).TabStop(true)
	if writing {
		pen.KeepFocus()
	}
	if n.pen && n.id == cl.ID {
		pen.Focus()
		n.pen = false
	}
	if pen.Clicked() {
		if writing {
			a.keepNote()
			return
		}
		*n = clipNote{id: cl.ID, draft: cl.Note, editing: true}
	}
}

// handOff is 复制引文, the sheet's one button with words: the passage with its
// source, link and note as Markdown, for Claude Code, Codex or a note. It
// says 已复制 in place for two seconds, the tick drawn left to right; or
// 复制失败, its tip asking for another press.
func (a *App) handOff(c *ui.Context, cl *library.Clip) {
	cs := a.clipsSt()
	now := c.Now()
	run := cs.copy
	said := run.says(cl.ID, now)
	if said {
		c.After(motion.Copied - now.Sub(run.at))
	}
	tip := locales.ClipsCopyTip()
	noted := strings.TrimSpace(cl.Note) != ""
	switch {
	case said && run.failed:
		tip = locales.ClipsCopyFailedTip()
	case len(cl.Pictures) > 0 && noted:
		tip = locales.ClipsCopyTipWithPicturesAndNote()
	case len(cl.Pictures) > 0:
		tip = locales.ClipsCopyTipWithPictures()
	case noted:
		tip = locales.ClipsCopyTipWithNote()
	}
	var b ui.Element
	b = a.button(c, locales.ClipsCopyQuote(), btnOpt{words: func(color ui.Color, weight int) {
		// The wordings share one cell, so the button keeps its width as they cross.
		ui.Grid(c.Key("says")).Children(func() {
			for i, on := range []bool{!said, said} {
				w := ui.Row(c.Key(i)).Gap(4).AlignItems(ui.Center).Justify(ui.Center).RowStart(1).ColumnStart(1)
				instant := run.kbd || a.kbd
				k := w.AnimateTo("on", map[bool]float32{true: 1, false: 0}[on], ui.Animation{Duration: map[bool]time.Duration{true: motion.SwapIn, false: motion.SwapOut}[on], Ease: motion.Ease, NonMotion: true})
				if instant {
					k = w.Jump("on", map[bool]float32{true: 1, false: 0}[on])
				}
				if k < 0.999 {
					w.Opacity(k)
					if !a.still(c) {
						w.Translate(0, 4*(1-k))
					}
					if bl := 2 * (1 - k); bl > 0.05 {
						w.Blur(bl)
					}
				}
				if !on {
					w.AccessHidden(true)
				}
				w.Children(func() {
					if i == 0 {
						ui.Text(c, locales.ClipsCopyQuote()).TextColor(color).FontWeight(weight).NoWrap()
						return
					}
					if run.failed {
						ui.Text(c, locales.ClipsCopyFailed()).TextColor(color).FontWeight(weight).NoWrap()
						return
					}
					tick := a.icon(c, "check", 16, false).TextColor(color)
					if on && !instant && !a.still(c) {
						d := tick.Keyframes("draw", ramp(100*time.Millisecond, motion.SwapIn), ui.KeyframeOptions{Run: uint64(run.at.UnixNano())})
						if d < 1 {
							tick.ClipInset(0, 16*(1-d), 0, 0, 0)
						}
					}
					ui.Text(c, locales.CommonCopied()).TextColor(color).FontWeight(weight).NoWrap()
				})
			}
		})
	}})
	a.tip(c, b, tip, false)
	if b.Clicked() {
		kbd, still := a.kbd, a.still(c)
		a.copyClip(c, cl, func(err error) {
			cs.copy = copyRun{id: cl.ID, at: now, failed: err != nil, kbd: kbd || still}
			if err == nil && !kbd {
				cs.washes["card"] = washRun{at: now, fade: still}
			}
		})
	}
}

// openClip opens a passage's article from the stage: the same crossing as
// from a list, the sheet spreading into the reader's page while the
// article's title travels to the headline; the stage and the dark let go
// with the page. Back, the page shrinks into the passage, at
// the scroll it had, which takes the focus.
func (a *App) openClip(c *ui.Context, cl *library.Clip) {
	if a.article(cl.ArticleID) == nil {
		a.fail(locales.ClipsArticleGone())
		return
	}
	cs := a.clipsSt()
	// The sheet becomes the article's page.
	var line ui.Rect
	if s := cs.stage; s != nil {
		line = s.sheet
	}
	a.closeClip(c, true)
	cs.back = cl.ID
	a.openAs(c, cl.ArticleID, line, citeTitle, theme.Body)
}

// ramp goes from 0 to 1 over d, after delay, along the one curve.
func ramp(delay, d time.Duration) []ui.Keyframe {
	if delay <= 0 {
		return []ui.Keyframe{{Value: 0}, {At: d, Value: 1, Ease: motion.Ease}} // keyframe times must increase
	}
	return []ui.Keyframe{{Value: 0}, {At: delay, Value: 0}, {At: delay + d, Value: 1, Ease: motion.Ease}}
}

// clipsKeys are 摘录's: Esc clears the search. (The stage's Esc, ← and →
// are its own: clipsStage, stageWords.)
func (a *App) clipsKeys(c *ui.Context) {
	cs := a.clipsSt()
	if cs.open == "" && c.Shortcut(0, ui.KeyEscape) && cs.query != "" {
		cs.query, cs.search = "", false
	}
}
