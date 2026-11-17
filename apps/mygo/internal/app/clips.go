package app

import (
	"fmt"
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
	settled *clipsPlaced       // where everyone went, once composed from measured heights
	seen    map[string]bool    // the passages here when the page came, which do not rise
	arrived uint64             // the crossing the page came by
	copied  map[string]time.Time
	washes  map[string]washRun // what was copied lighting up, by passage ("card" for the card's)
	open    string             // the passage on the stage (引文上台)
	stage   *clipStage         // the stage while it shows: coming, there or going back
	turns   uint64             // its turns to another passage, to cross them over
	turned  bool
	who     string // the person the stage showed, to cross them over when it changes
	whoRun  uint64
	cardBox ui.Rect            // the stage's words as last laid out
	boxes   map[string]ui.Rect // the passages as last laid out (App.Press)
	steps   map[string]step    // the leads' type as the page last set them
	folding map[string]*fold   // passages deleted, folding shut before they go
	doomed  string             // a passage deleted on the stage, which folds once it is back
	closed  time.Time
	note    clipNote
	copy    time.Time // 复制引文 said 已复制 then
	copyKbd bool
	back    string // the passage an article was opened from, which takes the focus back
	focus   string // the passage to take the focus in the next build
	order   []*library.Clip
}

type clipsPlaced struct {
	scope  string
	placed map[string]int
}

// clipNote is the note being written in the card's margin.
type clipNote struct {
	id, draft       string
	editing, cancel bool
}

// fold is a deleted passage folding shut: from the height
// it had, since when.
type fold struct {
	at   time.Time
	from float32
	sent bool // folded shut, the delete is on its way
}

// washRun is a wash under way: since when, and whether only fading (less motion).
type washRun struct {
	at   time.Time
	fade bool
}

