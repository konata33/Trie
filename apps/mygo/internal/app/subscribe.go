package app

import (
	"fmt"
	"strings"

	"github.com/egoist/mygo/ui"

	"trie/internal/library"
	"trie/internal/motion"
	"trie/internal/subscription"
	"trie/internal/theme"
)

// subscribeForm is 订阅一个博客 (SubscriptionForm.tsx): an address, read
// (识别) into a preview, then saved (确认订阅).
type subscribeForm struct {
	url     string
	preview *subscription.Preview
	working string // "preview" or "save"
	err     string
	gen     int
}

// blocking reports a form that is saving: Esc and the shade wait.
func (f *subscribeForm) blocking() bool { return f.working == "save" }

func (a *App) subscribeView(c *ui.Context, f *subscribeForm) {
	svc := a.opt.Subscriptions
	var existing *library.Source
	if f.preview != nil && f.preview.ExistingSourceID != nil {
		existing = a.source(*f.preview.ExistingSourceID)
	}
	discover := func() {
		if f.working != "" || strings.TrimSpace(f.url) == "" || svc == nil {
			return
		}
		f.gen++
		gen, url := f.gen, f.url
		f.working, f.preview, f.err = "preview", nil, ""
		a.said = append(a.said, "正在读取网站提供的订阅信息…")
		a.do(func() func() {
			p, err := svc.Preview(bg, url)
			return func() {
				if gen != f.gen {
					return
				}
				f.working = ""
				if err != nil {
					f.err = said(err, "暂时无法读取订阅地址，请重试")
					return
				}
				f.preview = &p
			}
		})
	}
	confirm := func() {
		if f.preview == nil || f.working != "" || svc == nil {
			return
		}
		f.working, f.err = "save", ""
		token := f.preview.Token
		a.do(func() func() {
			res, err := svc.Add(token, true)
			return func() {
				if err != nil {
					f.working, f.err = "", said(err, "订阅未保存，请重试")
					return
				}
				a.subscriptionAdded(c, res)
			}
		})
	}
	ui.Column(c.Key("form")).Margin(16, 0, 0, 0).Children(func() {
		ui.Text(c, "博客或订阅地址").FontSize(theme.Label).LineHeight(16.0/12).TextColor(theme.Text2).Margin(0, 0, 8, 0)
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			before := f.url
			in := ui.TextInputBase(c.Key("url"), &f.url).Label("博客或订阅地址").Placeholder("https://antfu.me").Grow(1).Height(32).Padding(0, 12).
				Radius(6).Background(theme.Field).FontSize(theme.Body).TextColor(theme.Text).PlaceholderColor(theme.Muted).CaretColor(theme.Accent).
				AutoFocus().Disabled(f.working == "save")
			edge := theme.Line
			if f.err != "" {
				edge = theme.Danger
			}
			in.InsetShadow(0, 0, 0, 1, edge)
			if in.Focused() {
				in.FocusRing(true).FocusRingOffset(-2)
			}
			if f.url != before {
				f.gen++
				f.preview, f.err, f.working = nil, "", ""
			}
			if in.Submitted() {
				discover()
			}
			if a.button(c, "识别", btnOpt{loading: f.working == "preview", disabled: f.working == "save" || strings.TrimSpace(f.url) == ""}).Clicked() {
				discover()
			}
		})
		// The error line opens its own height (180 ms), so the buttons move.
		k := float32(0)
		if f.err != "" {
			k = 1
		}
		slot := ui.Column(c.Key("error")).ClipY()
		var open float32
		if a.kbd || a.still(c) {
			open = slot.Jump("open", k)
		} else {
			open = slot.AnimateTo("open", k, ui.Animation{Duration: motion.ErrorOpen, Ease: motion.Ease})
		}
		slot.Height(24 * open).Children(func() {
			if f.err != "" {
				ui.Text(c, f.err).Margin(8, 0, 0, 0).FontSize(theme.Label).LineHeight(16.0 / 12).TextColor(theme.Danger).Role(ui.RoleAlert)
			}
		})
		if p := f.preview; p != nil {
			ui.Scroll(c.Key("preview")).Margin(16, 0, 0, 0).MaxHeight(280).Radius(10).Background(theme.Surface).Scrollbars(false).
				InsetShadow(0, 0, 0, 1, theme.LineSoft).Children(func() {
				ui.Column(c).Padding(16).Children(func() {
					ui.Text(c, p.Title).FontSize(theme.Item).FontWeight(600)
					ui.Column(c).Margin(12, 0, 0, 0).FontSize(theme.Label).LineHeight(1.8).TextColor(theme.Text2).Children(func() {
						ui.Text(c, p.HomeURL)
						ui.Text(c, p.FeedURL).Margin(4, 0, 10, 0)
						count := "订阅源暂时没有文章，添加后可刷新"
						if p.ArticleCount > 0 {
							count = fmt.Sprintf("本次提供 %d 篇文章", p.ArticleCount)
						}
						ui.Text(c, count).FontWeight(600)
						if existing != nil {
							ui.Text(c, map[bool]string{true: "已订阅，将更新这个来源，不会重复添加", false: "已保存过这个来源，可以重新订阅"}[existing.Subscribed])
						}
					})
					for _, t := range p.SampleTitles {
						ui.Row(c.Key(t)).Padding(0, 0, 0, 18).FontSize(theme.Label).LineHeight(1.8).TextColor(theme.Text2).Children(func() {
							ui.Text(c, "•").Absolute().Left(4)
							ui.Text(c, t)
						})
					}
					for _, w := range p.Warnings {
						ui.Text(c.Key(w), w).Margin(0, 0, 16, 0).FontSize(theme.Label).LineHeight(1.75).TextColor(theme.Text2)
					}
				})
			})
		}
		ui.Row(c).Justify(ui.End).Gap(8).Margin(24, 0, 0, 0).Children(func() {
			if a.button(c, "取消", btnOpt{inset: true, disabled: f.working == "save"}).Clicked() {
				a.layers.dialog = nil
			}
			if f.preview != nil {
				label := "确认订阅"
				if existing != nil && existing.Subscribed {
					label = "更新订阅"
				}
				if a.button(c, label, btnOpt{kind: primary, inset: true, loading: f.working == "save", disabled: f.working == "preview"}).Clicked() {
					confirm()
				}
			}
		})
	})
}

// said is the service's own sentence, or the fallback for a bare runtime
// error (SubscriptionForm.tsx said).
func said(err error, fallback string) string {
	text := err.Error()
	if strings.Contains(text, "Invalid URL") {
		return "请填写有效的 RSS/Atom 或博客首页地址"
	}
	if text == "" {
		return fallback
	}
	return text
}

// subscriptionAdded takes the new library and goes to the author's page,
// with a notice carrying their face.
func (a *App) subscriptionAdded(c *ui.Context, res library.SubscriptionResult) {
	a.accept(res.Library)
	a.layers.dialog = nil
	a.said = append(a.said, fmt.Sprintf("订阅已保存 · 新收录 %d 篇 · 更新 %d 篇", res.ImportedCount, res.UpdatedCount))
	src := a.source(res.SourceID)
	name := "博客"
	if src != nil {
		name = src.DisplayName
	}
	a.notify("已订阅 "+name, a.iconOf(src), nil)
	if a.writer(res.SourceID) != nil {
		a.page, a.from, a.came, a.left = page{"author", res.SourceID}, []page{{kind: "home"}}, nil, nil
	}
}
