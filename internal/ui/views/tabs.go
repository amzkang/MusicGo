package views

import (
	"github.com/egoist/mygo/ui"
	"musicgo/internal/ui/anim"
)

// choiceButton marks the current option without making it look disabled.
func choiceButton(c *ui.Context, label string, selected bool) ui.Element {
	t := c.Theme()
	button := ui.Button(c, label).Cursor(ui.CursorPointer).TextColor(t.Text)
	if selected {
		button.TextColor(t.Accent).Border(1, t.Accent)
	}
	return button
}

// renderTabs builds the theme's tabs, with the pointer cursor on each tab.
//
// ui.Tabs draws the tabs itself, and the runtime gives a clickable element
// the default cursor unless it sets one: a cursor on the row that holds the
// tabs stops at each tab, which takes the clicks. So the tabs are built here
// from TabsBase, with the theme's look, and each one sets its cursor.
func renderTabs(c *ui.Context, selected *int, labels ...string) ui.Element {
	t := c.Theme()
	tabs := ui.TabsBase(c, selected, len(labels))
	tabs.List.Wrap().MinHeight(40).Gap(t.Space(1))
	tabs.List.Children(func() {
		for i, label := range labels {
			tab := tabs.Tab(i).
				Padding(t.Space(2), t.Space(3)).
				FocusRing(false).
				Cursor(ui.CursorPointer)
			on := i == *selected
			col := t.Text
			if on {
				col = t.Accent
			}
			// The label comes up to the text color, and the line under the
			// chosen tab fades in there and out from the tab chosen before,
			// instead of jumping.
			tab.TextColor(tab.AnimateColor("tab_text", col, anim.Quick))
			if on {
				tab.FontWeight(600)
			}
			underline := float32(0)
			if on {
				underline = 1
			}
			underline = tab.AnimateTo("tab_underline", underline, anim.Quick)
			// The painter draws in frames that only paint, which the tab was
			// not built in: what it draws there is read now.
			focusVisible := tab.FocusVisible()
			tab.DrawOver(func(p *ui.Painter, r ui.Rect) {
				if underline > 0.01 {
					in, h := t.Space(1.5), t.Space(0.5)
					p.Fill(ui.Rect{X: r.X + in, Y: r.Y + r.H - h, W: r.W - 2*in, H: h}, t.Accent.Alpha(underline), h/2)
				}
				if focusVisible {
					p.FocusRing(r, [4]float32{t.Radius, t.Radius, t.Radius, t.Radius})
				}
			})
			tab.Children(func() { ui.Text(c, label).SingleLine() })
		}
	})
	return tabs.List
}
