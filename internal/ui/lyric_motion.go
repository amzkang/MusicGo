package ui

import (
	"github.com/egoist/mygo/ui"
	"musicgo/internal/ui/anim"
	"musicgo/internal/ui/components"
	"musicgo/internal/ui/icons"
)

// One stable artwork travels above both pages. Its layout and image size
// stay fixed: the compositor moves/scales it instead of relaying out a
// matched-geometry cover at every intermediate size.
func (app *App) renderTravellingArtwork(c *ui.Context, smallOnly bool) {
	w, h := c.Size()
	dest := components.LyricCoverBounds(w, h)
	scale := components.LyricCoverScale(app.Player)
	pose := ui.Pose{X: dest.X + dest.W*(1-scale)/2, Y: dest.Y + dest.H*(1-scale)/2, Scale: scale}
	label := "歌词封面"
	if smallOnly || !app.ShowFullScreenLyric {
		// 84-DIP player bar: 10-DIP seek track, then a centered
		// 44-DIP artwork inside the remaining 74 DIPs.
		pose = ui.Pose{X: 20, Y: h - 59, Scale: 44 / dest.W}
		label = "展开播放详情"
	}
	cover := ui.ButtonBase(c.Key("travelling_artwork")).Label(label).
		Absolute().Left(0).Top(0).Size(dest.W, dest.H).TransformOrigin(0, 0).
		AnimatePose(pose, anim.Immersive)
	if !app.ShowFullScreenLyric {
		cover.Cursor(ui.CursorPointer).OnClick(func() { app.ShowFullScreenLyric = true })
	}
	expandHint := !app.ShowFullScreenLyric && (cover.Hovered() || cover.FocusVisible())
	cover.Children(func() {
		app.renderArtworkCard(c, dest.W)
		if expandHint {
			// This cover is laid out at lyric size and scaled down to 44 DIPs.
			// Keep the hint above the cached artwork and its arrow 26 DIPs wide.
			arrowSize := dest.W * 26 / 44
			ui.Box(c.Key("expand_hint")).Absolute().Top(0).Left(0).Fill().Radius(components.LyricCoverRadius).
				Background(ui.RGBA(80, 80, 80, 0.8)).Center().PassThrough().Children(func() {
				ui.Icon(c, icons.ChevronDown).Size(arrowSize, arrowSize).Rotate(180).TextColor(ui.Hex("#ffffff"))
			})
		}
	})
}
