package components

import (
	"fmt"

	"github.com/egoist/mygo/ui"
	"musicgo/internal/player"
	"musicgo/internal/store"
	"musicgo/internal/ui/icons"
	"musicgo/internal/ui/theme"
)

// All drawer state is UI-owned. The engine owns queue contents and commands.
type QueueDrawerState struct {
	Open     bool
	list     ui.ListState
	snapshot player.QueueSnapshot
	wasOpen  bool
}

// Native drag values carry occurrence IDs, never indices from an earlier frame.
type queueDrag uint64

func renderQueueDrawer(c *ui.Context, th *theme.Theme, anchor ui.Element, eng *player.Engine, state *QueueDrawerState, getImage func(string) *ui.Bitmap, st *store.Store, redraw func(), barHeight float32) {
	if !state.Open {
		state.snapshot = player.QueueSnapshot{}
		state.list = ui.ListState{}
		state.wasOpen = false
		return
	}
	snap := eng.QueueSnapshot(state.snapshot.Version)
	if snap.Version == state.snapshot.Version {
		snap.Entries = state.snapshot.Entries
	}
	state.snapshot = snap
	entries := snap.Entries
	if len(entries) == 0 {
		state.list = ui.ListState{}
	}
	if !state.wasOpen {
		state.list.ScrollIntoView(snap.Current)
		state.wasOpen = true
	}
	ui.PopoverBase(c, anchor, &state.Open, func(panel ui.Element) {
		width, height := c.Size()
		panelWidth := min(float32(480), width-32)
		// Keep the panel between the 72px header and the play bar.
		panelHeight := min(float32(600), height-barHeight-88)
		// The panel's bottom end hangs at the queue button's top end; Bottom
		// lifts it just above the bar (the button sits barHeight/2-11 from
		// the bar top), so the seek track and the bar controls stay visible
		// and clickable while the drawer is open.
		panel.Label("播放队列面板").AttachTo(anchor, ui.AnchorTopEnd, ui.AnchorBottomEnd).Bottom(barHeight/2-3).
			Width(panelWidth).Height(panelHeight).Background(th.CardBg).Border(1, th.Border).Radius(16).
			Shadow(0, 12, 32, 0, ui.RGBA(0, 0, 0, 0.18))
		ui.Row(c).FillWidth().Shrink(0).Padding(16).Gap(8).AlignItems(ui.Center).Children(func() {
			ui.Text(c, "播放队列").FontSize(18).Bold().TextColor(th.TextMain)
			ui.Text(c, fmt.Sprintf("%d 首", len(entries))).FontSize(12).TextColor(th.TextSub)
			ui.Spacer(c)
			clear := Tooltip(c, Button(c.Key("clear"), "清空"), "清空队列并停止播放").Disabled(len(entries) == 0)
			if clear.Clicked() {
				eng.ClearQueue()
			}
			if IconButton(c.Key("close"), th, "关闭播放队列", icons.Close, th.TextSub).Clicked() {
				state.Open = false
			}
		})
		if len(entries) == 0 {
			ui.Column(c).Fill().Center().Children(func() {
				EmptyState(c, th, icons.Playlist, "播放队列暂无歌曲")
			})
			return
		}
		ui.Row(c).FillWidth().Shrink(0).Padding(0, 16, 12, 16).Gap(12).AlignItems(ui.Center).Children(func() {
			mode := "顺序播放"
			switch snap.Mode {
			case player.ModeRepeatAll:
				mode = "列表循环"
			case player.ModeRepeatOne:
				mode = "单曲循环"
			case player.ModeShuffle:
				mode = "随机播放"
			}
			ui.Text(c, mode).FontSize(12).TextColor(th.TextSub)
			ui.Text(c, fmt.Sprintf("当前 %d / %d", snap.Current+1, len(entries))).FontSize(12).TextColor(th.TextMuted)
			ui.Spacer(c)
			if Button(c.Key("locate"), "定位当前").Clicked() {
				state.list.ScrollIntoView(snap.Current)
			}
		})
		ui.Box(c).FillWidth().Height(1).Shrink(0).Background(th.Border)
		state.list.Key = func(i int) any { return entries[i].ID }
		list := ui.List(c.Key("queue_list"), &state.list, len(entries), func(index int) {
			entry := entries[index]
			renderQueueEntry(c, th, entry, index, snap, panelWidth-24, eng, getImage, func(m *ui.Menu) {
				if m.Item("下一首播放").Disabled(index == snap.Current).Chosen() {
					eng.QueueEntryNext(entry.ID)
				}
				songPlaylistMenu(m, entry.Song, st, redraw, nil)
				m.Separator()
				if m.Item("上移").Disabled(index == 0).Chosen() && index > 0 {
					eng.MoveQueueEntry(entry.ID, entries[index-1].ID)
				}
				if m.Item("下移").Disabled(index == len(entries)-1).Chosen() && index+1 < len(entries) {
					before := uint64(0)
					if index+2 < len(entries) {
						before = entries[index+2].ID
					}
					eng.MoveQueueEntry(entry.ID, before)
				}
				if m.Item("移出队列").Chosen() {
					eng.RemoveQueueEntry(entry.ID)
				}
			})
		}).Label("队列歌曲列表").FillWidth().Grow(1).MinHeight(0).Padding(6, 12, 12, 12)
		// Empty space below the last row accepts moving to the end.
		if dragged, ok := ui.Drop[queueDrag](list); ok {
			eng.MoveQueueEntry(uint64(dragged), 0)
		}
	})
}

