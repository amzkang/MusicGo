package ui

import (
	"math"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"musicgo/internal/lyric"
	"musicgo/internal/player"
	"musicgo/internal/store"
	"musicgo/internal/ui/icons"
	"musicgo/internal/ui/theme"
)

// DesktopLyricWindow manages the independent floating GPU desktop lyric window
type DesktopLyricWindow struct {
	mu            sync.RWMutex
	win           *mygo.Window
	eng           *player.Engine
	th            *theme.Theme
	configMgr     *store.ConfigManager
	parsed        *lyric.ParsedLyric
	locked        bool
	fontSize      float32
	opacity       float32
	color         string
	inactiveColor string
	redraw        func()
}

// NewDesktopLyricWindow creates the desktop lyric window manager
func NewDesktopLyricWindow(eng *player.Engine, th *theme.Theme, cm *store.ConfigManager) *DesktopLyricWindow {
	d := &DesktopLyricWindow{
		eng:           eng,
		th:            th,
		configMgr:     cm,
		fontSize:      22,
		opacity:       0.9,
		color:         "#FA586A",
		inactiveColor: "#CBD1DC",
	}
	if cm != nil {
		cfg := cm.Get().DesktopLyric
		if cfg.FontSize > 0 {
			d.fontSize = float32(cfg.FontSize)
		}
		if cfg.Opacity > 0 {
			d.opacity = float32(cfg.Opacity)
		}
		d.fontSize = max(16, min(d.fontSize, 32))
		if validLyricColor(cfg.ColorPrimary) {
			d.color = cfg.ColorPrimary
		}
		if validLyricColor(cfg.ColorInactive) {
			d.inactiveColor = cfg.ColorInactive
		}
		d.locked = cfg.Lock
	}
	eng.AddStateChangeListener(func(player.State) { d.requestRedraw() })
	eng.AddPlaybackListener(d.requestRedraw)
	return d
}

func validLyricColor(value string) bool {
	if len(value) != 7 || value[0] != '#' {
		return false
	}
	_, err := strconv.ParseUint(value[1:], 16, 24)
	return err == nil
}

func (d *DesktopLyricWindow) changeFont(delta float32) {
	d.mu.Lock()
	d.fontSize = max(16, min(32, d.fontSize+delta))
	size, win := d.fontSize, d.win
	d.mu.Unlock()
	if d.configMgr != nil {
		_ = d.configMgr.UpdateAsync(func(cfg *store.AppConfig) { cfg.DesktopLyric.FontSize = float64(size) })
	}
	if win != nil {
		w, h := win.Size()
		minHeight := max(136, int(size*2)+76)
		if h < minHeight {
			win.SetSize(w, minHeight)
		}
	}
	d.requestRedraw()
}

func (d *DesktopLyricWindow) changeColor(inactive bool) {
	colors := []string{"#FA586A", "#5B8DFF", "#52C9A1", "#F5CA70", "#FFFFFF"}
	d.mu.Lock()
	current := d.color
	if inactive {
		current = d.inactiveColor
	}
	next := 0
	for i, color := range colors {
		if strings.EqualFold(current, color) {
			next = (i + 1) % len(colors)
			break
		}
	}
	color := colors[next]
	if inactive {
		d.inactiveColor = color
	} else {
		d.color = color
	}
	d.mu.Unlock()
	if d.configMgr != nil {
		_ = d.configMgr.UpdateAsync(func(cfg *store.AppConfig) {
			if inactive {
				cfg.DesktopLyric.ColorInactive = color
			} else {
				cfg.DesktopLyric.ColorPrimary = color
			}
		})
	}
	d.requestRedraw()
}

// SetTheme changes the colors the window draws with.
func (d *DesktopLyricWindow) SetTheme(th *theme.Theme) {
	if th == nil {
		return
	}
	d.mu.Lock()
	d.th = th
	d.mu.Unlock()
	d.requestRedraw()
}

// SetLyric updates the active lyric for rendering
func (d *DesktopLyricWindow) SetLyric(parsed *lyric.ParsedLyric) {
	d.mu.Lock()
	d.parsed = parsed
	d.mu.Unlock()
	d.requestRedraw()
}

// IsVisible reports whether the desktop lyric window is shown.
func (d *DesktopLyricWindow) IsVisible() bool {
	d.mu.RLock()
	win := d.win
	d.mu.RUnlock()
	return win != nil && win.IsVisible()
}

