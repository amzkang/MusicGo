package components

import (
	"time"

	"github.com/egoist/mygo/ui"
	"musicgo/internal/ui/anim"
	"musicgo/internal/ui/theme"
)

const tooltipDelay = 600 * time.Millisecond

type tooltipState struct {
	blocked     bool
	resumeAfter time.Time
	showAfter   time.Time
}

func windowTooltips(c *ui.Context) *tooltipState {
	return ui.Local(c.Root(), "musicgo-tooltips", func() tooltipState { return tooltipState{} })
}

// TrackTooltips dismisses list tips on scrolling and pointer movement. A tip
// waits for the pointer to rest; scrolling also requires a new pointer move.
// The root observes input without consuming it or changing native scrolling.
func TrackTooltips(c *ui.Context) {
	state := windowTooltips(c)
	if wait := state.showAfter.Sub(c.Now()); !state.blocked && wait > 0 {
		c.After(wait)
	}
	c.Root().HandleInput(func(ev ui.InputEvent) bool {
		switch ev.Kind {
		case ui.InputScroll:
			if ev.DX == 0 && ev.DY == 0 {
				break
			}
			state.resumeAfter = ev.Time.Add(tooltipDelay)
			if !state.blocked {
				state.blocked = true
				c.Invalidate()
			}
		case ui.InputPointerMove:
			if state.blocked && ev.Time.Before(state.resumeAfter) {
				break
			}
			// Hide once, then let the pending frame check the latest deadline.
			// Continuous movement need not force a full rebuild for every event.
			redraw := state.blocked || !ev.Time.Before(state.showAfter)
			state.blocked = false
			state.showAfter = ev.Time.Add(tooltipDelay)
			if redraw {
				c.Invalidate()
			}
		}
		return false
	})
}

// Tooltip keeps MyGo's targeting, dismissal and keyboard behavior, but gates
// its warm handoff on pointer rest. Register even while hidden so the native
// hover delay runs concurrently instead of adding a second wait afterwards.
func Tooltip(c *ui.Context, anchor ui.Element, text string) ui.Element {
	anchor.Description(text)
	if text == "" {
		return anchor
	}
	state := windowTooltips(c)
	ready := anchor.FocusVisible() || !state.blocked && !c.Now().Before(state.showAfter)
	ui.TooltipBase(c, anchor, func(tip ui.Element) {
		if !ready {
			tip.Hidden(true)
			return
		}
		t := c.Theme()
		fill, ink := t.Inverse, t.InverseText
		if fill == ui.Transparent {
			fill = t.Text
		}
		if ink == ui.Transparent {
			ink = t.Background
		}
		tip.MaxWidth(t.Space(80)).Padding(t.Space(1.25), t.Space(2)).Radius(t.Space(1.25)).
			Background(fill).TextColor(ink).FontSize(t.FontSize-1).
			Shadow(0, 2, 8, 0, ui.RGBA(0, 0, 0, 0.2)).
			AttachTo(anchor, ui.AnchorTop, ui.AnchorBottom).Margin(0, 0, t.Space(1.5), 0)
		tip.Children(func() { ui.Text(c, text) })
	})
	return anchor
}

// HoverLights gives a small button its pointer feedback: it lights up
// under the pointer and holds a deeper shade while the pointer presses
// it, so a held click reads as held.
func HoverLights(btn ui.Element, th *theme.Theme) {
	switch {
	case btn.Pressed():
		btn.Background(th.ActiveBg)
	case btn.Hovered():
		btn.Background(th.HoverBg)
	}
}

// Scales grows a control under the pointer and leans it in while it is
// held: hover grows it to hoverScale, a held click settles to pressScale.
func Scales(btn ui.Element, hoverScale, pressScale float32) {
	scale := float32(1)
	switch {
	case btn.Pressed():
		scale = pressScale
	case btn.Hovered():
		scale = hoverScale
	}
	btn.AnimatePose(ui.Pose{Scale: scale}, anim.Press)
}
