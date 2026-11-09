package components

import (
	"fmt"

	"github.com/egoist/mygo/ui"
	"musicgo/internal/player"
	"musicgo/internal/store"
	"musicgo/internal/ui/anim"
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

func queueRowActive(row ui.Element) bool {
	keyboard := ui.Local(row, "queue-actions-keyboard", func() bool { return false })
	if row.FocusVisible() {
		*keyboard = true
	} else if !row.FocusWithin() {
		*keyboard = false
	}
	return row.Hovered() || *keyboard
}

func renderQueueDrawer(c *ui.Context, th *theme.Theme, anchor ui.Element, eng *player.Engine, state *QueueDrawerState, getImage func(string) *ui.Bitmap, st *store.Store, redraw func(), barHeight float32) {
	if !state.Open {
		state.snapshot = player.QueueSnapshot{}
		state.list = ui.ListState{}
		state.wasOpen = false
		// Keep the overlay parent alive so the panel can enter and exit.
		ui.Overlay(c.Key("queue_drawer_overlay"), func() {})
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
	opening := !state.wasOpen
	if opening {
		if snap.Current >= 0 && snap.Current < len(entries) {
			state.list.ScrollIntoView(snap.Current)
		}
		state.wasOpen = true
	}

	// This is a window-edge overlay, without PopoverBase's button attachment.
	ui.Overlay(c.Key("queue_drawer_overlay"), func() {
		width, height := c.Size()
		panelWidth := min(float32(504), max(float32(0), width-16))
		panel := ui.Column(c.Key("queue_panel")).Label("播放队列面板").
			Absolute().Top(0).Right(0).
			Width(panelWidth).Height(max(float32(0), height-barHeight)).
			Background(opaque(th.CardBg)).
			BorderWidth(0, 0, 0, 1).BorderColor(th.Border).
			OnClick(func() {}).
			Transition(ui.ElementTransition{
				Animation: &anim.Normal,
				Enter:     &ui.Motion{X: 28, Opacity: 0},
				Exit:      &ui.Motion{X: 28, Opacity: 0},
			})
		panel.Children(func() {
			ui.Row(c).FillWidth().Height(74).Shrink(0).Padding(0, 8, 0, 22).AlignItems(ui.Center).Children(func() {
				ui.Text(c, "播放队列").FontSize(18).TextColor(th.TextMain)
				ui.Spacer(c)
				ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
					locate := IconButton(c.Key("locate"), th, "定位当前播放歌曲", icons.Playlist, th.TextSub).
						Disabled(snap.Current < 0 || snap.Current >= len(entries))
					if locate.Clicked() {
						state.list.ScrollIntoView(snap.Current)
					}
					clear := IconButton(c.Key("clear"), th, "清空队列并停止播放", icons.Trash, th.TextSub).Disabled(len(entries) == 0)
					if clear.Clicked() {
						eng.ClearQueue()
					}
				})
			})
			ui.Row(c).FillWidth().Height(29).Shrink(0).Padding(0, 22).AlignItems(ui.Center).Children(func() {
				ui.Text(c, fmt.Sprintf("共%d首歌曲", len(entries))).FontSize(12).TextColor(th.TextSub)
			})

			if len(entries) == 0 {
				ui.Column(c).FillWidth().Grow(1).MinHeight(0).Center().Children(func() {
					EmptyState(c, th, icons.Playlist, "播放队列暂无歌曲")
				})
				return
			}

			state.list.Key = func(i int) any { return entries[i].ID }
			list := ui.List(c.Key("queue_list"), &state.list, len(entries), func(index int) {
				entry := entries[index]
				renderQueueEntry(c, th, entry, index, snap, eng, getImage, st, redraw, func(m *ui.Menu) {
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
			}).Label("队列歌曲列表").FillWidth().Grow(1).MinHeight(0).Padding(0, 12, 0, 12)
			// Empty space below the last row accepts moving to the end.
			if dragged, ok := ui.Drop[queueDrag](list); ok {
				eng.MoveQueueEntry(uint64(dragged), 0)
			}
		})
		if panel.OverlayShortcut(0, ui.KeyEscape) || !opening && !anchor.Pressed() && panel.PressedOutside() {
			state.Open = false
		}
	})
}

func renderQueueEntry(c *ui.Context, th *theme.Theme, entry player.QueueEntry, index int, snap player.QueueSnapshot, eng *player.Engine, getImage func(string) *ui.Bitmap, st *store.Store, redraw func(), menu func(*ui.Menu)) {
	song := entry.Song
	current := index == snap.Current
	isNext := !current && entry.ID == snap.NextID
	title, artist := song.Title, song.Artist
	if title == "" {
		title = "未知歌曲"
	}
	if artist == "" {
		artist = "未知歌手"
	}
	rowLabel := title + " / " + artist
	if isNext {
		rowLabel += " / 下一首"
	}
	if song.Duration > 0 {
		seconds := int(song.Duration)
		rowLabel += fmt.Sprintf(" / %02d:%02d", seconds/60, seconds%60)
	}

	row := ui.Row(c.Key("queue_entry")).Label(rowLabel).FillWidth().Height(64).Shrink(0).Radius(4).
		Padding(0, 10).Gap(12).AlignItems(ui.Center).Focusable().Drag(queueDrag(entry.ID)).ContextMenu(menu).
		Cursor(ui.CursorPointer).
		Transition(ui.ElementTransition{Colors: true, Animation: &anim.Quick})
	active := queueRowActive(row)
	switch {
	case current:
		row.Background(th.SelectedBg())
	case active:
		row.Background(th.HoverBg)
	case index%2 == 0:
		row.Background(opaque(th.CardBg).Mix(th.TextMain, 0.025))
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
		label, icon := "播放："+title, icons.Play
		if current && (snap.State == player.StatePlaying || snap.State == player.StateLoading) {
			label, icon = "暂停："+title, icons.Pause
		}
		cover := Tooltip(c, ui.ButtonBase(c.Key("play")).Label(label), label).
			Size(44, 44).Shrink(0).Radius(4).Clip().Background(th.ActiveBg).Center().Cursor(ui.CursorPointer)
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
			if active || cover.Hovered() || cover.FocusVisible() {
				ui.Box(c).Absolute().Top(0).Left(0).Fill().Background(ui.RGBA(0, 0, 0, 0.42)).Center().Children(func() {
					ui.Icon(c, icon).Size(16, 16).TextColor(theme.Stage().TextMain)
				})
			}
		})
		if cover.Clicked() {
			eng.ActivateQueueEntry(entry.ID)
		}

		titleColor, artistColor := th.TextMain, th.TextSub
		if current {
			titleColor, artistColor = th.PrimaryText(), th.PrimaryText()
		}
		ui.Column(c).Basis(0).Grow(1).MinWidth(0).Gap(4).Children(func() {
			ui.Row(c).FillWidth().MinWidth(0).Gap(5).AlignItems(ui.Center).Children(func() {
				Tooltip(c, ui.Text(c, title).Shrink(1).MinWidth(0).FontSize(14).TextColor(titleColor).SingleLine().Ellipsis("…"), title)
				if isNext {
					ui.Text(c, "下一首").Shrink(0).FontSize(10).TextColor(th.PrimaryText())
				}
			})
			Tooltip(c, ui.Text(c, artist).FillWidth().MinWidth(0).FontSize(12).TextColor(artistColor).SingleLine().Ellipsis("…"), artist)
		})

		// Reserve the same space at rest so revealing actions never moves the text.
		actions := ui.Row(c.Key("actions")).Width(112).Shrink(0).Gap(8).AlignItems(ui.Center).Justify(ui.End)
		if !current && !active {
			actions.Invisible()
		}
		actions.Children(func() {
			favIcon, favColor, favLabel := icons.HeartOutline, th.TextSub, "喜欢："+title
			if st != nil && st.IsFavorite(song.Key()) {
				favIcon, favColor, favLabel = icons.Heart, th.PrimaryText(), "取消喜欢："+title
			}
			favBtn := IconButton(c.Key("favorite"), th, favLabel, favIcon, favColor).Disabled(st == nil)
			if favBtn.Clicked() && st != nil {
				st.ToggleFavoriteAsync(song.Clone(), redraw)
			}
			if IconButton(c.Key("remove"), th, "移出队列："+title, icons.Close, th.TextSub).Clicked() {
				eng.RemoveQueueEntry(entry.ID)
			}
			more := Tooltip(c, ui.ButtonBase(c.Key("more")).Label("歌曲更多操作"), "更多操作").
				Size(32, 32).Shrink(0).Radius(8).Center().Cursor(ui.CursorPointer)
			HoverLights(more, th)
			more.Children(func() {
				ui.Box(c).Size(19, 19).Radius(10).Border(1.3, th.TextSub).Center().Children(func() {
					ui.Text(c, "···").FontSize(13).TextColor(th.TextSub)
				})
			})
			more.Menu(menu)
		})
	})
	if row.DoubleClicked() || row.Focused() && (row.Shortcut(0, ui.KeyEnter) || row.Shortcut(0, ui.KeySpace)) {
		eng.ActivateQueueEntry(entry.ID)
	}
}
