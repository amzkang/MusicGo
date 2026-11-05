package ui

import (
	"context"
	"log"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/egoist/mygo/ui"
	"musicgo/internal/download"
	"musicgo/internal/lyric"
	"musicgo/internal/player"
	"musicgo/internal/plugin"
	"musicgo/internal/store"
	"musicgo/internal/ui/anim"
	"musicgo/internal/ui/components"
	"musicgo/internal/ui/theme"
	"musicgo/internal/ui/views"
)

// App is the root application state and controller for MusicGo
type App struct {
	Theme        *theme.Theme
	Player       *player.Engine
	PluginMgr    *plugin.Manager
	Store        *store.Store
	ConfigMgr    *store.ConfigManager
	DesktopLyric *DesktopLyricWindow
	Images       *ImageCache

	// Invalidate draws the interface again. It is set when the window is
	// ready; until then the requests to draw are remembered.
	Invalidate func()

	mu            sync.RWMutex
	pendingRedraw bool

	// UI Navigation
	SelectedNav        string // "recommend", "personalized", "playlist", "search", "plugins", "history", "download", "settings"
	SelectedPlaylistID string // "favorites", "history" or playlist id
	lastNav            string // the page that was shown last
	zoom               int

	// lastPageKey names the page the content shows, as pageKey does, and
	// pageDir is which way the switch to it went: 1 for a page below the
	// one before in the sidebar, -1 for one above, 0 when the pages are
	// side by side. The content transition uses them.
	lastPageKey string
	pageDir     int

	// View states
	HeaderState       components.HeaderState
	SearchState       *views.SearchViewState
	PlaylistState     *views.PlaylistViewState
	RecommendState    *views.RecommendViewState
	PersonalizedState *views.PersonalizedPageState
	PluginState       *views.PluginManagerViewState
	SettingsState     *views.SettingsViewState
	DownloadState     *views.DownloadViewState
	DownloadMgr       *download.Manager
	BarState          components.PlayerBarState

	// AccountState backs the header account chip and the account mode of
	//「我的音乐」; ShowAccountMenu opens the chip's popover.
	AccountState    *views.AccountViewState
	ShowAccountMenu bool

	// Full-screen Lyric Mode
	ShowFullScreenLyric bool
	largeLyric          bool
	largeLyricState     components.LargeLyricState
	lyricBackdrop       components.LyricBackdropState
	lyricMotionReady    bool
	lyricMotionOpen     bool
	lyricMotionUntil    time.Time
	lyricMotionSize     [2]float32
	artworkSwipe        artworkSwipeState

	// Active lyrics
	lyricMu     sync.RWMutex
	activeLyric *lyric.ParsedLyric
	lastSongID  string
	lyricSeq    uint64
	lyricCancel context.CancelFunc
	closing     bool
	closeOnce   sync.Once
	sessionStop chan struct{}
	sessionDone chan struct{}

	// Window management callbacks
	OnMin   func()
	OnMax   func()
	OnClose func()

	// IsMaximized tells whether the window fills the screen. The header
	// shows the glyph of the restored window while it does, so that the
	// user sees what the button will do.
	IsMaximized  func() bool
	IsFullScreen func() bool

	// HotkeyStatus tells whether the global shortcuts are bound, and why
	// they are not when they are not. The second value is the state: off,
	// ok or error.
	HotkeyStatus func() (string, string)

	// ToggleGlobalShortcuts binds the global shortcuts when the user asks
	// for them, and gives them back to the system when the user does not.
	ToggleGlobalShortcuts func(enabled bool)
}

