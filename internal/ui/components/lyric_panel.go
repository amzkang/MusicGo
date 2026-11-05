package components

import (
	"math"
	"time"

	"github.com/egoist/mygo/ui"
	"musicgo/internal/lyric"
	"musicgo/internal/player"
	"musicgo/internal/plugin"
	"musicgo/internal/ui/anim"
	"musicgo/internal/ui/icons"
	"musicgo/internal/ui/theme"
)

// LyricCoverRadius rounds both the artwork and its card/shadow.
const LyricCoverRadius float32 = 20

const (
	lyricCoverInfoGap    float32 = 20
	lyricCoverInfoHeight float32 = 96
)

// LargeLyricState caches text fitting and tracks seek presentation on the UI thread.
type LargeLyricState struct {
	parsed       *lyric.ParsedLyric
	index        int
	revision     uint64
	skipEntrance bool
	text, font   string
	size         [2]float32
	fontSize     float32
}

// RenderLargeLyric uses the same audio cursor for the sentence, its entrance,
// and source-provided word timing. Pausing freezes all three.
func RenderLargeLyric(c *ui.Context, th *theme.Theme, eng *player.Engine, parsed *lyric.ParsedLyric, playback *PlayerBarState, state *LargeLyricState) {
	th = th.ForLyrics()
	pos := eng.Position()
	if playback != nil {
		pos = playback.Position(eng)
	}
	index := -1
	line := lyric.Line{Text: "纯音乐，请欣赏"}
	if parsed != nil && len(parsed.Lines) > 0 {
		index = parsed.FindCurrentIndex(pos)
		line = parsed.Lines[index]
	}
	if state.parsed != parsed || state.index != index {
		state.parsed, state.index, state.skipEntrance = parsed, index, false
	}
	if playback != nil {
		if playback.SeekPending || playback.SeekRevision != state.revision {
			state.skipEntrance = true
		}
		state.revision = playback.SeekRevision
	}
	w, h := c.Size()
	font := c.Theme().Font
	width := w * 0.84
	if state.text != line.Text || state.font != font || state.size != [2]float32{w, h} || state.fontSize == 0 {
		size := max(float32(14), min(w*0.105, (h-168)*0.26, float32(150)))
		unwrapped, _ := c.MeasureText(0, ui.Span{Text: line.Text, Font: font, Size: size, Weight: 700})
		if unwrapped > width && width/unwrapped >= 0.55 {
			size *= width / unwrapped * 0.97
		}
		low, high := float32(14), size
		for range 7 {
			mid := (low + high) / 2
			_, height := c.MeasureText(width, ui.Span{Text: line.Text, Font: font, Size: mid, Weight: 700})
			if height <= (h-168)*0.48 {
				low = mid
			} else {
				high = mid
			}
		}
		state.text, state.font, state.size, state.fontSize = line.Text, font, [2]float32{w, h}, low
	}
	progress := float32(1)
	if index >= 0 && len(line.Words) == 0 && !state.skipEntrance && !c.Preferences().ReduceMotion {
		elapsed := max(float64(0), pos-float64(line.TimeMs)/1000)
		progress = float32(1 - math.Pow(1-min(elapsed/0.42, 1), 3))
	}
	beforeStart := index >= 0 && pos*1000 < float64(line.TimeMs)
	if beforeStart {
		progress = 1
	}
	requestLyricFrame(c, eng, playback, parsed, index, pos, len(line.Words) == 0 && !state.skipEntrance)
	ui.Box(c).Label("大字歌词内容").Fill().Clip().Children(func() {
		ui.Column(c).Absolute().Fill().Center().Gap(18).Children(func() {
			var text ui.Element
			if len(line.Words) > 0 {
				text = ui.RichText(c, timedLyricSpans(line, th, pos)...).Label(line.Text)
			} else {
				color := th.TextMain
				if beforeStart {
					color = th.TextSub
				}
				text = ui.Text(c, line.Text).TextColor(color)
			}
			text.Width(width).TextAlign(ui.Center).Font(font).FontSize(state.fontSize).Bold().MaxLines(4).
				Opacity(0.7+0.3*progress).Translate(0, 5*(1-progress))
			if line.Translation != "" {
				ui.Text(c, line.Translation).Width(width).TextAlign(ui.Center).
					FontSize(max(float32(16), min(state.fontSize*0.28, 25))).TextColor(th.TextSub).MaxLines(2)
			}
		})
	})
}

