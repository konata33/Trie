// Package theme is Trie's palette and type. docs/DESIGN.md is the source
// of truth: change a value there first, then here. Nothing else in the app
// writes a colour.
package theme

import "github.com/egoist/mygo/ui"

// The app's own colours, named as DESIGN.md names them (window is Window,
// text-2 is Text2, accent-soft is AccentSoft, and so on).
var (
	Window     = ui.Hex("#141414")
	Sidebar    = ui.Hex("#101010")
	Surface    = ui.Hex("#1B1B1B")
	Elevated   = ui.Hex("#242424")
	Field      = ui.Hex("#1F1F1F")
	Line       = ui.RGBA(255, 255, 255, 0.08)
	LineSoft   = ui.RGBA(255, 255, 255, 0.06)
	Text       = ui.Hex("#E6E6E6")
	Text2      = ui.Hex("#A3A3A3")
	Muted      = ui.Hex("#8E8E8E")
	Faint      = ui.Hex("#6F6F6F")
	Accent     = ui.Hex("#E3B061")
	AccentSoft = ui.RGBA(227, 176, 97, 0.2)
	OnAccent   = ui.Hex("#141414")
	Danger     = ui.Hex("#E5867A")
	Shade      = ui.RGBA(6, 6, 6, 0.74)
	Scrim      = ui.RGBA(8, 8, 8, 0.9)
	ScrimLight = ui.RGBA(8, 8, 8, 0.42)
	Hover      = ui.RGBA(255, 255, 255, 0.035)
	Selected   = ui.RGBA(255, 255, 255, 0.06)
	Moon       = ui.Hex("#8E8E8E")
	MoonRing   = ui.Hex("#6F6F6F")
	Star       = ui.Hex("#E3B061")
	Focus      = ui.Hex("#E3B061")
	Mag1       = ui.Hex("#ECECEC")
	Mag2       = ui.Hex("#C2C2C2")
	Mag3       = ui.Hex("#9C9C9C")
	Mag4       = ui.Hex("#808080")
)

// Shadows (DESIGN.md Shadow Vocabulary): the floating layer's 1px edge and
// its fall, the dialog's fall, the tooltip's short one, a screenshot's.
var (
	ShadowEdge   = ui.RGBA(0, 0, 0, 0.2)  // 0 0 0 1px, under floating layers
	ShadowFloat  = ui.RGBA(0, 0, 0, 0.5)  // 0 12px 32px: panels, menus, the clip bar
	ShadowDialog = ui.RGBA(0, 0, 0, 0.6)  // 0 24px 64px: dialogs
	ShadowTip    = ui.RGBA(0, 0, 0, 0.35) // 0 4px 12px: tooltips; 0 6px 18px under the author's face
	ShadowShot   = ui.RGBA(0, 0, 0, 0.8)  // 0 16px 32px -16px: other people's pages
)

// Controls' own: an icon's dots at rest (icon, a step above text-2, since
// dots thinner than a stroke read lighter in the same grey), its fill at
// rest (icon-fill, the accent dimmed), a dot at its brightest under a
// press's ring (icon-lit), a switch's track off and pointed at.
var (
	Icon        = ui.Hex("#B2B2B2")
	IconFill    = ui.RGBA(227, 176, 97, 0.42)
	IconLit     = ui.Hex("#F6F6F6")
	SwitchTrack = ui.RGBA(255, 255, 255, 0.14)
	SwitchHover = ui.RGBA(255, 255, 255, 0.2)
)

// The reader's own: the picture viewer's round buttons pointed at, and the
// swipe's back mark armed, elevated lit by 6% white, on a brighter edge.
var (
	ViewerHover    = ui.RGBA(255, 255, 255, 0.1)
	SwipeArmed     = ui.Hex("#313131")
	SwipeArmedEdge = ui.RGBA(255, 255, 255, 0.16)
)

// The scrollbar's scale (DESIGN.md Scrollbar): its marks
// are this white at the alphas the scale gives them; the needle.
var (
	RulerInk   = ui.Hex("#FFFFFF")
	Needle     = ui.RGBA(236, 236, 236, 0.88)
	NeedleNear = ui.RGBA(242, 242, 242, 0.96)
)

