// Package anim holds how the interface moves: the curves and timings of
// each kind of change, in one place, so that pages, panels, and the small
// things inside them keep one feel.
package anim

import (
	"time"

	"github.com/egoist/mygo/ui"
)

// The curves of the interface. Coming in, a thing starts quickly and
// settles gently; going out, it leaves a little sooner, so that it makes
// room for what arrives.
var (
	inCurve  = ui.CubicBezier(0.22, 1, 0.36, 1)
	outCurve = ui.CubicBezier(0.4, 0, 1, 1)
)

// The timings of the interface.
var (
	// Quick is a small change: an icon that swaps, a tab that lights up,
	// a color under the pointer.
	Quick = ui.Animation{Duration: 140 * time.Millisecond, Ease: inCurve}
	// Normal is something appearing or going: a page, a panel.
	Normal = ui.Animation{Duration: 240 * time.Millisecond, Ease: inCurve}
	// Immersive is the selected B prototype: the entire lyric page slides
	// over the library in 800ms, with the artwork travelling independently.
	Immersive = ui.Animation{Duration: 800 * time.Millisecond, Ease: ui.CubicBezier(0.22, 0.7, 0.2, 1)}
	// Leave is how what goes out does it.
	Leave = ui.Animation{Duration: 180 * time.Millisecond, Ease: outCurve}
	// Press is how a control leans in while the pointer holds it, and how
	// it springs back the moment it is let go: quicker than Hover, so the
	// click feels immediate.
	Press = ui.Animation{Duration: 90 * time.Millisecond, Ease: outCurve}
	// Hover is how a cover or a button grows a little under the pointer.
	Hover = ui.Animation{Spring: ui.SpringOptions{Duration: 260 * time.Millisecond, Bounce: 0.15}}
	// Lyric uses the reference player's gentle 600ms spring.
	Lyric = ui.Animation{Spring: ui.SpringOptions{Duration: 600 * time.Millisecond, Bounce: 0.1}}
	// Stop native scrolling within half a display-independent pixel.
	LyricScroll = ui.Animation{Spring: ui.SpringOptions{Duration: 600 * time.Millisecond, Precision: 0.5}}
)

// pageShift is how far a page comes in from, and goes out to, as another
// replaces it.
const pageShift = 26

// Page is how the content of a page goes as another page replaces it:
// the new one comes in from the side the navigation went, the old one
// leaves the other way, and both fade. dir is 1 when the new page sits
// below the old one in the sidebar, -1 when it sits above, and 0 to only
// fade.
func Page(dir int) ui.ElementTransition {
	var in, out float32
	switch {
	case dir > 0:
		in, out = pageShift, -pageShift*0.7
	case dir < 0:
		in, out = -pageShift, pageShift*0.7
	}
	return ui.ElementTransition{
		Animation: &Normal,
		Enter:     &ui.Motion{X: in, Opacity: 0},
		Exit:      &ui.Motion{X: out, Opacity: 0},
	}
}

// ArtworkKey names the artwork of a song, so that the small cover in the
// player bar and the large one of the lyric page are one and the same
// picture, moving from one place to the other.
type ArtworkKey struct {
	Song string
}

// Artwork is how a song's artwork travels between the player bar and the
// lyric page. Both covers of it share the key of the song they show.
func Artwork(song string) ui.ElementTransition {
	return ui.ElementTransition{
		Animation: &Immersive,
		Match:     ArtworkKey{Song: song},
	}
}

// Tab is how the content of a tab comes in as another tab is chosen.
func Tab() ui.ElementTransition {
	return ui.ElementTransition{
		Animation: &Leave,
		Enter:     &ui.Motion{Y: 8, Opacity: 0},
		Exit:      &ui.Motion{Y: -6, Opacity: 0},
	}
}

// LyricLine is how a lyric line goes as the lyrics scroll under the song
// that plays: the window shifts by a line, and every line follows it,
// while the one that comes in fades and the one that goes out fades.
func LyricLine() ui.ElementTransition {
	return ui.ElementTransition{
		Position:  true,
		Animation: &Lyric,
		Enter:     &ui.Motion{Opacity: 0},
		Exit:      &ui.Motion{Opacity: 0},
	}
}
