package explore

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"trie/internal/extract"
	"trie/internal/fetch"
)

// SnapshotURL is the snapshot the app pulls Explore from: what a copy of the app read of every feed, picture and first
// screen (Publish), published every three hours on the project's site. The stats version is in the path, so a copy
// of the app pulls only stats it reads.
var SnapshotURL = "https://konata33.github.io/Trie/explore/v" + strconv.Itoa(version) + "/"

const (
	// pullEvery is how often a running app pulls the snapshot again, as Explore is asked for.
	pullEvery = time.Hour
	// pullWait is how long a pull of the snapshot's lists may take; the feeds are then read from their sites.
	pullWait = 20 * time.Second
)

// pullDue is the pull of the snapshot under way, started now when none was made within pullEvery; nil with no
// snapshot or after a recent pull. s.mu is held.
func (s *Service) pullDue() chan struct{} {
	if s.o.Snapshot == "" {
		return nil
	}
	if s.pulling == nil && (s.pulledAt.IsZero() || now().Sub(s.pulledAt) > pullEvery) {
		ch := make(chan struct{})
		s.pulling = ch
		s.bg.Go(func() { s.pull(ch) })
	}
	return s.pulling
}

// pull reads the snapshot's stats and icons. Stats newer than those kept are taken as a read of their feeds would
// take them: a feed nothing is shown for yet at once, one on the page once the pictures of what it adds are in
// (ahead). Icons not looked for yet are taken. A pull that lands counts as the launch's read of every feed (warmed), so
// only what the snapshot does not hold fresh is read from its site; one that fails leaves everything to the sites.
// ch is closed once the stats are known, before the pictures.
func (s *Service) pull(ch chan struct{}) {
	ctx, cancel := context.WithTimeout(context.Background(), pullWait)
	var cache map[string]*stats
	var icons map[string]*string
	var wg sync.WaitGroup
	wg.Go(func() { cache = pullJSON[map[string]*stats](ctx, s, "explore-cache.json") })
	wg.Go(func() { icons = pullJSON[map[string]*string](ctx, s, "explore-icons.json") })
	wg.Wait()
	cancel()
	s.mu.Lock()
	var later []string
	if cache != nil {
		s.warmed = true
		for url, next := range cache {
			prev := s.stats[url]
			if _, busy := s.inflight[url]; busy || next == nil || next.V != version || prev != nil && prev.At >= next.At {
				continue
			}
			if prev == nil {
				s.stats[url] = next
			} else {
				later = append(later, url)
			}
		}
	}
	for site, icon := range icons {
		if _, known := s.icons[site]; !known {
			s.icons[site] = icon
		}
	}
	landing := s.claim(later)
	s.pulling, s.pulledAt = nil, now()
	s.mu.Unlock()
	close(ch)
	if cache == nil && icons == nil {
		return
	}
	if icons != nil {
		s.save(s.iconsPath, func() any { return s.icons })
	}
	s.changed()
	next := make(chan string, len(landing.queue))
	for _, url := range landing.queue {
		next <- url
	}
	close(next)
	var lands sync.WaitGroup
	for range min(6, len(landing.queue)) {
		lands.Go(func() {
			for url := range next {
				s.ahead(url, cache[url])
				s.mu.Lock()
				s.stats[url] = cache[url]
				done := s.inflight[url]
				delete(s.inflight, url)
				s.mu.Unlock()
				close(done)
				s.tell()
			}
		})
	}
	lands.Wait()
	if cache != nil {
		s.save(s.o.Cache, func() any { return s.stats })
	}
}

// pullJSON is one of the snapshot's lists; the zero value when it cannot be had or read.
func pullJSON[T any](ctx context.Context, s *Service, name string) T {
	var v T
	if data := s.hosted(ctx, name); data == nil || json.Unmarshal(data, &v) != nil {
		var none T
		return none
	}
	return v
}

// hosted is a file of the snapshot, by its path there (as the library's folder keeps it); nil when it cannot be had.
func (s *Service) hosted(ctx context.Context, path string) []byte {
	if s.o.Snapshot == "" {
		return nil
	}
	get := s.o.Fetch
	if get == nil {
		get = fetch.New(6).Fetch
	}
	res, err := get(ctx, s.o.Snapshot+path, func(string) bool { return true }, 16*1024*1024)
	if err != nil || res.Status != 200 || len(res.Body) == 0 {
		return nil
	}
	return res.Body
}

