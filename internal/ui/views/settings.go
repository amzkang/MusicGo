package views

import (
	"encoding/json"
	"fmt"
	"os"
	"sync"

	"github.com/egoist/mygo/ui"
	"musicgo/internal/player"
	"musicgo/internal/store"
	"musicgo/internal/ui/anim"
	"musicgo/internal/ui/components"
	"musicgo/internal/ui/theme"
)

// SettingsViewState holds settings view state
type SettingsViewState struct {
	mu sync.RWMutex

	ActiveTab         int // 0: 主题与外观, 1: 音频, 2: 快捷键设置, 3: 下载设置, 4: 备份与数据
	BackupPath        string
	DownloadPathInput string
	StatusMsg         string
	IsError           bool
	IsBusy            bool
}

// NewSettingsViewState creates state for settings view
func NewSettingsViewState() *SettingsViewState {
	return &SettingsViewState{
		BackupPath: "musicfree_backup.json",
	}
}

// SetStatus records a message to show under the tabs.
func (s *SettingsViewState) SetStatus(msg string, isError bool) {
	s.mu.Lock()
	s.StatusMsg = msg
	s.IsError = isError
	s.mu.Unlock()
}

// Status returns the message to show under the tabs.
func (s *SettingsViewState) Status() (string, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.StatusMsg, s.IsError
}

// Busy reports whether a long operation is running.
func (s *SettingsViewState) Busy() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.IsBusy
}

// SetBusy records whether a long operation is running.
func (s *SettingsViewState) SetBusy(busy bool) {
	s.mu.Lock()
	s.IsBusy = busy
	s.mu.Unlock()
}

// eqBandNames are the bands of the equalizer, with the name they are known
// by and the label that shows them.
var eqBandNames = []struct {
	key   string
	label string
}{
	{"bass", "低音 60Hz"},
	{"lowMid", "中低 250Hz"},
	{"mid", "中音 1KHz"},
	{"treble", "高音 4KHz"},
}

