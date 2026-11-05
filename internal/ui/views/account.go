package views

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/egoist/mygo/ui"
	"musicgo/internal/download"
	"musicgo/internal/player"
	"musicgo/internal/plugin"
	"musicgo/internal/store"
	"musicgo/internal/ui/components"
	"musicgo/internal/ui/icons"
	"musicgo/internal/ui/theme"
)

// AccountViewState holds the read-only account library: the profile and the
// sheets of the one plugin that is logged in and exports the account hooks.
// Everything loads in the background; the renderers only take snapshots.
type AccountViewState struct {
	mu sync.RWMutex

	platform string
	userID   string
	loggedIn bool
	profile  plugin.UserProfile
	logoURL  string

	sheets     []plugin.UserSheet
	sheetsDone bool
	sheetsErr  string

	// selected names the open sheet; "" shows the sheet home.
	selected  string
	songCache map[string]*accountSongs
	songSeq   uint64
	songCtx   context.CancelFunc

	rev uint64

	// Scroll positions belong to the UI thread, independently of async data.
	sheetsScroll ui.ScrollState
	songsList    ui.ListState
	listFor      string
}

// accountSongs is one sheet's loaded pages.
type accountSongs struct {
	items   []plugin.MusicItem
	page    int
	isEnd   bool
	loading bool
	err     string
}

func NewAccountViewState() *AccountViewState {
	return &AccountViewState{songCache: map[string]*accountSongs{}}
}

// AccountChipData is the snapshot the header chip draws.
type AccountChipData struct {
	Platform string
	Nickname string
	Avatar   string
	Logo     string
	VipLabel string
}

// MaybeRefresh reloads the account state once per plugin revision: profile,
// sheets and the open sheet's songs. It is cheap when nothing changed.
func (s *AccountViewState) MaybeRefresh(pm *plugin.Manager, invalidate func()) {
	if pm == nil {
		return
	}
	s.mu.Lock()
	rev := pm.Revision()
	if s.rev == rev {
		s.mu.Unlock()
		return
	}
	s.rev = rev
	s.mu.Unlock()

	go func() {
		platform := ""
		logo := ""
		var vars map[string]string
		for _, meta := range pm.ListPlugins() {
			stored := pm.StoredUserVars(meta.Platform)
			if pm.SupportsAccountData(meta.Platform) && len(stored) > 0 {
				platform = meta.Platform
				vars = stored
				if v, ok := meta.Hints["logo"].(string); ok {
					logo = v
				}
				break
			}
		}

		s.mu.Lock()
		if s.rev != rev || pm.Revision() != rev {
			s.mu.Unlock()
			return
		}
		sameAccount := s.platform == platform && s.userID == vars["userid"]
		if !sameAccount || platform == "" {
			if s.songCtx != nil {
				s.songCtx()
			}
			s.songSeq++
			s.profile = plugin.UserProfile{Nickname: vars["nickname"], Avatar: vars["avatar"]}
			s.sheets = nil
			s.sheetsDone = false
			s.sheetsErr = ""
			s.selected = ""
			s.songCache = map[string]*accountSongs{}
		}
		if platform == "" {
			// Logged out (or the plugin is gone): the account library closes.
			s.platform = ""
			s.userID = ""
			s.loggedIn = false
			s.logoURL = ""
			s.mu.Unlock()
			if invalidate != nil {
				invalidate()
			}
			return
		}
		s.platform = platform
		s.userID = vars["userid"]
		s.loggedIn = true
		s.logoURL = logo
		s.mu.Unlock()
		if invalidate != nil {
			invalidate()
		}

		// A transient profile failure gets one retry. Each operation owns its
		// deadline, so profile/VIP latency cannot consume the sheets' budget.
		for attempt := 0; attempt < 2; attempt++ {
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			profile, err := pm.GetUserProfile(ctx, platform)
			cancel()
			s.mu.Lock()
			if s.rev != rev || pm.Revision() != rev {
				s.mu.Unlock()
				return
			}
			if err == nil && profile != nil {
				s.profile = *profile
			}
			s.mu.Unlock()
			if err == nil {
				if invalidate != nil {
					invalidate()
				}
				break
			}
		}

		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		sheets, serr := pm.GetUserSheets(ctx, platform)
		cancel()

		s.mu.Lock()
		if s.rev != rev || pm.Revision() != rev {
			s.mu.Unlock()
			return
		}
		if serr != nil {
			s.sheetsErr = "歌单加载失败: " + serr.Error()
		} else {
			s.sheetsErr = ""
			s.sheets = append([]plugin.UserSheet(nil), sheets...)
			s.sheetsDone = true
		}
		s.mu.Unlock()

		if invalidate != nil {
			invalidate()
		}

		// Reopen the sheet that was open, from its first page.
		s.mu.RLock()
		open := s.selected
		s.mu.RUnlock()
		if open != "" {
			s.reloadOpenSheet(pm, open, invalidate)
		}
		if serr == nil {
			go s.loadMissingSheetCovers(pm, platform, rev, sheets, invalidate)
		}
	}()
}