func renderQueueEntry(c *ui.Context, th *theme.Theme, entry player.QueueEntry, index int, snap player.QueueSnapshot, width float32, eng *player.Engine, getImage func(string) *ui.Bitmap, menu func(*ui.Menu)) {
	song := entry.Song
	current := index == snap.Current
	title, artist := song.Title, song.Artist
	if title == "" {
		title = "未知歌曲"
	}
	if artist == "" {
		artist = "未知歌手"
	}
	if source := formatPlatformTag(song.Platform); source != "" {
		artist += " / " + source
	}
	if current {
		switch snap.State {
		case player.StatePlaying:
			artist = "正在播放 · " + artist
		case player.StateLoading:
			artist = "正在加载 · " + artist
		case player.StatePaused:
			artist = "已暂停 · " + artist
		}
	} else if entry.ID == snap.NextID {
		artist = "下一首 · " + artist
	}
	row := ui.Row(c.Key("queue_entry")).Label(title+" / "+artist).FillWidth().Height(64).Shrink(0).Radius(8).
		Padding(0, 8).Gap(10).AlignItems(ui.Center).Focusable().Drag(queueDrag(entry.ID)).ContextMenu(menu)
	if current {
		row.Background(th.SelectedBg())
	} else if row.Hovered() {
		row.Background(th.HoverBg)
	}
	if row.FocusVisible() {
		row.Border(1, th.Primary)
	}
	if row.Dragging() {
		row.Opacity(0.45)
	}
	if dragged, ok := ui.DragOver[queueDrag](row); ok && uint64(dragged) != entry.ID {
		row.DrawOver(func(p *ui.Painter, r ui.Rect) { p.Fill(ui.Rect{X: r.X, Y: r.Y, W: r.W, H: 2}, th.Primary, 1) })
	}
	if dragged, ok := ui.Drop[queueDrag](row); ok {
		eng.MoveQueueEntry(uint64(dragged), entry.ID)
	}
	row.Children(func() {
		Tooltip(c, ui.Text(c, fmt.Sprintf("%d", index+1)).Width(24).Shrink(0).FontSize(12).TextColor(th.TextMuted), "拖动调整顺序")
		label, icon := "播放："+title, icons.Play
		if current && (snap.State == player.StatePlaying || snap.State == player.StateLoading) {
			label, icon = "暂停："+title, icons.Pause
		}
		cover := Tooltip(c, ui.ButtonBase(c.Key("play")).Label(label), label).Size(44, 44).Shrink(0).Radius(8).Clip().Background(th.ActiveBg).Center().Cursor(ui.CursorPointer)
		cover.Children(func() {
			if song.Artwork != "" && getImage != nil {
				if image := getImage(song.Artwork); image != nil {
					ui.Image(c, image).Fill().Fit(ui.Cover)
				}
			}
			ui.Box(c).Absolute().Top(0).Left(0).Fill().Background(ui.RGBA(0, 0, 0, .4)).Center().Children(func() {
				ui.Icon(c, icon).Size(16, 16).TextColor(theme.Stage().TextMain)
			})
		})
		if cover.Clicked() {
			eng.ActivateQueueEntry(entry.ID)
		}
		ui.Column(c).Basis(0).Grow(1).MinWidth(0).Gap(2).Children(func() {
			color := th.TextMain
			if current {
				color = th.Primary
			}
			Tooltip(c, ui.Text(c, title).FillWidth().FontSize(14).TextColor(color).SingleLine().Ellipsis("…"), title)
			Tooltip(c, ui.Text(c, artist).FillWidth().FontSize(12).TextColor(th.TextSub).SingleLine().Ellipsis("…"), artist)
		})
		if width >= 420 {
			duration := "--:--"
			if song.Duration > 0 {
				seconds := int(song.Duration)
				duration = fmt.Sprintf("%02d:%02d", seconds/60, seconds%60)
			}
			ui.Text(c, duration).Width(42).Shrink(0).FontSize(12).TextColor(th.TextMuted)
		}
		more := Tooltip(c, ui.ButtonBase(c.Key("more")).Label("歌曲更多操作"), "更多操作").Size(32, 32).Shrink(0).Radius(8).Center().Cursor(ui.CursorPointer)
		HoverLights(more, th)
		more.Children(func() { ui.Text(c, "···").FontSize(18).TextColor(th.TextSub) })
		more.Menu(menu)
		if IconButton(c.Key("remove"), th, "移出队列："+title, icons.Close, th.TextSub).Clicked() {
			eng.RemoveQueueEntry(entry.ID)
		}
	})
	if row.DoubleClicked() || row.Focused() && (row.Shortcut(0, ui.KeyEnter) || row.Shortcut(0, ui.KeySpace)) {
		eng.ActivateQueueEntry(entry.ID)
	}
}