// NewApp creates the main UI application state
func NewApp(
	eng *player.Engine,
	pm *plugin.Manager,
	st *store.Store,
	cm *store.ConfigManager,
	dl *download.Manager,
) *App {
	// Window visibility belongs to this session, even when an older config
	// saved the desktop lyrics as enabled.
	_ = cm.UpdateAsync(func(cfg *store.AppConfig) { cfg.DesktopLyric.Enabled = false })
	cfg := cm.Get()
	th := theme.FromConfig(cfg)

	deskLyric := NewDesktopLyricWindow(eng, th, cm)

	app := &App{
		Theme:              th,
		Player:             eng,
		PluginMgr:          pm,
		Store:              st,
		ConfigMgr:          cm,
		DesktopLyric:       deskLyric,
		SelectedNav:        "recommend",
		SelectedPlaylistID: "favorites",
		HeaderState:        components.HeaderState{},
		SearchState:        views.NewSearchViewState(),
		PlaylistState:      views.NewPlaylistViewState(),
		RecommendState:     views.NewRecommendViewState(),
		PersonalizedState:  views.NewPersonalizedPageState(cm),
		PluginState:        views.NewPluginManagerViewState(),
		SettingsState:      views.NewSettingsViewState(),
		DownloadState:      views.NewDownloadViewState(),
		AccountState:       views.NewAccountViewState(),
		BarState: components.PlayerBarState{
			Volume:       eng.Volume(),
			VolumePushed: eng.Volume(),
		},
	}

	deskLyric.redraw = app.requestRedraw
	app.Images = NewImageCache(func() {
		app.requestRedraw()
	})

	// Listen to song change to auto-fetch lyric and save history
	eng.AddSongChangeListener(func(song *plugin.MusicItem) {
		if song == nil {
			app.Images.Pin("")
			app.lyricMu.Lock()
			app.lyricSeq++
			if app.lyricCancel != nil {
				app.lyricCancel()
				app.lyricCancel = nil
			}
			app.activeLyric = nil
			app.lastSongID = ""
			app.lyricMu.Unlock()
			if app.DesktopLyric != nil {
				app.DesktopLyric.SetLyric(nil)
			}
			app.requestRedraw()
			return
		}
		// The artwork of the song that plays stays in the cache: the covers
		// of the player bar and the lyric page must not load again because
		// browsing filled the cache with the pictures of other pages.
		app.Images.Pin(song.Artwork)
		// The history is written on its own goroutine: the disk is slow and
		// the interface must not wait for it.
		state := eng.State()
		if st != nil && (state == player.StateLoading || state == player.StatePlaying) {
			s := song.Clone()
			st.RunAsync(func() error { return st.AddHistory(s) }, app.requestRedraw)
		}

		app.startLyricForSong(*song)

		app.requestRedraw()
	})

	eng.AddStateChangeListener(func(player.State) {
		app.requestRedraw()
	})

	eng.AddPlaybackListener(app.requestRedraw)

	if mode := player.Mode(cfg.PlaybackMode); mode >= player.ModeSequence && mode <= player.ModeShuffle {
		eng.SetMode(mode)
	}
	if cfg.PlaybackQueue != nil {
		q := cfg.PlaybackQueue
		eng.RestoreSession(player.PlaybackSession{Songs: q.Songs, Index: q.Index, Mode: player.Mode(cfg.PlaybackMode), PositionMs: q.PositionMs, ShuffleHistory: q.ShuffleHistory, ShuffleCursor: q.ShuffleCursor, ShuffleCandidate: q.ShuffleCandidate, NextIndices: q.NextIndices})
	} else if st != nil {
		// Older configurations have no saved queue. Use the library only
		// for that first start, never to replace an intentionally empty queue.
		favs, _ := st.ListFavorites()
		if len(favs) > 0 {
			eng.LoadQueue(favs, 0)
		} else {
			hist, _ := st.ListHistory()
			if len(hist) > 0 {
				eng.LoadQueue(hist, 0)
			}
		}
	}
	// LoadQueue restores the selection without notifying listeners, so load
	// its artwork and lyrics here even before the user starts playback.
	if song := eng.CurrentSong(); song != nil {
		app.Images.Pin(song.Artwork)
		app.startLyricForSong(*song)
	}

	if dl == nil {
		dl = download.NewManager(pm, st, cm)
	}
	app.DownloadMgr = dl
	dl.OnUpdate(func() {
		app.requestRedraw()
	})

	// The discovery page gathers what the plugins offer.
	app.RecommendState.Load(pm, app.requestRedraw)

	app.startSessionPersistence()
	return app
}

// SetInvalidate sets the function that draws the interface again. A redraw
// that was asked for before the window was ready is done now.
func (app *App) SetInvalidate(fn func()) {
	app.mu.Lock()
	app.Invalidate = fn
	pending := app.pendingRedraw
	app.pendingRedraw = false
	app.mu.Unlock()

	if pending && fn != nil {
		fn()
	}
}

// requestRedraw asks for the interface to be drawn again. Before the window
// is ready the request is remembered, so nothing is lost.
func (app *App) requestRedraw() {
	app.mu.Lock()
	fn := app.Invalidate
	if fn == nil {
		app.pendingRedraw = true
	}
	app.mu.Unlock()

	if fn != nil {
		fn()
	}
}

