package main

import (
	_ "embed"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/audio"
	"github.com/egoist/mygo/ui"
	"musicgo/internal/download"
	"musicgo/internal/player"
	"musicgo/internal/plugin"
	"musicgo/internal/plugin/builtin"
	"musicgo/internal/store"
	"musicgo/internal/sys"
	musicui "musicgo/internal/ui"
)

//go:embed icon.png
var defaultIcon []byte

//go:embed tray_icon.png
var trayIcon []byte

func main() {
	mygo.App.SetName("MusicGo")
	audio.SetLatency(audio.LatencyPlayback)
	// Register PingFang font if present on system
	for _, fName := range []string{"PingFangSC-Regular.ttf", "PingFangSC-Medium.ttf", "PingFangSC-Semibold.ttf"} {
		fp := filepath.Join(os.Getenv("HOME"), ".local/share/fonts/pingfang", fName)
		if data, err := os.ReadFile(fp); err == nil && len(data) > 0 {
			_ = ui.RegisterFont(data, "PingFang SC")
		}
	}
	// 1. Resolve user data directory
	var appDataDir string
	if custom := os.Getenv("MUSICGO_DATA_DIR"); custom != "" {
		appDataDir = custom
	} else if userConfigDir, err := os.UserConfigDir(); err == nil && os.MkdirAll(filepath.Join(userConfigDir, "MusicGo"), 0755) == nil {
		appDataDir = filepath.Join(userConfigDir, "MusicGo")
	} else {
		appDataDir = ".data"
	}
	_ = os.MkdirAll(appDataDir, 0755)

	dbPath := filepath.Join(appDataDir, "musicgo.db")
	cfgPath := filepath.Join(appDataDir, "config.json")
	pluginsDir := filepath.Join(appDataDir, "plugins")

	// 2. Initialize Store
	st, err := store.NewStore(dbPath)
	if err != nil {
		log.Fatalf("failed to initialize sqlite store: %v", err)
	}
	defer st.Close()

	// 3. Initialize ConfigManager
	configMgr := store.NewConfigManager(cfgPath)
	if warning := configMgr.LoadWarning(); warning != "" {
		log.Printf("MusicGo config: %s", warning)
	}
	cfg := configMgr.Get()

	// 4. Initialize plugins and import MusicFree plugins on first launch.
	pluginMgr := loadStartupPlugins(pluginsDir, musicFreePluginDirs(), configMgr, st)
	defer pluginMgr.CloseAll()

	// 6. Initialize Audio Player Engine
	audioEngine := player.NewEngine(pluginMgr)
	audioEngine.SetQuality(cfg.PlaybackQuality)
	audioEngine.SetAudioCache(player.NewAudioCache(filepath.Join(appDataDir, "cache", "audio"), cfg.AudioCacheMB))
	defer audioEngine.Close()
	audioEngine.SetVolume(cfg.Volume)
	if cfg.Speed > 0 {
		audioEngine.SetSpeed(cfg.Speed)
	}
	for band, gain := range cfg.Equalizer {
		audioEngine.Equalizer().SetGain(band, gain)
	}

	// 7. Initialize Media Controller
	mediaController := sys.NewMediaController(audioEngine)
	defer mediaController.Close()
	mediaController.SetEnabled(func() bool { return configMgr.SettingsSnapshot().EnableTrackNotifications })
	mediaController.SetNotifier(func(title, body string) {
		go func() {
			notification := mygo.NewNotification(mygo.NotificationOptions{ID: "musicgo-now-playing", Title: title, Body: body, Silent: true})
			if err := notification.Show(); err != nil {
				log.Printf("MusicGo notification: %v", err)
			}
		}()
	})

	// 7.5 Initialize Download Manager
	downloadMgr := download.NewManager(pluginMgr, st, configMgr)
	defer downloadMgr.Close()

	// 8. Initialize Main UI Application
	app := musicui.NewApp(audioEngine, pluginMgr, st, configMgr, downloadMgr)
	app.Images.SetDiskCacheDir(filepath.Join(appDataDir, "cache", "artwork"))

	var mainWindow *mygo.Window

	// 9. Hotkeys & System Tray
	hotkeyMgr := sys.NewHotkeyManager(audioEngine,
		func() {
			if mainWindow != nil {
				if mainWindow.IsVisible() {
					mainWindow.Hide()
				} else {
					mainWindow.Show()
					mainWindow.Focus()
				}
			}
		},
		func() {
			if app.DesktopLyric != nil {
				app.DesktopLyric.Toggle()
			}
		},
	)

	// The interface tells whether the global shortcuts are bound.
	app.HotkeyStatus = hotkeyMgr.Status

	// The user turns the global shortcuts on and off in the settings.
	app.ToggleGlobalShortcuts = func(enabled bool) {
		go func() {
			if enabled {
				if err := hotkeyMgr.Enable(configMgr.Get().Hotkeys); err != nil {
					log.Printf("MusicGo shortcuts: %v", err)
				}
			} else {
				hotkeyMgr.Disable()
			}
			app.RequestRedraw()
		}()
	}

	mygo.App.WhenReady(func() {
		// Set application / dock / taskbar icon
		_ = mygo.App.Dock.SetIcon(defaultIcon)

		// Keep the UI opaque, with a transparent outer edge for rounded corners.
		mainWindow = mygo.NewWindow(mygo.WindowOptions{
			Title:           "MusicGo",
			Width:           1150,
			Height:          740,
			MinWidth:        900,
			MinHeight:       600,
			Frameless:       true,
			Transparent:     mainWindowTransparent,
			AutoHideMenuBar: true,
			Content:         ui.View(app.View),
		})
		configureWindowCorners(mainWindow)

		_ = mainWindow.SetIcon(defaultIcon)

		// Keep the native loop alive while background persistence completes,
		// then let MyGo close its windows and release their renderers.
		var exitOnce sync.Once
		var offBeforeQuit, offWindowClose func()
		cleanExit := func() {
			exitOnce.Do(func() {
				app.SetInvalidate(nil)
				if app.Images != nil {
					app.Images.SetInvalidate(nil)
				}
				if mainWindow != nil {
					mainWindow.Hide()
				}
				if app.DesktopLyric != nil {
					app.DesktopLyric.Hide()
				}
				go func() {
					mediaController.Close()
					saveSettings(configMgr, audioEngine)
					app.Close()
					audioEngine.Close()
					pluginMgr.CloseAll()
					if err := st.Close(); err != nil {
						log.Printf("MusicGo store close: %v", err)
					}
					offWindowClose()
					offBeforeQuit()
					mygo.App.Quit()
				}()
			})
		}

		// What the user changed is kept when the application leaves.
		offBeforeQuit = mygo.App.OnBeforeQuit(func(e *mygo.QuitEvent) {
			// Keep the event loop alive until background persistence completes.
			e.PreventDefault()
			cleanExit()
		})

		// Initialize System Tray (status bar logo)
		trayErr := hotkeyMgr.SetupTray(trayIcon)
		if trayErr != nil {
			log.Printf("MusicGo tray unavailable: %v", trayErr)
		}

		var visible atomic.Bool
		visible.Store(mainWindow.IsVisible())
		mainWindow.OnShow(func() {
			visible.Store(true)
			mainWindow.Invalidate()
		})
		mainWindow.OnHide(func() { visible.Store(false) })
		invalidate := func() {
			if visible.Load() {
				mainWindow.Invalidate()
			}
		}
		app.SetInvalidate(invalidate)
		if app.Images != nil {
			app.Images.SetInvalidate(invalidate)
		}

		app.OnMin = func() {
			mainWindow.Minimize()
		}
		app.OnMax = func() {
			mainWindow.ToggleMaximize()
		}
		// Closing the window keeps playback alive. Without a tray, leave a
		// taskbar entry so that the background player can still be reopened.
		closeMain := mainWindow.Hide
		if trayErr != nil {
			closeMain = mainWindow.Minimize
		}
		app.OnClose = func() {
			// A header click runs inside a GtkGLArea render callback. Hiding
			// there removes its GL context before the frame is presented.
			// Post from a goroutine so the current GPU frame can finish first.
			go mygo.RunOnMain(closeMain)
		}
		offWindowClose = mainWindow.OnClose(func(e *mygo.CloseEvent) {
			e.PreventDefault()
			app.OnClose()
		})
		mygo.App.OnActivate(func(visible bool) {
			if !visible {
				mainWindow.Show()
				mainWindow.Focus()
			}
		})

		// The header shows the glyph of what the maximize button will do, so
		// it asks the window whether it fills the screen, and is told when
		// that changes.
		app.IsMaximized = mainWindow.IsMaximized
		app.IsFullScreen = mainWindow.IsFullScreen
		mainWindow.OnMaximize(func() {
			app.RequestRedraw()
		})
		mainWindow.OnUnmaximize(func() {
			app.RequestRedraw()
		})
		mainWindow.OnEnterFullScreen(app.RequestRedraw)
		mainWindow.OnLeaveFullScreen(app.RequestRedraw)

		// The global shortcuts work while the window is in the background.
		// The desktop binds them, which may ask the user to confirm them
		// and takes its time: it happens after the window is up, and never
		// holds the interface.
		//
		// They are only bound when the user asked for them: on Wayland the
		// desktop asks for a confirmation on every start, which is a poor
		// welcome for an application the user just opened.
		go func() {
			time.Sleep(2 * time.Second)
			if configMgr.Get().EnableGlobalShortcuts {
				if err := hotkeyMgr.SetupHotkeys(configMgr.Get().Hotkeys); err != nil {
					fmt.Printf("MusicGo: 全局快捷键不可用: %v\n", err)
				}
			}
			app.RequestRedraw()
		}()

	})

	if err := mygo.App.Run(); err != nil {
		fmt.Printf("MusicGo exited: %v\n", err)
	}
}

