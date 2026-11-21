package app

import (
	"io/fs"
	"math"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/egoist/mygo/ui"

	"trie/internal/motion"
	"trie/internal/theme"
)

// A door's drawing (PAGES.md 探索, 门): one brush-pen drawing for each kind
// on a canvas doorW × doorH, traced by colour into layers in
// art/doors/<kind>/: still, the lines that stay; moving, the lines of the
// one part that acts; amber, its one fill; face, the ground of a part that
// hides what lies behind it; faint, the marks drawn dim; faint-act, faint
// marks that come with the act. A layer's viewBox is its own box on the
// canvas.

const doorW, doorH = 1024, 1280

type doorLayer struct {
	svg *ui.SVG
	box ui.Rect // on the canvas
}

type doorArt struct{ still, moving, amber, face, faint, faintAct doorLayer }

var viewBoxAttr = regexp.MustCompile(`viewBox="([^"]*)"`)

var doorArts = sync.OnceValue(func() map[string]*doorArt {
	read := func(k, name string) doorLayer {
		data, err := fs.ReadFile(artFiles, "art/doors/"+k+"/"+name+".svg")
		if err != nil {
			return doorLayer{}
		}
		var v [4]float32
		if m := viewBoxAttr.FindSubmatch(data); m != nil {
			for i, f := range strings.Fields(string(m[1])) {
				if n, err := strconv.ParseFloat(f, 32); err == nil && i < len(v) {
					v[i] = float32(n)
				}
			}
		}
		return doorLayer{ui.MustParseSVG(data), ui.Rect{X: v[0], Y: v[1], W: v[2], H: v[3]}}
	}
	out := map[string]*doorArt{}
	for _, k := range kindOrder {
		out[k] = &doorArt{still: read(k, "still"), moving: read(k, "moving"), amber: read(k, "amber"),
			face: read(k, "face"), faint: read(k, "faint"), faintAct: read(k, "faint-act")}
	}
	return out
})

// doorPose is how a kind's moving part acts, in the canvas's pixels and
// degrees, turned about (ox, oy). It is drawn as it stands with the door
// lit; at rest it stands dx, dy and deg away from there, still showing a
// little of the amber it reveals. The pendulum swings deg at its widest; the
// stamp presses dx, dy down. cx is where the drawing's weight lies across
// the canvas as it rests, where that is not its middle.
type doorPose struct{ ox, oy, dx, dy, deg, cx float32 }

var doorPoses = map[string]doorPose{
	// The sun sits low behind the hills, its top showing; the fold hides what the hills do not.
	"landing": {ox: 512, oy: 251, dy: 170},
	// The vase's print lies over the pear's, turned a little towards it, an edge of the pear showing.
	"portfolio": {ox: 699, oy: 573, dx: -205, deg: -5, cx: 450},
	// The rod hangs from the crossbar.
	"motion": {ox: 512, oy: 250, deg: 24},
	// The pen is lifted off the letter.
	"type": {ox: 729, oy: 354, dx: 40, dy: -48, deg: -6},
	// The stamp comes down onto the mark.
	"brand": {ox: 507, oy: 356, dy: 318},
}

const (
	// The landing door's fold, on the canvas.
	doorFold = 485
	// The type door's bowl, its top and foot on the canvas, and how full it
	// is at rest.
	doorBowlTop, doorBowlFoot, doorBowlRest = 696, 844, 0.3
	// The faint marks, as a share of the lines' colour.
	doorFaint = 0.34
	// How far through its press the stamp lights the mark it lands on.
	doorStampLights = 0.3
	// How much of a door's width a drawing of average weight spans, and the
	// room every drawing keeps from the door's top and sides, in points.
	doorSpan, doorMargin = 0.5, 16
	// Under the drawings: the gap to the name, the name's line, its gap to
	// the door's foot.
	doorUnder = 14 + 20 + 18
)

// doorShape is what doorArtBox needs of a drawing, on the canvas: the box
// all its marks take in either pose, the line its lowest mark stands on, and
// how much to scale it so that the five carry about the same weight.
type doorShape struct {
	span         ui.Rect
	foot, weight float32
}

// doorInkW is the width, on the canvas, of the drawings' ink on average.
const doorInkW = 772