// Rich text preserves shaping, spaces and wrapping; a source-timed word may
// contain several characters, and its duration must not be split heuristically.
func timedLyricSpans(line lyric.Line, th *theme.Theme, pos float64) []ui.Span {
	spans := make([]ui.Span, 0, len(line.Words))
	for _, word := range line.Words {
		progress := word.Progress(pos)
		spans = append(spans, ui.Span{Text: word.Text, Color: th.TextSub.Mix(th.Primary, progress)})
	}
	return spans
}

// Only refresh while a real word or a short line entrance is advancing. Timers
// are paced by playback speed; a paused/seeking cursor schedules no animation.
func requestLyricFrame(c *ui.Context, eng *player.Engine, playback *PlayerBarState, parsed *lyric.ParsedLyric, index int, pos float64, lineEntrance bool) {
	if index < 0 || parsed == nil || eng.State() != player.StatePlaying || playback != nil && playback.SeekPending {
		return
	}
	line := parsed.Lines[index]
	next := math.Inf(1)
	consider := func(at float64) {
		if at > pos {
			next = min(next, at-pos)
		}
	}
	consider(float64(line.TimeMs) / 1000)
	if index+1 < len(parsed.Lines) {
		consider(float64(parsed.Lines[index+1].TimeMs) / 1000)
	}
	if lineEntrance && !c.Preferences().ReduceMotion && pos*1000 >= float64(line.TimeMs) && pos < float64(line.TimeMs)/1000+0.42 {
		next = min(next, 1.0/60)
	}
	for _, word := range line.Words {
		start := float64(word.TimeMs) / 1000
		consider(start)
		if pos >= start && pos < start+float64(word.DurationMs)/1000 {
			next = min(next, 1.0/60)
		}
	}
	if !math.IsInf(next, 1) {
		c.After(max(time.Millisecond, time.Duration(next/max(eng.Speed(), 0.5)*float64(time.Second))))
	}
}

// RenderLyricBackdrop stretches the small, preblurred bitmap prepared by the
// image cache's worker. Moving the page requires no full-window blur pass.
// The owning lyric sheet clips the overscan to its rounded boundary.
func RenderLyricBackdrop(c *ui.Context, th *theme.Theme, song *plugin.MusicItem, getImage func(string) *ui.Bitmap, states ...*LyricBackdropState) {
	w, h := c.Size()
	var bitmap *ui.Bitmap
	if song != nil && song.Artwork != "" && getImage != nil {
		bitmap = getImage(song.Artwork)
	}
	var state *LyricBackdropState
	if len(states) > 0 {
		state = states[0]
		key := ""
		if song != nil {
			key = song.Artwork
		}
		state.update(c.Now(), key, bitmap, c.Preferences().ReduceMotion)
	}
	if state == nil {
		state = &LyricBackdropState{current: bitmap}
	}
	ui.Box(c).Label("歌词背景").Absolute().Top(0).Left(0).Width(w).Height(h).
		Cache().Draw(func(p *ui.Painter, r ui.Rect) { state.draw(p, r) })
}