// Show creates and displays the floating desktop lyric window
func (d *DesktopLyricWindow) Show() {
	mygo.RunOnMain(func() {
		d.mu.RLock()
		win := d.win
		d.mu.RUnlock()
		if win == nil || win.IsDestroyed() {
			cfg := store.DesktopLyricCfg{}
			if d.configMgr != nil {
				cfg = d.configMgr.Get().DesktopLyric
			}
			w := cfg.Width
			if w <= 0 {
				w = 760
			}
			d.mu.RLock()
			fontSize := d.fontSize
			d.mu.RUnlock()
			minHeight := max(136, int(fontSize*2)+76)
			w = max(w, 360)
			h := max(cfg.Height, minHeight)

			win = mygo.NewWindow(mygo.WindowOptions{
				Title:         "Desktop Lyric",
				Width:         w,
				Height:        h,
				MinWidth:      360,
				MinHeight:     minHeight,
				Frameless:     true,
				AlwaysOnTop:   true,
				Transparent:   true,
				Panel:         true,
				DisableShadow: true,
				Hidden:        true,
				Content:       ui.View(d.view),
			})
			d.mu.Lock()
			d.win = win
			d.mu.Unlock()
			win.OnClosed(func() {
				d.mu.Lock()
				d.win = nil
				d.mu.Unlock()
				d.visibilityChanged()
			})
		}
		win.ShowInactive()
		d.visibilityChanged()
	})
}

// Hide hides the desktop lyric window
func (d *DesktopLyricWindow) Hide() {
	mygo.RunOnMain(func() {
		d.mu.RLock()
		win := d.win
		d.mu.RUnlock()
		if win != nil {
			win.Hide()
		}
		d.visibilityChanged()
	})
}

// Toggle toggles the desktop lyric window
func (d *DesktopLyricWindow) Toggle() {
	mygo.RunOnMain(func() {
		if d.IsVisible() {
			d.Hide()
		} else {
			d.Show()
		}
	})
}

func (d *DesktopLyricWindow) visibilityChanged() {
	visible := d.IsVisible()
	if d.configMgr != nil {
		_ = d.configMgr.UpdateAsync(func(cfg *store.AppConfig) { cfg.DesktopLyric.Enabled = visible })
	}
	if d.redraw != nil {
		d.redraw()
	}
	d.requestRedraw()
}

