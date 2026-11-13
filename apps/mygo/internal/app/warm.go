package app

import (
	"time"

	"github.com/egoist/mygo/ui"

	"trie/internal/locales"
	"trie/internal/theme"
)

// Warming up: what costs the most the first time it shows is done before
// it is asked for, so that its first frame does not stall.

// warmFonts makes the faces the dialogs, the sheet and the reader set text
// in, off the window's thread: a face is made once, on first use.
func warmFonts() {
	for _, f := range []ui.Font{
		{Family: theme.Sans, Size: theme.Subtitle, Weight: 600}, {Family: theme.Sans, Size: theme.Item, Weight: 600}, {Family: theme.Sans, Size: theme.Body, Weight: 600},
		{Family: theme.Sans, Size: theme.Label}, {Family: theme.Sans, Size: theme.Body}, {Family: theme.Sans, Size: theme.Item}, {Family: theme.Sans, Size: theme.ReadingSize},
		{Family: theme.Sans, Size: theme.Headline, Weight: 600}, {Family: theme.Sans, Size: theme.Title, Weight: 600}, {Family: theme.Sans, Size: theme.Subtitle},
		{Family: theme.Mono, Size: theme.Body}, {Family: theme.Mono, Size: theme.Label}, {Family: "PingFang SC", Size: theme.ReadingSize}, {Family: "PingFang SC", Size: theme.Headline, Weight: 600},
		{Family: theme.Serif, Size: theme.EnglishReadingSize}, {Family: theme.Serif, Size: theme.Headline, Weight: 600},
	} {
		ui.Shape("订阅一个博客 Trie 0123456789 快捷键", f)
	}
}

// warmBlur blurs the window for the first frames after it shows, too
// little to see (0.3), so that the textures and passes a blurred window
// needs are made then and not as the first dialog opens over the page.
const warmBlur = 0.3

// warmFrames is how many frames the warm-up lasts.
const warmFrames = 2

// hoverWarm is how long the pointer rests on a row before its article is
// laid out, unseen: the reader's first frame then finds its text laid out.
const hoverWarm = 120 * time.Millisecond

// unseen builds a page that is only being laid out ahead of its click: while
// it builds, nothing it shows is fetched (thumbView), so a pointer passing
// over rows and authors touches no site.
func (a *App) unseen(build func()) {
	a.warming = true
	defer func() { a.warming = false }()
	build()
}

// crossingTime is how long after a page changes nothing is laid out unseen:
// the longest crossing, its ground melting.
const crossingTime = 900 * time.Millisecond

// warmDialog draws a dialog, unseen (1% opaque), growing and out of focus
// as one opens, for the first frames: its first opening then finds its
// glyphs drawn and its layers made.
func (a *App) warmDialog(c *ui.Context) {
	ui.Overlay(c, func() {
		ui.Column(c.Key("warm-dialog")).Absolute().Left(320).Top(160).Width(480).Padding(24).Radius(12).Background(theme.Elevated).
			Shadow(0, 0, 0, 1, theme.Line).Shadow(0, 24, 64, 0, theme.ShadowDialog).Opacity(0.01).Blur(3).TransformOrigin(0.5, 0.5).
			Scale(0.75, 0.75).IgnoreInput(true).AccessHidden(true).Children(func() {
			ui.Text(c, locales.SubscribeTitle()).FontSize(theme.Subtitle).FontWeight(600).LineHeight(1.4)
			ui.Text(c, locales.SubscribeAddress()).FontSize(theme.Label).TextColor(theme.Text2)
			ui.Row(c).Height(32).Padding(0, 12).Radius(6).Background(theme.Field).InsetShadow(0, 0, 0, 1, theme.Line).Children(func() {
				ui.Text(c, "https://antfu.me").FontSize(theme.Body).TextColor(theme.Muted)
			})
			ui.Text(c, locales.SubscribeFind()+" "+locales.CommonCancel()+" "+locales.SubscribeFollow()).FontSize(theme.Body)
		})
	})
}
