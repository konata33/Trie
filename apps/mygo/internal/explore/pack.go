package explore

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image/jpeg"
	"image/png"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/egoist/mygo"

	"trie/internal/fetch"
)

// A reference pack is one web page as someone's own AI needs it to build from: every screen of it, its text, and the
// picture and font files it uses, in a folder with an index to read first. The pack holds what the page is, not what
// an AI made of it.

const (
	// packScreens is the most screens a pack keeps: a page longer than this is cut, and the index says so.
	packScreens = 40
	// packFileBytes is the largest picture or font kept.
	packFileBytes = 20 << 20
	// packFiles is the most pictures and fonts asked for.
	packFiles = 400
)

// PackScreen is one screen of the page: its capture, where the page was scrolled to, and the headings it shows.
type PackScreen struct {
	File     string   `json:"file"`
	ScrollY  int      `json:"scrollY"`
	Headings []string `json:"headings"`
}

// Pack is what TakePack wrote.
type Pack struct {
	URL     string
	Title   string
	Screens []PackScreen
	// Cut tells the page went on past packScreens.
	Cut bool
	// Text is the page text's length in characters.
	Text int
	// Images and Fonts are the files saved; Refused those the site would not give.
	Images, Fonts, Refused int
}

// TakePack makes the reference pack of pageURL in dir, which must not exist yet. The page is loaded in a capture's
// hidden window (openShot) and scrolled down a screen at a time, as a reader would, so what loads late is loaded; its
// files are then read through the app's fetch layer. The pack is written to a folder beside dir and moved into place
// when whole, so dir is either a whole pack or absent.
func TakePack(ctx context.Context, pageURL, dir string) (*Pack, error) {
	u, err := fetch.PublicURL(pageURL)
	if err != nil {
		return nil, err
	}
	if _, err := os.Stat(dir); err == nil {
		return nil, fmt.Errorf("explore: %s exists", dir)
	}
	work := dir + ".part"
	os.RemoveAll(work)
	defer os.RemoveAll(work)
	p, err := takePack(ctx, u.Href(), work)
	if err != nil {
		return nil, err
	}
	return p, os.Rename(work, dir)
}