// RenderFullScreenLyric is the content between the immersive header
// and the shared player bar: cover and metadata left, full-height lyrics right.
func RenderFullScreenLyric(c *ui.Context, th *theme.Theme, eng *player.Engine, parsed *lyric.ParsedLyric, getImage func(string) *ui.Bitmap, coverOffset float32, showCover bool, playback *PlayerBarState) {
	th = th.ForLyrics()
	song := eng.CurrentSong()
	pos := eng.Position()
	if playback != nil {
		pos = playback.Position(eng)
	}
	if parsed != nil {
		requestLyricFrame(c, eng, playback, parsed, parsed.FindCurrentIndex(pos), pos, false)
	}
	w, h := c.Size()
	coverSize := LyricCoverBounds(w, h).W
	title, artist, album := "暂无播放歌曲", "未知歌手", ""
	if song != nil {
		if song.Title != "" {
			title = song.Title
		}
		if song.Artist != "" {
			artist = song.Artist
		}
		album = song.Album
	}
	root := ui.Row(c).Fill().Padding(12, w*0.06, 20, w*0.06).Gap(w * 0.04)
	root.Children(func() {
		ui.Column(c).Basis(0).Grow(2).MinWidth(0).FillHeight().Center().Gap(lyricCoverInfoGap).Children(func() {
			anchor := ui.Box(c.Key("lrc_cover_slot")).Size(coverSize, coverSize).Shrink(0).Translate(0, -coverOffset)
			if showCover {
				ui.Overlay(c, func() {
					ui.Box(c.Key("lrc_cover")).AttachTo(anchor, ui.AnchorCenter, ui.AnchorCenter).
						Label("歌词封面").Size(coverSize, coverSize).Radius(LyricCoverRadius).
						AnimatePose(ui.Pose{Scale: LyricCoverScale(eng)}, anim.Lyric).
						Background(th.CardBg).Shadow(0, 20, 60, 0, ui.RGBA(0, 0, 0, 0.16)).Center().
						Transition(anim.Artwork(artworkKey(song))).Children(func() {
						if song != nil && song.Artwork != "" && getImage != nil {
							if bm := getImage(song.Artwork); bm != nil {
								ui.Image(c, bm).Fill().Radius(LyricCoverRadius).Fit(ui.Cover)
								return
							}
						}
						ui.Icon(c, icons.Album).Size(80, 80).TextColor(th.TextSub)
					})
				})
			}
			ui.Column(c).Label("歌曲信息").Width(coverSize).Height(lyricCoverInfoHeight).AlignItems(ui.Center).Gap(8).Shrink(0).Children(func() {
				ui.Text(c, title).FillWidth().FontSize(max(float32(24), min(w*0.026, float32(32)))).LineHeight(1.2).Bold().TextAlign(ui.Center).TextColor(th.TextMain).SingleLine().Ellipsis("…").ContentTransition(ui.ContentTransition{})
				ui.Column(c).FillWidth().Gap(6).AlignItems(ui.Center).Children(func() {
					ui.Text(c, "歌手："+artist).FillWidth().TextAlign(ui.Center).FontSize(15).LineHeight(1.2).TextColor(th.TextSub).SingleLine().Ellipsis("…")
					if album != "" {
						ui.Text(c, "专辑："+album).FillWidth().TextAlign(ui.Center).FontSize(15).LineHeight(1.2).TextColor(th.TextSub).SingleLine().Ellipsis("…")
					}
				})
			})
		})
		ui.Column(c).Label("歌词内容").Basis(0).Grow(3).MinWidth(0).FillHeight().Children(func() {
			renderScrollableLyrics(c, eng, parsed, playback, pos)
		})
	})
}

// LyricCoverBounds is the cover's slot in the 40% left column, between
// the 72-DIP header and 96-DIP bar (body padding: 12 above, 20 below).
// Reserve the metadata beneath it so the travelling cover lands in this slot.
func LyricCoverBounds(w, h float32) ui.Rect {
	infoHeight := lyricCoverInfoGap + lyricCoverInfoHeight
	size := min(w*0.25, (h-168)*0.64, float32(420), max(0, h-200-infoHeight))
	return ui.Rect{X: w*0.228 - size/2, Y: h/2 - 16 - (size+infoHeight)/2, W: size, H: size}
}
