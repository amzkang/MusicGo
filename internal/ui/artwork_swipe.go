package ui

import (
	"math"
	"time"

	"github.com/egoist/mygo/ui"
	"musicgo/internal/ui/anim"
	"musicgo/internal/ui/components"
	"musicgo/internal/ui/icons"
)

const artworkSwipeDuration = 420 * time.Millisecond

// Pointer and animation state belong to the UI thread. A new generation drops
// the native drag record when the song, window size or lyric mode changes.
type artworkSwipeState struct {
	offset     ui.Point
	generation uint64
	songKey    string
	size       float32
	immersive  bool
	started    time.Time
	from       float32
	direction  float32
	outgoing   *ui.Bitmap
}

func (s *artworkSwipeState) reset() {
	s.offset = ui.Point{}
	s.started = time.Time{}
	s.outgoing = nil
	s.generation++
}

func (app *App) renderArtworkCard(c *ui.Context, size float32) {
	s := &app.artworkSwipe
	song := app.Player.CurrentSong()
	key := ""
	var bitmap *ui.Bitmap
	if song != nil {
		key = string(song.Key())
		if song.Artwork != "" {
			bitmap = app.Images.Get(song.Artwork)
		}
	}
	if s.songKey != key || s.size != size || s.immersive != app.ShowFullScreenLyric {
		s.reset()
		s.songKey, s.size, s.immersive = key, size, app.ShowFullScreenLyric
	}
	paint := func(card ui.Element, bm *ui.Bitmap) {
		card.Cache().Radius(components.LyricCoverRadius).Background(app.Theme.CardBg).
			Shadow(0, 20, 60, 0, ui.RGBA(0, 0, 0, 0.16)).Center().Children(func() {
			if bm != nil {
				ui.Image(c, bm).Fill().Radius(components.LyricCoverRadius).Fit(ui.Cover)
			} else {
				ui.Icon(c, icons.Album).Size(80, 80).TextColor(app.Theme.TextSub)
			}
		})
	}
	if !s.started.IsZero() {
		progress := min(float32(c.Now().Sub(s.started))/float32(artworkSwipeDuration), 1)
		if c.Preferences().ReduceMotion || progress >= 1 {
			s.reset()
		} else {
			// The old card leaves in the gesture's direction while its replacement
			// slides out of the stack on the opposite side.
			ease := 1 - float32(math.Pow(float64(1-progress), 3))
			x := s.from + (s.direction*size*1.15-s.from)*ease
			paint(ui.Box(c.Key("artwork_outgoing")).Absolute().Left(0).Top(0).Fill().
				Translate(x, 0).Rotate(max(-8, min(x/size*7, 8))).Scale(1-0.08*ease, 1-0.08*ease).Opacity(1-ease), s.outgoing)
			paint(ui.Box(c.Key("artwork_incoming")).Absolute().Left(0).Top(0).Fill().
				Translate(-s.direction*size*0.85*(1-ease), 0).
				Scale(0.92+0.08*ease, 0.92+0.08*ease).Opacity(ease), bitmap)
			c.AnimationFrame()
			return
		}
	}

	card := ui.Box(c.Key(struct {
		Name       string
		Generation uint64
	}{"artwork_card", s.generation})).Fill()
	if app.ShowFullScreenLyric && song != nil && !c.Now().Before(app.lyricMotionUntil) {
		commit := float32(0)
		_, _, released := card.DragEnded()
		card.Cursor(ui.CursorGrab).Draggable(&s.offset, ui.DraggableOptions{
			Axis:      ui.Horizontal,
			Bounds:    &ui.Rect{X: -size * 0.9, W: size * 1.8},
			Rigid:     true,
			Animation: anim.Normal,
			Snap: func(projected ui.Point) ui.Point {
				// Require intentional horizontal travel even for a fast fling.
				distance := s.offset.X
				if released && (absArtworkOffset(distance) >= size*0.22 ||
					absArtworkOffset(distance) >= 24 && absArtworkOffset(projected.X) >= size*0.4 && distance*projected.X > 0) {
					commit = distance
				}
				return ui.Point{}
			},
		})
		shown := card.DragOffset()
		card.Rotate(max(-8, min(shown.X/size*7, 8)))
		if card.Dragging() {
			card.Cursor(ui.CursorGrabbing)
		}
		if commit != 0 {
			if commit < 0 {
				app.Player.Next()
			} else {
				app.Player.Prev()
			}
			current := app.Player.CurrentSong()
			if current != nil && string(current.Key()) != key {
				s.songKey = string(current.Key())
				s.started, s.from, s.outgoing = c.Now(), commit, bitmap
				s.direction = 1
				if commit < 0 {
					s.direction = -1
				}
				c.AnimationFrame()
			}
		}
	}
	paint(card, bitmap)
}

func absArtworkOffset(x float32) float32 {
	return max(x, -x)
}