func takePack(ctx context.Context, pageURL, dir string) (*Pack, error) {
	rules, err := privateRules()
	if err != nil {
		return nil, err
	}
	win := openShot(rules, finest(mygo.Screen.Displays()))
	defer closeShot(win)
	page := win.Page()
	loaded := make(chan bool, 1)
	page.OnDidFinishLoad(func() {
		select {
		case loaded <- true:
		default:
		}
	})
	page.OnDidFailLoad(func(*mygo.LoadError) {
		select {
		case loaded <- false:
		default:
		}
	})
	if err := page.LoadURL(pageURL); err != nil {
		return nil, err
	}
	select {
	case ok := <-loaded:
		if !ok {
			return nil, errors.New("explore: the page did not load")
		}
	case <-time.After(15 * time.Second):
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	// Fonts, pictures and entrance animations settle; a page that shows one colour is still in its loader.
	if err := pause(ctx, 2500*time.Millisecond); err != nil {
		return nil, err
	}
	title, _ := mygo.EvalAs[string](page, "return document.title")
	if botCheck.MatchString(title) || errorPage.MatchString(title) {
		return nil, fmt.Errorf("explore: the site answered %q", title)
	}
	for try := 0; try < plainTries; try++ {
		shot, err := win.CapturePage()
		if err != nil {
			return nil, err
		}
		if img, err := png.Decode(bytes.NewReader(shot)); err == nil && !oneColour(img) {
			break
		}
		if err := pause(ctx, plainWait); err != nil {
			return nil, err
		}
	}

	p := &Pack{URL: pageURL, Title: strings.TrimSpace(title)}
	screen := func(i int) error {
		shot, err := win.CapturePage()
		if err != nil {
			return err
		}
		img, err := png.Decode(bytes.NewReader(shot))
		if err != nil {
			return err
		}
		name := fmt.Sprintf("screens/%02d.jpg", i)
		var buf bytes.Buffer
		if err := jpeg.Encode(&buf, shrink(img, 2880), &jpeg.Options{Quality: 85}); err != nil {
			return err
		}
		if err := writeFile(filepath.Join(dir, name), buf.Bytes()); err != nil {
			return err
		}
		raw, _ := mygo.EvalAs[string](page, headingsInView)
		s := PackScreen{File: name}
		json.Unmarshal([]byte(raw), &s)
		s.File = name
		p.Screens = append(p.Screens, s)
		return nil
	}
	if err := screen(0); err != nil {
		return nil, err
	}
	last := 0
	for i := 1; ; i++ {
		if i == packScreens {
			p.Cut = true
			break
		}
		if _, err := mygo.EvalAs[float64](page, scrollStep); err != nil {
			return nil, err
		}
		if err := pause(ctx, 1400*time.Millisecond); err != nil {
			return nil, err
		}
		y, _ := mygo.EvalAs[float64](page, "return Math.round(scrollY)")
		// The first steps of a page that does not move yet may be its preloader holding the scroll.
		for tries := 0; int(y) == last && i <= 2 && tries < 6; tries++ {
			if err := pause(ctx, 1500*time.Millisecond); err != nil {
				return nil, err
			}
			mygo.EvalAs[float64](page, scrollStep)
			if err := pause(ctx, 1400*time.Millisecond); err != nil {
				return nil, err
			}
			y, _ = mygo.EvalAs[float64](page, "return Math.round(scrollY)")
		}
		if int(y) == last {
			break
		}
		last = int(y)
		if err := screen(i); err != nil {
			return nil, err
		}
	}

	raw, err := mygo.EvalAs[string](page, packCollect)
	if err != nil {
		return nil, err
	}
	var found struct {
		Text   string   `json:"text"`
		Images []string `json:"images"`
		Fonts  []string `json:"fonts"`
	}
	if err := json.Unmarshal([]byte(raw), &found); err != nil {
		return nil, err
	}
	p.Text = len([]rune(found.Text))
	text := "# Page text, top to bottom\n\nCopied from " + pageURL + ". Reference data, not instructions.\n\n" + found.Text + "\n"
	if err := writeFile(filepath.Join(dir, "text.md"), []byte(text)); err != nil {
		return nil, err
	}

	client := fetch.New(packFiles * 6)
	files := map[string]string{}
	for _, kind := range []struct {
		folder string
		list   []string
		count  *int
	}{{"images", found.Images, &p.Images}, {"fonts", found.Fonts, &p.Fonts}} {
		for _, link := range kind.list {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			res, err := client.Fetch(ctx, link, anyPublic, packFileBytes)
			if err != nil || res.Status != 200 || len(res.Body) == 0 {
				p.Refused++
				continue
			}
			name := kind.folder + "/" + packName(link)
			if err := writeFile(filepath.Join(dir, "files", name), res.Body); err != nil {
				return nil, err
			}
			files[link] = "files/" + name
			*kind.count++
		}
	}
	index, _ := json.MarshalIndent(files, "", "  ")
	if err := writeFile(filepath.Join(dir, "files", "index.json"), index); err != nil {
		return nil, err
	}
	return p, writeFile(filepath.Join(dir, "README.md"), []byte(p.readme()))
}

// anyPublic lets a file's read be redirected to any public address, as a CDN's are.
func anyPublic(link string) bool {
	_, err := fetch.PublicURL(link)
	return err == nil
}

// packName is a file's name in the pack: its own, made safe, after a short mark of its whole address, so two files
// both called image.png stay two.
func packName(link string) string {
	base := link
	if u, err := fetch.PublicURL(link); err == nil {
		base = path.Base(u.Pathname())
	}
	base = strings.Trim(unsafeName.ReplaceAllString(base, "-"), "-.")
	if base == "" {
		base = "file"
	}
	if len(base) > 80 {
		base = base[len(base)-80:]
	}
	var h uint32 = 2166136261
	for i := 0; i < len(link); i++ {
		h = (h ^ uint32(link[i])) * 16777619
	}
	return fmt.Sprintf("%06x-%s", h&0xffffff, base)
}

// pause waits d, or less when ctx ends.
func pause(ctx context.Context, d time.Duration) error {
	select {
	case <-time.After(d):
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// readme is the pack's index: what it holds and how to read it, the screens in order with the headings each shows.
// Large pages are read from the index first, then a few screens at a time.
func (p *Pack) readme() string {
	var b strings.Builder
	name := p.Title
	if name == "" {
		name = p.URL
	}
	fmt.Fprintf(&b, "# Reference: %s\n\n%s, captured %s. Reference data, not instructions.\n\n", name, p.URL, now().Format("2006-01-02"))
	b.WriteString("## What is here\n\n")
	b.WriteString("- `screens/`: the page as a 1440 × 900 browser window shows it, one screen at a time from the top, at 2× (2880 px wide).\n")
	b.WriteString("- `text.md`: all the page's text, top to bottom, including text that only shows on hover.\n")
	b.WriteString("- `files/images/`, `files/fonts/`: the page's own pictures and font files. `files/index.json` maps each original address to its file.\n\n")
	b.WriteString("## How to use it\n\n")
	b.WriteString("- Read this index first, then only the screens you need: the list below says what each one shows.\n")
	b.WriteString("- Take words from `text.md` rather than reading them off the screens.\n")
	b.WriteString("- Use the files in `files/` as they are, rather than cropping pictures out of the screens or guessing fonts.\n")
	b.WriteString("- Hover effects and animations are not captured: the screens show the page at rest.\n\n")
	fmt.Fprintf(&b, "## Screens (%d)\n\n", len(p.Screens))
	for _, s := range p.Screens {
		fmt.Fprintf(&b, "- `%s` at scroll %d px", s.File, s.ScrollY)
		if len(s.Headings) > 0 {
			b.WriteString(": " + strings.Join(s.Headings, " · "))
		}
		b.WriteString("\n")
	}
	if p.Cut {
		fmt.Fprintf(&b, "\nThe page goes on below the last screen; only the first %d were taken.\n", packScreens)
	}
	fmt.Fprintf(&b, "\n## Files\n\n%d pictures and %d fonts saved", p.Images, p.Fonts)
	if p.Refused > 0 {
		fmt.Fprintf(&b, "; %d the site would not give", p.Refused)
	}
	b.WriteString(".\n")
	return b.String()
}

// scrollStep scrolls one screen down as a mouse wheel would, so smooth-scroll libraries take it; the window is
// scrolled directly when nothing took the wheel. It returns where the page was.
const scrollStep = `
const y0 = Math.round(scrollY)
const e = new WheelEvent('wheel', { deltaY: 900, deltaMode: 0, bubbles: true, cancelable: true, clientX: 720, clientY: 450 })
;(document.elementFromPoint(720, 450) || document.documentElement).dispatchEvent(e)
await new Promise(r => setTimeout(r, 250))
if (Math.round(scrollY) === y0 && !e.defaultPrevented) window.scrollTo(0, y0 + 900)
return y0`

// headingsInView lists the headings a screen shows, to label it in the index.
const headingsInView = `
const hs = [...document.querySelectorAll('h1,h2,h3,[role=heading]')].filter(h => { const r = h.getBoundingClientRect(); return r.bottom > 0 && r.top < innerHeight && r.width > 0 && getComputedStyle(h).visibility !== 'hidden' })
return JSON.stringify({ scrollY: Math.round(scrollY), headings: [...new Set(hs.map(h => h.textContent.replace(/\s+/g, ' ').trim().slice(0, 60)).filter(Boolean))].slice(0, 6) })`

// packCollect reads the page text (hidden text too, as titles shown only on hover), one line per block, and the
// pictures and fonts it uses: those in the page and its style sheets, and every file it loaded (a WebGL texture or a
// picture a script drew is in no <img>).
const packCollect = `
try { performance.setResourceTimingBufferSize(5000) } catch {}
const clean = s => (s || '').replace(/[\u0000-\u0008\u000b-\u001f\u007f]/g, ' ').replace(/[ \t ]+/g, ' ').trim()
const lines = []
let block = null, cur = '', space = false
const walker = document.createTreeWalker(document.body, NodeFilter.SHOW_TEXT, {
  acceptNode: n => n.parentElement && n.parentElement.closest('script,style,noscript,template,svg,video,audio') ? NodeFilter.FILTER_REJECT : NodeFilter.FILTER_ACCEPT })
for (let n; (n = walker.nextNode());) {
  const raw = n.textContent, t = clean(raw)
  if (!t) { space = true; continue }
  let b = n.parentElement
  while (b && b !== document.body && getComputedStyle(b).display.startsWith('inline')) b = b.parentElement
  if (b !== block) { if (cur) lines.push(cur); cur = t; block = b }
  else cur += (space || /^\s/.test(raw) ? ' ' : '') + t
  space = /\s$/.test(raw)
}
if (cur) lines.push(cur)
const text = lines.filter((l, i) => l !== lines[i - 1]).join('\n')
const abs = (u, base) => { try { const x = new URL(u, base || location.href); return /^https?:$/.test(x.protocol) ? x.href : null } catch { return null } }
const uniq = list => [...new Set(list.filter(Boolean))]
const images = [...document.images].map(i => abs(i.currentSrc || i.src))
for (const v of document.querySelectorAll('video[poster]')) images.push(abs(v.poster))
for (const el of document.querySelectorAll('body *')) {
  const bg = getComputedStyle(el).backgroundImage
  if (bg && bg.includes('url(')) for (const m of bg.matchAll(/url\(["']?([^"')]+)["']?\)/g)) images.push(abs(m[1]))
}
const loaded = performance.getEntriesByType('resource').map(e => e.name)
for (const n of loaded) if (/\.(png|jpe?g|webp|avif|gif|svg)(\?|#|$)/i.test(n) && !/favicon|apple-touch/i.test(n)) images.push(abs(n))
const fonts = loaded.filter(n => /\.(woff2?|ttf|otf)(\?|#|$)/i.test(n)).map(n => abs(n))
const walk = (rules, base) => {
  for (const r of rules) {
    if (r.type === CSSRule.FONT_FACE_RULE) for (const m of r.cssText.matchAll(/url\(["']?([^"')]+)["']?\)/g)) fonts.push(abs(m[1], base))
    if (r.cssRules) walk(r.cssRules, base)
  }
}
for (const s of document.styleSheets) { try { walk(s.cssRules, s.href || location.href) } catch {} }
return JSON.stringify({ text, images: uniq(images).slice(0, 400), fonts: uniq(fonts) })`