var doorShapes = sync.OnceValue(func() map[string]doorShape {
	union := func(r, o ui.Rect) ui.Rect {
		if o.W <= 0 {
			return r
		}
		if r.W <= 0 {
			return o
		}
		x0, y0 := min(r.X, o.X), min(r.Y, o.Y)
		return ui.Rect{X: x0, Y: y0, W: max(r.X+r.W, o.X+o.W) - x0, H: max(r.Y+r.H, o.Y+o.H) - y0}
	}
	shift := func(r ui.Rect, p doorPose) ui.Rect {
		if r.W > 0 {
			r.X, r.Y = r.X+p.dx, r.Y+p.dy
		}
		return r
	}
	areas := map[string]float32{}
	out := map[string]doorShape{}
	var mean float32
	for _, k := range kindOrder {
		art, p := doorArts()[k], doorPoses[k]
		ink := union(union(union(art.still.box, art.moving.box), art.face.box), art.amber.box)
		span := union(union(union(ink, art.faint.box), shift(art.moving.box, p)), shift(art.face.box, p))
		foot := max(art.still.box.Y+art.still.box.H, art.faint.box.Y+art.faint.box.H) - 4
		out[k] = doorShape{span: span, foot: foot}
		areas[k] = ink.W * ink.H
		mean += ink.W * ink.H / float32(len(kindOrder))
	}
	for k, s := range out {
		s.weight = min(1.12, max(0.9, float32(math.Sqrt(float64(mean/areas[k])))))
		out[k] = s
	}
	return out
})

// doorArtBox is where kind k's drawing lies on a door w×h: its lowest mark
// on the row's one line above the name; about doorSpan of the door across,
// scaled by its weight so that the five look alike in size; doorMargin clear
// of the door's top and sides in every pose; centred on its weight.
func doorArtBox(k string, w, h float32) ui.Rect {
	g := doorShapes()[k]
	shelf := h - doorUnder
	s := doorSpan * w / doorInkW * g.weight
	s = min(s, (w-2*doorMargin)/g.span.W, (shelf-doorMargin)/(g.foot-g.span.Y))
	cx := float32(doorW / 2)
	if p := doorPoses[k]; p.cx != 0 {
		cx = p.cx
	}
	x := w/2 - cx*s
	x = min(max(x, doorMargin-g.span.X*s), w-doorMargin-(g.span.X+g.span.W)*s)
	return ui.Rect{X: x, Y: shelf - g.foot*s, W: doorW * s, H: doorH * s}
}

// doorBeats are how long the acts that play once take.
var doorBeats = map[string]time.Duration{"motion": motion.DoorSwingFor, "brand": motion.DoorStampFor}

// doorActing is how a door's drawing last acted, kept on its kind's wall:
// whether the pointer was on it, how many times its act that plays once has
// been set off and when it last was, and how far through it it is.
type doorActing struct {
	pointed bool
	runs    uint64
	started time.Time
	beat    float32
}

// doorAct is how far a door's moving part has gone to where it is drawn, as
// the door is lit (on 1) or not: along Ease over DoorAct, back over
// DoorActBack; at once from the keys. The pendulum and the stamp act once as
// the pointer comes onto the door (beat, through that act), and finish if
// it leaves; an act over is not played again when the door is built anew, as
// Explore is on the way back from a wall. Under less motion the part stands
// where it is drawn and only the light changes.
func (a *App) doorAct(c *ui.Context, b ui.Element, k string, kw *kindWall, on float32, live bool) (act, beat float32) {
	if a.still(c) {
		return 1, 0
	}
	anim := ui.Animation{Duration: motion.DoorAct, Ease: motion.Ease}
	switch {
	case a.kbd:
		anim = ui.Animation{Duration: time.Nanosecond}
	case on == 0:
		anim.Duration = motion.DoorActBack
	}
	act = b.AnimateTo("act", on, anim)
	st := &kw.acting
	end := doorBeats[k]
	if end == 0 {
		return act, 0
	}
	playing := st.runs > 0 && c.Now().Sub(st.started) < end
	if live {
		pointed := on == 1 && !a.kbd
		if pointed && !st.pointed && !playing {
			st.runs, st.started, playing = st.runs+1, c.Now(), true
		}
		st.pointed = pointed
	}
	if playing {
		beat = b.Keyframes("beat", []ui.Keyframe{{Value: 0}, {At: end, Value: 1}, {At: end + time.Millisecond, Value: 0}},
			ui.KeyframeOptions{Run: st.runs})
	}
	if live {
		st.beat = beat
	}
	return act, beat
}