// hostedShot takes site's first screen from the snapshot, both sizes as a capture keeps them; false when it holds none.
func (s *Service) hostedShot(site string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), pullWait)
	defer cancel()
	small := s.hosted(ctx, "explore-shots/"+filepath.Base(ShotFile("", site, 400)))
	large := s.hosted(ctx, "explore-shots/"+filepath.Base(ShotFile("", site, 1280)))
	if extract.ImageType(small) != "image/jpeg" || extract.ImageType(large) != "image/jpeg" {
		return false
	}
	// The large one last: it says the site is taken (Shots.Failed, Shots.Stale).
	return writeFile(ShotFile(s.o.Shots.dir, site, 400), small) == nil && writeFile(ShotFile(s.o.Shots.dir, site, 1280), large) == nil
}

// Publish reads Explore from the sites themselves, as a first visit sees it, and waits for all of it: every feed, the
// icons and openings the feeds lead to, the picture behind every card and every work, the design sites' first
// screens. It then writes what it kept to out/explore/v<version>, laid out as the library's folder keeps it
// (explore-cache.json, explore-icons.json, explore-works, explore-shots): the snapshot at SnapshotURL. It fails, and
// writes nothing, when fewer than half of the feeds could be read, so a bad run does not replace a good snapshot.
func (s *Service) Publish(ctx context.Context, out string) error {
	started := float64(nowMS())
	s.mu.Lock()
	s.o.Snapshot = ""
	s.mu.Unlock()
	var entries []Entry
	// The first read brings writers and sites whose icons and openings are looked for behind it; the second waits for those.
	for range 2 {
		var err error
		if entries, err = s.List(ctx); err != nil {
			return err
		}
		s.bg.Wait()
	}
	s.mu.Lock()
	read, feeds := 0, 0
	for _, st := range s.stats {
		feeds++
		if st != nil && st.Error == "" && st.At >= started {
			read++
		}
	}
	s.mu.Unlock()
	if read*2 < feeds {
		return fmt.Errorf("explore: only %d of %d feeds could be read", read, feeds)
	}
	sem := make(chan struct{}, 8)
	var wg sync.WaitGroup
	do := func(job func()) {
		wg.Go(func() {
			sem <- struct{}{}
			defer func() { <-sem }()
			job()
		})
	}
	for _, e := range entries {
		do(func() { s.Shot(ctx, e.ID) })
		for i := range e.Works {
			do(func() { s.Work(ctx, e.ID, i) })
		}
	}
	wg.Wait()
	if err := ctx.Err(); err != nil {
		return err
	}
	s.save(s.o.Cache, func() any { return s.stats })
	s.save(s.iconsPath, func() any { return s.icons })
	// What goes out: the lists, the pictures this run showed, the first screens of the design sites it showed.
	dir := filepath.Join(out, "explore", "v"+strconv.Itoa(version))
	files := map[string]string{filepath.Join(dir, "explore-cache.json"): s.o.Cache, filepath.Join(dir, "explore-icons.json"): s.iconsPath}
	s.pmu.Lock()
	for key, f := range s.pictures {
		select {
		case <-f.done:
			if f.value != "" {
				file, _, _ := strings.Cut(key, "#")
				files[filepath.Join(dir, "explore-works", filepath.Base(file))] = file
			}
		default:
		}
	}
	s.pmu.Unlock()
	for _, e := range entries {
		if site, err := s.site(e.ID); err == nil && site != nil && !site.Writer && s.o.Shots != nil {
			for _, w := range []int{400, 1280} {
				file := ShotFile(s.o.Shots.dir, site.Host, w)
				files[filepath.Join(dir, "explore-shots", filepath.Base(file))] = file
			}
		}
	}
	if err := os.RemoveAll(dir); err != nil {
		return err
	}
	for to, from := range files {
		data, err := os.ReadFile(from)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return err
		}
		if err := writeFile(to, data); err != nil {
			return err
		}
	}
	return nil
}
