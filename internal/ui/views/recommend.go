package views

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/egoist/mygo/ui"
	"musicgo/internal/download"
	"musicgo/internal/player"
	"musicgo/internal/plugin"
	"musicgo/internal/plugin/builtin"
	"musicgo/internal/store"
	"musicgo/internal/ui/components"
	"musicgo/internal/ui/icons"
	"musicgo/internal/ui/theme"
)

// recommendQueries are the searches the discovery page offers, one for each
// of its tabs.
var recommendQueries = []string{"热门", "新歌", "经典", "轻音乐"}

// searchTags are the quick searches the discovery page shows.
var searchTags = []string{"周杰伦", "林俊杰", "陈奕迅", "华语热歌", "纯音乐", "摇滚"}

// RecommendViewState holds state for recommend page
type RecommendViewState struct {
	mu sync.RWMutex

	activeCategory int
	// platform narrows the list to one of the music sources, empty for
	// all of them.
	platform       string
	songs          []plugin.MusicItem
	isLoading      bool
	errMsg         string
	loaded         bool
	pluginRevision uint64
	seq            uint64
	dailyDate      string
	noSources      bool
	// Scroll state is owned by the UI thread. Resets are applied before List
	// starts building, never from one of its header controls during layout.
	List      ui.ListState
	resetList bool
}

// RecommendSnapshot binds the artwork, visible rows and playback callbacks to
// the same generation and source filter for the whole frame.
type RecommendSnapshot struct {
	Songs     []plugin.MusicItem
	Platforms []PlatformCount
	Total     int
	Category  int
	Source    string
	Loading   bool
	NoSources bool
	Error     string
}

func (s *RecommendViewState) Snapshot() RecommendSnapshot {
	s.mu.RLock()
	out := RecommendSnapshot{Total: len(s.songs), Category: s.activeCategory, Source: s.platform, Loading: s.isLoading, NoSources: s.noSources, Error: s.errMsg}
	counts := make(map[string]int)
	for _, item := range s.songs {
		source := sourceOf(item)
		if _, exists := counts[source]; !exists {
			out.Platforms = append(out.Platforms, PlatformCount{Name: source})
		}
		counts[source]++
		if s.platform == "" || source == s.platform {
			out.Songs = append(out.Songs, item.Clone())
		}
	}
	s.mu.RUnlock()
	for i := range out.Platforms {
		out.Platforms[i].Count = counts[out.Platforms[i].Name]
	}
	sort.SliceStable(out.Platforms, func(i, j int) bool { return out.Platforms[i].Count > out.Platforms[j].Count })
	return out
}

// NewRecommendViewState creates initial recommend state
func NewRecommendViewState() *RecommendViewState {
	return &RecommendViewState{}
}

// SetActiveCategory sets the tab of the discovery page that is shown.
// Another tab holds other songs, so the source that was chosen no longer
// means anything.
func (s *RecommendViewState) SetActiveCategory(tab int) {
	s.mu.Lock()
	if s.activeCategory != tab {
		s.platform = ""
		s.resetList = true
	}
	s.activeCategory = tab
	s.mu.Unlock()
}

// SetPlatformFilter narrows the list to the songs of a music source, or
// shows all of them again when it is empty.
func (s *RecommendViewState) SetPlatformFilter(source string) {
	s.mu.Lock()
	if s.platform != source {
		s.resetList = true
	}
	s.platform = source
	s.mu.Unlock()
}

// PlatformCount is a music source of the discovery page and how many of
// the songs come from it.
type PlatformCount struct {
	Name  string
	Count int
}

// sourceOf names the music source a song comes from, as the list shows it.
func sourceOf(item plugin.MusicItem) string {
	if item.Platform == "" {
		return "网络"
	}
	return item.Platform
}