// loadMissingSheetCovers fetches only the first page, off the UI thread.
func (s *AccountViewState) loadMissingSheetCovers(pm *plugin.Manager, platform string, rev uint64, sheets []plugin.UserSheet, invalidate func()) {
	for _, sheet := range sheets {
		if sheet.CoverImg != "" {
			continue
		}
		s.mu.RLock()
		valid := s.rev == rev && s.platform == platform && pm.Revision() == rev
		s.mu.RUnlock()
		if !valid {
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		result, err := pm.GetSheetSongs(ctx, platform, sheet.ID, 1)
		cancel()
		if err != nil || result == nil || len(result.Data) == 0 || result.Data[0].Artwork == "" {
			continue
		}
		s.mu.Lock()
		if s.rev != rev || s.platform != platform || pm.Revision() != rev {
			s.mu.Unlock()
			return
		}
		for i := range s.sheets {
			if s.sheets[i].ID == sheet.ID && s.sheets[i].CoverImg == "" {
				s.sheets[i].CoverImg = result.Data[0].Artwork
				break
			}
		}
		s.mu.Unlock()
		if invalidate != nil {
			invalidate()
		}
	}
}

// Chip returns the header chip data, or nil when no account is logged in.
func (s *AccountViewState) Chip() *AccountChipData {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if !s.loggedIn || s.platform == "" {
		return nil
	}
	nick := s.profile.Nickname
	if nick == "" {
		nick = s.platform
	}
	return &AccountChipData{
		Platform: s.platform,
		Nickname: nick,
		Avatar:   s.profile.Avatar,
		Logo:     s.logoURL,
		VipLabel: s.profile.VipLabel,
	}
}

// LoggedIn tells whether an account backs the library right now.
func (s *AccountViewState) LoggedIn() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.loggedIn
}

// SheetsSnapshot copies the sheet list for rendering.
func (s *AccountViewState) SheetsSnapshot() ([]plugin.UserSheet, bool, string) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]plugin.UserSheet, len(s.sheets))
	copy(out, s.sheets)
	return out, s.sheetsDone, s.sheetsErr
}

// SongsSnapshot copies the open sheet's loaded songs.
func (s *AccountViewState) SongsSnapshot() (string, *accountSongs) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	cache := s.songCache[s.selected]
	if cache == nil {
		return s.selected, nil
	}
	copyItems := make([]plugin.MusicItem, len(cache.items))
	copy(copyItems, cache.items)
	return s.selected, &accountSongs{items: copyItems, page: cache.page, isEnd: cache.isEnd, loading: cache.loading, err: cache.err}
}

// OpenSheet shows a sheet's songs, loading its first page.
func (s *AccountViewState) OpenSheet(pm *plugin.Manager, id string, invalidate func()) {
	s.mu.Lock()
	if s.selected == id && s.songCache[id] != nil {
		s.mu.Unlock()
		return
	}
	if s.songCtx != nil {
		s.songCtx()
	}
	s.songSeq++
	s.selected = id
	s.songCache[id] = &accountSongs{loading: true}
	s.mu.Unlock()
	if invalidate != nil {
		invalidate()
	}
	s.reloadOpenSheet(pm, id, invalidate)
}