// ActiveLyric returns the lyric of the song that plays, if it has one.
func (app *App) ActiveLyric() *lyric.ParsedLyric {
	app.lyricMu.RLock()
	defer app.lyricMu.RUnlock()
	return app.activeLyric
}

// setActiveLyric puts the lyric of the song that plays.
func (app *App) setActiveLyric(parsed *lyric.ParsedLyric) {
	app.lyricMu.Lock()
	app.activeLyric = parsed
	app.lyricMu.Unlock()
}

// ApplyTheme switches the interface to the theme the configuration names.
func (app *App) ApplyTheme() {
	cfg := app.ConfigMgr.SettingsSnapshot()
	th := theme.FromConfig(cfg)
	app.Theme = th
	if app.DesktopLyric != nil {
		app.DesktopLyric.SetTheme(th)
	}
	app.requestRedraw()
}

// ToggleTheme switches between the light and the dark theme, and keeps the
// choice in the configuration.
func (app *App) ToggleTheme() {
	if app.Theme.IsDark() {
		_ = app.ConfigMgr.UpdateAsync(func(c *store.AppConfig) { c.SelectTheme("light") })
	} else {
		_ = app.ConfigMgr.UpdateAsync(func(c *store.AppConfig) { c.SelectTheme("dark") })
	}
	app.ApplyTheme()
}

