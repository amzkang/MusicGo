package components

import (
	"fmt"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
	"musicgo/internal/plugin"
	"musicgo/internal/store"
	"musicgo/internal/ui/icons"
	"musicgo/internal/ui/theme"
)

// sidebarItem builds a navigation item whose background follows the
// pointer and the selection smoothly.
func sidebarItem(c *ui.Context, th *theme.Theme, selected, id string, icon *ui.SVG, label string) ui.Element {
	color := th.TextMain
	if selected == id {
		color = th.Primary
	}
	// SidebarItem colors its icon explicitly, using different theme fields
	// with and without focus. Keep both in sync with the label's state.
	nativeTheme := c.Theme()
	itemTheme := *nativeTheme
	itemTheme.Accent, itemTheme.AccentText = color, color
	c.SetTheme(&itemTheme)
	item := ui.SidebarItem(c, id, icon, label).
		TextColor(color).
		Cursor(ui.CursorPointer).
		Transition(ui.ElementTransition{Colors: true, Duration: 150 * time.Millisecond, Ease: ui.EaseOut})
	c.SetTheme(nativeTheme)
	if selected == id {
		item.Background(th.SelectedBg())
	}
	return item
}

// RenderSidebar renders the native MyGo sidebar navigation
func RenderSidebar(
	c *ui.Context,
	th *theme.Theme,
	selectedNav *string,
	selectedPlaylistID *string,
	st *store.Store,
	pluginMgr *plugin.Manager,
	downloadActiveCount int,
	onCreatePlaylist func(),
) {
	var playlists []store.Playlist
	if st != nil {
		playlists, _ = st.ListPlaylists()
	}

	ui.Column(c).
		Width(SidebarWidth).Shrink(0).
		FillHeight().
		Background(SidebarSurface(th)).
		BorderWidth(0, 1, 0, 0).
		BorderColor(th.Border).
		Children(func() {
			ui.Row(c).FillWidth().Height(72).Padding(0, 24).AlignItems(ui.Center).Gap(10).DragWindow().Children(func() {
				ui.Image(c, icons.Logo).Label("MusicGo 标志").Size(36, 36).Fit(ui.Contain)
				ui.Text(c, "MusicGo").FontSize(20).Bold().TextColor(th.TextMain)
			})
			nativeTheme := *c.Theme()
			sidebarTheme := nativeTheme
			sidebarTheme.Surface = th.SidebarBg
			sidebarTheme.SurfacePressed = th.ActiveBg
			c.SetTheme(&sidebarTheme)
			nav := selectedNav
			if *nav == "playlist" && *selectedPlaylistID != "favorites" {
				*nav = "pl_" + *selectedPlaylistID
			}
			ui.Sidebar(c, nav, func() {
				sidebarItem(c, th, *nav, "recommend", icons.Fire, "发现音乐")
				sidebarItem(c, th, *nav, "personalized", icons.Sparkles, "为你推荐")

				// 2. 我的媒体库
				ui.SidebarSection(c, "我的媒体库", nil, func() {
					sidebarItem(c, th, *nav, "playlist", icons.HeartOutline, "我喜欢")
					sidebarItem(c, th, *nav, "history", icons.History, "最近播放")

					dlItem := sidebarItem(c, th, *nav, "download", icons.Download, "下载管理")
					if downloadActiveCount > 0 {
						dlItem.Children(func() { ui.Badge(c, fmt.Sprintf("%d", downloadActiveCount)) })
					}
				})

				// 3. 自建歌单
				if len(playlists) > 0 {
					ui.SidebarSection(c, "自建歌单", nil, func() {
						for _, pl := range playlists {
							currPl := pl
							title := currPl.Title
							if len([]rune(title)) > 10 {
								title = string([]rune(title)[:9]) + "..."
							}
							sidebarItem(c, th, *nav, "pl_"+currPl.ID, icons.Note, title)
						}
					})
				}

				// 4. 扩展中心
				ui.SidebarSection(c, "扩展中心", nil, func() {
					sidebarItem(c, th, *nav, "plugins", icons.Plugin, "插件管理")
				})
			}).FillWidth().Grow(1).MinHeight(0)
			if strings.HasPrefix(*nav, "pl_") {
				*selectedPlaylistID = strings.TrimPrefix(*nav, "pl_")
				*selectedNav = "playlist"
			} else if *nav == "playlist" {
				*selectedPlaylistID = "favorites"
			}

			c.SetTheme(&nativeTheme)
		})
}
