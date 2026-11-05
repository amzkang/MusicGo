package components

import (
	"github.com/egoist/mygo/ui"
	"musicgo/internal/ui/theme"
)

// DrawArtworkBackdrop keeps a cover tint behind ordinary, readable theme text.
func DrawArtworkBackdrop(p *ui.Painter, r ui.Rect, th *theme.Theme, bitmap *ui.Bitmap, radius float32) {
	draw := func() {
		if bitmap != nil {
			p.Image(bitmap, ui.Rect{X: r.X - 144, Y: r.Y - 144, W: r.W + 288, H: r.H + 288}, ui.Cover)
		}
		p.Fill(r, th.Background.Alpha(0.90), 0)
	}
	if radius > 0 {
		p.Clip(r, radius, draw)
	} else {
		draw()
	}
}

// Solid surfaces preserve the monochrome palette independently of desktop blur.
func SidebarSurface(th *theme.Theme) ui.Color { return opaque(th.SidebarBg) }
func ContentSurface(th *theme.Theme) ui.Color { return opaque(th.PanelBg) }
func HeaderSurface(th *theme.Theme) ui.Color  { return opaque(th.HeaderBg) }
func PlaybarSurface(th *theme.Theme) ui.Color { return opaque(th.PlayBarBg) }
func opaque(color ui.Color) ui.Color          { return ui.RGBA(color.R, color.G, color.B, 1) }
