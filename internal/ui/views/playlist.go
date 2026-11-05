package views

import (
	"fmt"
	"strings"

	"github.com/egoist/mygo/ui"
	"musicgo/internal/download"
	"musicgo/internal/player"
	"musicgo/internal/plugin"
	"musicgo/internal/store"
	"musicgo/internal/ui/components"
	"musicgo/internal/ui/icons"
	"musicgo/internal/ui/theme"
)

// songMatches reports whether a song holds the text of a filter, in its
// title, its artist or its album.
func songMatches(song plugin.MusicItem, lowerFilter string) bool {
	for _, field := range []string{song.Title, song.Artist, song.Album} {
		if field != "" && strings.Contains(strings.ToLower(field), lowerFilter) {
			return true
		}
	}
	return false
}

// PlaylistViewState holds state for playlist view
type PlaylistViewState struct {
	SelectedPlaylistID string
	FilterQuery        string
	ActiveTab          int // 0: 歌曲列表, 1: 评论
	List               ui.ListState
	listFor            string
}

// NewPlaylistViewState creates initial playlist state
func NewPlaylistViewState() *PlaylistViewState {
	return &PlaylistViewState{
		SelectedPlaylistID: "favorites",
		ActiveTab:          0,
	}
}

// RenderPlaylist renders the playlist and history view
func RenderPlaylist(
	c *ui.Context,
	th *theme.Theme,
	state *PlaylistViewState,
	eng *player.Engine,
	st *store.Store,
	dlMgr *download.Manager,
	getImage func(string) *ui.Bitmap,
	redraw ...func(),
) {
	var invalidate func()
	if len(redraw) > 0 {
		invalidate = redraw[0]
	}
	t := c.Theme()
	currentSong := eng.CurrentSong()
	var title string
	var songs []plugin.MusicItem
	builtinPlaylist := state.SelectedPlaylistID == "history" || state.SelectedPlaylistID == "favorites" || state.SelectedPlaylistID == ""

	if state.SelectedPlaylistID == "history" {
		title = "最近播放"
		if st != nil {
			songs, _ = st.ListHistory()
		}
	} else if state.SelectedPlaylistID == "favorites" || state.SelectedPlaylistID == "" {
		title = "我喜欢"
		if st != nil {
			songs, _ = st.ListFavorites()
		}
	} else {
		if st != nil {
			pl, err := st.GetPlaylist(state.SelectedPlaylistID)
			if err == nil && pl != nil {
				title = pl.Title
				songs = pl.Songs
			}
		}
	}

	// The filter narrows the list down to the songs that match it.
	filter := strings.TrimSpace(state.FilterQuery)
	totalSongs := len(songs)
	if filter != "" {
		lower := strings.ToLower(filter)
		filtered := make([]plugin.MusicItem, 0, len(songs))
		for _, song := range songs {
			if songMatches(song, lower) {
				filtered = append(filtered, song)
			}
		}
		songs = filtered
	}

	// A new playlist starts its list from the top, not where the previous
	// one was scrolled.
	if state.listFor != state.SelectedPlaylistID {
		state.listFor = state.SelectedPlaylistID
		state.List = ui.ListState{}
	}

	ui.Column(c).Fill().Padding(24, components.PagePadding(c), 32, components.PagePadding(c)).Gap(18).Children(func() {
		// Custom playlists keep their artwork and name above the toolbar.
		ui.Row(c).FillWidth().Gap(24).AlignItems(ui.Center).Children(func() {
			if !builtinPlaylist {
				ui.Box(c).
					Size(120, 120).
					Background(th.CardBg).
					Radius(10).
					Center().
					Children(func() {
						if len(songs) > 0 && songs[0].Artwork != "" && getImage != nil {
							if bm := getImage(songs[0].Artwork); bm != nil {
								ui.Image(c, bm).Size(120, 120).Radius(10).Fit(ui.Cover)
								return
							}
						}
						ui.Icon(c, icons.Album).Size(50, 50).TextColor(th.TextSub)
					})
			}

			ui.Column(c).Grow(1).MinWidth(0).Gap(10).Children(func() {
				components.PageTitle(c, th, title)

				ui.Row(c).FillWidth().Wrap().AlignItems(ui.Center).Gap(10).Children(func() {
					countText := fmt.Sprintf("歌曲数: %d 首", len(songs))
					if filter != "" {
						countText = fmt.Sprintf("筛选出 %d 首 (共 %d 首)", len(songs), totalSongs)
					}
					ui.Text(c, countText).FontSize(12).TextColor(t.TextMuted)

					playAllBtn := components.PrimaryButton(c, "播放全部").Disabled(len(songs) == 0 || eng == nil)
					if playAllBtn.Clicked() && len(songs) > 0 && eng != nil {
						eng.SetQueue(songs, 0)
					}

					if state.SelectedPlaylistID == "history" && st != nil {
						clearBtn := components.Button(c, "清空历史")
						if clearBtn.Clicked() {
							st.RunAsync(st.ClearHistory, invalidate)
						}
					}

					ui.Spacer(c)

					ui.SearchField(c, &state.FilterQuery).
						Width(200).MinWidth(120).Shrink(1).
						Placeholder("过滤当前列表...")
				})
			})
		})

		// The song list leaves space for artwork and two lines of metadata.
		ui.Column(c).
			FillWidth().
			Grow(1).
			MinHeight(0).
			Children(func() {
				// Songs list
				if len(songs) == 0 {
					message := "列表中暂无歌曲"
					if filter != "" {
						message = "没有歌曲匹配筛选条件"
					}
					components.EmptyState(c, th, icons.Playlist, message)
				} else {
					// A virtual List builds only the rows in view: a rebuild
					// frame during the lyric-page transition walks ~20 rows,
					// not the whole library.
					ui.List(c, &state.List, len(songs), func(idx int) {
						currSong := songs[idx]
						isCurrent := currentSong != nil && currentSong.Key() == currSong.Key()

						var play func()
						if eng != nil {
							play = func() { eng.SetQueue(songs, idx) }
						}
						renderMusicRow(c.Key(fmt.Sprintf("song_%d", idx)), th, currSong, songRowOptions{Index: idx, Current: isCurrent, Play: play}, eng, st, dlMgr, getImage, invalidate, nil, nil)
					}).Grow(1)
				}
			})
	})
}