func (app *App) beginLyric(song plugin.MusicItem) (context.Context, uint64) {
	app.lyricMu.Lock()
	defer app.lyricMu.Unlock()
	if app.lyricCancel != nil {
		app.lyricCancel()
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	app.lyricCancel = cancel
	app.lyricSeq++
	app.lastSongID = string(song.Key())
	app.activeLyric = nil
	if app.closing {
		cancel()
	}
	if app.DesktopLyric != nil {
		app.DesktopLyric.SetLyric(nil)
	}
	return ctx, app.lyricSeq
}
func (app *App) startLyricForSong(song plugin.MusicItem) {
	ctx, seq := app.beginLyric(song)
	go app.fetchLyric(ctx, seq, song.Clone())
}
func (app *App) fetchLyricForSong(song plugin.MusicItem) {
	ctx, seq := app.beginLyric(song)
	app.fetchLyric(ctx, seq, song.Clone())
}
func (app *App) fetchLyric(ctx context.Context, seq uint64, song plugin.MusicItem) {
	raw := song.RawLrc
	if raw == "" {
		raw = song.Lyric
	}
	parsed := lyric.ParseSources(raw, "", song.LxLrc)
	if len(parsed.Lines) > 0 && !app.publishLyric(ctx, seq, song, parsed) {
		return
	}
	// Keep bundled line lyrics visible while the worker requests richer timing.
	if !parsed.HasWordTiming() && app.PluginMgr != nil && song.Platform != "" && song.Platform != "local" {
		if result, err := app.PluginMgr.GetLyric(ctx, song); err == nil && result != nil {
			if result.RawLrc != "" {
				raw = result.RawLrc
			}
			if raw != "" || result.LxLrc != "" {
				parsed = lyric.ParseSources(raw, result.Translation, result.LxLrc)
			}
		}
	}
	if len(parsed.Lines) == 0 {
		parsed = nil
	}
	app.publishLyric(ctx, seq, song, parsed)
	app.lyricMu.Lock()
	if seq == app.lyricSeq && app.lyricCancel != nil {
		app.lyricCancel()
		app.lyricCancel = nil
	}
	app.lyricMu.Unlock()
}

func (app *App) publishLyric(ctx context.Context, seq uint64, song plugin.MusicItem, parsed *lyric.ParsedLyric) bool {
	app.lyricMu.Lock()
	if app.closing || ctx.Err() != nil || seq != app.lyricSeq || app.lastSongID != string(song.Key()) {
		app.lyricMu.Unlock()
		return false
	}
	app.activeLyric = parsed
	if app.DesktopLyric != nil {
		app.DesktopLyric.SetLyric(parsed)
	}
	app.lyricMu.Unlock()
	app.requestRedraw()
	return true
}
func (app *App) Close() {
	app.closeOnce.Do(func() {
		app.SetInvalidate(nil)
		if app.SearchState != nil {
			app.SearchState.Close()
		}
		if app.Images != nil {
			app.Images.Close()
		}
		if app.sessionStop != nil {
			close(app.sessionStop)
			<-app.sessionDone
		}
		app.saveSession()
		if app.PersonalizedState != nil {
			app.PersonalizedState.Data.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			_ = app.PersonalizedState.Data.Wait(ctx)
			cancel()
		}
		app.lyricMu.Lock()
		app.closing = true
		app.lyricSeq++
		if app.lyricCancel != nil {
			app.lyricCancel()
		}
		app.lyricMu.Unlock()
		if app.DownloadMgr != nil {
			app.DownloadMgr.Close()
			if message := app.DownloadMgr.PersistenceError(); message != "" {
				log.Printf("MusicGo download records save: %s", message)
			}
		}
		if app.ConfigMgr != nil {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := app.ConfigMgr.Flush(ctx); err != nil {
				log.Printf("MusicGo config save: %v", err)
			}
		}
	})
}

// View is the top-level MyGo native GPU rendering loop
func (app *App) View(c *ui.Context) {
	components.TrackTooltips(c)
	zoomLevels := [...]float32{0.8, 0.9, 1, 1.1, 1.25, 1.5}
	switch {
	case c.Shortcut(ui.Cmd, ui.KeyEqual) || c.Shortcut(ui.Cmd|ui.Shift, ui.KeyEqual):
		app.zoom = min(app.zoom+1, len(zoomLevels)-3)
	case c.Shortcut(ui.Cmd, ui.KeyMinus):
		app.zoom = max(app.zoom-1, -2)
	case c.Shortcut(ui.Cmd, ui.Key0):
		app.zoom = 0
	}
	c.SetZoom(zoomLevels[app.zoom+2])
	app.Images.BeginFrame()
	defer app.Images.EndFrame()
	th := app.Theme
	c.SetTheme(th.ToMyGoTheme())
	app.BarState.ShowLyrics = app.ShowFullScreenLyric
	app.BarState.DesktopLyricsVisible = app.DesktopLyric != nil && app.DesktopLyric.IsVisible()
	if app.IsMaximized != nil {
		app.HeaderState.Maximized = app.IsMaximized()
	}
	if app.ShowFullScreenLyric {
		c.OnShortcut(0, ui.KeyEscape, func() { app.ShowFullScreenLyric = false })
	}
	radius := float32(16)
	if runtime.GOOS == "windows" || app.HeaderState.Maximized || app.IsFullScreen != nil && app.IsFullScreen() {
		// Windows uses DWM's native window corners. Maximized/full-screen
		// windows fill their entire rectangle on every platform.
		radius = 0
	}
	root := ui.Box(c).Fill().Background(th.Background).Clip()
	// Keep a stable sheet node so AnimatePose can move it in paint-only
	// frames. Rebuild once at the end to release the covered/closed content.
	w, height := c.Size()
	size := [2]float32{w, height}
	if app.lyricMotionReady && app.lyricMotionSize != size {
		// Resizing settles the sheet and artwork into the new layout.
		c.DisableAnimations()
		app.lyricMotionUntil = time.Time{}
	} else if app.lyricMotionReady && app.lyricMotionOpen != app.ShowFullScreenLyric {
		app.lyricMotionUntil = c.Now().Add(anim.Immersive.Duration)
		if app.ShowFullScreenLyric && app.largeLyric {
			app.largeLyricState = components.LargeLyricState{}
		}
	}
	app.lyricMotionReady = true
	app.lyricMotionOpen = app.ShowFullScreenLyric
	app.lyricMotionSize = size
	moving := c.Now().Before(app.lyricMotionUntil) && !c.Preferences().ReduceMotion
	if moving {
		c.After(app.lyricMotionUntil.Sub(c.Now()))
	}
	offset := height
	if app.ShowFullScreenLyric {
		offset = 0
	}
	root.Children(func() {
		// Keep the real library beneath the sheet until it is fully covered.
		if moving || !app.ShowFullScreenLyric {
			ui.Column(c.Key("library_layer")).Absolute().Top(0).Left(0).Fill().Children(func() {
				app.renderLibrary(c)
				app.renderPlayerBar(c, false)
				if app.largeLyric {
					app.renderTravellingArtwork(c, true)
				}
			})
		}
		if moving || app.ShowFullScreenLyric {
			// Catch input over the exposed library during the transition. Moving
			// the sheet must not activate the controls behind it.
			ui.Box(c.Key("lyric_input_shield")).Absolute().Top(0).Left(0).Fill().Focusable().AutoFocus().OnClick(func() {})
		}
		// The stationary window supplies the rounded mask during motion.
		sheet := ui.Box(c.Key("lyric_layer")).Absolute().Top(0).Left(0).Fill().Background(th.Background).Clip().
			AnimatePose(ui.Pose{Y: offset}, anim.Immersive)
		if moving {
			sheet.Shadow(0, -6, 24, 0, ui.RGBA(0, 0, 0, 0.24))
		} else {
			sheet.Radius(radius)
		}
		if moving || app.ShowFullScreenLyric {
			sheet.Label("歌词整页").Children(func() {
				lyricTheme := th.ForLyrics()
				components.RenderLyricBackdrop(c, th, app.Player.CurrentSong(), app.Images.GetBackdrop, &app.lyricBackdrop)
				ui.Column(c).Absolute().Top(0).Left(0).Fill().Children(func() {
					app.renderHeader(c, true)
					ui.Box(c.Key("screen_lyrics")).Grow(1).MinHeight(0).FillWidth().Children(func() {
						if app.largeLyric {
							components.RenderLargeLyric(c, th, app.Player, app.ActiveLyric(), &app.BarState, &app.largeLyricState)
						} else {
							components.RenderFullScreenLyric(c, th, app.Player, app.ActiveLyric(), app.Images.Get, 0, false, &app.BarState)
						}
					})
					app.renderPlayerBar(c, true)
				})
				label := "大字歌词"
				if app.largeLyric {
					label = "标准歌词"
				}
				button := ui.ButtonBase(c.Key("lyric_size_toggle")).Label(label).Absolute().Top(20).Right(24).
					Height(32).Padding(0, 12).Radius(16).Background(lyricTheme.CardBg).Center().Cursor(ui.CursorPointer).Tooltip(label)
				if button.Hovered() {
					button.Background(lyricTheme.HoverBg)
				}
				largeColor, smallColor := lyricTheme.TextMuted, lyricTheme.TextMain
				if app.largeLyric {
					largeColor, smallColor = lyricTheme.TextMain, lyricTheme.TextMuted
				}
				button.Children(func() {
					ui.Row(c).AlignItems(ui.End).Gap(3).Children(func() {
						ui.Text(c, "大").FontSize(19).TextColor(largeColor)
						ui.Text(c, "小").FontSize(13).TextColor(smallColor)
					})
				})
				if button.Clicked() {
					app.largeLyric = !app.largeLyric
					if app.largeLyric {
						app.largeLyricState = components.LargeLyricState{}
					}
				}
			})
		}
		if !app.largeLyric {
			app.renderTravellingArtwork(c, false)
		}
	})
	// Apply this after child views restore their themes: SetTheme also
	// resets the native root's background. Clip overlays as well as content.
	c.Root().Background(ui.Transparent).Radius(radius).Clip()
}

func (app *App) renderLibrary(c *ui.Context) {
	th := app.Theme
	ui.Row(c).Key("main_shell").Grow(1).MinHeight(0).FillWidth().Children(func() {
		activeDl := 0
		if app.DownloadMgr != nil {
			activeDl = app.DownloadMgr.ActiveCount()
		}
		components.RenderSidebar(c, th, &app.SelectedNav, &app.SelectedPlaylistID, app.Store, app.PluginMgr, activeDl, func() {
			if app.Store != nil {
				app.Store.RunAsync(func() error { _, err := app.Store.CreatePlaylist("新建歌单", ""); return err }, app.requestRedraw)
			}
		})
		ui.Column(c).Grow(1).MinWidth(0).FillHeight().Children(func() {
			app.renderHeader(c, false)
			page := app.notePage()
			ui.Box(c).Key("screen_main").Grow(1).MinHeight(0).FillWidth().Background(components.ContentSurface(th)).Clip().Children(func() {
				ui.Box(c).Key("page:" + page).Fill().Transition(anim.Page(app.pageDir)).Children(func() { app.renderActiveView(c) })
			})
		})
	})
}

func (app *App) renderPlayerBar(c *ui.Context, immersive bool) {
	// Input handlers run between frames; the active bar must keep its state.
	// Only the noninteractive underlay can use a disposable snapshot.
	state := &app.BarState
	if immersive != app.ShowFullScreenLyric {
		underlay := app.BarState
		underlay.Queue = components.QueueDrawerState{}
		state = &underlay
	}
	state.ShowLyrics = immersive
	state.ArtworkHidden = true
	th := app.Theme
	if immersive {
		th = th.ForLyrics()
	}
	components.RenderPlayerBar(c, th, app.Player, state, app.Images.Get, app.ConfigMgr, app.Store, app.requestRedraw, func() { app.ShowFullScreenLyric = !app.ShowFullScreenLyric }, app.toggleDesktopLyric)
}

func (app *App) toggleDesktopLyric() {
	if app.DesktopLyric == nil {
		return
	}
	app.DesktopLyric.Toggle()
}

// backAction uses the same parent destinations as the page's own back button.
func (app *App) backAction() func() {
	if app.ShowFullScreenLyric {
		return func() { app.ShowFullScreenLyric = false }
	}
	switch app.SelectedNav {
	case "recommend", "":
		return nil
	case "search":
		if app.SearchState.HasCollection() {
			return func() { app.SearchState.BackToResults(); app.requestRedraw() }
		}
	case "personalized":
		if app.PersonalizedState.CanGoBack() {
			return func() { app.PersonalizedState.Back(app.requestRedraw) }
		}
	case "playlist":
		mode := app.ConfigMgr.LibraryModeValue()
		if (mode == "account" || mode == "") && app.AccountState.LoggedIn() && app.AccountState.HasOpenSheet() {
			return func() { app.AccountState.BackToSheets(app.requestRedraw) }
		}
	}
	return func() { app.SelectedNav = "recommend"; app.requestRedraw() }
}

func (app *App) renderHeader(c *ui.Context, immersive bool) {
	state := app.HeaderState
	state.SearchHistory = app.ConfigMgr.SearchHistorySnapshot()
	state.ClearSearchHistory = app.ConfigMgr.ClearSearchHistory
	state.Immersive = immersive
	state.SettingsActive = app.SelectedNav == "settings"
	onBack := app.backAction()
	state.CanGoBack = onBack != nil
	th := app.Theme
	if immersive {
		th = th.ForLyrics()
	}

	app.AccountState.MaybeRefresh(app.PluginMgr, app.requestRedraw)
	var accountChip *components.AccountChip
	if data := app.AccountState.Chip(); data != nil {
		accountChip = &components.AccountChip{
			Platform: data.Platform,
			Nickname: data.Nickname,
			Avatar:   data.Avatar,
			Logo:     data.Logo,
			VipLabel: data.VipLabel,
		}
	}

	components.RenderHeader(c, th, &state,
		accountChip,
		&app.ShowAccountMenu,
		func() bool {
			mode := app.ConfigMgr.LibraryModeValue()
			return mode == "account" || (mode == "" && app.AccountState.LoggedIn())
		},
		app.Images.Get,
		func(useAccount bool) {
			mode := "local"
			if useAccount {
				mode = "account"
			}
			_ = app.ConfigMgr.UpdateAsync(func(cfg *store.AppConfig) { cfg.LibraryMode = mode })
			app.requestRedraw()
		},
		func() {
			if data := app.AccountState.Chip(); data != nil {
				app.ShowAccountMenu = false
				go views.LogoutAccount(app.PluginState, app.PluginMgr, app.ConfigMgr, data.Platform, app.requestRedraw)
			}
		},
		app.submitSearch,
		onBack, nil,
		func() {
			if app.OnMin != nil {
				app.OnMin()
			}
		},
		func() {
			if app.OnMax != nil {
				app.OnMax()
			}
		},
		func() {
			if app.OnClose != nil {
				app.OnClose()
			}
		},
		func() { app.SelectedNav = "settings" },
	)
	if immersive == app.ShowFullScreenLyric {
		app.HeaderState = state
	}
}

// pageKey names the page the content shows. A playlist is named by the
// playlist that shows, so that switching from one to another animates
// too.
func (app *App) pageKey() string {
	switch app.SelectedNav {
	case "playlist":
		return "playlist:" + app.SelectedPlaylistID
	case "history":
		return "history"
	case "download":
		return "download"
	case "":
		return "recommend"
	}
	return app.SelectedNav
}

// notePage notes the page that shows, and which way the interface goes
// from the one that showed before, and returns the page's key.
func (app *App) notePage() string {
	key := app.pageKey()
	if key != app.lastPageKey {
		app.pageDir = 0
		if app.lastPageKey != "" {
			switch rank, last := navRank(key), navRank(app.lastPageKey); {
			case rank > last:
				app.pageDir = 1
			case rank < last:
				app.pageDir = -1
			}
		}
		app.lastPageKey = key
	}
	return app.lastPageKey
}

// navRank orders the pages as the sidebar does. A page switched to from
// one above it comes in from below, and the other way around.
func navRank(key string) int {
	if i := strings.IndexByte(key, ':'); i >= 0 {
		key = key[:i]
	}
	switch key {
	case "recommend":
		return 0
	case "personalized":
		return 1
	case "playlist":
		return 2
	case "history":
		return 3
	case "download":
		return 4
	case "search":
		return 5
	case "plugins":
		return 6
	case "settings":
		return 7
	}
	return 2
}

func (app *App) renderActiveView(c *ui.Context) {
	// The discovery page shows another tab when the user comes from another
	// place of the interface.
	if app.lastNav != app.SelectedNav {
		app.lastNav = app.SelectedNav
		switch app.SelectedNav {
		case "recommend":
			app.RecommendState.SelectCategory(0, app.PluginMgr, app.requestRedraw)
		case "personalized":
			app.PersonalizedState.Data.Ensure(app.PluginMgr, app.Store, app.requestRedraw)
		}
	}

	switch app.SelectedNav {
	case "personalized":
		app.PersonalizedState.Data.Ensure(app.PluginMgr, app.Store, app.requestRedraw)
		views.RenderPersonalized(c, app.Theme, app.PersonalizedState, app.Player, app.PluginMgr, app.Store, app.DownloadMgr, app.Images.Get, app.requestRedraw, func(nav string) {
			app.SelectedNav = nav
			app.requestRedraw()
		})
	case "recommend":
		app.RecommendState.Load(app.PluginMgr, app.requestRedraw)
		views.RenderRecommend(c, app.Theme, app.RecommendState, app.Player, app.PluginMgr, app.Store, app.DownloadMgr, app.Images.Get, app.Images.GetBackdrop, app.requestRedraw, app.submitSearch, func(nav string) { app.SelectedNav = nav; app.requestRedraw() })
	case "playlist":
		// 账号模式：登录了账号数据插件且用户选了账号模式（未选择时跟随
		// 登录状态）时，「我的音乐」显示云端歌单；本地模式与从前完全一样。
		mode := app.ConfigMgr.LibraryModeValue()
		if (mode == "account" || mode == "") && app.AccountState.LoggedIn() {
			views.RenderAccountLibrary(c, app.Theme, app.AccountState, app.Player, app.PluginMgr, app.Store, app.DownloadMgr, app.Images.Get, app.requestRedraw)
			return
		}
		app.PlaylistState.SelectedPlaylistID = app.SelectedPlaylistID
		views.RenderPlaylist(c, app.Theme, app.PlaylistState, app.Player, app.Store, app.DownloadMgr, app.Images.Get, app.requestRedraw)
	case "history":
		// Show recently played
		app.PlaylistState.SelectedPlaylistID = "history"
		views.RenderPlaylist(c, app.Theme, app.PlaylistState, app.Player, app.Store, app.DownloadMgr, app.Images.Get, app.requestRedraw)
	case "download":
		views.RenderDownload(c, app.Theme, app.DownloadState, app.DownloadMgr, app.Player, app.Images.Get, app.requestRedraw)
	case "search":
		views.RenderSearch(c, app.Theme, app.SearchState, app.PluginMgr, app.Player, app.Store, app.DownloadMgr, app.Images.Get, app.requestRedraw)
	case "plugins":
		views.RenderPluginManager(c, app.Theme, app.PluginState, app.PluginMgr, app.ConfigMgr, app.Images.Get, app.requestRedraw)
	case "settings":
		views.RenderSettings(c, app.Theme, app.SettingsState, app.Player, app.ConfigMgr, app.Store, app.ApplyTheme, app.HotkeyStatus, app.ToggleGlobalShortcuts)
	default:
		views.RenderRecommend(c, app.Theme, app.RecommendState, app.Player, app.PluginMgr, app.Store, app.DownloadMgr, app.Images.Get, app.Images.GetBackdrop, app.requestRedraw, nil, func(nav string) { app.SelectedNav = nav; app.requestRedraw() })
	}
}

func (app *App) RequestRedraw() { app.requestRedraw() }
