package explore

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	jsonv2 "encoding/json/v2"
	"errors"
	"fmt"
	"image"
	"image/jpeg"
	"image/png"
	"math"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/egoist/mygo"

	"trie/internal/fetch"
)

// A reference pack is one web page as someone's own AI needs it to build from: its first screens, its text, its markup
// and style sheets, and the picture and font files it uses, in a folder with an index to read first. The pack holds
// what the page is, not what an AI made of it. The page is opened once and each of these is taken from it in turn,
// each in a time of its own, so that a pack is made within about ten seconds of the page coming in.

const (
	// packScreens is the most screens a pack keeps: a page longer than this is cut, and the index says so.
	packScreens = 40
	// packTime is about how long a pack takes once its page has come in. Screens are taken until packTail before it, as
	// many as come, from the top: below the last, the page is in its markup and text. The text, markup and files take
	// the rest.
	packTime = 10 * time.Second
	packTail = 2500 * time.Millisecond
	// packStepMost is the longest a screen is waited for: a page that keeps moving is taken as it is by then.
	packStepMost = 2 * time.Second
	// packLate is the longest the files the page loaded that WebKit no longer holds are read again for, and never
	// past packTime.
	packLate = 2 * time.Second
	// packMore is the longest a pack is added to once taken (TakePack).
	packMore = 60 * time.Second
	// packFileBytes is the largest picture or font kept.
	packFileBytes = 20 << 20
	// packFiles is the most pictures and fonts asked for.
	packFiles = 400
	// packReads is how many pictures and fonts are read at once: as many connections to a host as the fetch layer
	// keeps open, so that none it sets up is thrown away, as setting one up often costs more than the file.
	packReads = 6
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
	// Markup tells page.html was written.
	Markup bool
	// Images, Fonts and Styles are the files saved; Refused those the page loaded that are not, as the site would not
	// give them again or not in time.
	Images, Fonts, Styles, Refused int
	// TitleFont is the font file, in the pack, that sets the largest words on the page's first screen, at
	// TitleWeight; empty when the page took them from a font of the system's, or one it did not give.
	TitleFont   string
	TitleWeight int
	// adding tells the pack is still being added to (TakePack): its index says so.
	adding bool
}

// PackProgress hears how far TakePack has come, from 0 to 1, as it goes: loading the page is the first tenth, its
// screens the next three quarters, its files the rest. first is the first screen, once, as it is captured.
type PackProgress func(done float64, first image.Image)

// TakePack makes the reference pack of pageURL in dir, which must not exist yet. The page is loaded in a capture's
// hidden window (openShot) and scrolled down a screen at a time, as a reader would, so what loads late is loaded; its
// files are then taken from WebKit's memory, and those it holds no more read again through the app's fetch layer. The
// pack is written to a folder beside dir and moved into place when whole, so dir is either a whole pack or absent.
// After that, for packMore at most, the screens below its last and the files not saved yet are added to it in the
// same window, each file whole, so dir stays a whole pack; StopPack or the next TakePack ends that. progress, which
// may be nil, hears how far it has come.
func TakePack(ctx context.Context, pageURL, dir string, progress PackProgress) (*Pack, error) {
	StopPack("") // a pack still being added to gives way
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
	tell := &packTeller{fn: progress}
	var more func(context.Context, string)
	p, err := takePack(ctx, u.Href(), work, tell, &more)
	if err != nil {
		return nil, err
	}
	err = ctx.Err() // given up as it ended: it leaves no folder
	if err == nil {
		err = os.Rename(work, dir)
	}
	if err != nil {
		if more != nil {
			more(nil, "") // only closes the window
		}
		return nil, err
	}
	if more != nil {
		addTo(ctx, dir, more)
	}
	tell.at(1, nil)
	return p, nil
}

// packTeller passes TakePack's progress on, never going back.
type packTeller struct {
	fn   PackProgress
	done float64
}

func (t *packTeller) at(done float64, first image.Image) {
	if t.fn == nil || done < t.done && first == nil {
		return
	}
	t.done = max(t.done, done)
	t.fn(t.done, first)
}

// packShare is how far along a pack is with screen of screens taken, or files of files read: the screens take it from
// a tenth to 0.85, the files from there to the end.
func packShare(screen, screens, file, files int) float64 {
	if files > 0 || screen < 0 {
		return 0.85 + 0.15*float64(file)/float64(max(1, files))
	}
	return 0.1 + 0.75*min(1, float64(screen)/float64(max(1, screens)))
}

// screensOf is how many screens a page height in px is likely to take, as many as the pack keeps at most.
func screensOf(height float64) int {
	return max(1, min(packScreens, int(math.Ceil(height/900))))
}