// BackToSheets returns from a sheet's songs to the sheet home.
func (s *AccountViewState) HasOpenSheet() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.selected != ""
}

func (s *AccountViewState) BackToSheets(invalidate func()) {
	s.mu.Lock()
	if s.songCtx != nil {
		s.songCtx()
	}
	s.songSeq++
	s.selected = ""
	s.mu.Unlock()
	if invalidate != nil {
		invalidate()
	}
}

// LoadMoreSongs fetches the next page of the open sheet.
func (s *AccountViewState) LoadMoreSongs(pm *plugin.Manager, invalidate func()) {
	s.mu.Lock()
	platform, id := s.platform, s.selected
	cache := s.songCache[id]
	if platform == "" || id == "" || cache == nil || cache.loading || cache.isEnd {
		s.mu.Unlock()
		return
	}
	cache.loading = true
	cache.err = ""
	page := cache.page + 1
	if s.songCtx != nil {
		s.songCtx()
	}
	s.songSeq++
	seq := s.songSeq
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	s.songCtx = cancel
	s.mu.Unlock()
	if invalidate != nil {
		invalidate()
	}
	go s.loadSheetPage(ctx, seq, pm, platform, id, page, invalidate)
}

func (s *AccountViewState) reloadOpenSheet(pm *plugin.Manager, id string, invalidate func()) {
	s.mu.Lock()
	platform := s.platform
	if platform == "" {
		s.mu.Unlock()
		return
	}
	if s.songCtx != nil {
		s.songCtx()
	}
	s.songSeq++
	seq := s.songSeq
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	s.songCtx = cancel
	if cache := s.songCache[id]; cache != nil {
		cache.loading = true
		cache.err = ""
	}
	s.mu.Unlock()
	go s.loadSheetPage(ctx, seq, pm, platform, id, 1, invalidate)
}

func (s *AccountViewState) loadSheetPage(ctx context.Context, seq uint64, pm *plugin.Manager, platform, id string, page int, invalidate func()) {
	var result *plugin.SheetSongs
	var err error
	if pm == nil {
		err = fmt.Errorf("音源插件未加载")
	} else {
		result, err = pm.GetSheetSongs(ctx, platform, id, page)
	}

	s.mu.Lock()
	if seq != s.songSeq {
		s.mu.Unlock()
		return
	}
	cache := s.songCache[id]
	if cache == nil {
		cache = &accountSongs{}
		s.songCache[id] = cache
	}
	cache.loading = false
	if err != nil {
		cache.err = "歌曲加载失败: " + err.Error()
	} else {
		cache.err = ""
		cache.page = page
		if page == 1 {
			cache.items = result.Data
			if len(result.Data) > 0 && result.Data[0].Artwork != "" {
				for i := range s.sheets {
					if s.sheets[i].ID == id && s.sheets[i].CoverImg == "" {
						s.sheets[i].CoverImg = result.Data[0].Artwork
						break
					}
				}
			}
		} else {
			seen := map[plugin.SongKey]bool{}
			for _, it := range cache.items {
				seen[it.Key()] = true
			}
			for _, it := range result.Data {
				if !seen[it.Key()] {
					cache.items = append(cache.items, it)
				}
			}
		}
		cache.isEnd = result.IsEnd || len(result.Data) == 0
	}
	s.mu.Unlock()
	if invalidate != nil {
		invalidate()
	}
}

// RenderAccountLibrary draws the account mode of「我的音乐」: the logged-in
// plugin's sheets on the home page, one sheet's songs once a sheet is open.
func RenderAccountLibrary(
	c *ui.Context,
	th *theme.Theme,
	state *AccountViewState,
	eng *player.Engine,
	pm *plugin.Manager,
	st *store.Store,
	dl *download.Manager,
	getImage func(string) *ui.Bitmap,
	invalidate func(),
) {
	state.MaybeRefresh(pm, invalidate)
	chip := state.Chip()
	if chip == nil {
		ui.Column(c).Fill().Center().Gap(10).Children(func() {
			ui.Icon(c, icons.Headphone).Size(32, 32).TextColor(th.TextMuted)
			ui.Text(c, "账号未登录").FontSize(14).TextColor(th.TextMain)
			ui.Text(c, "在插件管理中扫码登录后，这里显示云端歌单").FontSize(12).TextColor(th.TextSub)
		})
		return
	}

	if state.HasOpenSheet() {
		renderAccountSheet(c, th, state, chip, eng, pm, st, dl, getImage, invalidate)
		return
	}
	renderAccountSheetsHome(c, th, state, chip, pm, getImage, invalidate)
}