// RenderSettings renders the settings view
func RenderSettings(
	c *ui.Context,
	th *theme.Theme,
	state *SettingsViewState,
	eng *player.Engine,
	configMgr *store.ConfigManager,
	st *store.Store,
	onThemeChange func(),
	hotkeyStatus func() (string, string),
	onGlobalShortcutsToggle func(bool),
) {
	cfg := configMgr.SettingsSnapshot()

	statusMsg, isError := state.Status()
	if warning := configMgr.LoadWarning(); warning != "" {
		statusMsg, isError = warning, true
	}
	if err := configMgr.LastSaveError(); err != nil {
		statusMsg = "保存设置失败: " + err.Error()
		isError = true
	}
	if state.DownloadPathInput == "" {
		state.DownloadPathInput = cfg.Download.DownloadPath
	}

	ui.Column(c).Fill().Padding(24, components.PagePadding(c), 32, components.PagePadding(c)).Gap(16).Children(func() {
		// Navigation tabs
		renderTabs(c.Key("settings_tabs"), &state.ActiveTab, "主题外观", "音频效果", "快捷键设置", "下载设置", "数据备份")

		if statusMsg != "" {
			color := th.TextSub
			if isError {
				color = th.Danger
			}
			ui.Text(c, statusMsg).FontSize(13).TextColor(color)
		}

		// The panel of a tab comes in as another tab is chosen, and the
		// one that goes fades away.
		ui.Box(c.Key(fmt.Sprintf("settings_panel_%d", state.ActiveTab))).
			FillWidth().
			Grow(1).
			Transition(anim.Tab()).
			Children(func() {
				switch state.ActiveTab {
				case 0:
					// Tab 0: Theme
					ui.Scroll(c.Key(fmt.Sprintf("settings_scroll_%d", state.ActiveTab))).Fill().MinHeight(0).Gap(16).Children(func() {
						ui.Text(c, "选择主题风格:").FontSize(15).Bold().TextColor(th.TextMain)
						ui.Row(c).FillWidth().Wrap().Gap(16).Children(func() {
							custom := cfg.UsesCustomTheme()
							mode := "light"
							if custom {
								mode = "custom"
							} else if cfg.Theme == "dark" {
								mode = "dark"
							}
							options := []struct{ mode, label string }{{"light", "浅色模式"}, {"dark", "深色模式"}}
							if cfg.CustomTheme != nil {
								options = append(options, struct{ mode, label string }{"custom", "自定义主题"})
							}
							for _, option := range options {
								if choiceButton(c.Key("theme_"+option.mode), option.label, mode == option.mode).Height(36).Radius(10).Clicked() && mode != option.mode {
									_ = configMgr.UpdateAsync(func(config *store.AppConfig) { config.SelectTheme(option.mode) })
									if onThemeChange != nil {
										onThemeChange()
									}
								}
							}
						})
						ui.Text(c, "主题切换会立刻生效，无需重启应用").FontSize(12).TextColor(th.TextMuted)
					})

				case 1:
					// Tab 1: Audio
					ui.Scroll(c.Key(fmt.Sprintf("settings_scroll_%d", state.ActiveTab))).Fill().MinHeight(0).Gap(16).Children(func() {
						ui.Row(c).Gap(12).AlignItems(ui.Center).Children(func() {
							enabled := cfg.EnableTrackNotifications
							control := ui.Switch(c.Key("toggle_track_notifications"), &enabled).Label("切歌通知开关")
							ui.Text(c, "切歌通知").FontSize(13).TextColor(th.TextMain)
							if control.Changed() {
								_ = configMgr.UpdateAsync(func(config *store.AppConfig) { config.EnableTrackNotifications = enabled })
							}
						})
						if eng == nil {
							ui.Text(c, "播放器未就绪").FontSize(13).TextColor(th.TextMuted)
							return
						}

						renderAudioCache(c, th, state, eng, configMgr, cfg.AudioCacheMB)
						// Playback speed
						speed := eng.Speed()
						ui.Row(c).FillWidth().Gap(12).Center().Children(func() {
							ui.Text(c, "播放速度:").FontSize(14).TextColor(th.TextSub).Width(90)
							speedSlider := ui.Slider(c.Key("speed_slider"), &speed, 0.5, 2).Grow(1).Cursor(ui.CursorPointer)
							ui.Text(c, fmt.Sprintf("%.2fx", speed)).FontSize(13).TextColor(th.TextMain).Width(60)
							if speedSlider.Changed() {
								eng.SetSpeed(speed)
								_ = configMgr.UpdateAsync(func(c *store.AppConfig) {
									c.Speed = speed
								})
							}
						})

						ui.Row(c).FillWidth().Gap(12).AlignItems(ui.Center).Children(func() {
							ui.Text(c, "播放音质:").FontSize(14).TextColor(th.TextSub).Width(90)
							components.RenderQualityControl(c.Key("settings_quality"), th, eng, configMgr)
						})

						// Volume
						volume := eng.Volume()
						ui.Row(c).FillWidth().Gap(12).Center().Children(func() {
							ui.Text(c, "音量:").FontSize(14).TextColor(th.TextSub).Width(90)
							volSlider := ui.Slider(c.Key("settings_volume_slider"), &volume, 0, 1).Grow(1).Cursor(ui.CursorPointer)
							ui.Text(c, fmt.Sprintf("%d%%", int(volume*100))).FontSize(13).TextColor(th.TextMain).Width(60)
							if volSlider.Changed() {
								eng.SetVolume(volume)
								_ = configMgr.UpdateAsync(func(c *store.AppConfig) {
									c.Volume = volume
								})
							}
						})

						ui.Divider(c)
						ui.Text(c, "均衡器:").FontSize(15).Bold().TextColor(th.TextMain)

						gains := eng.Equalizer().Gains()
						for _, band := range eqBandNames {
							currBand := band
							gain := gains[currBand.key]
							ui.Row(c).FillWidth().Gap(12).Center().Children(func() {
								ui.Text(c, currBand.label).FontSize(13).TextColor(th.TextSub).Width(110)
								gainSlider := ui.Slider(c.Key("eq_"+currBand.key), &gain, -12, 12).Grow(1).Cursor(ui.CursorPointer)
								ui.Text(c, fmt.Sprintf("%+.1f dB", gain)).FontSize(12).TextColor(th.TextMain).Width(70)
								if gainSlider.Changed() {
									eng.Equalizer().SetGain(currBand.key, gain)
									_ = configMgr.UpdateAsync(func(c *store.AppConfig) {
										if c.Equalizer == nil {
											c.Equalizer = map[string]float64{}
										}
										c.Equalizer[currBand.key] = gain
									})
								}
							})
						}
					})

				case 2:
					// Tab 2: Keyboard Shortcuts
					ui.Scroll(c.Key(fmt.Sprintf("settings_scroll_%d", state.ActiveTab))).Fill().MinHeight(0).Gap(16).Children(func() {
						ui.Text(c, "全局快捷键:").FontSize(15).Bold().TextColor(th.TextMain)
						ui.Text(c, "开启后，MusicGo 在后台也能响应这些快捷键。Wayland 桌面会弹出确认窗口，这是桌面系统的要求。").FontSize(12).TextColor(th.TextSub)
						ui.Row(c).FillWidth().Wrap().Gap(12).AlignItems(ui.Center).Justify(ui.Start).Children(func() {
							globalEnabled := cfg.EnableGlobalShortcuts
							switchEl := ui.Switch(c.Key("toggle_global_shortcuts"), &globalEnabled).Label("启用全局快捷键开关").Cursor(ui.CursorPointer)
							ui.Text(c, "启用全局快捷键:").FontSize(13).TextColor(th.TextSub).Width(110)
							if switchEl.Changed() {
								_ = configMgr.UpdateAsync(func(c *store.AppConfig) {
									c.EnableGlobalShortcuts = globalEnabled
								})
								if onGlobalShortcutsToggle != nil {
									onGlobalShortcutsToggle(globalEnabled)
								}
							}
						})
						if hotkeyStatus != nil {
							msg, state := hotkeyStatus()
							msgCol := th.TextSub
							switch state {
							case "error":
								msgCol = th.Danger
							}
							ui.Text(c, msg).FontSize(12).TextColor(msgCol)
						}
						ui.Row(c).FillWidth().Wrap().Gap(12).AlignItems(ui.Center).Children(func() {
							ui.Text(c, "播放 / 暂停:").FontSize(13).TextColor(th.TextSub).Width(110)
							ui.Text(c, cfg.Hotkeys.PlayPause).FontSize(13).TextColor(th.TextMain)
						})
						ui.Row(c).FillWidth().Wrap().Gap(12).AlignItems(ui.Center).Children(func() {
							ui.Text(c, "下一首 / 上一首:").FontSize(13).TextColor(th.TextSub).Width(110)
							ui.Text(c, fmt.Sprintf("%s / %s", cfg.Hotkeys.NextTrack, cfg.Hotkeys.PrevTrack)).FontSize(13).TextColor(th.TextMain)
						})
						ui.Row(c).FillWidth().Wrap().Gap(12).AlignItems(ui.Center).Children(func() {
							ui.Text(c, "音量增减:").FontSize(13).TextColor(th.TextSub).Width(110)
							ui.Text(c, fmt.Sprintf("%s / %s", cfg.Hotkeys.VolumeUp, cfg.Hotkeys.VolumeDown)).FontSize(13).TextColor(th.TextMain)
						})
					})

				case 3:
					// Tab 3: Download Settings
					ui.Scroll(c.Key(fmt.Sprintf("settings_scroll_%d", state.ActiveTab))).Fill().MinHeight(0).Gap(16).Children(func() {
						ui.Text(c, "歌曲下载设置:").FontSize(15).Bold().TextColor(th.TextMain)

						ui.Row(c).FillWidth().Wrap().Gap(12).AlignItems(ui.Center).Children(func() {
							ui.Text(c, "下载存储目录:").FontSize(13).TextColor(th.TextSub).Width(110)
							ui.TextInput(c.Key("dl_path_input"), &state.DownloadPathInput).Grow(1).MinWidth(120)

							saveBtn := components.PrimaryButton(c.Key("dl_path_save"), th, "保存目录").Cursor(ui.CursorPointer)
							if saveBtn.Clicked() {
								if state.DownloadPathInput != "" {
									_ = configMgr.UpdateAsync(func(c *store.AppConfig) {
										c.Download.DownloadPath = state.DownloadPathInput
									})
									state.SetStatus("下载目录已保存", false)
								}
							}

							openBtn := components.Button(c.Key("dl_path_open"), "打开目录").Cursor(ui.CursorPointer)
							if openBtn.Clicked() {
								go openFolderInSystem(state.DownloadPathInput)
							}
						})

						ui.Row(c).FillWidth().Wrap().Gap(12).AlignItems(ui.Center).Children(func() {
							ui.Text(c, "最大并发下载数:").FontSize(13).TextColor(th.TextSub).Width(110)
							for _, count := range []int{1, 2, 3, 5} {
								val := count
								btn := ui.ButtonBase(c.Key(fmt.Sprintf("dl_concur_%d", val))).
									Height(36).Padding(0, 14).
									Radius(10).
									Cursor(ui.CursorPointer)
								if cfg.Download.MaxConcurrent == val {
									btn.Background(th.Primary)
									btn.Children(func() {
										ui.Text(c, fmt.Sprintf("%d 个任务", val)).FontSize(12).TextColor(th.OnPrimary())
									})
								} else {
									btn.Background(th.HoverBg)
									btn.Children(func() {
										ui.Text(c, fmt.Sprintf("%d 个任务", val)).FontSize(12).TextColor(th.TextMain)
									})
									if btn.Clicked() {
										_ = configMgr.UpdateAsync(func(c *store.AppConfig) {
											c.Download.MaxConcurrent = val
										})
									}
								}
							}
						})

						ui.Row(c).FillWidth().Wrap().Gap(12).AlignItems(ui.Center).Children(func() {
							ui.Text(c, "自动保存歌词:").FontSize(13).TextColor(th.TextSub).Width(110)
							btn := ui.ButtonBase(c.Key("dl_auto_lyrics_btn")).
								Height(36).Padding(0, 14).
								Radius(10).
								Cursor(ui.CursorPointer)
							if cfg.Download.AutoDownloadLyrics {
								btn.Background(th.Primary)
								btn.Children(func() {
									ui.Text(c, "已开启 (保存同名 .lrc 文件)").FontSize(12).TextColor(th.OnPrimary())
								})
							} else {
								btn.Background(th.HoverBg)
								btn.Children(func() {
									ui.Text(c, "已关闭").FontSize(12).TextColor(th.TextMain)
								})
							}
							if btn.Clicked() {
								_ = configMgr.UpdateAsync(func(c *store.AppConfig) {
									c.Download.AutoDownloadLyrics = !c.Download.AutoDownloadLyrics
								})
							}
						})
					})

				case 4:
					// Tab 4: Backup
					ui.Scroll(c.Key(fmt.Sprintf("settings_scroll_%d", state.ActiveTab))).Fill().MinHeight(0).Gap(16).Children(func() {
						ui.Text(c, "MusicFree 兼容数据备份:").FontSize(15).Bold().TextColor(th.TextMain)

						ui.Row(c).FillWidth().Wrap().Gap(12).AlignItems(ui.Center).Children(func() {
							ui.Text(c, "备份文件路径:").FontSize(14).TextColor(th.TextSub)
							ui.TextInput(c.Key("backup_path_input"), &state.BackupPath).Grow(1).MinWidth(120)
						})

						ui.Row(c).Gap(16).Children(func() {
							if components.PrimaryButton(c.Key("export_btn"), th, "📤 导出备份 (JSON)").Cursor(ui.CursorPointer).Clicked() {
								path := state.BackupPath
								// The file is written on its own goroutine: the disk
								// is slow and the interface must not wait for it.
								go exportBackup(state, st, path)
							}

							if components.Button(c.Key("import_btn"), "📥 导入 MusicFree 备份").Cursor(ui.CursorPointer).Clicked() {
								path := state.BackupPath
								go importBackup(state, st, path)
							}
						})
					})
				}
			})
	})
}

