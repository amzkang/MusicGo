package components

import (
	"github.com/egoist/mygo/ui"
	"time"
)

const lyricBackdropFade = 700 * time.Millisecond

// LyricBackdropState holds at most two already prepared bitmaps on the UI thread.
// Loading the next song never clears the last successfully displayed background.
type LyricBackdropState struct {
	current, previous *ui.Bitmap
	started           time.Time
}

func (s *LyricBackdropState) update(now time.Time, key string, next *ui.Bitmap, reduce bool) {
	if next == nil && key != "" {
		if reduce {
			s.previous = nil
		}
		return
	}
	if next != s.current {
		s.previous, s.current, s.started = s.current, next, now
	}
	if reduce {
		s.previous = nil
	}
}
func (s *LyricBackdropState) draw(p *ui.Painter, r ui.Rect) {
	progress := float32(1)
	if s.previous != nil {
		progress = min(1, max(0, float32(p.Now().Sub(s.started))/float32(lyricBackdropFade)))
		progress = 1 - (1-progress)*(1-progress)*(1-progress)
	}
	p.Fill(r, ui.Hex("#ffffff"), 0)
	area := ui.Rect{X: r.X - 144, Y: r.Y - 144, W: r.W + 288, H: r.H + 288}
	if s.previous != nil {
		alpha := float32(1)
		if s.current == nil {
			alpha = 1 - progress
		}
		paintBackdropImage(p, s.previous, area, alpha)
	}
	if s.current != nil {
		paintBackdropImage(p, s.current, area, progress)
	}
	// Match the reference's 35% -> 24% artwork brightness across the page.
	// A light application theme must not wash out the white lyric text.
	p.FillGradient(r, ui.LinearGradient{Angle: 180, Stops: []ui.GradientStop{
		{Color: ui.RGBA(0, 0, 0, 0.65), At: 0},
		{Color: ui.RGBA(0, 0, 0, 0.76), At: 1},
	}}, 0)
	if progress < 1 {
		p.AnimationFrameRate(30)
	} else {
		s.previous = nil
	}
}
func paintBackdropImage(p *ui.Painter, bm *ui.Bitmap, dst ui.Rect, alpha float32) {
	if bm == nil || alpha <= 0 {
		return
	}
	bw, bh := bm.Size()
	src := ui.Rect{W: float32(bw), H: float32(bh)}
	if src.W*dst.H > src.H*dst.W {
		width := src.H * dst.W / dst.H
		src.X = (src.W - width) / 2
		src.W = width
	} else {
		height := src.W * dst.H / dst.W
		src.Y = (src.H - height) / 2
		src.H = height
	}
	p.ImageRegion(bm, src, dst, ui.ImageOptions{Tint: ui.RGB(255, 255, 255).Alpha(alpha)})
}
