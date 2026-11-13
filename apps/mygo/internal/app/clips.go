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
// length, the others as opening lines. A passage opens in a card that grows
// out of it, whose article title opens the article at the passage.

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
	open    string             // the passage the card shows
	keyed   bool               // the card was opened, turned or closed from the keys
	opens   uint64             // the card's openings, to play its words settling in
	turns   uint64             // and its turns, to cross them over
	turned  bool
	who     string // the person the card showed, to cross them over when it changes
	whoRun  uint64
	cardBox ui.Rect            // the card as last laid out, whose ground becomes an article's page
	boxes   map[string]ui.Rect // the passages as last laid out (App.Press)
	folding map[string]*fold   // passages deleted, folding shut before they go
	leaving bool               // the card goes with its page (an article opening)
	doomed  string             // a passage deleted from the card, which folds once the card has closed
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
			boxes: map[string]ui.Rect{}, folding: map[string]*fold{}}
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
		if cs.doomed != "" && (a.kbd || c.Now().Sub(cs.closed) >= motion.DialogOut) {
			a.foldClip(c, cs.doomed)
			cs.doomed = ""
		} else if cs.doomed != "" {
			c.After(motion.DialogOut - c.Now().Sub(cs.closed))
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
		a.clipCard(c, p)
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
	land := motion.Land // closing up as a page above folds away (弹一下)
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
			ui.Row(c.Key("head")).Gap(gap).AlignItems(ui.Center).MinWidth(0).Margin(0, 0, mb, 0).Children(func() {
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
// it rises 10 into place on RiseIn (弹一下). Not from the keys, nor under
// less motion.
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
		b.Children(func() {
			size := stepMark[st]
			left := 24 - size
			if st == stepXL {
				left = 20 - size
			}
			ink, lit := theme.Muted, false
			if hover {
				ink, lit = theme.Text2, true
			}
			a.icon(c, "clips", size, lit).Absolute().Top(8).Left(left).TextColor(b.AnimateColor("mark", ink, a.ease(motion.Hover)))
			body := func() {
				ui.Column(c.Key("words")).MinWidth(0).Children(func() {
					lines := 6
					if !hero {
						lines = 5
					}
					sz := stepSize[st]
					fam, k := reader.PassageFace(cl.Text)
					q := washed(c, "quote", wash, func() ui.Element {
						t := ui.RichText(c, hits(excerpt(cl.Text, terms), terms, ui.Span{})...).Font(fam).FontSize(sz[0] * k).LineHeight(sz[1] / (sz[0] * k)).TextColor(theme.Text).
							MaxLines(lines).Ellipsis("…").Pretty()
						switch st {
						case stepXL:
							t.LetterSpacing(-0.01 * 32)
						case stepL:
							t.LetterSpacing(-0.005 * 24)
						}
						return t
					})
					q.FillWidth()
					washed(c, "cite", wash, func() ui.Element {
						return ui.RichText(c, hits(book(clipTitle(cl)), terms, ui.Span{})...).FontSize(theme.Body).LineHeight(20.0 / 13).TextColor(theme.Text2).
							SingleLine().Ellipsis("…")
					}).Margin(16, 0, 0, 0)
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
				washed(c, "excerpt", wash, func() ui.Element {
					fam, k := reader.PassageFace(cl.Text)
					return ui.RichText(c, hits(excerpt(cl.Text, terms), terms, ui.Span{})...).Font(fam).FontSize(theme.Item * k).LineHeight(24 / (theme.Item * k)).TextColor(theme.Text).
						MaxLines(2).Ellipsis("…").Pretty()
				}).FillWidth()
				if note != "" {
					a.noteLine(c, note, terms, wash, 1).Margin(8, 0, 0, 0)
				}
			})
		})
		a.clipTools(c, cl, hover, false)
	})
}

// passageButton opens the card from a passage, and takes the focus or the
// ground back from an article it opened.
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
		a.showClip(cl.ID, r)
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
// both are in the card.
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
		icon, label := "copy", locales.ClipsCopyQuote()
		if held {
			icon, label = "check", locales.CommonCopied()
		}
		cp := a.toolButton(c, "copy", icon, label, held)
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
// ground; delete goes danger under the pointer, a done copy amber.
func (a *App) toolButton(c *ui.Context, key, icon, label string, done bool) ui.Element {
	b := ui.ButtonBase(c.Key(key)).Label(label).Size(24, 24).Radius(6).Center().Shrink(0).FocusRing(false).TabStop(false)
	color, ground := theme.Text2, ui.Transparent
	if b.Hovered() {
		color, ground = theme.Text, theme.Hover
		if key == "delete" {
			color = theme.Danger
		}
	}
	if done {
		color = theme.Accent
	}
	b.Background(b.AnimateColor("bg", ground, a.ease(motion.Hover))).TextColor(b.AnimateColor("fg", color, a.ease(motion.Hover)))
	a.press(b, motion.Squashed)
	b.Children(func() { a.pressIcon(c, icon, 16, false, b.Pressed()) })
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

// --- The card.

// showClip opens the card on passage id, grown out of r.
func (a *App) showClip(id string, r ui.Rect) {
	cs := a.clipsSt()
	if cs.open == "" {
		cs.opens++
		cs.turned = false
		a.layers.opener, a.layers.in, a.layers.out = r, motion.DialogIn, motion.DialogOut
		if a.kbd {
			a.layers.opener = ui.Rect{}
		}
	} else {
		cs.turns++
		cs.turned = true
	}
	cs.open, cs.keyed, cs.leaving = id, a.kbd, false
	cs.note = clipNote{}
}

// closeClip closes the card; leaving, it goes with its page.
func (a *App) closeClip(c *ui.Context, leaving bool) {
	cs := a.clipsSt()
	cs.open, cs.keyed, cs.leaving, cs.closed = "", a.kbd, leaving, c.Now()
}

// clipCard is the whole passage, grown out of the line it was opened
// from: who said it heads it, the words set as a quotation with the
// article under them, the reader's note and the day it was kept in the
// margin beside them. ← → turn to the neighbours; Esc or a click outside
// closes.
func (a *App) clipCard(c *ui.Context, p page) {
	cs := a.clipsSt()
	at := slices.IndexFunc(cs.order, func(cl *library.Clip) bool { return cl.ID == cs.open })
	if cs.open != "" && at < 0 {
		cs.open = "" // its passage is gone
	}
	open := cs.open != ""
	was := open
	t := ui.ElementTransition{Duration: motion.DialogIn, Ease: motion.Ease, ExitDuration: motion.DialogOut, ExitEase: motion.Ease,
		Enter: &ui.Motion{Scale: 0.96}, Exit: &ui.Motion{Scale: 0.96}}
	if cs.leaving {
		// Opening its article, the card lets go with the page it sits on.
		x := motion.Deep.Crossing()
		t.ExitDuration, t.Exit = x.Out, &ui.Motion{Blur: motion.Defocus, Scale: x.To}
	}
	if cs.keyed {
		t = motion.Instant(t)
	}
	w, h := c.Size()
	a.centred(c, "clip-card", &open, t, func(panel ui.Element) {
		cs.cardBox = panel.LayoutBounds()
		// Kept as one bitmap (Cache): it grows out of its line and shrinks back scaled, which would cut what it holds
		// at its rounded corners with a mask drawn on the CPU every frame.
		panel.ClipX().ClipY().Width(min(800, w-96)).MaxHeight(h-96).Padding(24, 48, 32, 48).InsetShadow(0, 0, 0, 1, theme.Line).Shadow(0, 24, 64, 0, theme.ShadowDialog).
			FontSize(theme.Body).LineHeight(1.5).Label(locales.ClipsCard()).Cache()
	}, func() {
		if at < 0 {
			return
		}
		a.cardContent(c, p, cs.order[at], at, len(cs.order))
	})
	if was && !open {
		a.closeClip(c, false)
	}
}

// cardContent is the card's head, passage and margin, and foot.
func (a *App) cardContent(c *ui.Context, p page, cl *library.Clip, at, total int) {
	cs := a.clipsSt()
	ic := a.iconOfClip(cl)
	who := personOf(cl)
	swap := func(e ui.Element, run uint64) {
		if cs.keyed || !cs.turned || a.kbd {
			return
		}
		if k := e.Keyframes("swap", ramp(0, motion.SwapIn), ui.KeyframeOptions{Run: run, NonMotion: true}); k < 1 {
			e.Opacity(k)
		}
	}
	// ← → turn to the neighbours, outside the note being written (which keeps its arrows).
	if at > 0 {
		prev := cs.order[at-1].ID
		c.OnShortcut(0, ui.KeyLeft, func() { a.kbd = true; a.showClip(prev, ui.Rect{}) })
	}
	if at+1 < total {
		next := cs.order[at+1].ID
		c.OnShortcut(0, ui.KeyRight, func() { a.kbd = true; a.showClip(next, ui.Rect{}) })
	}
	a.aura(c, false)
	// The person crosses over with the words only when the card turns to someone else.
	if cs.who != who {
		cs.who = who
		cs.whoRun++
	}
	ui.Row(c.Key("head")).Gap(8).Height(32).Margin(0, 0, 32, 0).AlignItems(ui.Center).Children(func() {
		whoRow := ui.Row(c.Key("who:" + who)).Gap(8).AlignItems(ui.Center).MinWidth(0).Shrink(1).Role(ui.RoleHeading).Level(2).Label(who)
		swap(whoRow, cs.whoRun)
		whoRow.Children(func() {
			a.avatar(c, who, ic, 24)
			ui.Text(c, who).FontSize(theme.Item).LineHeight(20.0 / 15).FontWeight(600).TextColor(theme.Text).SingleLine().Ellipsis("…").Shrink(1).MinWidth(0)
		})
		if d := dateText(cl.Citation.PublishedAt); d != "" {
			// When it was written sits by who wrote it, small and quiet.
			t := ui.Text(c.Key("date:"+cl.ID), d).Margin(0, 0, 0, 4).FontSize(theme.Label).LineHeight(16.0 / 12).TextColor(theme.Muted).
				FontFeatures(theme.Numbers).NoWrap().Shrink(0)
			swap(t, cs.turns)
		}
		ui.Box(c).Grow(1)
		if total > 1 {
			prev := a.iconBtn(c, "prev", iconOpt{icon: "back", label: locales.ClipsPrevious(), tip: locales.ClipsPreviousTip()}).Size(28, 28)
			if at == 0 {
				prev.Opacity(0.4)
			}
			if prev.Clicked() && at > 0 {
				a.showClip(cs.order[at-1].ID, ui.Rect{})
			}
			ui.Text(c, fmt.Sprintf("%d / %d", at+1, total)).MinWidth(40).TextAlign(ui.Center).FontSize(theme.Label).LineHeight(16.0 / 12).TextColor(theme.Muted).
				FontFeatures(theme.Numbers)
			next := a.iconBtn(c, "next", iconOpt{icon: "forward", label: locales.ClipsNext(), tip: locales.ClipsNextTip()}).Size(28, 28)
			if at == total-1 {
				next.Opacity(0.4)
			}
			if next.Clicked() && at < total-1 {
				a.showClip(cs.order[at+1].ID, ui.Rect{})
			}
		}
		if a.iconBtn(c, "close", iconOpt{icon: "close", label: locales.CommonClose(), tip: locales.CommonCloseTip()}).Size(28, 28).Clicked() {
			a.closeClip(c, false)
		}
	})
	wash := a.washOf(c, "card")
	body := ui.Grid(c.Key("body:"+cl.ID)).ColumnTracks(ui.Fr(1), ui.Fixed(176)).GapX(48).AlignItems(ui.Start)
	swap(body, cs.turns)
	if !cs.turned && !cs.keyed && !a.still(c) {
		// Once the card has grown out of the line, the words and the note settle in, then the mark lights: kept.
		if k := body.Keyframes("in", ramp(motion.SettleWait, motion.SwapIn), ui.KeyframeOptions{Run: cs.opens}); k < 1 {
			body.Opacity(k).Translate(0, 4*(1-k))
			if b := 2 * (1 - k); b > 0.05 {
				body.Blur(b)
			}
		}
	}
	lit := cs.keyed || cs.turned || a.still(c) || body.Keyframes("lit", ramp(motion.MarkWait, motion.SwapIn), ui.KeyframeOptions{Run: cs.opens}) >= 0.5
	body.Children(func() {
		ui.Column(c.Key("quote")).MinWidth(0).Children(func() {
			st := cardStep(cl.Text)
			sz := stepSize[st]
			fam, k := reader.PassageFace(cl.Text)
			size := sz[0] * k
			var paras []string
			for _, line := range paragraphBreaks.Split(cl.Text, -1) {
				if line = strings.TrimSpace(line); line != "" {
					paras = append(paras, line)
				}
			}
			for i, line := range paras {
				ui.Box(c.Key(i)).Margin(map[bool]float32{true: 16, false: 0}[i > 0], 0, 0, 0).Children(func() {
					if i == 0 {
						a.icon(c, "clips", stepMark[st], lit).Absolute().Top(8).Left(-12 - stepMark[st]).TextColor(theme.Text2)
					}
					// Later paragraphs hang a Western opening quote the same way.
					first, _ := utf8.DecodeRuneInString(line)
					if i > 0 && strings.ContainsRune("“‘\"'", first) {
						ui.Text(c, string(first)).Absolute().Top(0).Left(-textWidthIn(string(first), fam, size)).Font(fam).FontSize(size).LineHeight(sz[1] / size).TextColor(theme.Text)
						line = line[utf8.RuneLen(first):]
					}
					washed(c, "p", wash, func() ui.Element {
						return ui.Text(c, line).Font(fam).FontSize(size).LineHeight(sz[1] / size).TextColor(theme.Text).Pretty()
					}).FillWidth()
				})
			}
			// The article, as a reference reads: 《title》. The title is the way back to it: it opens the article at this passage.
			title := clipTitle(cl)
			ui.Row(c.Key("cite")).Margin(16, 0, 0, 0).AlignItems(ui.Start).FontSize(theme.Body).LineHeight(20.0 / 13).TextColor(theme.Text2).Children(func() {
				hanTitle := han.MatchString(title)
				if hanTitle {
					ui.Text(c, "《")
				}
				link := ui.ButtonBase(c.Key("title")).Label(title).Radius(2).FocusRing(false).Shrink(1).MinWidth(0)
				if link.Clicked() {
					a.openClip(c, cl)
				}
				color, line := theme.Text2, theme.Faint
				if link.Hovered() {
					color, line = theme.Text, theme.Text2
				}
				if link.FocusVisible() {
					link.FocusRing(true)
				}
				link.Children(func() {
					t := washed(c, "cite", wash, func() ui.Element {
						return citeTitle(c, title).TextColor(link.AnimateColor("fg", color, a.ease(motion.Hover))).
							DecorationColor(link.AnimateColor("line", line, a.ease(motion.Hover)))
					})
					a.end(t, p, "title:"+cl.ArticleID)
				})
				a.tip(c, link, locales.ClipsShowInArticle(), false)
				if hanTitle {
					ui.Text(c, "》")
				}
			})
		})
		a.cardMargin(c, cl, wash)
	})
	ui.Row(c.Key("foot")).Gap(12).Margin(32, 0, 0, 0).AlignItems(ui.Center).Children(func() {
		ui.Text(c, locales.ClipsClippedOn(dateText(&cl.CreatedAt))).FontSize(theme.Label).LineHeight(16.0 / 12).TextColor(theme.Muted).FontFeatures(theme.Numbers)
		ui.Box(c).Grow(1)
		del := a.toolButton(c, "delete", "trash", locales.CommonDelete(), false).Size(32, 32).TabStop(true)
		if del.Clicked() && !a.busy {
			// Focus goes back to a neighbouring passage, since this one is about to fold away.
			cs := a.clipsSt()
			if at+1 < total {
				cs.focus = cs.order[at+1].ID
			} else if at > 0 {
				cs.focus = cs.order[at-1].ID
			}
			cs.doomed = cl.ID
			a.closeClip(c, false)
		}
		a.handOff(c, cl)
	})
}

// citeTitle is the title in the card's citation: ui/controls LinkButton's
// look, underlined.
func citeTitle(c *ui.Context, s string) ui.Element {
	return ui.Text(c, s).FontSize(theme.Body).LineHeight(20.0 / 13).TextColor(theme.Text2).Underline().UnderlineOffset(3).Pretty()
}

// cardMargin is the reader's own line in the margin beside the words: edited
// in place, saved on leaving; Esc puts the old words back. With no note yet,
// no stroke, only the quiet way to write one.
func (a *App) cardMargin(c *ui.Context, cl *library.Clip, wash float32) {
	cs := a.clipsSt()
	n := &cs.note
	note := strings.TrimSpace(cl.Note)
	editing := n.editing && n.id == cl.ID
	m := ui.Column(c.Key("margin")).Gap(12).MinWidth(0).AlignItems(ui.Start).Padding(8, 0, 0, 20).Role(ui.RoleRegion).Label(locales.ClipsNote())
	m.Children(func() {
		if note != "" || editing {
			ui.Box(c).Absolute().Top(18).Left(0).Size(12, 1).Background(theme.Accent).IgnoreInput(true)
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

// handOff is 复制引文, the card's one amber button: the passage with its
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

// openClip opens a passage's article from the card:
// the same crossing as from a list, the card's ground becoming the
// reader's page while the title in its citation travels to the headline;
// the card and its shade let go with the page. Back, the page shrinks into
// the passage, at the scroll it had, which takes the focus.
func (a *App) openClip(c *ui.Context, cl *library.Clip) {
	if a.article(cl.ArticleID) == nil {
		a.fail(locales.ClipsArticleGone())
		return
	}
	cs := a.clipsSt()
	card := cs.cardBox
	a.closeClip(c, true)
	cs.back = cl.ID
	a.openAs(c, cl.ArticleID, card, citeTitle, theme.Body)
}

// ramp goes from 0 to 1 over d, after delay, along the one curve.
func ramp(delay, d time.Duration) []ui.Keyframe {
	return []ui.Keyframe{{Value: 0}, {At: delay, Value: 0}, {At: delay + d, Value: 1, Ease: motion.Ease}}
}

// clipsKeys are 摘录's: Esc clears the search. (The card's ← and → are its
// own: cardContent.)
func (a *App) clipsKeys(c *ui.Context) {
	cs := a.clipsSt()
	if cs.open == "" && c.Shortcut(0, ui.KeyEscape) && cs.query != "" {
		cs.query, cs.search = "", false
	}
}