// exportBackup writes the backup to a file, off the interface thread.
func exportBackup(state *SettingsViewState, st *store.Store, path string) {
	if st == nil {
		state.SetStatus("数据存储未就绪", true)
		return
	}
	state.SetBusy(true)
	state.SetStatus("正在导出备份...", false)

	backup, err := st.ExportBackup()
	if err != nil {
		state.SetBusy(false)
		state.SetStatus("导出失败: "+err.Error(), true)
		return
	}

	data, err := json.MarshalIndent(backup, "", "  ")
	if err != nil {
		state.SetBusy(false)
		state.SetStatus("序列化备份失败: "+err.Error(), true)
		return
	}

	if err := os.WriteFile(path, data, 0644); err != nil {
		state.SetBusy(false)
		state.SetStatus("写入文件失败: "+err.Error(), true)
		return
	}

	state.SetBusy(false)
	state.SetStatus("成功导出备份至: "+path, false)
}

// importBackup reads a backup file and merges it into the store, off the
// interface thread.
func importBackup(state *SettingsViewState, st *store.Store, path string) {
	if st == nil {
		state.SetStatus("数据存储未就绪", true)
		return
	}
	state.SetBusy(true)
	state.SetStatus("正在导入备份...", false)

	data, err := os.ReadFile(path)
	if err != nil {
		state.SetBusy(false)
		state.SetStatus("读取文件失败: "+err.Error(), true)
		return
	}

	var backup store.BackupData
	if err := json.Unmarshal(data, &backup); err != nil {
		state.SetBusy(false)
		state.SetStatus("解析备份 JSON 失败: "+err.Error(), true)
		return
	}

	if err := st.ImportBackup(&backup); err != nil {
		state.SetBusy(false)
		state.SetStatus("导入失败: "+err.Error(), true)
		return
	}

	state.SetBusy(false)
	state.SetStatus("成功导入备份数据！", false)
}