// Reading content (DESIGN.md Reading Content): colours that mark what a
// piece of an article is, not the interface.
var (
	CodeBackground = ui.Hex("#171717")
	// The foot of a link's light, the glow under its words, and the same pointed at or focused.
	LinkGround    = ui.RGBA(227, 176, 97, 0.34)
	LinkGroundLit = ui.RGBA(227, 176, 97, 0.46)
	QuoteRule     = ui.RGBA(255, 255, 255, 0.16)
	TableHeadRule = ui.RGBA(255, 255, 255, 0.16) // under a table's head; its rows part on Line
	// A callout's label by its kind; CAUTION takes Danger.
	CalloutNote      = ui.Hex("#8DB4F5")
	CalloutTip       = ui.Hex("#9CC69B")
	CalloutImportant = ui.Hex("#B7A3EA")
	CalloutWarning   = ui.Hex("#D7B46A")
	// Code, as Vitesse Dark colours it: its text, then its tokens by role (reader-token-1 to -8).
	CodeText        = ui.RGBA(219, 215, 202, 0.93)
	CodeString      = ui.Hex("#C98A7D")
	CodeNumber      = ui.Hex("#4C9A91")
	CodeKeyword     = ui.Hex("#4D9375")
	CodeFunction    = ui.Hex("#80A665")
	CodeKey         = ui.Hex("#B8A965")
	CodeType        = ui.Hex("#5DA994")
	CodePunctuation = ui.Hex("#666666")
	CodeComment     = ui.RGBA(117, 133, 117, 0.87)
	// The lines and words a code's notations set apart: a line's ground, with a rule at its start (the accent for a
	// highlighted one), and a word's.
	CodeLine         = ui.RGBA(227, 176, 97, 0.08)
	CodeInserted     = ui.RGBA(133, 232, 157, 0.08)
	CodeInsertedRule = ui.Hex("#85E89D")
	CodeDeleted      = ui.RGBA(253, 174, 183, 0.08)
	CodeDeletedRule  = ui.Hex("#FDAEB7")
	CodeWord         = ui.RGBA(227, 176, 97, 0.18)
	// ChipLight is the disc behind a site's icon in the reader's byline.
	ChipLight = ui.Hex("#EDEDED")
	// Swatches are a gallery's colour tags drawn as the colour the word
	// names (DESIGN.md 作品页色名).
	Swatches = map[string]ui.Color{
		"black": ui.Hex("#0B0B0B"), "white": ui.Hex("#F4F4F2"), "grey": ui.Hex("#8A8A8A"), "gray": ui.Hex("#8A8A8A"), "red": ui.Hex("#E5484D"),
		"orange": ui.Hex("#F76B15"), "yellow": ui.Hex("#FFC53D"), "green": ui.Hex("#30A46C"), "blue": ui.Hex("#0090FF"), "purple": ui.Hex("#8E4EC6"),
		"pink": ui.Hex("#E93D82"), "brown": ui.Hex("#A07553"), "beige": ui.Hex("#E4D7BF"), "gold": ui.Hex("#C9A227"), "teal": ui.Hex("#12A594"),
		"navy": ui.Hex("#1F2F5C"),
	}
)

// Type. Body text is 13 on 1.5 lines in the system face (SF, with PingFang SC
// for Chinese), digits of one width throughout; Mono is for code.
const (
	FontSize   = 13
	LineHeight = 1.5
	Mono       = "monospace"
	Numbers    = "tnum"
	Sans       = "system-ui, PingFang SC"
)

// The type ladder (DESIGN.md Typography, The Ladder Rule): no size between.
const (
	Label    = 12
	Body     = 13 // FontSize
	Item     = 15
	Subtitle = 20
	Title    = 24
	Headline = 32
)

// Reading (DESIGN.md Reading): the body at 17 on 1.85 lines by default,
// 15 to 23 as the reader sets it.
const (
	ReadingSize       = 17
	ReadingLineHeight = 1.85
	ReadingMin        = 15
	ReadingMax        = 23
)

// English bodies are set in Serif, New York, at 20 where Chinese is at 17
// (its letters are smaller than SF Pro's: at 20 they look as large), on 1.6
// lines. Serif is the name the app registers New York under.
const (
	Serif                    = "New York"
	EnglishReadingSize       = 20
	EnglishReadingLineHeight = 1.6
)

// ReaderColumn is the reading column, 640 (PAGES.md 阅读器); ReaderWide is
// 宽松 (840).
const (
	ReaderColumn = 640
	ReaderWide   = 840
)

// Explore's own: the grey halo every writer glows; a palette dot's ring; a link's
// underline on a picture's band; the subscribe pill pointed at once on.
var (
	HaloGrey       = ui.RGB(118, 118, 124)
	HaloGreyDeep   = ui.RGB(58, 58, 62)
	DotRing        = ui.RGBA(255, 255, 255, 0.28)
	UnderlineHover = ui.RGBA(255, 255, 255, 0.35)
	FollowHover    = ui.RGBA(255, 255, 255, 0.1)
	// Darken is no colour the interface shows: the opaque end of a mask, and
	// the dark a picture's brightness() filter takes it toward.
	Darken = ui.RGB(0, 0, 0)
)

// 收藏 and 摘录's own: the hairline
// between an author's latest keeps on their picture, the ··· disc's glass at
// rest and pointed at, a page of words pointed at, a card's inner hairline
// pointed at, and the ground of a search's hit.
var (
	TitleRule       = ui.RGBA(255, 255, 255, 0.1)
	MoreGround      = ui.RGBA(20, 20, 20, 0.72)
	MoreGroundHover = ui.RGBA(36, 36, 36, 0.92)
	PaperHover      = ui.Hex("#1E1E1E")
	EdgeHover       = ui.RGBA(255, 255, 255, 0.14)
	Hit             = ui.RGBA(255, 255, 255, 0.14)
)

// 首页's own: the 接着读 control's ground pointed at.
var ResumeHover = ui.RGBA(255, 255, 255, 0.09)

// UI is MyGo's dark theme in the app's colours, for MyGo's own widgets.
// Fields with no counterpart among the app's colours keep MyGo's dark values.
func UI() *ui.Theme {
	t := ui.DarkTheme()
	t.Background = Window
	t.Surface = Surface
	t.Border = Line
	t.Text = Text
	t.TextMuted = Muted
	t.Accent = Accent
	t.AccentText = OnAccent
	t.Danger = Danger
	t.Focus = Focus
	t.FocusRingWidth = 2
	t.FocusRingOffset = 2
	t.FontSize = FontSize
	return t
}