func (a *App) clipsSt() *clipsState {
	if a.clips == nil {
		a.clips = &clipsState{heights: map[string]float32{}, seen: map[string]bool{}, copied: map[string]time.Time{}, washes: map[string]washRun{},
			boxes: map[string]ui.Rect{}, steps: map[string]step{}, folding: map[string]*fold{}}
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

// clipAuthor is shared/clip-markdown's: feeds often send "name@example.com
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

// clipMarkdown is shared/clip-markdown's: the passage with its source, link
// and note, as copied for an agent.
func clipMarkdown(cl *library.Clip) string {
	var quote []string
	for _, line := range strings.Split(strings.ReplaceAll(strings.TrimSpace(cl.Text), "\r\n", "\n"), "\n") {
		if strings.TrimSpace(line) != "" {
			quote = append(quote, "> "+strings.TrimRightFunc(line, unicode.IsSpace))
		} else {
			quote = append(quote, ">")
		}
	}
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
	return strings.Join(quote, "\n") + "\n\n— " + by + ", " + clipTitle(cl) + date + "\n" + cl.Citation.URL + note + "\n"
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

// excerpt is a passage's opening; searching, it starts a little before the
// first hit, so that the hit is in view.
func excerpt(text string, terms []string) string {
	flat := []rune(strings.TrimSpace(lineBreaks.ReplaceAllString(text, " ")))
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
// placed stay in their column, so a page deleted closes up where it was.
func arrange(people []*person, columns int, heightOf func(*person) (float32, bool), kept map[string]int) clipsLayout {
	hero, others := people[0], people[1:]
	type column struct {
		height float32
		people []*person
	}
	rest := make([]column, min(columns, len(others)))
	var used []int
	for _, p := range others {
		if was, ok := kept[p.name]; ok && !slices.Contains(used, was) {
			used = append(used, was)
		}
	}
	slices.Sort(used)
	onto := map[int]int{}
	if len(used) <= len(rest) {
		for i, was := range used {
			onto[was] = i
		}
	}
	placed := map[string]int{}
	for _, p := range others {
		col := -1
		if was, ok := kept[p.name]; ok {
			if i, ok := onto[was]; ok {
				col = i
			}
		}
		if col < 0 {
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
		if col < len(used) {
			placed[p.name] = used[col]
		} else {
			placed[p.name] = col
		}
	}
	// A column left empty means the old places no longer fit: compose afresh.
	if kept != nil && slices.ContainsFunc(rest, func(c column) bool { return len(c.people) == 0 }) {
		return arrange(people, columns, heightOf, nil)
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
		if cs.doomed != "" && (a.kbd || c.Now().Sub(cs.closed) >= motion.StageOut) {
			a.foldClip(c, cs.doomed)
			cs.doomed = ""
		} else if cs.doomed != "" {
			c.After(motion.StageOut - c.Now().Sub(cs.closed))
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
				a.emptyState(c, "clips", locales.ClipsEmptyTitle(), locales.ClipsEmptyText(), nil)
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
	box := ui.Box(c.Key(cl.ID)).FillWidth()
	box.Transition(a.passageMove(c, cl.ID))
	if !alone {
		a.folded(c, box, cl.ID) // alone, its page folds instead
	}
	hover := box.Hovered()
	box.Children(func() {
		// It reaches 32 into the side, so that the hanging mark is inside it, and so is the focus ring (stretched, less its margin).
		b := ui.ButtonBase(c.Key("open")).Label(excerpt(cl.Text, terms)).Margin(0, 0, 0, -32).Padding(0, 0, 0, 32).Radius(6).FocusRing(false)
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
						return ui.RichText(c, hits(excerpt(cl.Text, terms), terms, ui.Span{})...).Font(fam).FontSize(sz[0] * k).LineHeight(sz[1] / (sz[0] * k)).TextColor(theme.Text).
							LetterSpacing(track(st)).MaxLines(lines).Ellipsis("…").Pretty()
					})
					q.FillWidth()
					cite := washed(c, "cite", wash, func() ui.Element {
						return ui.RichText(c, hits(book(clipTitle(cl)), terms, ui.Span{})...).FontSize(theme.Body).LineHeight(20.0 / 13).TextColor(theme.Text2).
							SingleLine().Ellipsis("…")
					}).Margin(16, 0, 0, 0)
					if s != nil {
						s.from.quote, s.from.cite = q.LayoutBounds(), cite.LayoutBounds()
						s.from.look = quoteLook{text: excerpt(cl.Text, terms), fam: fam, size: sz[0] * k, line: sz[1], track: track(st), lines: lines,
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
		b := ui.ButtonBase(c.Key("open")).Label(excerpt(cl.Text, terms)).Margin(4, -12).Padding(12).Radius(6).FocusRing(false)
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
				ex := washed(c, "excerpt", wash, func() ui.Element {
					return ui.RichText(c, hits(excerpt(cl.Text, terms), terms, ui.Span{})...).Font(fam).FontSize(theme.Item * k).LineHeight(24 / (theme.Item * k)).TextColor(theme.Text).
						MaxLines(2).Ellipsis("…").Pretty()
				}).FillWidth()
				if s := a.clipsSt().onStage(cl.ID, live); s != nil {
					// The article named above a run stays: on the stage the passage's own comes with its words.
					s.from.quote, s.from.cite = ex.LayoutBounds(), ui.Rect{}
					s.from.look = quoteLook{text: excerpt(cl.Text, terms), fam: fam, size: theme.Item * k, line: 24, lines: 2}
					if s.hides() {
						ex.Opacity(0) // on the stage
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
	if live {
		cs.boxes[cl.ID] = r
		a.layInto(p, cl.ArticleID, r)
		if cs.focus == cl.ID {
			cs.focus = ""
			b.Focus()
		}
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
	held := false
	if at, ok := cs.copied[cl.ID]; ok {
		if left := motion.Copied - now.Sub(at); left > 0 {
			held = true
			c.After(left)
		} else {
			delete(cs.copied, cl.ID)
		}
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
	}
	bar.Children(func() {
		label := locales.ClipsCopyQuote()
		if held {
			label = locales.CommonCopied()
		}
		cp := a.toolButton(c, "copy", "copy", label, held)
		if cp.Clicked() {
			c.Services().WriteClipboard(clipMarkdown(cl))
			cs.copied[cl.ID] = now
			a.wash(c, cl.ID)
		}
		del := a.toolButton(c, "delete", "trash", locales.CommonDelete(), false)
		if del.Clicked() {
			a.foldClip(c, cl.ID)
		}
	})
}

// toolButton is a small IconButton of the clip bar: text-2, the hover
// ground; delete goes danger under the pointer; done, copy's dots flow
// into an amber check (DESIGN.md 点会走).
func (a *App) toolButton(c *ui.Context, key, icon, label string, done bool) ui.Element {
	b := ui.ButtonBase(c.Key(key)).Label(label).Size(24, 24).Radius(6).Center().Shrink(0).FocusRing(false).TabStop(false)
	color, ground := theme.Text2, ui.Transparent
	if b.Hovered() {
		color, ground = theme.Text, theme.Hover
		if key == "delete" {
			color = theme.Danger
		}
	}
	fg := b.AnimateColor("fg", color, a.ease(motion.Hover))
	b.Background(b.AnimateColor("bg", ground, a.ease(motion.Hover))).TextColor(fg)
	b.Children(func() { a.dotIcon(c, b, icon, 16, iconLook{ink: fg, copied: done}) })
	a.tip(c, b, label, false)
	return b
}

// --- Copying, deleting.

// wash lights up what was just copied of passage id (or "card"), so the eye
// sees what went: not from the keys; only fading under less motion.
func (a *App) wash(c *ui.Context, id string) {
	if a.kbd {
		return
	}
	a.clipsSt().washes[id] = washRun{at: c.Now(), fade: a.still(c)}
}

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
	if a.kbd || a.still(c) || a.busy {
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
	restore := library.AddClip{Clip: library.ClipInput{ID: cl.ID, ArticleID: cl.ArticleID, Text: cl.Text, Note: cl.Note, UnitID: cl.UnitID,
		ContentRevision: cl.ContentRevision, CreatedAt: cl.CreatedAt}}
	a.mutate(library.DeleteClip{ID: id}, func() { a.notify(locales.ClipsDeleted(), nil, restore) })
}

// --- The stage (引文上台).

// The stage sets a passage stageScale times as large as the opening spread
// would. The page behind it goes dark, black at stageDark over it (a
// quarter of its brightness left), and the card the words came from gets
// stageLift of that back (to 0.38) while the pointer rests in the dark: the
// way back. What only the stage has rises ownRise into place out of
// ownSoft of blur. Around its words the stage keeps stageHit of its own,
// where a click is not one in the dark.
const (
	stageScale       = 1.25
	stageDark        = 0.75
	stageLift        = 0.13
	ownRise, ownSoft = 4, 2
	stageHit         = 24
)

// clipFrom is where a passage's shared parts stand in the list, in the
// window, as the page last laid them out: the person's page, the row of
// their face and name (the face's size, the gap after it), the words, and
// the article (none under a lead, where the article is named above a run).
type clipFrom struct {
	card, head, quote, cite ui.Rect
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
	hit    ui.Rect   // the stage with its own room around the words, as last laid out
	cite   ui.Rect   // the article's line, whose ground becomes the article's page
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
		s.id, s.from, s.cut = id, clipFrom{}, a.kbd
		cs.turns++
		cs.turned = true
	case s == nil || s.id != id:
		cs.stage = &clipStage{id: id, open: true, keyed: a.kbd}
		cs.turned = false
	}
	cs.open = id
	cs.note = clipNote{}
}

// closeClip sends the passage on the stage back into the page, which takes
// the focus back once it is there (syncStage); leaving, the stage goes with
// its page instead (an article opening), as its last build said.
func (a *App) closeClip(c *ui.Context, leaving bool) {
	cs := a.clipsSt()
	if leaving {
		cs.stage = nil
	}
	cs.open, cs.closed = "", c.Now()
	c.Invalidate()
}

// clipsStage builds the stage over the window while a passage is on it or
// on its way: the dark over the page, the card the words came from a way
// back in it, and the passage standing in the middle of the window. A
// click in the dark, or Esc, sends it back; ← → turn to the neighbours.
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
		cs.stage, cs.open = nil, "" // its passage is gone
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
	wq := min(600, w-2*48-48-176)
	editing := cs.note.editing
	ui.Overlay(c, func() {
		// Modal until it is back: the page takes no clicks while the words go home.
		back := ui.Box(c.Key("clip-stage")).Absolute().Left(0).Top(0).Right(0).Bottom(0).Center().ObserveInput(a.observe).Modal()
		px, py, in := back.PointerPosition()
		r := s.hit
		over := s.open && in && !(px >= r.X && px < r.X+r.W && py >= r.Y && py < r.Y+r.H)
		lift := back.AnimateTo("lift", map[bool]float32{true: 1, false: 0}[over], a.ease(motion.Hover))
		back.Children(func() {
			dark := ui.Box(c.Key("dark")).Absolute().IgnoreInput(true).AccessHidden(true)
			if r := s.from.card; r.W > 0 {
				// The card is a window in the dark, which lifts while the pointer rests outside the stage.
				dark.Left(r.X).Top(r.Y).Size(r.W, r.H).Radius(10).Background(theme.Darken.Alpha(e*(stageDark-stageLift*lift))).
					Shadow(0, 0, 0, w+h, theme.Darken.Alpha(e*stageDark))
			} else {
				dark.Left(0).Top(0).Right(0).Bottom(0).Background(theme.Darken.Alpha(e * stageDark))
			}
			col := ui.Column(c.Key("stage")).Width(wq + 48 + 176 + 2*stageHit).Padding(stageHit).ZIndex(1).Role(ui.RoleDialog).Label(locales.ClipsCard()).
				FontSize(theme.Body).LineHeight(1.5).TextColor(theme.Text) // over the dark
			col.Clicked() // a click on the stage is not one in the dark
			b := col.LayoutBounds()
			s.hit, cs.cardBox = b, ui.Rect{X: b.X + stageHit, Y: b.Y + stageHit, W: b.W - 2*stageHit, H: b.H - 2*stageHit}
			switch {
			case s.still && !s.keyed:
				// Less motion: all of it comes into focus where it stands.
				col.Opacity(own)
				if bl := ownSoft * (1 - own); s.open && bl > 0.05 {
					col.Blur(bl)
				}
			case !s.built && !s.keyed:
				col.Opacity(0) // laid out first, so that its parts know where they go
			}
			col.Children(func() { a.stageWords(c, p, s, cs.order[at], at, len(cs.order), q, own, wq) })
		})
		if s.open && !editing && (back.Clicked() || back.OverlayShortcut(0, ui.KeyEscape)) {
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
	s.built = true
}

// stageWords are what stands on the stage: who said it, the day it was
// written beside them and the turning at the other end; the words with
// their mark and the article under them, the note in the margin; the
// foot. Who said it, the words and the article come from the list on the
// shared value; the rest is the stage's own.
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
	// Turned to another passage, what changes crosses over where it stands: the one coming fades in over
	// SwapIn as the one going fades over SwapOut. At once from the keys. (Its clock runs from the opening on,
	// so that what does not change, the person turning to their own next passage, stays.)
	swap := func(e ui.Element, run uint64) float32 {
		k := e.Keyframes("swap", ramp(0, motion.SwapIn), ui.KeyframeOptions{Run: run, NonMotion: true})
		if !cs.turned || a.kbd {
			return 1
		}
		return k
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
		ui.Box(c).Grow(1)
		if total > 1 {
			turn := ui.Row(c.Key("turn")).AlignItems(ui.Center)
			mine(turn, 1)
			turn.Children(func() {
				prev := a.iconBtn(c, "prev", iconOpt{icon: "back", label: locales.ClipsPrevious(), tip: locales.ClipsPreviousTip()}).Size(28, 28)
				if at == 0 {
					prev.Opacity(0.4)
				}
				if prev.Clicked() && at > 0 {
					a.showClip(cs.order[at-1].ID)
				}
				ui.Text(c, fmt.Sprintf("%d / %d", at+1, total)).MinWidth(40).TextAlign(ui.Center).FontSize(theme.Label).LineHeight(16.0 / 12).TextColor(theme.Muted).
					FontFeatures(theme.Numbers)
				next := a.iconBtn(c, "next", iconOpt{icon: "forward", label: locales.ClipsNext(), tip: locales.ClipsNextTip()}).Size(28, 28)
				if at == total-1 {
					next.Opacity(0.4)
				}
				if next.Clicked() && at < total-1 {
					a.showClip(cs.order[at+1].ID)
				}
			})
		}
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
	body := ui.Grid(c.Key("body:"+cl.ID)).ColumnTracks(ui.Fixed(wq), ui.Fixed(176)).GapX(48).AlignItems(ui.Start).Margin(40, 0, 0, 0)
	if k := swap(body, cs.turns); k < 1 {
		body.Opacity(k)
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
					var paras []string
					for _, l := range paragraphBreaks.Split(cl.Text, -1) {
						if l = strings.TrimSpace(l); l != "" {
							paras = append(paras, l)
						}
					}
					for i, l := range paras {
						ui.Box(c.Key(i)).Margin(map[bool]float32{true: 16 * stageScale, false: 0}[i > 0], 0, 0, 0).Children(func() {
							if i == 0 {
								a.icon(c, "clips", stepMark[st]*stageScale, lit).Absolute().Top(8 * stageScale).Left(markLeft(st) * stageScale).TextColor(theme.Text2)
							}
							// Later paragraphs hang a Western opening quote the same way.
							first, _ := utf8.DecodeRuneInString(l)
							if i > 0 && strings.ContainsRune("“‘\"'", first) {
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
		mine(a.cardMargin(c, cl, wash, (line-20)/2), 1)
	})
	foot := ui.Row(c.Key("foot")).Gap(12).Margin(32, 0, 0, 0).AlignItems(ui.Center)
	mine(foot, 1)
	foot.Children(func() {
		ui.Text(c, locales.ClipsClippedOn(dateText(&cl.CreatedAt))).FontSize(theme.Label).LineHeight(16.0 / 12).TextColor(theme.Muted).FontFeatures(theme.Numbers)
		ui.Box(c).Grow(1)
		del := a.toolButton(c, "delete", "trash", locales.CommonDelete(), false).Size(32, 32).TabStop(true)
		if del.Clicked() && !a.busy {
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

// cardMargin is the reader's own line in the margin beside the words, its
// first line top down, on theirs: edited in place, saved on leaving; Esc
// puts the old words back. With no note yet, no stroke, only the quiet way
// to write one.
func (a *App) cardMargin(c *ui.Context, cl *library.Clip, wash, top float32) ui.Element {
	cs := a.clipsSt()
	n := &cs.note
	note := strings.TrimSpace(cl.Note)
	editing := n.editing && n.id == cl.ID
	m := ui.Column(c.Key("margin")).Gap(12).MinWidth(0).AlignItems(ui.Start).Padding(top, 0, 0, 20).Role(ui.RoleRegion).Label(locales.ClipsNote())
	return m.Children(func() {
		if note != "" || editing {
			ui.Box(c).Absolute().Top(top+10).Left(0).Size(12, 1).Background(theme.Accent).IgnoreInput(true)
		}
		switch {
		case editing:
			in := ui.TextAreaBase(c.Key("editor"), &n.draft).Label(locales.ClipsNote()).Placeholder(locales.ClipsNotePlaceholder()).MaxLength(4000).Lines(1, 40).
				FillWidth().FontSize(theme.Body).LineHeight(20.0 / 13).TextColor(theme.Text).PlaceholderColor(theme.Muted).CaretColor(theme.Accent).AutoFocus()
			if in.OverlayShortcut(0, ui.KeyEscape) {
				n.cancel = true
				n.editing = false
			}
			if in.Shortcut(ui.Cmd, ui.KeyEnter) {
				n.editing = false
			}
			if in.FocusLeft() {
				n.editing = false
			}
			if !n.editing {
				next := strings.TrimSpace(n.draft)
				if !n.cancel && next != note {
					a.mutate(library.UpdateClipNote{ID: cl.ID, Note: next}, nil)
				}
				n.cancel = false
			}
		case note != "":
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
		default:
			if a.button(c, locales.ClipsAddNote(), btnOpt{kind: ghost, inset: true}).Margin(-6, 0, -6, -8).Clicked() {
				*n = clipNote{id: cl.ID, draft: cl.Note, editing: true}
			}
		}
	})
}

// handOff is 复制引文, the stage's one amber button: the passage with its
// source, link and note as Markdown, for Claude Code, Codex or a note. It
// says 已复制 in place for two seconds, the tick drawn left to right.
func (a *App) handOff(c *ui.Context, cl *library.Clip) {
	cs := a.clipsSt()
	now := c.Now()
	said := !cs.copy.IsZero() && now.Sub(cs.copy) < motion.Copied
	if said {
		c.After(motion.Copied - now.Sub(cs.copy))
	}
	tip := locales.ClipsCopyTip()
	if strings.TrimSpace(cl.Note) != "" {
		tip = locales.ClipsCopyTipWithNote()
	}
	var b ui.Element
	b = a.button(c, locales.ClipsCopyQuote(), btnOpt{kind: primary, inset: true, words: func(color ui.Color, weight int) {
		// The wordings share one cell, so the button keeps its width as they cross.
		ui.Grid(c.Key("says")).Children(func() {
			for i, on := range []bool{!said, said} {
				w := ui.Row(c.Key(i)).Gap(4).AlignItems(ui.Center).Justify(ui.Center).RowStart(1).ColumnStart(1)
				instant := cs.copyKbd || a.kbd
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
					tick := a.icon(c, "check", 16, false).TextColor(color)
					if on && !instant && !a.still(c) {
						d := tick.Keyframes("draw", ramp(100*time.Millisecond, motion.SwapIn), ui.KeyframeOptions{Run: uint64(cs.copy.UnixNano())})
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
		c.Services().WriteClipboard(clipMarkdown(cl))
		cs.copy, cs.copyKbd = now, a.kbd || a.still(c)
		a.wash(c, "card")
	}
}

// openClip opens a passage's article from the stage: the same crossing as
// from a list, the article's line lighting up and spreading into the
// reader's page while its title travels to the headline; the stage and the
// dark let go with the page. Back, the page shrinks into the passage, at
// the scroll it had, which takes the focus.
func (a *App) openClip(c *ui.Context, cl *library.Clip) {
	if a.article(cl.ArticleID) == nil {
		a.fail(locales.ClipsArticleGone())
		return
	}
	cs := a.clipsSt()
	var line ui.Rect
	if s := cs.stage; s != nil {
		line = s.cite
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
