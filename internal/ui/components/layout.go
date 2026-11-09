package components

import (
	"strings"

	"github.com/egoist/mygo/ui"
	"musicgo/internal/ui/icons"
	"musicgo/internal/ui/theme"
)

const SidebarWidth float32 = 224

// PageWidth is the space beside the sidebar, in the current zoom's DIPs.
func PageWidth(c *ui.Context) float32 {
	w, _ := c.Size()
	return max(0, w-SidebarWidth)
}

func PagePadding(c *ui.Context) float32 {
	if PageWidth(c) < 836 {
		return 24
	}
	return 32
}

func PageContentWidth(c *ui.Context) float32 {
	return max(0, PageWidth(c)-2*PagePadding(c))
}

func PageTitle(c *ui.Context, th *theme.Theme, title string) ui.Element {
	return ui.Text(c, title).FontSize(24).Bold().TextColor(th.TextMain).MinWidth(0).MinHeight(32).SingleLine().Ellipsis("…")
}

func Button(c *ui.Context, label string) ui.Element {
	return ui.Button(c, label).Height(36).Shrink(0).Radius(10).Padding(0, 16).Cursor(ui.CursorPointer)
}

func PrimaryButton(c *ui.Context, th *theme.Theme, label string) ui.Element {
	native := c.Theme()
	c.SetTheme(th.ButtonTheme(native))
	button := ui.PrimaryButton(c, label).Height(36).Shrink(0).Radius(10).Padding(0, 16).Cursor(ui.CursorPointer)
	c.SetTheme(native)
	return button
}

func IconButton(c *ui.Context, th *theme.Theme, label string, icon *ui.SVG, color ui.Color) ui.Element {
	button := Tooltip(c, ui.ButtonBase(c).Label(label), label).Size(32, 32).Shrink(0).Radius(8).Center().Cursor(ui.CursorPointer)
	HoverLights(button, th)
	button.Children(func() { ui.Icon(c, icon).Size(15, 15).TextColor(color) })
	return button
}

func BackButton(c *ui.Context, th *theme.Theme, label string) ui.Element {
	return IconButton(c, th, label, icons.ChevronLeft, th.TextMain)
}

func HeroButton(c *ui.Context, palette *theme.Theme, label string, primary bool) ui.Element {
	button := ui.ButtonBase(c).Label(label).Height(40).Padding(0, 18).Radius(20).Center().Cursor(ui.CursorPointer).FocusRing(false)
	color := palette.TextMain
	if primary {
		buttonTheme := palette.ButtonTheme(palette.ToMyGoTheme())
		color = buttonTheme.AccentText
		button.Background(buttonTheme.Accent)
		if button.Hovered() || button.Pressed() {
			button.Background(buttonTheme.AccentHover)
		}
	} else {
		button.Border(1, palette.Border)
		HoverLights(button, palette)
	}
	focused := button.FocusVisible()
	button.DrawOver(func(p *ui.Painter, r ui.Rect) {
		if focused {
			p.Stroke(ui.Rect{X: r.X - 3, Y: r.Y - 3, W: r.W + 6, H: r.H + 6}, palette.TextMain, 23, 1.5)
		}
	})
	button.Children(func() {
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			if primary && strings.HasPrefix(label, "播放") {
				ui.Icon(c, icons.Play).Size(14, 14).TextColor(color)
			}
			ui.Text(c, label).FontSize(13).Bold().TextColor(color)
		})
	})
	return button
}

func EmptyState(c *ui.Context, th *theme.Theme, icon *ui.SVG, message string) {
	ui.Column(c).FillWidth().MinHeight(144).Center().Gap(12).Children(func() {
		ui.Icon(c, icon).Size(36, 36).TextColor(th.TextMuted)
		ui.Text(c, message).FontSize(14).TextColor(th.TextSub).TextAlign(ui.Center)
	})
}