// Load gathers the songs the discovery page shows, from the plugins that
// are installed. It does nothing when it already has songs for the tab.
func (s *RecommendViewState) Load(pm *plugin.Manager, invalidate func()) {
	if pm == nil {
		return
	}

	revision := pm.Revision()
	today := time.Now().Format("2006-01-02")
	s.mu.Lock()
	tab := s.activeCategory
	if s.pluginRevision == revision && (s.loaded || s.isLoading) && (tab != 0 || s.dailyDate == today) {
		s.mu.Unlock()
		return
	}
	noSources := len(pm.ListPlugins()) == 0
	if s.pluginRevision != revision {
		s.songs = nil
		s.platform = ""
	}
	s.pluginRevision = revision
	if tab == 0 {
		s.dailyDate = today
	}
	s.seq++
	seq := s.seq
	s.loaded = false
	s.isLoading = true
	s.errMsg = ""
	s.noSources = noSources
	s.mu.Unlock()

	if invalidate != nil {
		invalidate()
	}

	go func(tab int) {
		var items []plugin.MusicItem
		var err error
		if tab == 0 {
			if inst := pm.GetPlugin(builtin.Platform); inst != nil && inst.HasMethod("getDailyRecommend") {
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				result, e := inst.GetDailyRecommend(ctx)
				cancel()
				if e == nil && result != nil {
					items = result.MusicList
				}
			}
		}
		if len(items) == 0 {
			ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
			query := recommendQueries[max(0, tab-1)%len(recommendQueries)]
			items, err = pm.Search(ctx, query, 1, "music")
			cancel()
		}

		s.mu.Lock()
		if seq != s.seq || revision != pm.Revision() {
			s.mu.Unlock()
			if invalidate != nil {
				invalidate()
			}
			return
		}
		s.isLoading = false
		s.loaded = true
		if noSources {
			s.errMsg = "启用一个音源，开始发现歌曲"
		} else if err != nil {
			s.errMsg = "获取推荐失败，请重试"
		}
		if err == nil || len(items) > 0 || noSources {
			s.songs = items
		}
		s.mu.Unlock()

		if invalidate != nil {
			invalidate()
		}
	}(tab)
}

// Reload refreshes the current category without dropping its source filter.
func (s *RecommendViewState) Reload(pm *plugin.Manager, invalidate func()) {
	s.mu.Lock()
	s.loaded = false
	s.isLoading = false
	s.mu.Unlock()
	s.Load(pm, invalidate)
}

// SelectCategory shows another tab of the discovery page, and gathers its
// songs again when it is another one than the tab that is shown.
func (s *RecommendViewState) SelectCategory(tab int, pm *plugin.Manager, invalidate func()) {
	s.mu.Lock()
	changed := s.activeCategory != tab
	s.activeCategory = tab
	if changed {
		s.platform = ""
		s.resetList = true
	}
	s.mu.Unlock()

	if changed {
		s.Reload(pm, invalidate)
	}
}

