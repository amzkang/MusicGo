package components

import (
	"github.com/egoist/mygo/ui"
	"musicgo/internal/player"
	"musicgo/internal/plugin"
	"musicgo/internal/store"
)

// SongMenu adds the shared queue and local-playlist actions to a song menu.
func SongMenu(m *ui.Menu, song plugin.MusicItem, eng *player.Engine, st *store.Store, redraw func(), notice func(string)) {
	if m.Item("下一首播放").Disabled(eng == nil).Chosen() && eng != nil {
		eng.PlayNext(song)
		if notice != nil {
			notice("已加入下一首")
		}
		if redraw != nil {
			redraw()
		}
	}
	songPlaylistMenu(m, song, st, redraw, notice)
}

func songPlaylistMenu(m *ui.Menu, song plugin.MusicItem, st *store.Store, redraw func(), notice func(string)) {
	m.Submenu("加入歌单", func(sub *ui.Menu) {
		var lists []store.Playlist
		if st != nil {
			lists, _ = st.ListPlaylists()
		}
		if len(lists) == 0 {
			sub.Item("暂无自建歌单").Disabled(true)
		}
		for _, list := range lists {
			list := list
			if sub.Item(list.Title).Chosen() && st != nil {
				item := song.Clone()
				st.RunAsync(func() error { return st.AddSongToPlaylist(list.ID, item) }, redraw)
				if notice != nil {
					notice("已提交加入歌单")
				}
			}
		}
	})
}
