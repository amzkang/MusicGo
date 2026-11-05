package components

import (
	"fmt"
	"time"

	"github.com/egoist/mygo/ui"
	"musicgo/internal/lyric"
	"musicgo/internal/player"
	"musicgo/internal/ui/anim"
)

// lyricScrollState belongs to one lyric document and one native scroll view.
// Browsing suspends following until playback resumes or the listener seeks.
type lyricScrollState struct {
	scroll      ui.ScrollState
	index       int
	revision    uint64
	viewport    ui.Rect
	browsing    bool
	resumeAt    time.Time
	follow      bool
	initialized bool
}

func renderScrollableLyrics(c *ui.Context, eng *player.Engine, parsed *lyric.ParsedLyric, playback *PlayerBarState, pos float64) {
	const fontSize = 30
	pane := ui.Box(c.Key(parsed)).Label("同步歌词").FillWidth().Grow(1).MinHeight(0).
		Clip()
	white := ui.Hex("#ffffff")
	state := ui.Local(pane, "scroll", func() lyricScrollState { return lyricScrollState{index: -1} })
	if parsed == nil || len(parsed.Lines) == 0 {
		pane.Children(func() {
			ui.Column(c).Fill().Center().Children(func() {
				ui.Text(c, "纯音乐，请欣赏").FontSize(24).FontWeight(700).TextColor(white.Alpha(0.4))
			})
		})
		return
	}
	index := parsed.FindCurrentIndex(pos)
	var revision uint64
	if playback != nil {
		revision = playback.SeekRevision
	}
	if revision != state.revision {
		state.browsing, state.follow = false, true
	}
	if state.browsing && eng.IsPlaying() {
		if c.Now().Before(state.resumeAt) {
			c.After(state.resumeAt.Sub(c.Now()))
		} else {
			state.browsing, state.follow = false, true
		}
	}
	state.revision = revision
	pane.Children(func() {
		scroll := ui.Scroll(c.Key("lines")).Fill().TrackScroll(&state.scroll).Padding(0, 8, 0, 0)
		box := scroll.LayoutBounds()
		if box.H <= 0 {
			c.AnimationFrame()
		}
		// Native scrolling still handles wheel, touchpad and scrollbar input.
		// Merely looking at the lyrics never changes the audio cursor.
		scroll.HandleInput(func(ev ui.InputEvent) bool {
			if ev.Kind == ui.InputScroll || ev.Kind == ui.InputPointerDown {
				state.browsing = true
				state.resumeAt = ev.Time.Add(6 * time.Second)
			}
			return false
		})
		var active ui.Element
		sought := false
		scroll.Children(func() {
			ui.Column(c).FillWidth().Gap(6).Children(func() {
				// Keep the active line at 30% of the viewport, including both ends.
				ui.Box(c).Height(max(0, box.H*0.3)).Shrink(0)
				for i, line := range parsed.Lines {
					row := ui.ButtonBase(c.Key(i)).Label(fmt.Sprintf("歌词跳转_%d", i)).
						Column().FillWidth().Shrink(0).Padding(10, 12).Gap(6).
						AlignItems(ui.Start).Radius(12).FocusRing(false).Cursor(ui.CursorPointer)
					on := i == index
					alpha, scale := float32(0.28), float32(0.94)
					if on {
						alpha, scale = 1, 1
					} else if row.Hovered() || row.FocusVisible() {
						alpha = 0.55
					}
					if row.Hovered() || row.FocusVisible() {
						row.Background(white.Alpha(0.06))
					}
					row.TransformOrigin(0, 0.5).AnimatePose(ui.Pose{Scale: scale}, anim.Lyric)
					row.Children(func() {
						ui.Column(c).FillWidth().AlignItems(ui.Start).Gap(6).
							AnimatePose(ui.Pose{Fade: 1 - alpha}, ui.Animation{Duration: 380 * time.Millisecond}).Children(func() {
							var text ui.Element
							if on && len(line.Words) > 0 {
								spans := make([]ui.Span, len(line.Words))
								for j, word := range line.Words {
									spans[j] = ui.Span{Text: word.Text, Color: white.Alpha(0.38 + 0.62*word.Progress(pos))}
								}
								text = ui.RichText(c, spans...).Label(line.Text)
							} else {
								text = ui.Text(c, line.Text)
							}
							text.FillWidth().TextAlign(ui.Start).FontSize(fontSize).FontWeight(800).
								LineHeight(1.25).LetterSpacing(-0.4).TextColor(white)
							if line.Translation != "" {
								ui.Text(c, line.Translation).FillWidth().TextAlign(ui.Start).
									FontSize(17).FontWeight(600).TextColor(white.Alpha(0.7))
							}
						})
					})
					if row.Clicked() {
						sought = true
						if playback != nil {
							playback.SeekPending, playback.SeekDragging = false, false
							playback.SeekRevision++
						}
						eng.Seek(float64(line.TimeMs) / 1000)
						state.browsing, state.follow = false, true
						c.AnimationFrame()
					}
					if on {
						active = row
					}
				}
				ui.Box(c).Height(max(0, box.H*0.7)).Shrink(0)
			})
		})
		if box.W != state.viewport.W || box.H != state.viewport.H {
			// End padding uses the viewport height. Wait for its layout before
			// measuring a line, including on the first frame after opening.
			state.viewport, state.follow = box, true
			c.AnimationFrame()
			return
		}
		if !sought && !state.browsing && (index != state.index || state.follow) && active.Valid() && box.H > 0 {
			line := active.LayoutBounds()
			if line.H > 0 {
				target := min(state.scroll.MaxY, max(0, line.Y-box.Y-box.H*0.3+state.scroll.Y))
				if !state.initialized || c.Preferences().ReduceMotion || playback != nil && playback.SeekPending {
					state.scroll.Y = target
				} else {
					state.scroll.ScrollTo(0, target, anim.LyricScroll)
				}
				state.index, state.follow, state.initialized = index, false, true
				state.viewport = box
				c.AnimationFrame()
			} else {
				c.AnimationFrame()
			}
		}
	})
}

// LyricCoverScale keeps the paused artwork at 88% of its playing size.
// Loading retains the full size so buffering does not pulse the cover.
func LyricCoverScale(eng *player.Engine) float32 {
	if eng.State() == player.StatePaused {
		return 0.88
	}
	return 1
}