func renderAccountSheetsHome(c *ui.Context, th *theme.Theme, state *AccountViewState, chip *AccountChipData, pm *plugin.Manager, getImage func(string) *ui.Bitmap, invalidate func()) {
	sheets, done, errMsg := state.SheetsSnapshot()
	ui.Scroll(c.Key("acct_sheets")).TrackScroll(&state.sheetsScroll).Fill().Padding(24, components.PagePadding(c), 32, components.PagePadding(c)).Gap(18).Children(func() {
		ui.Row(c).FillWidth().Gap(16).AlignItems(ui.Center).Children(func() {
			avatar := ui.Box(c).Size(64, 64).Shrink(0).Radius(32).Background(th.CardBg).Border(1, th.Border).Center()
			avatar.Children(func() {
				if bm := getImage(chip.Avatar); bm != nil {
					ui.Image(c, bm).Size(64, 64).Radius(32).Fit(ui.Cover)
					return
				}
				if bm := getImage(chip.Logo); bm != nil {
					ui.Image(c, bm).Size(64, 64).Radius(32).Fit(ui.Cover)
					return
				}
				ui.Icon(c, icons.Headphone).Size(30, 30).TextColor(th.TextSub)
			})
			ui.Column(c).Grow(1).MinWidth(0).Gap(8).Children(func() {
				ui.Row(c).AlignItems(ui.Center).Gap(10).Children(func() {
					ui.Badge(c, chip.Platform)
					components.PageTitle(c, th, chip.Nickname).Grow(1)
				})
				sub := fmt.Sprintf("云端歌单 %d 个", len(sheets))
				if chip.VipLabel != "" {
					sub += " · " + chip.VipLabel
				}
				ui.Text(c, sub).FontSize(12).TextColor(th.TextMuted)
			})
		})

		if errMsg != "" {
			ui.Text(c, errMsg).FontSize(13).TextColor(th.Danger)
		}
		if !done && errMsg == "" {
			ui.Text(c, "正在加载歌单...").FontSize(13).TextColor(th.TextSub)
		}
		if done && len(sheets) == 0 {
			components.EmptyState(c, th, icons.Playlist, "账号里还没有歌单")
		} else if len(sheets) > 0 {
			ui.Grid(c).FillWidth().Columns(max(1, min(3, int((components.PageContentWidth(c)+16)/208)))).Gap(16).Children(func() {
				for _, sheet := range sheets {
					sheet := sheet
					card := ui.Column(c.Key("acct_sheet_"+sheet.ID)).Label("歌单卡片："+sheet.Title).
						MinWidth(0).Padding(16).Gap(12).
						Background(th.CardBg).Border(1, th.Border).Radius(12).
						Cursor(ui.CursorPointer)
					if card.Hovered() {
						card.Background(th.HoverBg)
					}
					card.Children(func() {
						cover := ui.Box(c).Size(96, 96).Radius(12).Background(th.Background).Center()
						cover.Children(func() {
							if bm := getImage(sheet.CoverImg); bm != nil {
								ui.Image(c, bm).Size(96, 96).Radius(12).Fit(ui.Cover)
								return
							}
							ui.Icon(c, icons.Playlist).Size(34, 34).TextColor(th.TextSub)
						})
						ui.Column(c).Grow(1).MinWidth(0).Gap(4).Children(func() {
							ui.Text(c, sheet.Title).FontSize(14).Bold().TextColor(th.TextMain).SingleLine().Ellipsis("…")
							ui.Text(c, fmt.Sprintf("%d 首", sheet.Count)).FontSize(12).TextColor(th.TextMuted)
						})
					})
					if card.Clicked() {
						state.OpenSheet(pm, sheet.ID, invalidate)
					}
				}
			})
		}
	})
}

