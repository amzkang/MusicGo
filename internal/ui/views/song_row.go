package views

import (
	"fmt"
	"strings"

	"github.com/egoist/mygo/ui"
	"musicgo/internal/download"
	"musicgo/internal/player"
	"musicgo/internal/plugin"
	"musicgo/internal/store"
	"musicgo/internal/ui/anim"
	"musicgo/internal/ui/components"
	"musicgo/internal/ui/icons"
	"musicgo/internal/ui/theme"
)

const songRowHeight float32 = 64

// A row only renders a snapshot and invokes callbacks; its caller owns data,
// playback queues, async work and any page-specific menu entries.
type songRowOptions struct {
	Index        int
	Current      bool
	Width        float32
	Preview      bool
	Reason       string
	TrailingText string
	Play         func()
	Actions      func(*ui.Context)
	Menu         func(*ui.Menu)
}

func songRowActive(row ui.Element) bool {
	keyboard := ui.Local(row, "song-actions-keyboard", func() bool { return false })
	if row.FocusVisible() {
		*keyboard = true
	} else if !row.FocusWithin() {
		*keyboard = false
	}
	return row.Hovered() || *keyboard
}

func renderSongRow(c *ui.Context, th *theme.Theme, song plugin.MusicItem, options songRowOptions, getImage func(string) *ui.Bitmap) {
	width := options.Width
	if width <= 0 {
		width = components.PageContentWidth(c)
	}
	row := ui.Row(c).Label("歌曲："+song.Title+"（"+songSourceLabel(song)+"）").Focusable().FillWidth().
		Height(songRowHeight).Shrink(0).Radius(8).Padding(2, 10).AlignItems(ui.Center).Cursor(ui.CursorPointer).Transition(ui.ElementTransition{Colors: true, Animation: &anim.Quick})
	details := song.Title + " / " + song.Artist + " / " + song.Album + " / " + songSourceLabel(song)
	if song.Duration > 0 {
		seconds := int(song.Duration)
		details += fmt.Sprintf(" / %02d:%02d", seconds/60, seconds%60)
	}
	if options.TrailingText != "" {
		details += " / " + options.TrailingText
	}
	components.Tooltip(c, row, details)
	active := songRowActive(row)
	if options.Current {
		row.Background(th.SelectedBg())
	} else if row.Hovered() {
		row.Background(th.HoverBg)
	}
	if row.FocusVisible() {
		row.Border(1, th.Primary)
	}
	if options.Menu != nil {
		row.ContextMenu(options.Menu)
	}
	row.Children(func() {
		if !options.Preview && width >= 480 {
			songIndex(c.Key("index"), options.Index, th, options.Current || active, options.Play)
		}
		songIdentity(c, song, th, options, width, getImage)
		if !options.Preview {
			if width >= 720 {
				source := songSourceLabel(song)
				components.Tooltip(c, ui.Text(c, source).Width(96).Shrink(0).FontSize(11).TextColor(th.TextMuted).SingleLine().Ellipsis("…"), source)
			}
			if width >= 560 {
				text := options.TrailingText
				if text == "" {
					text = "--:--"
					if song.Duration > 0 {
						seconds := int(song.Duration)
						text = fmt.Sprintf("%02d:%02d", seconds/60, seconds%60)
					}
				}
				components.Tooltip(c, ui.Text(c, text).Width(64).Shrink(0).FontSize(12).TextColor(th.TextMuted).SingleLine().Ellipsis("…"), text)
			}
		}
		actionsWidth := float32(96)
		if options.Preview {
			actionsWidth = 64
		}
		ui.Row(c).Width(actionsWidth).Shrink(0).AlignItems(ui.Center).Justify(ui.End).Children(func() {
			if active && options.Actions != nil {
				options.Actions(c)
			}
		})
	})
	if (row.DoubleClicked() || row.Focused() && row.Shortcut(0, ui.KeyEnter)) && options.Play != nil {
		options.Play()
	}
}