// doorAmber is how far a door's amber is lit: with the door, but the brand
// door's mark only once the stamp has pressed it, which it does a moment
// into its act; from the keys and under less motion, with the door.
func (a *App) doorAmber(c *ui.Context, b ui.Element, k string, kw *kindWall, on, light float32) float32 {
	if k != "brand" || a.kbd || a.still(c) {
		return light
	}
	st := &kw.acting
	pressed := float32(0)
	if on == 1 && st.runs > 0 && c.Now().Sub(st.started) >= time.Duration(doorStampLights*float32(doorBeats[k])) {
		pressed = 1
	}
	return b.AnimateTo("amber", pressed, a.ease(motion.Light))
}

// doorLit is how a door's drawing stands: lit light of the way, its amber
// amber of the way, its moving part act of the way to where it is drawn and
// beat through its act that plays once.
type doorLit struct{ light, amber, act, beat float32 }

// doorDrawing is a kind's drawing over a box w×h on its door, standing as l
// says. Its lines go from icon to text as it lights, its amber from
// icon-fill to the accent, as an icon's do; its faces are the window's
// colour the doors stand on, so that they hide what they pass over.
func (a *App) doorDrawing(c *ui.Context, k string, w, h float32, l doorLit) {
	art, mv := doorArts()[k], doorPoses[k]
	if art == nil || art.still.svg == nil {
		return
	}
	u := w / doorW
	ink, amber, face := theme.Icon.Mix(theme.Text, l.light), theme.IconFill.Mix(theme.Accent, l.amber), theme.Window
	act, beat := l.act, l.beat
	layer := func(l doorLayer, col ui.Color, op float32) {
		if l.svg != nil && op > 0.001 {
			ui.Icon(c, l.svg).Absolute().Left(l.box.X*u).Top(l.box.Y*u).Size(l.box.W*u, l.box.H*u).TextColor(col).Opacity(op)
		}
	}
	// The moving part, as a whole over the door: its lines over what it carries (its face, or the sun's and the
	// bob's amber).
	part := func(under doorLayer, col ui.Color) ui.Element {
		return ui.Box(c).Absolute().Left(0).Top(0).Size(w, h).TransformOrigin(mv.ox/doorW, mv.oy/doorH).Children(func() {
			layer(under, col, 1)
			layer(art.moving, ink, 1)
		})
	}
	rest := 1 - act
	switch k {
	case "landing":
		// The sun rises from behind the hills.
		ui.Box(c).Absolute().Left(0).Top(0).Size(w, doorFold*u).ClipX().ClipY().Children(func() {
			part(art.amber, amber).Translate(mv.dx*u*rest, mv.dy*u*rest)
		})
		layer(art.face, face, 1)
		layer(art.faint, ink, doorFaint)
		layer(art.still, ink, 1)
	case "motion":
		layer(art.faint, ink, doorFaint)
		layer(art.still, ink, 1)
		part(art.amber, amber).Rotate(mv.deg * motion.DoorSwing(beat))
	case "type":
		// The bowl fills from its foot as the pen dips, and two drops fall from the nib.
		top := doorBowlFoot - (doorBowlFoot-doorBowlTop)*(doorBowlRest+(1-doorBowlRest)*act)
		ui.Box(c).Absolute().Left(0).Top(top*u).Size(w, h-top*u).ClipX().ClipY().Children(func() {
			l := art.amber
			l.box.Y -= top
			layer(l, amber, 1)
		})
		layer(art.faint, ink, doorFaint)
		layer(art.faintAct, ink, min(1, max(0, act-0.6)/0.4))
		layer(art.still, ink, 1)
		part(art.face, face).Translate(mv.dx*u*rest, mv.dy*u*rest).Rotate(mv.deg * rest)
	case "brand":
		// The stamp comes down onto the sheet and lifts off it; the marks of its lifting go while it is down.
		p := motion.DoorStamp(beat)
		layer(art.faint, ink, doorFaint*(1-p))
		layer(art.amber, amber, 1)
		layer(art.still, ink, 1)
		part(art.face, face).Translate(mv.dx*u*p, mv.dy*u*p)
	default: // portfolio: the print over the other slides aside.
		layer(art.faint, ink, doorFaint)
		layer(art.amber, amber, 1)
		layer(art.still, ink, 1)
		part(art.face, face).Translate(mv.dx*u*rest, mv.dy*u*rest).Rotate(mv.deg * rest)
	}
}