// view builds the GPU desktop lyric frame
func (d *DesktopLyricWindow) view(c *ui.Context) {
	pos := d.eng.Position()

	d.mu.RLock()
	parsed := d.parsed
	fontSize := d.fontSize
	opacity := d.opacity
	lyricColor := ui.Hex(d.color)
	inactiveLyricColor := ui.Hex(d.inactiveColor)
	locked := d.locked
	th := d.th
	d.mu.RUnlock()

	// The floating window always has a dark plate, independent of the library
	// theme. Use a complete dark palette so text, icons and tooltips agree.
	palette := theme.DarkTheme().ToMyGoTheme()
	if th != nil {
		palette.Font = th.ToMyGoTheme().Font
	}
	c.SetTheme(palette)
	c.Root().Background(ui.Transparent).Radius(12).Clip()
	primary, inactive := ui.Hex("#ffffff"), ui.Hex("#cbd1dc")
	line1, line2 := "MusicGo 原生桌面歌词", "等待播放"
	var current lyric.Line
	title := "MusicGo"
	if song := d.eng.CurrentSong(); song != nil {
		title = song.Title
		if song.Artist != "" {
			title += " · " + song.Artist
		}
		line1, line2 = song.Title, "暂无歌词"
	}

	if parsed != nil && len(parsed.Lines) > 0 {
		line2 = ""
		idx := parsed.FindCurrentIndex(pos)
		if idx >= 0 && idx < len(parsed.Lines) {
			current = parsed.Lines[idx]
			line1 = parsed.Lines[idx].Text
			if d.eng.IsPlaying() && len(current.Words) > 0 {
				next := math.Inf(1)
				for _, word := range current.Words {
					start := float64(word.TimeMs) / 1000
					if start > pos {
						next = min(next, start-pos)
					} else if pos < start+float64(word.DurationMs)/1000 {
						next = min(next, 1.0/60)
					}
				}
				if !math.IsInf(next, 1) {
					c.After(max(time.Millisecond, time.Duration(next/max(d.eng.Speed(), 0.5)*float64(time.Second))))
				}
			}
			if idx+1 < len(parsed.Lines) {
				line2 = parsed.Lines[idx+1].Text
				if d.eng.IsPlaying() {
					until := float64(parsed.Lines[idx+1].TimeMs)/1000 - pos
					c.After(max(time.Millisecond, time.Duration(until/d.eng.Speed()*float64(time.Second))))
				}
			}
		}
	}

	bgCol := ui.RGBA(20, 23, 29, max(float32(0.86), min(opacity, 1)))
	button := func(label string, icon *ui.SVG, action func()) {
		b := ui.ButtonBase(c.Key(label)).Label(label).Tooltip(label).
			Size(28, 28).Shrink(0).Radius(7).Center().Cursor(ui.CursorPointer)
		if b.Hovered() {
			b.Background(ui.RGBA(255, 255, 255, 0.14))
		}
		b.Children(func() { ui.Icon(c, icon).Size(15, 15).TextColor(primary) })
		if b.Clicked() {
			action()
		}
	}
	ui.Column(c).Label("桌面歌词面板").Fill().Background(bgCol).Radius(12).
		Border(1, ui.RGBA(255, 255, 255, 0.14)).Padding(8, 14, 12).Gap(6).Children(func() {
		ui.Row(c).FillWidth().Height(28).Shrink(0).AlignItems(ui.Center).Gap(4).Children(func() {
			handle := ui.Row(c).Label("拖动桌面歌词").Grow(1).MinWidth(0).FillHeight().AlignItems(ui.Center)
			if !locked {
				handle.DragWindow().Cursor(ui.CursorGrab)
			}
			handle.Children(func() {
				ui.Text(c, title).FontSize(12).TextColor(inactive).SingleLine().Ellipsis("…")
			})
			button("桌面歌词上一曲", icons.SkipLeft, d.eng.Prev)
			playIcon, playLabel := icons.Play, "桌面歌词播放"
			if d.eng.State() == player.StatePlaying {
				playIcon, playLabel = icons.Pause, "桌面歌词暂停"
			}
			button(playLabel, playIcon, d.eng.TogglePlay)
			button("桌面歌词下一曲", icons.SkipRight, d.eng.Next)
			textButton := func(label, text string, action func()) {
				b := ui.ButtonBase(c.Key(label)).Label(label).Tooltip(label).Size(28, 28).Shrink(0).Radius(7).Center().Cursor(ui.CursorPointer)
				if b.Hovered() {
					b.Background(ui.RGBA(255, 255, 255, 0.14))
				}
				b.Children(func() { ui.Text(c, text).FontSize(12).Bold().TextColor(primary) })
				if b.Clicked() {
					action()
				}
			}
			textButton("缩小歌词字号", "A−", func() { d.changeFont(-2) })
			textButton("放大歌词字号", "A+", func() { d.changeFont(2) })
			textButton("切换歌词默认颜色", "未", func() { d.changeColor(true) })
			textButton("切换歌词高亮颜色", "高", func() { d.changeColor(false) })
			lockIcon, lockLabel := icons.LockOpen, "锁定桌面歌词位置"
			if locked {
				lockIcon, lockLabel = icons.LockClosed, "解锁桌面歌词位置"
			}
			button(lockLabel, lockIcon, func() {
				d.mu.Lock()
				d.locked = !d.locked
				locked := d.locked
				d.mu.Unlock()
				if d.configMgr != nil {
					_ = d.configMgr.UpdateAsync(func(cfg *store.AppConfig) { cfg.DesktopLyric.Lock = locked })
				}
				d.requestRedraw()
			})
			button("关闭桌面歌词", icons.Close, d.Hide)
		})
		body := ui.Column(c).FillWidth().Grow(1).MinHeight(0).Center().Gap(5)
		if !locked {
			body.DragWindow()
		}
		body.Children(func() {
			ui.Box(c).Label("桌面歌词当前行").FillWidth().Children(func() {
				if len(current.Words) > 0 {
					spans := make([]ui.Span, 0, len(current.Words))
					for _, word := range current.Words {
						spans = append(spans, ui.Span{Text: word.Text, Color: inactiveLyricColor.Mix(lyricColor, word.Progress(pos))})
					}
					ui.RichText(c, spans...).Label(line1).FillWidth().TextAlign(ui.Center).FontSize(fontSize).Bold().SingleLine().Ellipsis("…")
				} else {
					ui.Text(c, line1).FillWidth().TextAlign(ui.Center).
						FontSize(fontSize).Bold().TextColor(lyricColor).SingleLine().Ellipsis("…")
				}
			})
			if line2 != "" {
				ui.Box(c).Label("桌面歌词下一行").FillWidth().Children(func() {
					ui.Text(c, line2).FillWidth().TextAlign(ui.Center).
						FontSize(fontSize * 0.7).TextColor(inactiveLyricColor).SingleLine().Ellipsis("…")
				})
			}
		})
	})
}

func (d *DesktopLyricWindow) requestRedraw() {
	d.mu.RLock()
	win := d.win
	d.mu.RUnlock()
	if win != nil {
		win.Invalidate()
	}
}