func songIdentity(c *ui.Context, song plugin.MusicItem, th *theme.Theme, options songRowOptions, width float32, getImage func(string) *ui.Bitmap) {
	ui.Row(c).Basis(0).Grow(1).MinWidth(0).AlignItems(ui.Center).Gap(12).Children(func() {
		cover := ui.ButtonBase(c.Key("cover")).Label("播放歌曲："+song.Title).Size(44, 44).Shrink(0).Radius(8).Clip().Background(th.ActiveBg).Center().Cursor(ui.CursorPointer).Disabled(options.Play == nil)
		cover.Children(func() {
			var bitmap *ui.Bitmap
			if song.Artwork != "" && getImage != nil {
				bitmap = getImage(song.Artwork)
			}
			if bitmap != nil {
				ui.Image(c, bitmap).Fill().Fit(ui.Cover)
			} else {
				ui.Icon(c, icons.Album).Size(18, 18).TextColor(th.TextSub)
			}
			if cover.Hovered() || cover.FocusVisible() {
				ui.Box(c).Absolute().Top(0).Left(0).Fill().Background(ui.RGBA(0, 0, 0, .5)).Center().Children(func() {
					ui.Icon(c, icons.Play).Size(18, 18).TextColor(theme.Stage().TextMain)
				})
			}
		})
		if cover.Clicked() && options.Play != nil {
			options.Play()
		}
		ui.Column(c).Basis(0).Grow(1).MinWidth(0).Gap(1).Children(func() {
			color := th.TextMain
			if options.Current {
				color = th.Primary
			}
			components.Tooltip(c, ui.Text(c, song.Title).FillWidth().FontSize(14).LineHeight(1.3).TextColor(color).SingleLine().Ellipsis("…"), song.Title)
			artist := song.Artist
			if artist == "" {
				artist = "未知歌手"
			}
			if !options.Preview && song.Album != "" {
				artist += " / " + song.Album
			}
			if !options.Preview && width < 720 {
				artist += " / " + songSourceLabel(song)
			}
			if width < 560 && options.TrailingText != "" {
				artist += " / " + options.TrailingText
			}
			components.Tooltip(c, ui.Text(c, artist).FillWidth().FontSize(12).LineHeight(1.3).TextColor(th.TextSub).SingleLine().Ellipsis("…"), artist)
			if options.Reason != "" {
				components.Tooltip(c, ui.Text(c, options.Reason).FillWidth().FontSize(11).LineHeight(1.3).TextColor(th.TextMuted).SingleLine().Ellipsis("…"), options.Reason)
			}
		})
	})
}

// renderMusicRow connects shared drawing to the existing queue/library actions.
func renderMusicRow(c *ui.Context, th *theme.Theme, song plugin.MusicItem, options songRowOptions, eng *player.Engine, st *store.Store, dl *download.Manager, getImage func(string) *ui.Bitmap, redraw func(), notice func(string), extraMenu func(*ui.Menu)) {
	menu := func(m *ui.Menu) {
		components.SongMenu(m, song, eng, st, redraw, notice)
		if options.Preview && m.Item("下载").Disabled(dl == nil || song.Platform == "local").Chosen() && dl != nil {
			_, err := dl.AddTask(song, "")
			if err != nil && notice != nil {
				notice("下载失败：" + err.Error())
			}
		}
		if extraMenu != nil {
			extraMenu(m)
		}
	}
	options.Menu = menu
	options.Actions = func(c *ui.Context) {
		icon, color, label := icons.HeartOutline, th.TextMain, "收藏"
		if st != nil && st.IsFavorite(song.Key()) {
			icon, color, label = icons.Heart, th.Primary, "取消收藏"
		}
		fav := components.IconButton(c.Key("favorite"), th, label, icon, color).Disabled(st == nil)
		if fav.Clicked() && st != nil {
			st.ToggleFavoriteAsync(song, redraw)
		}
		if !options.Preview {
			color := th.TextSub
			if dl != nil && (dl.IsDownloaded(song.Platform, song.ID) || dl.IsDownloading(song.Platform, song.ID)) {
				color = th.Primary
			}
			button := components.IconButton(c.Key("download"), th, "下载", icons.Download, color).Disabled(dl == nil || song.Platform == "local")
			if button.Clicked() && dl != nil {
				_, err := dl.AddTask(song, "")
				if err != nil && notice != nil {
					notice("下载失败：" + err.Error())
				}
			}
		}
		button := components.Tooltip(c, ui.ButtonBase(c.Key("more")).Label("歌曲更多操作"), "更多操作").Size(32, 32).Shrink(0).Radius(8).Center().Cursor(ui.CursorPointer)
		components.HoverLights(button, th)
		button.Children(func() { ui.Text(c, "···").FontSize(18).TextColor(th.TextSub) })
		button.Menu(menu)
	}
	renderSongRow(c, th, song, options, getImage)
}

func songSourceLabel(song plugin.MusicItem) string {
	if song.Platform == "local" {
		return "本地"
	}
	if song.Platform == "" {
		return "网络"
	}
	return song.Platform
}

func shortPlatformName(name string) string {
	if i := strings.Index(name, "("); i > 0 {
		return name[:i]
	}
	return name
}

func songIndex(c *ui.Context, index int, th *theme.Theme, showPlay bool, play func()) {
	if !showPlay {
		ui.Box(c).Width(36).Shrink(0).Center().Children(func() {
			ui.Text(c, fmt.Sprintf("%02d", index+1)).FontSize(12).TextColor(th.TextMuted)
		})
		return
	}
	button := components.IconButton(c, th, "播放歌曲", icons.Play, th.Primary).Width(36).Disabled(play == nil)
	if button.Clicked() && play != nil {
		play()
	}
}

func renderSongSkeleton(c *ui.Context, th *theme.Theme) {
	ui.Column(c).Label("正在加载推荐").FillWidth().Gap(12).Children(func() {
		for i := 0; i < 3; i++ {
			ui.Row(c).FillWidth().Height(songRowHeight).Gap(12).AlignItems(ui.Center).Children(func() {
				ui.Box(c).Size(44, 44).Radius(8).Background(th.ActiveBg)
				ui.Column(c).Grow(1).Gap(9).Children(func() {
					ui.Box(c).Width(160).Height(12).Radius(4).Background(th.ActiveBg)
					ui.Box(c).Width(100).Height(10).Radius(4).Background(th.HoverBg)
				})
			})
		}
	})
}