// saveSettings keeps what the user changed in the configuration.
func saveSettings(configMgr *store.ConfigManager, eng *player.Engine) {
	_ = configMgr.UpdateAsync(func(c *store.AppConfig) {
		c.Volume = eng.Volume()
		c.Speed = eng.Speed()
	})
}

// musicFreePluginDirs are the folders MusicFreeDesktop keeps its plugins
// in, so they can be taken over by MusicGo.
func musicFreePluginDirs() []string {
	dirs := []string{}
	if home, err := os.UserHomeDir(); err == nil {
		dirs = append(dirs,
			filepath.Join(home, ".config", "MusicFree", "musicfree-plugins"),
			filepath.Join(home, ".config", "MusicFreeDesktop", "plugins"),
			filepath.Join(home, "MusicFree", "plugins"),
		)
	}
	return dirs
}

func loadStartupPlugins(pluginsDir string, importDirs []string, configMgr *store.ConfigManager, st *store.Store) *plugin.Manager {
	_, err := os.Stat(pluginsDir)
	firstInstall := os.IsNotExist(err)
	pluginMgr := plugin.NewManager(pluginsDir)
	pluginMgr.SetUserVarsProvider(func(platform string) map[string]string {
		return configMgr.Get().PluginVars[platform]
	})
	pluginMgr.SetStorageProvider(func(platform string) plugin.Storage {
		return st.PluginStorageScope(platform)
	})
	_ = pluginMgr.LoadFromDir()
	if !firstInstall {
		loadBuiltinPlugins(pluginMgr)
		return pluginMgr
	}

	// Import only when creating MusicGo's plugin directory. Repeating this
	// on later starts would reinstall plugins the user explicitly removed.
	for _, dir := range importDirs {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			if entry.IsDir() || filepath.Ext(entry.Name()) != ".js" {
				continue
			}
			targetFile := filepath.Join(pluginsDir, entry.Name())
			if _, statErr := os.Stat(targetFile); os.IsNotExist(statErr) {
				_, _ = pluginMgr.LoadPluginFile(filepath.Join(dir, entry.Name()))
			}
		}
	}
	loadBuiltinPlugins(pluginMgr)
	return pluginMgr
}

// loadBuiltinPlugins loads the plugins shipped in the binary. It runs last,
// so a built-in replaces any stale copy of the same platform in the data
// directory; the platform is known, so its stored variables are injected in
// a single load.
func loadBuiltinPlugins(pluginMgr *plugin.Manager) {
	pluginMgr.LoadPluginSource(string(builtin.KugouConcept), pluginMgr.StoredUserVars(builtin.Platform))
}