// RenderRecommend renders the modern native MyGo music discovery and recommendation page
func RenderRecommend(
	c *ui.Context,
	th *theme.Theme,
	state *RecommendViewState,
	eng *player.Engine,
	pm *plugin.Manager,
	st *store.Store,
	dlMgr *download.Manager,
	getImage func(string) *ui.Bitmap,
	getBackdrop func(string) *ui.Bitmap,
	invalidate func(),
	onSearchTag func(tag string),
	navigate func(string),
) {
	snap := state.Snapshot()
	compact := components.PageWidth(c) < 836
	padding, heroHeight, artSize, titleSize := components.PagePadding(c), float32(208), float32(160), float32(34)
	if compact {
		heroHeight, artSize, titleSize = 152, 104, 28
	}
	var curSong *plugin.MusicItem
	if eng != nil {
		curSong = eng.CurrentSong()
	}
	songs := snap.Songs
	hero := plugin.MusicItem{}
	if len(songs) > 0 {
		hero = songs[0]
	}

	var backdrop *ui.Bitmap
	if hero.Artwork != "" && getBackdrop != nil {
		backdrop = getBackdrop(hero.Artwork)
	}

	state.mu.Lock()
	resetList := state.resetList
	state.resetList = false
	state.mu.Unlock()
	if resetList {
		state.List = ui.ListState{}
	}
	ui.List(c, &state.List, len(songs)+1, func(row int) {
		if row > 0 {
			i := row - 1
			item := songs[i]
			var play func()
			if eng != nil {
				play = func() { eng.SetQueue(songs, i) }
			}
			renderMusicRow(c.Key(fmt.Sprintf("curated_row_%d", i)), th, item, songRowOptions{Index: i, Current: curSong != nil && curSong.Key() == item.Key(), Play: play}, eng, st, dlMgr, getImage, invalidate, nil, nil)
			return
		}
		sectionGap := float32(18)
		if compact {
			sectionGap = 14
		}
		ui.Column(c).FillWidth().Gap(sectionGap).Padding(0, 0, sectionGap, 0).Children(func() {
			// Clear artwork anchors the card; the backdrop follows the lyric page.
			heroText, heroMuted := th.TextMain, th.TextSub
			ui.Row(c).Label("推荐唱片").FillWidth().Height(heroHeight).Shrink(0).Clip().Radius(16).
				Background(th.CardBg).Draw(func(p *ui.Painter, r ui.Rect) {
				components.DrawArtworkBackdrop(p, r, th, backdrop, 16)
			}).Padding(0, padding).AlignItems(ui.Center).Gap(20).Children(func() {
				// Keep the card's focus feedback local to its controls.
				focusRing := func(button ui.Element) {
					focused := button.FocusVisible()
					button.FocusRing(false).DrawOver(func(p *ui.Painter, r ui.Rect) {
						if focused {
							p.Stroke(ui.Rect{X: r.X - 3, Y: r.Y - 3, W: r.W + 6, H: r.H + 6}, heroText, 23, 1.5)
						}
					})
				}
				ui.Column(c).Grow(1).MinWidth(0).Gap(12).Children(func() {
					heroTitle, heroSub := "听见，心之所向。", "搜索喜欢的歌手，开始今天的音乐。"
					if len(songs) > 0 {
						heroTitle, heroSub = hero.Title, hero.Artist
						if hero.Album != "" && hero.Album != hero.Title {
							heroSub += " / " + hero.Album
						}
					}
					ui.Text(c, heroTitle).FontSize(titleSize).Bold().TextColor(heroText).SingleLine().Ellipsis("…")
					ui.Text(c, heroSub).FontSize(13).TextColor(heroMuted).SingleLine().Ellipsis("…")
					ui.Row(c).Margin(8, 0, 0, 0).AlignItems(ui.Center).Gap(10).Children(func() {
						play := components.HeroButton(c.Key("rec_play_all"), th, "播放全部", true).Disabled(len(songs) == 0 || eng == nil)
						if play.Clicked() && len(songs) > 0 && eng != nil {
							eng.SetQueue(songs, 0)
						}
						isFav := len(songs) > 0 && st != nil && st.IsFavorite(hero.Key())
						favIcon, favLabel, favColor := icons.HeartOutline, "收藏首曲", th.TextMain
						if isFav {
							favIcon, favLabel, favColor = icons.Heart, "已收藏首曲", th.Primary
						}
						favorite := ui.ButtonBase(c.Key("rec_favorite")).Label(favLabel).Tooltip(favLabel).Size(40, 40).Radius(20).
							Border(1, th.TextSub.Alpha(0.55)).Center().Disabled(len(songs) == 0 || st == nil).Cursor(ui.CursorPointer).
							Transition(ui.ElementTransition{Colors: true, Duration: 140 * time.Millisecond, Ease: ui.EaseOut})
						focusRing(favorite)
						if favorite.Hovered() {
							favorite.Background(th.HoverBg)
						}
						favorite.Children(func() { ui.Icon(c, favIcon).Size(16, 16).TextColor(favColor) })
						if favorite.Clicked() && st != nil && len(songs) > 0 {
							st.ToggleFavoriteAsync(hero, invalidate)
						}
					})
				})
				var artwork *ui.Bitmap
				if hero.Artwork != "" && getImage != nil {
					artwork = getImage(hero.Artwork)
				}
				ui.Box(c).Label("推荐唱片封面").Size(artSize, artSize).Shrink(0).Radius(6).Clip().Center().Children(func() {
					if artwork != nil {
						ui.Image(c, artwork).Fill().Fit(ui.Contain)
					} else {
						ui.Icon(c, icons.Note).Size(artSize*0.24, artSize*0.24).TextColor(heroMuted)
					}
				})
			})

			// ==================== 3. Native Segmented Tabs ====================
			activeCategory := snap.Category
			ui.Row(c).FillWidth().Wrap().AlignItems(ui.Center).Gap(12).Children(func() {
				tabs := renderTabs(c.Key("rec_tabs"), &activeCategory, "每日推荐", "热门", "新歌", "经典", "轻音乐")
				if tabs.Changed() {
					state.SetActiveCategory(activeCategory)
					state.Reload(pm, invalidate)
				}
				if !compact {
					ui.Spacer(c)
					ui.Row(c).Wrap().MinWidth(0).Gap(16).AlignItems(ui.Center).Children(func() {
						for _, tag := range searchTags {
							currTag := tag
							btn := ui.ButtonBase(c.Key("rec_tag_" + currTag)).Height(32).Center().Cursor(ui.CursorPointer)
							col := th.TextSub
							if btn.Hovered() || btn.FocusVisible() {
								col = th.TextMain
							}
							btn.Children(func() { ui.Text(c, currTag).FontSize(12).TextColor(col) })
							if btn.Clicked() && onSearchTag != nil {
								onSearchTag(currTag)
							}
						}
					})
				}
			})

			// ==================== 3.5 Music Source Filter ====================
			// The songs come from every plugin that is installed, so the
			// list can be narrowed to one of the sources.
			platforms := snap.Platforms
			if len(platforms) > 1 || snap.Source != "" {
				filter := snap.Source
				all := append([]PlatformCount{{Name: "", Count: snap.Total}}, platforms...)
				ui.Row(c).Label("推荐音源筛选").FillWidth().Wrap().AlignItems(ui.Center).Gap(8).Children(func() {
					ui.Text(c, "音源筛选:").FontSize(12).TextColor(th.TextSub)
					for _, src := range all {
						source := src.Name
						label := fmt.Sprintf("全部 %d", src.Count)
						key := "rec_src_all"
						if source != "" {
							label = fmt.Sprintf("%s %d", source, src.Count)
							key = "rec_src_" + source
						}
						on := source == filter
						chip := ui.ButtonBase(c.Key(key)).
							Height(28).MaxWidth(200).Shrink(0).Tooltip(label).
							Radius(14).
							Border(1, th.Border).
							Padding(0, 12).
							Center().
							Cursor(ui.CursorPointer).
							Transition(ui.ElementTransition{Colors: true, Duration: 120 * time.Millisecond, Ease: ui.EaseOut})
						if on {
							chip.Background(th.SelectedBg())
						} else {
							chip.Background(th.CardBg)
							if chip.Hovered() {
								chip.Background(th.HoverBg)
							}
						}
						chip.Children(func() {
							col := th.TextMain
							if on {
								col = th.Primary
							}
							ui.Text(c, label).FontSize(11).TextColor(col).SingleLine().Ellipsis("…")
						})
						if chip.Clicked() {
							state.SetPlatformFilter(source)
							if invalidate != nil {
								invalidate()
							}
						}
					}
				})
			}

			if snap.Loading {
				if len(songs) == 0 {
					renderSongSkeleton(c, th)
					return
				}
				ui.Text(c, "正在更新推荐…").FontSize(12).TextColor(th.TextSub)
			}
			if snap.Error != "" {
				ui.Row(c).FillWidth().Wrap().Gap(12).AlignItems(ui.Center).Children(func() {
					message := snap.Error
					if len(songs) > 0 {
						message = "更新失败，正在显示上次推荐"
					}
					color := th.Danger
					if snap.NoSources {
						color = th.TextSub
					}
					ui.Text(c, message).MinWidth(0).FontSize(13).TextColor(color)
					if snap.NoSources {
						if components.Button(c.Key("rec_plugins"), "管理插件").Clicked() && navigate != nil {
							navigate("plugins")
						}
					} else if components.Button(c.Key("rec_retry"), "重试").Disabled(snap.Loading).Clicked() {
						state.Reload(pm, invalidate)
					}
				})
			}
			if len(songs) == 0 {
				if snap.Error == "" {
					message := "暂无可展示的推荐曲目"
					if snap.Source != "" {
						message = "该音源暂无曲目"
					}
					components.EmptyState(c, th, icons.Note, message)
				}
				return
			}
		})
	}).Label("发现音乐").Grow(1).MinHeight(0).FillWidth().Padding(24, padding, 32, padding)
}