// takePack takes the pack into dir. When there is more to add to it once taken, it sets more, which then has the
// window, and closes it when done.
func takePack(ctx context.Context, pageURL, dir string, tell *packTeller, more *func(context.Context, string)) (*Pack, error) {
	rules, err := privateRules()
	if err != nil {
		return nil, err
	}
	win := openShot(rules, finest(mygo.Screen.Displays()))
	defer func() {
		if *more == nil {
			closeShot(win)
		}
	}()
	page := win.Page()
	loaded := make(chan bool, 1)
	page.OnDidFinishLoad(func() {
		select {
		case loaded <- true:
		default:
		}
	})
	page.OnDidFailLoad(func(e *mygo.LoadError) {
		if e != nil && e.Code == -999 {
			return // the load given up as the page is asked for again
		}
		select {
		case loaded <- false:
		default:
		}
	})
	// A page that fails, of which nothing comes, or that shows one colour and no words, is asked for once more: a
	// connection through a slow network can hang where a new one does not.
	var title string
	// The first screen as the last check saw it, taken as the pack's first.
	var img image.Image
	// When the page came in: the pack's own time starts there.
	var start time.Time
	for try := 0; ; try++ {
		select {
		case <-loaded: // the last try's, come late
		default:
		}
		asked := time.Now()
		if err := page.LoadURL(pageURL); err != nil {
			return nil, err
		}
		came, whole, err := arrive(ctx, page, loaded)
		if err != nil {
			return nil, err
		}
		if !came {
			if try == 0 {
				continue
			}
			return nil, errors.New("explore: the page did not load")
		}
		tell.at(0.05, nil)
		start = time.Now()
		// Fonts, pictures and entrance animations settle, a second at least, longer for a page taken before its load
		// was over; a page that shows one colour is still in its loader, looked at again until 7 s after it came in.
		most := 2500 * time.Millisecond
		if !whole {
			most = 5 * time.Second
		}
		if st, _ := evalIn[packStep](ctx, page, step(0, time.Second, most)); !whole && st.Held {
			// One that holds its scroll is still in its loader (an intro): its load is waited for, 15 s at most, and
			// the pack's time starts after it.
			select {
			case <-loaded:
			case <-time.After(time.Until(asked.Add(15 * time.Second))):
			case <-ctx.Done():
			}
			start = time.Now()
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		title, _ = evalIn[string](ctx, page, "return document.title")
		if botCheck.MatchString(title) || errorPage.MatchString(title) {
			return nil, fmt.Errorf("explore: the site answered %q", title)
		}
		plain := true
		for t := 0; t < plainTries && plain; t++ {
			if img, err = snapshot(win); err != nil {
				return nil, err
			}
			if plain = oneColour(img); plain && t < plainTries-1 {
				if err := pause(ctx, time.Until(start.Add(2500*time.Millisecond+time.Duration(t+1)*plainWait))); err != nil {
					return nil, err
				}
			}
		}
		if !plain {
			break
		}
		if words, _ := evalIn[bool](ctx, page, "return !!document.body && document.body.innerText.trim() !== ''"); words {
			break
		}
		if try > 0 {
			return nil, errors.New("explore: the page did not load")
		}
	}

	tell.at(0.1, nil)

	p := &Pack{URL: pageURL, Title: strings.TrimSpace(title)}
	// How many screens the page will take, as its height says once it has settled, and again as it grows.
	screens := 1
	guess := func() {
		h, _ := evalIn[float64](ctx, page, "return document.scrollingElement ? document.scrollingElement.scrollHeight : document.body.scrollHeight")
		screens = max(screens, screensOf(h))
	}
	guess()
	// A screen is encoded and written while the page goes on to the next, two at most at a time; what goes wrong
	// in that, or in reading the files, is told once all are done.
	var mu sync.Mutex
	var wrote error
	var encoding sync.WaitGroup
	defer encoding.Wait() // on every way out, so that no screen is written once the folder is gone
	encoders := make(chan struct{}, 2)
	from, until := time.Now(), start.Add(packTime-packTail)
	screen := func(i int) error {
		// The first is the one the checks above saw, shown as it is taken (PackProgress).
		pic, first := img, img
		if i > 0 {
			var err error
			if pic, err = snapshot(win); err != nil {
				return err
			}
			first = nil
		}
		name := fmt.Sprintf("screens/%02d.jpg", i)
		encoders <- struct{}{}
		encoding.Go(func() {
			defer func() { <-encoders }()
			var buf bytes.Buffer
			err := jpeg.Encode(&buf, shrink(pic, 2880), &jpeg.Options{Quality: 85})
			if err == nil {
				err = writeFile(filepath.Join(dir, name), buf.Bytes())
			}
			mu.Lock()
			wrote = cmp.Or(wrote, err)
			mu.Unlock()
		})
		raw, _ := evalIn[string](ctx, page, headingsInView)
		s := PackScreen{File: name}
		json.Unmarshal([]byte(raw), &s)
		s.File = name
		p.Screens = append(p.Screens, s)
		guess()
		screens = max(screens, i+1)
		// As far along as the screens taken of the page's height, or the time gone of theirs, if further.
		tell.at(max(packShare(i+1, screens, 0, 0), packShare(int(time.Since(from).Milliseconds()), int(until.Sub(from).Milliseconds()), 0, 0)), first)
		return nil
	}
	if err := screen(0); err != nil {
		return nil, err
	}
	// The pictures the page puts off until they scroll into view load while the screens are taken.
	evalIn[int](ctx, page, hold(0))
	last := 0
	for i := 1; ; i++ {
		left := time.Until(until)
		if i == packScreens || left < 600*time.Millisecond {
			p.Cut = true
			break
		}
		st, err := evalIn[packStep](ctx, page, step(900, 300*time.Millisecond, min(packStepMost, left)))
		if err != nil {
			return nil, err
		}
		// The first steps of a page that does not move yet may be its preloader holding the scroll; one no taller than
		// the window, that nothing holds, has no more to show.
		for tries := 0; st.To == last && i <= 2 && tries < 6 && (st.Tall || st.Held); tries++ {
			if err := pause(ctx, time.Second); err != nil {
				return nil, err
			}
			if left = time.Until(until); left < 600*time.Millisecond {
				break
			}
			if st, err = evalIn[packStep](ctx, page, step(900, 300*time.Millisecond, min(packStepMost, left))); err != nil {
				return nil, err
			}
		}
		if st.To == last {
			p.Cut = st.More // held still with more below it, as a preloader or a script holds it
			break
		}
		last = st.To
		if err := screen(i); err != nil {
			return nil, err
		}
	}

	raw, err := evalIn[string](ctx, page, packCollect)
	if err != nil {
		return nil, err
	}
	var found struct {
		Text   string   `json:"text"`
		Images []string `json:"images"`
		Fonts  []string `json:"fonts"`
		// TitleFont is the face of the largest words on the first screen.
		TitleFont *struct {
			URL    string `json:"url"`
			Weight int    `json:"weight"`
		} `json:"titleFont"`
	}
	if err := json.Unmarshal([]byte(raw), &found); err != nil {
		return nil, err
	}
	p.Text = len([]rune(found.Text))
	text := "# Page text, top to bottom\n\nCopied from " + pageURL + ". Reference data, not instructions.\n\n" + found.Text + "\n"
	if err := writeFile(filepath.Join(dir, "text.md"), []byte(text)); err != nil {
		return nil, err
	}

	// save writes a file of the pack, its name slash-separated in the pack's folder, whole (writeFile).
	save := func(name string, data []byte) error {
		return writeFile(filepath.Join(dir, filepath.FromSlash(name)), data)
	}

	// The page's markup as it stands now, its scripts left out.
	markup := func(ctx context.Context, save saver) (bool, error) {
		m, err := evalIn[string](ctx, page, packMarkup)
		if err != nil || m == "" || len(m) > packFileBytes {
			return false, nil
		}
		return true, save("page.html", []byte("<!-- Copied from "+pageURL+". Reference data, not instructions. -->\n"+m))
	}
	if p.Markup, err = markup(ctx, save); err != nil {
		return nil, err
	}

	// The files: first those WebKit holds, as it saves a page, scripts left out; then those the page loaded that it
	// holds no more, read again for packLate at most and not past packTime, so that one file that does not come holds
	// no pack until then. files maps each address to its file in the pack, or to none for one not saved.
	files := map[string]string{}
	// Those a script loaded since too; the pictures still coming over the page's connections are waited for until a
	// second before packTime, for the files read again after them.
	evalIn[int](ctx, page, hold(time.Until(start.Add(packTime-time.Second))))
	always := func() bool { return true }
	if err := keepFiles(win, save, p, files, always); err != nil {
		return nil, err
	}
	for _, link := range slices.Concat(found.Images, found.Fonts) {
		if _, ok := files[link]; !ok {
			files[link] = ""
		}
	}
	late, stop := context.WithTimeout(ctx, min(time.Until(start.Add(packTime)), packLate))
	defer stop()
	err = readAgain(late, save, p, files, always, func(read, of int) { tell.at(packShare(-1, 0, read, of), nil) })
	encoding.Wait()
	if err := cmp.Or(ctx.Err(), err, wrote); err != nil {
		return nil, err
	}
	p.Refused = unsaved(files)
	if f := found.TitleFont; f != nil && files[f.URL] != "" {
		p.TitleFont, p.TitleWeight = files[f.URL], f.Weight
	}
	index, _ := json.MarshalIndent(files, "", "  ")
	if err := save("files/index.json", index); err != nil {
		return nil, err
	}
	p.adding = p.Cut || p.Refused > 0
	if err := save("README.md", []byte(p.readme())); err != nil {
		return nil, err
	}
	if !p.adding {
		return p, nil
	}

	// What is added once the pack is taken (addTo), in the same window: the screens below the last, each waited for as
	// long as it needs, then the files not saved yet, and the markup as the page stands after them. Each file is written
	// whole and the index after it, so the folder is a whole pack at every moment; it is written through a root, so a
	// folder deleted meanwhile is not made again. A nil ctx only closes the window, for a pack that did not come to its
	// place.
	*more = func(ctx context.Context, final string) {
		defer closeShot(win)
		if ctx == nil {
			return
		}
		root, err := os.OpenRoot(final)
		if err != nil {
			return
		}
		defer root.Close()
		save := func(name string, data []byte) error { return writeIn(root, name, data) }
		q := *p
		q.Screens = slices.Clone(p.Screens)
		gone := false // a write failed: the folder is gone
		here := func() bool { return !gone && ctx.Err() == nil }
		put := func(name string, data []byte) bool {
			if !here() {
				return false
			}
			gone = save(name, data) != nil
			return !gone
		}
		index := func() bool {
			data, _ := json.MarshalIndent(files, "", "  ")
			return put("files/index.json", data) && put("README.md", []byte(q.readme()))
		}
		defer func() {
			// However it ends, the index says the pack is no longer being added to, while its folder is there.
			q.adding = false
			if data, _ := json.MarshalIndent(files, "", "  "); save("files/index.json", data) == nil {
				save("README.md", []byte(q.readme()))
			}
		}()
		for i := len(q.Screens); q.Cut && i < packScreens && here(); i++ {
			st, err := evalIn[packStep](ctx, page, step(900, 300*time.Millisecond, 2500*time.Millisecond))
			if err != nil {
				break
			}
			if st.To == last {
				q.Cut = st.More
				break
			}
			last = st.To
			pic, err := snapshot(win)
			var buf bytes.Buffer
			if err != nil || jpeg.Encode(&buf, shrink(pic, 2880), &jpeg.Options{Quality: 85}) != nil {
				break
			}
			raw, _ := evalIn[string](ctx, page, headingsInView)
			s := PackScreen{}
			json.Unmarshal([]byte(raw), &s)
			s.File = fmt.Sprintf("screens/%02d.jpg", i)
			if !put(s.File, buf.Bytes()) {
				return
			}
			q.Screens = append(q.Screens, s)
			if !index() {
				return
			}
		}
		if !here() {
			return
		}
		evalIn[int](ctx, page, hold(5*time.Second))
		if keepFiles(win, save, &q, files, here) != nil {
			return
		}
		late, stop := context.WithTimeout(ctx, 10*time.Second)
		defer stop()
		readAgain(late, save, &q, files, here, nil)
		q.Refused = unsaved(files)
		if here() {
			if ok, _ := markup(ctx, save); ok {
				q.Markup = true
			}
		}
	}
	return p, nil
}

// saver writes a file of a pack whole, its name slash-separated in the pack's folder.
type saver func(name string, data []byte) error

// writeIn writes the file name, slash-separated, in root whole: into a file beside it, then renamed. A root's writes
// stay in its folder, and fail once the folder is gone rather than make it again.
func writeIn(root *os.Root, name string, data []byte) error {
	if dir := path.Dir(name); dir != "." {
		if err := root.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	tmp := path.Join(path.Dir(name), "."+path.Base(name)+".tmp")
	if err := root.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return root.Rename(tmp, name)
}

// evalIn is mygo.EvalAs that stops waiting when ctx ends, or after 20 s, longer than any of the pack's scripts waits,
// as a page whose script hangs never answers; the script runs on in a page about to close. The answer is decoded as
// EvalAs decodes it, by exact name.
func evalIn[T any](ctx context.Context, page *mygo.Page, code string) (T, error) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	raw, err := page.EvalContext(ctx, code)
	if err != nil || raw == nil {
		var v T
		return v, err
	}
	return decodeAs[T](raw)
}

// decodeAs is what Page.EvalContext answered as a T, by exact name, as EvalAs decodes it.
func decodeAs[T any](raw any) (T, error) {
	var v T
	data, err := jsonv2.Marshal(raw)
	if err != nil {
		return v, err
	}
	return v, jsonv2.Unmarshal(data, &v)
}

// capturePNG is win's page as Window.CapturePage gives it, decoded.
func capturePNG(win *mygo.Window) (image.Image, error) {
	data, err := win.CapturePage()
	if err != nil {
		return nil, err
	}
	return png.Decode(bytes.NewReader(data))
}

// keepFiles saves the files WebKit holds for win's page (keptFiles) that files has not saved yet, scripts left out,
// each as long as ok holds, and counts them in p.
func keepFiles(win *mygo.Window, save saver, p *Pack, files map[string]string, ok func() bool) error {
	if !ok() {
		return nil
	}
	kept, _ := keptFiles(win) // none kept: all are read again
	for _, f := range kept {
		folder, count := fileKind(f.Type, f.URL, p)
		if folder == "" || files[f.URL] != "" {
			continue
		}
		if !ok() {
			return nil
		}
		name := folder + "/" + packName(f.URL)
		name += typeExt(name, f.Type)
		if err := save("files/"+name, f.Data); err != nil {
			return err
		}
		files[f.URL] = "files/" + name
		*count++
	}
	return nil
}

// fileKind is where a file of MIME type t at link goes in a pack, and the count of p it adds to; none for a script or
// the like.
func fileKind(t, link string, p *Pack) (string, *int) {
	switch {
	case t == "text/css":
		return "styles", &p.Styles
	case strings.HasPrefix(t, "image/"):
		return "images", &p.Images
	case strings.Contains(t, "font") || fontFile.MatchString(link):
		return "fonts", &p.Fonts
	}
	return "", nil
}

// readAgain reads through the app's fetch layer the files that files lists as not saved, packReads at a time until
// ctx ends, saving each as it comes while ok holds, as what its type says it is (a page or a script is not saved),
// and counts them in p. heard, if not nil, hears how many of how many are done.
func readAgain(ctx context.Context, save saver, p *Pack, files map[string]string, ok func() bool, heard func(read, of int)) error {
	var links []string
	for link, file := range files {
		if file == "" {
			links = append(links, link)
		}
	}
	client := fetch.New(packFiles * 6)
	var mu sync.Mutex
	var wrote error
	read := 0
	reading := make(chan struct{}, packReads)
	var wg sync.WaitGroup
	for _, link := range links {
		wg.Go(func() {
			reading <- struct{}{}
			res, err := client.Fetch(ctx, link, anyPublic, packFileBytes)
			if err != nil && ctx.Err() == nil {
				// A connection dropped on the way is tried once more, as more are set up at once (fetchPicture).
				res, err = client.Fetch(ctx, link, anyPublic, packFileBytes)
			}
			<-reading
			var t string
			if err == nil {
				t, _, _ = strings.Cut(res.Type, ";")
				t = strings.ToLower(strings.TrimSpace(t))
			}
			mu.Lock()
			defer mu.Unlock()
			if read++; heard != nil {
				heard(read, len(links))
			}
			if err != nil || res.Status != 200 || len(res.Body) == 0 || !ok() {
				return
			}
			folder, count := fileKind(t, link, p)
			if folder != "images" && folder != "fonts" {
				return // a page, a script: left not saved
			}
			name := folder + "/" + packName(link)
			name += typeExt(name, t)
			if err := save("files/"+name, res.Body); err != nil {
				wrote = cmp.Or(wrote, err)
				return
			}
			files[link] = "files/" + name
			*count++
		})
	}
	wg.Wait()
	return wrote
}

// unsaved is how many files lists as not saved.
func unsaved(files map[string]string) (n int) {
	for _, f := range files {
		if f == "" {
			n++
		}
	}
	return n
}

// adding is the pack being added to once taken (addTo), if any: one at a time.
var adding struct {
	sync.Mutex
	dir  string
	stop context.CancelFunc
	done chan struct{}
}

// addTo runs more on the pack taken in dir, for packMore at most, until StopPack or the next TakePack.
func addTo(ctx context.Context, dir string, more func(context.Context, string)) {
	ctx, stop := context.WithTimeout(context.WithoutCancel(ctx), packMore)
	done := make(chan struct{})
	adding.Lock()
	adding.dir, adding.stop, adding.done = dir, stop, done
	adding.Unlock()
	go func() {
		defer close(done)
		defer stop()
		more(ctx, dir)
	}()
}

// StopPack stops adding to the pack in dir once taken, or to any for "", and returns once nothing more is written to
// it: before its folder is deleted. Not on the main thread, which the adding waits on.
func StopPack(dir string) {
	adding.Lock()
	of, stop, done := adding.dir, adding.stop, adding.done
	adding.Unlock()
	if stop == nil || dir != "" && dir != of {
		return
	}
	stop()
	<-done
}

// arrive waits for the page asked for to come: its load event; or, as many pages send it late or never (pictures still
// coming on a slow network, a film streaming, a request left open), its first screen ready at 4 s (its document read,
// its fonts in, the pictures on it loaded, words to show), its document read by 8 s, or 15 s at most. It reports
// whether the page came, and whether its load was over. A page that failed has not come, nor one of which nothing
// came, nor one still being read at 15 s with no word to show, as a connection hangs where a new one does not.
func arrive(ctx context.Context, page *mygo.Page, loaded <-chan bool) (came, whole bool, err error) {
	ready := time.NewTimer(4 * time.Second)
	defer ready.Stop()
	read := time.NewTimer(8 * time.Second)
	defer read.Stop()
	limit := time.NewTimer(15 * time.Second)
	defer limit.Stop()
	for {
		select {
		case ok := <-loaded:
			return ok, ok, nil
		case <-ready.C:
			if d, _ := evalIn[packDoc](ctx, page, packRead); d.Href != "about:blank" && d.Ready != "loading" && d.Words && d.Fonts && d.Pictures {
				return true, false, nil
			}
		case <-read.C:
			if d, _ := evalIn[packDoc](ctx, page, packRead); d.Href != "about:blank" && d.Ready != "loading" {
				return true, false, nil
			}
		case <-limit.C:
			d, _ := evalIn[packDoc](ctx, page, packRead)
			return d.Href != "about:blank" && (d.Ready != "loading" || d.Words), false, nil
		case <-ctx.Done():
			return false, false, ctx.Err()
		}
	}
}

// packDoc is how far a page's document has come (packRead): its address, its readyState, whether it shows words,
// whether its fonts are in, and whether the pictures on its first screen are loaded.
type packDoc struct {
	Href     string `json:"href"`
	Ready    string `json:"ready"`
	Words    bool   `json:"words"`
	Fonts    bool   `json:"fonts"`
	Pictures bool   `json:"pictures"`
}

const packRead = `
const shown = el => { const r = el.getBoundingClientRect(); return r.width > 0 && r.height > 0 && r.bottom > 0 && r.right > 0 && r.top < innerHeight && r.left < innerWidth }
return { href: location.href, ready: document.readyState, words: !!document.body && document.body.innerText.trim() !== '',
  fonts: !document.fonts || document.fonts.status === 'loaded', pictures: [...document.images].every(i => i.complete || !shown(i)) }`

// packStep is where a step down the page left it (stepScreen), whether the page goes on below the window there, and
// whether it can move at all: taller than the window, held (its overflow hidden, as a preloader holds it), or the
// wheel taken by its own script. The names are as the script gives them: MyGo decodes what a page answers by exact
// name.
type packStep struct {
	To    int  `json:"to"`
	More  bool `json:"more"`
	Tall  bool `json:"tall"`
	Held  bool `json:"held"`
	Taken bool `json:"taken"`
}

// keptFile is a file WebKit holds for a page (keptFiles): its address, its MIME type and its bytes.
type keptFile struct {
	URL, Type string
	Data      []byte
}

// fontFile tells a font by its address, for one served as any file.
var fontFile = regexp.MustCompile(`(?i)\.(woff2?|ttf|otf|eot)([?#]|$)`)

// typeExts are the extensions given to files named without one, as an image service names them, by their type.
var typeExts = map[string]string{"image/jpeg": ".jpg", "image/png": ".png", "image/webp": ".webp", "image/avif": ".avif",
	"image/gif": ".gif", "image/svg+xml": ".svg", "text/css": ".css", "font/woff2": ".woff2", "font/woff": ".woff",
	"font/ttf": ".ttf", "font/otf": ".otf"}

// typeExt is the extension of type t to add to name, so that the file opens as what it is; none when name has one.
func typeExt(name, t string) string {
	if path.Ext(name) != "" {
		return ""
	}
	return typeExts[t]
}

// step is stepScreen going down px (none for 0) and waiting least at least, most at most.
func step(down int, least, most time.Duration) string {
	return fmt.Sprintf("const down = %d, least = %d, most = %d\n", down, least.Milliseconds(), most.Milliseconds()) + stepScreen
}

// hold is packHold waiting up to wait for the page's pictures to come in.
func hold(wait time.Duration) string {
	return fmt.Sprintf("const wait = %d\n", max(0, wait.Milliseconds())) + packHold
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
	b.WriteString("- `screens/`: the page as a 1440 × 900 browser window shows it, one screen at a time from the top, at 2×.\n")
	b.WriteString("- `text.md`: all the page's text, top to bottom, including text that only shows on hover.\n")
	if p.Markup {
		b.WriteString("- `page.html`: the page's markup as it stood once captured, its scripts left out.\n")
	}
	b.WriteString("- `files/images/`, `files/fonts/`, `files/styles/`: the page's own pictures, font files and style sheets. `files/index.json` maps each original address to its file.\n\n")
	b.WriteString("## How to use it\n\n")
	b.WriteString("- Read this index first, then only the screens you need: the list below says what each one shows.\n")
	b.WriteString("- Take words from `text.md` rather than reading them off the screens.\n")
	if p.Markup {
		b.WriteString("- Take exact values (colours, sizes, spacing, font stacks, layout) from `page.html` and `files/styles/`: search them rather than reading them whole.\n")
	}
	b.WriteString("- Use the files in `files/` as they are, rather than cropping pictures out of the screens or guessing fonts.\n")
	b.WriteString("- The pictures and fonts are the site's own: check their licences before publishing anything that uses them.\n")
	b.WriteString("- Hover effects and animations are not captured: the screens show the page at rest, without its cookie notice, and without analytics, ads or chat widgets, which were not loaded.\n\n")
	fmt.Fprintf(&b, "## Screens (%d)\n\n", len(p.Screens))
	for _, s := range p.Screens {
		fmt.Fprintf(&b, "- `%s` at scroll %d px", s.File, s.ScrollY)
		if len(s.Headings) > 0 {
			b.WriteString(": " + strings.Join(s.Headings, " · "))
		}
		b.WriteString("\n")
	}
	if p.Cut {
		rest := "`text.md`"
		if p.Markup {
			rest = "`page.html` and " + rest
		}
		fmt.Fprintf(&b, "\nThe page goes on below the last screen: the rest of it is in %s.\n", rest)
	}
	if p.adding {
		b.WriteString("\nThis pack was still being completed when this index was written, for a minute at most: list `screens/` and `files/` again, or read this index again, for what came since.\n")
	}
	fmt.Fprintf(&b, "\n## Files\n\n%d pictures, %d fonts and %d style sheets saved", p.Images, p.Fonts, p.Styles)
	if p.Refused > 0 {
		fmt.Fprintf(&b, "; %d more the page loaded were not saved, listed in `files/index.json` without a file: fetch one from its address if you need it", p.Refused)
	}
	b.WriteString(".\n")
	if p.TitleFont != "" {
		fmt.Fprintf(&b, "\nThe largest words on the first screen are set in `%s`, at weight %d.\n", p.TitleFont, p.TitleWeight)
	}
	return b.String()
}

// packNotices hides the cookie and consent notices of a page that stand over it (hideNotices); the scripts that
// read a page run it first.
const packNotices = `
// A cookie or consent notice standing over the page (fixed, below the top bar, smaller than most of the window,
// short, with a button to answer it) is hidden and marked, to be left out of the text and markup; so is one not
// shown, as a copy kept for another width.
const hideNotices = () => {
  const w = document.createTreeWalker(document.body || document.documentElement, NodeFilter.SHOW_TEXT, { acceptNode: n => /cookie|consent|gdpr/i.test(n.data) ? 1 : 3 })
  for (let n; (n = w.nextNode());)
    for (let el = n.parentElement; el && el !== document.body; el = el.parentElement) {
      if (getComputedStyle(el).position !== 'fixed') continue
      const r = el.getBoundingClientRect(), t = el.textContent || ''
      if ((!r.width && !r.height || r.top >= 60 && r.width * r.height < innerWidth * innerHeight * 0.6) && t.length < 1000 &&
        (el.querySelector('button, [role=button], input[type=button], input[type=submit]') ||
          /accept|agree|allow|decline|reject|deny|settings|preferences|manage|\bok\b|got it|understand|同意|接受|拒绝|知道了/i.test(t))) {
        el.setAttribute('data-trie-notice', '')
        el.style.setProperty('display', 'none', 'important')
      }
      break
    }
}
hideNotices()
`

// stepScreen scrolls down by down px as a mouse wheel would, so smooth-scroll libraries take it, the window scrolled
// directly when nothing took the wheel; then waits for the screen to hold still: nothing on it changing or moving for
// 350 ms (an entrance animation, a picture coming in, a smooth scroll easing out), its pictures loaded and its fonts
// in; least ms at least, most ms at most (step sets the three). What moves for ever is not waited for: an animation
// not ending within 1.5 s, a looping Lottie (in an SVG), a film, a canvas, and what has kept changing for 1.2 s since
// the step (a ticker, a marquee), nor pictures loading out of view (packHold): of the files that come, only those a
// style sheet asks for count, as the pictures on the screen tell themselves when they are loaded. A page the step did
// not move is not waited for either, as no screen is taken of it. It returns a packStep.
const stepScreen = packNotices + `
const shown = el => { const r = el.getBoundingClientRect(); return r.width > 0 && r.height > 0 && r.bottom > 0 && r.right > 0 && r.top < innerHeight && r.left < innerWidth }
const watch = window.__trieWatch || (window.__trieWatch = (() => {
  const w = { at: 0, step: 0, since: new WeakMap() }
  new MutationObserver(list => {
    const now = performance.now()
    for (const m of list) {
      const el = m.target.nodeType === 1 ? m.target : m.target.parentElement
      if (!el || el.closest('svg') || m.type === 'attributes' && m.oldValue === el.getAttribute(m.attributeName)) continue
      const s = w.since.get(el)
      if (s >= w.step && now - s >= 1200 || !shown(el)) continue
      if (!(s >= w.step)) w.since.set(el, now)
      w.at = now
    }
  }).observe(document.documentElement, { subtree: true, childList: true, characterData: true, attributes: true,
    attributeOldValue: true, attributeFilter: ['style', 'class', 'src', 'srcset'] })
  return w
})())
const sleep = ms => new Promise(r => setTimeout(r, ms))
const t0 = performance.now(), y0 = Math.round(scrollY)
watch.step = t0
let taken = false
if (down) {
  const wheel = new WheelEvent('wheel', { deltaY: down, deltaMode: 0, bubbles: true, cancelable: true, clientX: 720, clientY: 450 })
  ;(document.elementFromPoint(720, 450) || document.documentElement).dispatchEvent(wheel)
  // A script that took the wheel scrolls the page itself, or not at all.
  if (!(taken = wheel.defaultPrevented)) window.scrollTo(0, y0 + down)
}
const arrived = () => performance.getEntriesByType('resource').filter(e => e.initiatorType === 'css').length
let y = scrollY, n = arrived(), at = performance.now()
const ending = a => {
  const e = a.effect, t = e && e.target
  if (a.playState !== 'running' || a.timeline !== document.timeline || !t || t.closest('svg')) return false
  const c = e.getComputedTiming()
  return c.endTime - c.localTime < 1500 && shown(t)
}
for (;;) {
  await sleep(100)
  const now = performance.now(), m = arrived()
  if (scrollY !== y || m !== n) { y = scrollY; n = m; at = now }
  if (now - t0 >= most || down && Math.round(scrollY) === y0 && now - t0 >= 600) break
  if (now - t0 < least || now - Math.max(at, watch.at) < 350) continue
  if (document.getAnimations().some(ending)) continue
  if ([...document.images].some(i => !i.complete && shown(i)) || document.fonts && document.fonts.status !== 'loaded') continue
  break
}
hideNotices() // one that came while it settled
const se = document.scrollingElement || document.documentElement
return { to: Math.round(scrollY), more: Math.ceil(scrollY + innerHeight) < se.scrollHeight - 1, tall: se.scrollHeight > innerHeight + 1, taken,
  held: [document.documentElement, document.body].some(e => e && /hidden|clip/.test(getComputedStyle(e).overflowY)) }`

// headingsInView lists the headings a screen shows, to label it in the index.
const headingsInView = `
const hs = [...document.querySelectorAll('h1,h2,h3,[role=heading]')].filter(h => { const r = h.getBoundingClientRect(); return r.bottom > 0 && r.top < innerHeight && r.width > 0 && getComputedStyle(h).visibility !== 'hidden' })
return JSON.stringify({ scrollY: Math.round(scrollY), headings: [...new Set(hs.map(h => h.textContent.replace(/\s+/g, ' ').trim().slice(0, 60)).filter(Boolean))].slice(0, 6) })`

// packHold has the page load now the pictures it puts off until they scroll into view (loading=lazy), and hold those
// a script loaded for itself (a WebGL texture, in no <img>), in pictures of its own out of sight (#trie-pack-hold):
// WebKit then keeps them all among the page's files, read over the page's own connections. It waits up to wait ms
// (hold sets it) for the page's pictures to come in, and answers how many are still coming.
const packHold = `
for (const i of document.querySelectorAll('img[loading=lazy]')) i.loading = 'eager'
const box = document.getElementById('trie-pack-hold') || document.documentElement.appendChild(Object.assign(document.createElement('div'), { id: 'trie-pack-hold', hidden: true }))
const have = new Set([...document.images].map(i => i.currentSrc || i.src))
for (const e of performance.getEntriesByType('resource'))
  if (/\.(png|jpe?g|webp|avif|gif|svg)(\?|#|$)/i.test(e.name) && !/favicon|apple-touch/i.test(e.name) && !have.has(e.name)) {
    have.add(e.name)
    box.appendChild(new Image()).src = e.name
  }
const coming = () => [...document.images].filter(i => !i.complete).length
for (const t0 = performance.now(); coming() && performance.now() - t0 < wait;) await new Promise(r => setTimeout(r, 100))
return coming()`

// packMarkup is the page's markup as it stands, scripts and consent notices left out: the style a script put in
// through a style sheet's rules (CSS in JS), which no element's text holds, written into its style element.
const packMarkup = packNotices + `
const doc = document.documentElement.cloneNode(true)
doc.querySelector('#trie-pack-hold')?.remove()
const live = document.querySelectorAll('style'), copies = doc.querySelectorAll('style')
live.forEach((s, i) => { try { if (copies[i] && !s.textContent.trim() && s.sheet && s.sheet.cssRules.length) copies[i].textContent = [...s.sheet.cssRules].map(r => r.cssText).join('\n') } catch {} })
try {
  const adopted = [...(document.adoptedStyleSheets || [])].map(s => [...s.cssRules].map(r => r.cssText).join('\n')).join('\n')
  if (adopted.trim()) { const s = document.createElement('style'); s.textContent = adopted; (doc.querySelector('head') || doc).append(s) }
} catch {}
for (const s of doc.querySelectorAll('script, [data-trie-notice], ` + consentNotices + `')) s.remove()
// A lone half of a surrogate pair, which the answer cannot carry, becomes U+FFFD.
const html = (document.doctype ? '<!doctype ' + document.doctype.name + '>\n' : '') + doc.outerHTML
return html.toWellFormed ? html.toWellFormed() : html`

// packCollect reads the page text (hidden text too, as titles shown only on hover), one line per block, and the
// pictures and fonts it uses: those in the page and its style sheets, and every file it loaded (a WebGL texture or a
// picture a script drew is in no <img>). Of the fonts its style sheets name, only those it loaded are taken.
const packCollect = packNotices + `
try { performance.setResourceTimingBufferSize(5000) } catch {}
const clean = s => (s || '').replace(/[\u0000-\u0008\u000b-\u001f\u007f]/g, ' ').replace(/[ \t ]+/g, ' ').trim()
const lines = []
let block = null, cur = '', space = false
const walker = document.createTreeWalker(document.body || document.documentElement, NodeFilter.SHOW_TEXT, {
  acceptNode: n => n.parentElement && n.parentElement.closest('script,style,noscript,template,svg,video,audio,[data-trie-notice],` + consentNotices + `') ? NodeFilter.FILTER_REJECT : NodeFilter.FILTER_ACCEPT })
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
// An address is a file's own: an empty one, or one to a part of this page (url(#clip)), is the page's and is left out.
const page = location.href.split('#')[0]
const abs = (u, base) => { try { const x = new URL(u, base || location.href); return /^https?:$/.test(x.protocol) && x.href.split('#')[0] !== page ? x.href : null } catch { return null } }
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
// Every @font-face: its family, weights, style, the characters it covers and its files.
const faces = []
const parseFaces = (css, base) => {
  for (const m of css.matchAll(/@font-face\s*\{([^}]*)\}/g)) {
    const body = m[1]
    const get = k => { const x = body.match(new RegExp('(?:^|;|\\{)\\s*' + k + '\\s*:\\s*([^;]+)', 'i')); return x ? x[1].trim() : '' }
    const fam = get('font-family').replace(/^["']|["']$/g, '').toLowerCase()
    const w = get('font-weight').split(/\s+/).map(v => v === 'bold' ? 700 : v === 'normal' || v === '' ? 400 : +v).filter(v => !isNaN(v))
    const urls = [...get('src').matchAll(/url\(\s*["']?([^"')]+)["']?\s*\)/g)].map(u => abs(u[1], base)).filter(Boolean)
    if (fam && urls.length) faces.push({ fam, lo: w[0] ?? 400, hi: w[1] ?? w[0] ?? 400, italic: /italic|oblique/i.test(get('font-style')), range: get('unicode-range'), urls })
  }
}
const walk = (rules, base) => {
  for (const r of rules) {
    if (r.type === CSSRule.FONT_FACE_RULE) parseFaces(r.cssText, base)
    if (r.cssRules) walk(r.cssRules, base)
  }
}
const blocked = []
for (const s of document.styleSheets) { try { walk(s.cssRules, s.href || location.href) } catch { if (s.href) blocked.push(s.href) } }
// A style sheet from another site (a font service's) is read as text, when it lets the page read it, all at once.
await Promise.all(blocked.slice(0, 6).map(async href => {
  try {
    const stop = new AbortController()
    setTimeout(() => stop.abort(), 1500)
    parseFaces(await (await fetch(href, { signal: stop.signal })).text(), href)
  } catch {}
}))
// The largest words on the first screen, and the face that sets them: of their families the first with a face of its
// own that covers Latin capitals, at the weight nearest theirs: its WOFF2 file first, WOFF last.
const coversA = range => !range || range.split(',').some(p => {
  const [a, b] = p.trim().replace(/^u\+/i, '').split('-')
  if (a.includes('?')) return parseInt(a.replace(/\?/g, '0'), 16) <= 0x41 && parseInt(a.replace(/\?/g, 'f'), 16) >= 0x41
  const lo = parseInt(a, 16), hi = b ? parseInt(b, 16) : lo
  return lo <= 0x41 && hi >= 0x41
})
let largest = null
for (const el of document.querySelectorAll('body *')) {
  if (!el.childNodes.length || el.closest('script,style,noscript,template,svg')) continue
  if (![...el.childNodes].some(n => n.nodeType === 3 && n.textContent.trim().length > 1)) continue
  const r = el.getBoundingClientRect()
  if (!r.width || !r.height || r.top + scrollY > 1100) continue
  const cs = getComputedStyle(el)
  if (cs.visibility === 'hidden' || +cs.opacity === 0) continue
  const size = parseFloat(cs.fontSize)
  if (!largest || size > largest.size) largest = { size, cs }
}
let titleFont = null
if (largest) {
  const weight = parseInt(largest.cs.fontWeight) || 400, italic = largest.cs.fontStyle !== 'normal'
  for (const fam of largest.cs.fontFamily.split(',').map(f => f.trim().replace(/^["']|["']$/g, '').toLowerCase())) {
    const own = faces.filter(f => f.fam === fam && coversA(f.range))
    if (!own.length) continue
    const far = f => (f.italic !== italic ? 1000 : 0) + (weight < f.lo ? f.lo - weight : weight > f.hi ? weight - f.hi : 0)
    const face = own.sort((a, b) => far(a) - far(b))[0]
    const rank = u => /\.woff2(\?|#|$)/i.test(u) ? 0 : /\.woff(\?|#|$)/i.test(u) ? 2 : 1
    const url = [...face.urls].sort((a, b) => rank(a) - rank(b))[0]
    titleFont = { url, weight: Math.round(Math.min(Math.max(weight, face.lo), face.hi)) }
    fonts.push(url)
    break
  }
}
return JSON.stringify({ text, images: uniq(images).slice(0, 400), fonts: uniq(fonts), titleFont })`