func renderAccountSheet(c *ui.Context, th *theme.Theme, state *AccountViewState, chip *AccountChipData, eng *player.Engine, pm *plugin.Manager, st *store.Store, dl *download.Manager, getImage func(string) *ui.Bitmap, invalidate func()) {
	selected, songs := state.SongsSnapshot()
	if state.listFor != selected {
		state.listFor = selected
		state.songsList = ui.ListState{}
	}
	title := ""
	var sheets []plugin.UserSheet
	if sheets, _, _ = state.SheetsSnapshot(); len(sheets) > 0 {
		for _, sheet := range sheets {
			if sheet.ID == selected {
				title = sheet.Title
				break
			}
		}
	}
	if title == "" {
		title = "歌单 " + selected
	}

	ui.Column(c).Fill().Padding(24, components.PagePadding(c), 32, components.PagePadding(c)).Gap(16).Children(func() {
		ui.Row(c).FillWidth().Gap(16).AlignItems(ui.Center).Children(func() {
			back := components.BackButton(c.Key("acct_sheet_back"), th, "返回歌单列表")
			if back.Clicked() {
				state.BackToSheets(invalidate)
			}

			cover := ui.Box(c).Size(88, 88).Shrink(0).Radius(10).Background(th.CardBg).Center()
			cover.Children(func() {
				art := ""
				if songs != nil && len(songs.items) > 0 {
					art = songs.items[0].Artwork
				}
				if bm := getImage(art); bm != nil {
					ui.Image(c, bm).Size(88, 88).Radius(10).Fit(ui.Cover)
					return
				}
				ui.Icon(c, icons.Album).Size(38, 38).TextColor(th.TextSub)
			})

			ui.Column(c).Grow(1).MinWidth(0).Gap(8).Children(func() {
				ui.Row(c).AlignItems(ui.Center).Gap(10).Children(func() {
					ui.Badge(c, chip.Platform)
					components.PageTitle(c, th, title).Grow(1)
				})
				count := 0
				if songs != nil {
					count = len(songs.items)
				}
				ui.Text(c, fmt.Sprintf("已加载 %d 首", count)).FontSize(12).TextColor(th.TextMuted)
				playLabel := "播放全部"
				if songs != nil && !songs.isEnd {
					playLabel = "播放已加载"
				}
				playAll := components.PrimaryButton(c, playLabel).AlignSelf(ui.Start).Disabled(eng == nil || songs == nil || len(songs.items) == 0)
				if playAll.Clicked() && eng != nil && songs != nil && len(songs.items) > 0 {
					eng.SetQueue(songs.items, 0)
				}
			})
		})

		if songs == nil {
			ui.Text(c, "正在加载歌曲...").FontSize(13).TextColor(th.TextSub)
		}
		if songs != nil && songs.err != "" {
			ui.Text(c, songs.err).FontSize(13).TextColor(th.Danger)
		}
		if songs != nil && len(songs.items) == 0 && !songs.loading && songs.err == "" {
			components.EmptyState(c, th, icons.Playlist, "歌单里没有歌曲")
		}

		if songs != nil && len(songs.items) > 0 {
			var currentSong *plugin.MusicItem
			if eng != nil {
				currentSong = eng.CurrentSong()
			}
			ui.List(c.Key("acct_songs_"+selected), &state.songsList, len(songs.items), func(i int) {
				song, index := songs.items[i], i
				isCurrent := currentSong != nil && currentSong.Key() == song.Key()
				var play func()
				if eng != nil {
					play = func() { eng.SetQueue(songs.items, index) }
				}
				renderMusicRow(c.Key(fmt.Sprintf("song_%d", index)), th, song, songRowOptions{Index: index, Current: isCurrent, Play: play}, eng, st, dl, getImage, invalidate, nil, nil)
			}).FillWidth().Grow(1).MinHeight(0)

			if !songs.isEnd && !songs.loading {
				if components.Button(c.Key("acct_more"), "加载更多").Clicked() {
					state.LoadMoreSongs(pm, invalidate)
				}
			}
			if songs.loading {
				ui.Text(c, "正在加载...").FontSize(12).TextColor(th.TextSub)
			}
		}
	})
}
