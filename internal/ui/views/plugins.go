package views

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/egoist/mygo/ui"
	"musicgo/internal/plugin"
	"musicgo/internal/store"
	"musicgo/internal/ui/components"
	"musicgo/internal/ui/icons"
	"musicgo/internal/ui/theme"
)

// PluginManagerViewState holds state for plugin management
type PluginManagerViewState struct {
	mu sync.RWMutex

	InstallInput       string
	IsInstalling       bool
	StatusMsg          string
	IsError            bool
	ShowNetModal       bool
	showLocalInstaller bool
	NetworkURL         string
	LocalPath          string
	// TestingStatus holds the outcome of the test of each platform.
	TestingStatus map[string]string
	// TestingBroken tells the platforms whose source is broken.
	TestingBroken map[string]bool
	IsBatchClean  bool
	CleanConfirm  bool

	// —— Plugin QR login ——
	// ShowLoginModal is written on the UI thread only; background goroutines
	// go through loginSeq and loginCloseRequested.
	ShowLoginModal bool
	LoginPlatform  string
	LoginPhase     string
	LoginMsg       string
	LoginQR        *ui.Bitmap
	// LoginStates caches each platform's login state message; empty means
	// logged out or unknown.
	LoginStates     map[string]string
	loginSeq        uint64
	loginCloseReq   bool
	loginStatesRev  uint64
	loginStatesBusy bool
}

func (s *PluginManagerViewState) SetLocalInstaller(show bool) {
	s.mu.Lock()
	s.showLocalInstaller = show
	s.mu.Unlock()
}
func (s *PluginManagerViewState) LocalInstallerShown() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.showLocalInstaller
}

// NewPluginManagerViewState creates initial state
func NewPluginManagerViewState() *PluginManagerViewState {
	return &PluginManagerViewState{
		TestingStatus: make(map[string]string),
		TestingBroken: make(map[string]bool),
		LoginStates:   make(map[string]string),
	}
}

// SetStatus records the message shown next to the title.
func (s *PluginManagerViewState) SetStatus(msg string, isError bool) {
	s.mu.Lock()
	s.StatusMsg = msg
	s.IsError = isError
	s.mu.Unlock()
}

// Status returns the message shown next to the title.
func (s *PluginManagerViewState) Status() (string, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.StatusMsg, s.IsError
}

// SetInstalling records whether a plugin is being installed.
func (s *PluginManagerViewState) SetInstalling(installing bool) {
	s.mu.Lock()
	s.IsInstalling = installing
	s.mu.Unlock()
}

// Installing reports whether a plugin is being installed.
func (s *PluginManagerViewState) Installing() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.IsInstalling
}

// SetNetworkURL sets the address the plugin is downloaded from.
func (s *PluginManagerViewState) SetNetworkURL(url string) {
	s.mu.Lock()
	s.NetworkURL = url
	s.mu.Unlock()
}

// NetworkURLValue returns the address the plugin is downloaded from.
func (s *PluginManagerViewState) NetworkURLValue() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.NetworkURL
}

// SetShowNetModal shows or hides the network installer.
func (s *PluginManagerViewState) SetShowNetModal(show bool) {
	s.mu.Lock()
	s.ShowNetModal = show
	s.mu.Unlock()
}

// NetModalShown reports whether the network installer is shown.
func (s *PluginManagerViewState) NetModalShown() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.ShowNetModal
}

// SetTestStatus records the outcome of the test of a platform.
func (s *PluginManagerViewState) SetTestStatus(platform, msg string, broken bool) {
	s.mu.Lock()
	if s.TestingStatus == nil {
		s.TestingStatus = make(map[string]string)
	}
	if s.TestingBroken == nil {
		s.TestingBroken = make(map[string]bool)
	}
	s.TestingStatus[platform] = msg
	s.TestingBroken[platform] = broken
	s.mu.Unlock()
}

// TestStatus returns the outcome of the test of a platform.
func (s *PluginManagerViewState) TestStatus(platform string) (string, bool, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	msg, has := s.TestingStatus[platform]
	return msg, has, s.TestingBroken[platform]
}

// SetBatchClean records whether the plugins are being checked.
func (s *PluginManagerViewState) SetBatchClean(cleaning bool) {
	s.mu.Lock()
	s.IsBatchClean = cleaning
	if !cleaning {
		s.CleanConfirm = false
	}
	s.mu.Unlock()
}

// BatchCleaning reports whether the plugins are being checked.
func (s *PluginManagerViewState) BatchCleaning() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.IsBatchClean
}

// SetCleanConfirm records whether the cleanup waits for a confirmation.
func (s *PluginManagerViewState) SetCleanConfirm(confirm bool) {
	s.mu.Lock()
	s.CleanConfirm = confirm
	s.mu.Unlock()
}

// CleanConfirmed reports whether the cleanup waits for a confirmation.
func (s *PluginManagerViewState) CleanConfirmed() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.CleanConfirm
}

// RenderPluginManager renders the modern MusicFree plugin management view with source testing and cleanup
func RenderPluginManager(
	c *ui.Context,
	th *theme.Theme,
	state *PluginManagerViewState,
	pluginMgr *plugin.Manager,
	configMgr *store.ConfigManager,
	getImage func(string) *ui.Bitmap,
	invalidate func(),
) {
	var plugins []plugin.PluginMeta
	if pluginMgr != nil {
		plugins = pluginMgr.ListPlugins()
	}

	statusMsg, isError := state.Status()
	showNetModal := state.NetModalShown()
	networkURL := state.NetworkURLValue()

	maybeRefreshLoginStates(state, pluginMgr, invalidate)

	ui.Scroll(c).
		Fill().
		Padding(24, components.PagePadding(c), 32, components.PagePadding(c)).
		Gap(14).
		Children(func() {
			// 1. Installed plugins & Counter
			ui.Row(c).FillWidth().Wrap().MinHeight(36).AlignItems(ui.Center).Children(func() {
				components.PageTitle(c, th, "插件管理")
				ui.Box(c).Width(10)
				ui.Badge(c, fmt.Sprintf("%d 个已启用", len(plugins)))
				ui.Spacer(c)

				// Status message
				if statusMsg != "" {
					msgCol := th.TextSub
					if isError {
						msgCol = th.Danger
					}
					ui.Text(c, statusMsg).FontSize(12).TextColor(msgCol)
				}
			})

			// 2. Action Operations Bar
			ui.Row(c).FillWidth().Wrap().MinHeight(36).AlignItems(ui.Center).Gap(10).Children(func() {
				// Install buttons
				localBtn := components.Button(c, "从本地安装").Padding(0, 16).Cursor(ui.CursorPointer)
				if localBtn.Clicked() {
					state.SetShowNetModal(false)
					state.SetLocalInstaller(true)
					state.SetStatus("", false)
				}

				netBtn := components.Button(c, "从网络安装").Padding(0, 16).Cursor(ui.CursorPointer)
				if netBtn.Clicked() {
					state.SetShowNetModal(true)
					state.SetLocalInstaller(false)
					state.SetStatus("", false)
				}

				ui.Spacer(c)

				// "一键清理失效音源" button (Requirement 5)
				cleanLabel := "一键清理失效插件"
				if state.CleanConfirmed() {
					cleanLabel = "再次点击确认清理"
				}
				cleanBtn := components.Button(c.Key("clean_plugins"), cleanLabel)

				if cleanBtn.Clicked() && !state.BatchCleaning() && pluginMgr != nil {
					if !state.CleanConfirmed() {
						// The plugins are only removed when the user says so
						// twice: a network that is down must not take every
						// plugin with it.
						state.SetCleanConfirm(true)
						state.SetStatus("将只移除检测到音源失效的插件，请再次点击确认", false)
					} else {
						state.SetBatchClean(true)
						state.SetStatus("正在全量检测并清理失效音源插件...", false)

						go func() {
							ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
							defer cancel()

							var cleanedCount int
							var checkedCount int
							for _, p := range pluginMgr.ListPlugins() {
								// 内置插件不参与清理：它们没有本地副本可卸
								if pluginMetaIsBuiltin(p) {
									continue
								}
								res := pluginMgr.TestPlugin(ctx, p.Platform)
								checkedCount++
								state.SetTestStatus(p.Platform, res.Message, res.Broken)

								if res.Broken {
									_ = pluginMgr.RemovePlugin(p.Platform)
									cleanedCount++
								}
							}

							state.SetBatchClean(false)
							state.SetStatus(fmt.Sprintf("检测完成: 共检测 %d 个插件, 清理失效音源 %d 个", checkedCount, cleanedCount), false)

							if invalidate != nil {
								invalidate()
							}
						}()
					}
				}
			})

			if state.LocalInstallerShown() {
				// 3. Local plugin installer
				ui.Row(c).FillWidth().Wrap().MinHeight(36).AlignItems(ui.Center).Gap(10).Children(func() {
					ui.Text(c, "本地插件路径:").FontSize(12).Bold().TextColor(th.TextMain)
					ui.TextInputBase(c.Key("local_plugin_path"), &state.LocalPath).
						Label("本地插件路径").
						Grow(1).MinWidth(80).Shrink(1).
						Placeholder("输入 .js 插件文件的绝对路径").
						TextColor(th.TextMain)

					installBtn := components.PrimaryButton(c.Key("local_plugin_install"), th, "安装").
						Padding(0, 14).
						Cursor(ui.CursorPointer)
					if installBtn.Clicked() {
						path := strings.TrimSpace(state.LocalPath)
						if path == "" {
							state.SetStatus("请填写本地插件文件的路径", true)
						} else {
							go func() {
								installLocalPlugin(state, pluginMgr, path)
								if invalidate != nil {
									invalidate()
								}
							}()
						}
					}
				})

			}

			// 4. Optional inline network installer modal
			if showNetModal {
				ui.Row(c.Key("net_install_card")).
					FillWidth().Wrap().
					Background(th.CardBg).
					Radius(12).
					Border(1, th.Border).
					Padding(10, 14).
					AlignItems(ui.Center).
					Gap(10).
					Children(func() {
						ui.Text(c, "网络地址:").FontSize(12).Bold().TextColor(th.TextMain)
						ui.Row(c).
							Grow(1).MinWidth(100).
							Height(36).
							Border(1, th.Border).
							Radius(10).
							Padding(0, 8).
							AlignItems(ui.Center).
							Children(func() {
								ui.TextInputBase(c.Key("net_url_input"), &networkURL).
									Label("插件网络地址").
									Grow(1).
									Placeholder("输入插件网络URL (http://... 或 https://...)").
									TextColor(th.TextMain).
									OnChange(func() { state.SetNetworkURL(networkURL) })
							})

						confirmBtn := components.PrimaryButton(c, th, "下载并安装").
							Padding(0, 12).
							Cursor(ui.CursorPointer)
						if confirmBtn.Clicked() && networkURL != "" {
							state.SetNetworkURL(networkURL)
							installFromRemoteURL(state, pluginMgr, networkURL)
						}

						cancelBtn := components.Button(c, "取消").
							Padding(0, 12).
							Cursor(ui.CursorPointer)
						if cancelBtn.Clicked() {
							state.SetShowNetModal(false)
						}
					})
			}

			for _, group := range []struct {
				title   string
				builtin bool
			}{{"平台登录", true}, {"音源插件", false}} {
				ui.Text(c, group.title).FontSize(16).Bold().TextColor(th.TextMain)
				count := 0
				for _, meta := range plugins {
					if pluginMetaIsBuiltin(meta) == group.builtin {
						count++
					}
				}
				if count == 0 {
					ui.Text(c, "暂无"+group.title).FontSize(13).TextColor(th.TextMuted)
					continue
				}
				ui.Grid(c).FillWidth().Columns(max(1, min(2, int((components.PageContentWidth(c)+16)/396)))).Gap(16).Children(func() {
					for _, meta := range plugins {
						if pluginMetaIsBuiltin(meta) == group.builtin {
							renderPluginCard(c, th, state, meta, pluginMgr, configMgr, getImage, invalidate)
						}
					}
				})
			}
		})

	renderLoginModal(c, th, state, pluginMgr, configMgr, invalidate)
}

// pluginMetaIsBuiltin tells whether the plugin ships inside the app binary
// (hints.builtin): it cannot be uninstalled or cleaned away.
func pluginMetaIsBuiltin(meta plugin.PluginMeta) bool {
	v, _ := meta.Hints["builtin"].(bool)
	return v
}

func pluginMetaLogo(meta plugin.PluginMeta) string {
	v, _ := meta.Hints["logo"].(string)
	return v
}

func renderPluginCard(c *ui.Context, th *theme.Theme, state *PluginManagerViewState, meta plugin.PluginMeta, pm *plugin.Manager, configMgr *store.ConfigManager, getImage func(string) *ui.Bitmap, invalidate func()) {
	name := meta.Platform
	loginMsg := state.LoginState(name)
	supportsLogin := pm != nil && pm.SupportsLogin(name)
	isBuiltin := pluginMetaIsBuiltin(meta)
	ui.Column(c.Key("plugin_card_"+name)).Label("插件卡片："+name).MinWidth(0).MinHeight(164).Padding(18).Gap(12).
		Background(th.CardBg).Border(1, th.Border).Radius(12).Children(func() {
		ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(12).Children(func() {
			logoBox := ui.Box(c).Size(40, 40).Shrink(0).Radius(10).Background(th.Background).Center()
			logoBox.Children(func() {
				if bm := getImage(pluginMetaLogo(meta)); bm != nil {
					ui.Image(c, bm).Size(40, 40).Radius(10).Fit(ui.Contain)
					return
				}
				ui.Icon(c, icons.Plugin).Size(22, 22).TextColor(th.TextSub)
			})
			ui.Column(c).Grow(1).MinWidth(0).Gap(4).Children(func() {
				ui.Text(c, name).FontSize(15).Bold().TextColor(th.TextMain).SingleLine().Ellipsis("…")
				author := meta.Author
				if author == "" {
					author = "MusicFree 插件"
				}
				ui.Text(c, author).FontSize(12).TextColor(th.TextMuted).SingleLine().Ellipsis("…")
			})
			if supportsLogin && loginMsg != "" {
				ui.Badge(c, loginMsg).MaxWidth(96).Clip().Tooltip(loginMsg)
			}
		})
		description := meta.Description
		if description == "" {
			var capabilities []string
			names := map[string]string{"music": "单曲", "album": "专辑", "sheet": "歌单", "artist": "歌手", "lyric": "歌词"}
			for _, kind := range meta.SupportedSearchType {
				if label, ok := names[kind]; ok {
					capabilities = append(capabilities, label)
				}
			}
			description = "音乐搜索与播放"
			if len(capabilities) > 0 {
				description = "支持" + strings.Join(capabilities, "、") + "搜索"
			}
		}
		ui.Text(c, description).FontSize(12).TextColor(th.TextSub).SingleLine().Ellipsis("…")
		ui.Spacer(c)
		ui.Row(c).FillWidth().Wrap().AlignItems(ui.Center).Gap(8).Children(func() {
			version := meta.Version
			if version != "" {
				ui.Text(c, "v"+version).FontSize(11).TextColor(th.TextMuted).MaxWidth(85).SingleLine().Ellipsis("…")
			}
			ui.Spacer(c)
			test := components.Button(c.Key("test_"+name), "测试音源").Padding(0, 12).Cursor(ui.CursorPointer)
			if test.Clicked() && pm != nil {
				state.SetTestStatus(name, "测试中...", false)
				go func() {
					ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
					defer cancel()
					result := pm.TestPlugin(ctx, name)
					state.SetTestStatus(name, result.Message, result.Broken)
					if invalidate != nil {
						invalidate()
					}
				}()
			}
			if supportsLogin {
				label, key := "扫码登录", "login_"+name
				if loginMsg != "" {
					label, key = "退出登录", "logout_"+name
				}
				loginBtn := components.Button(c.Key(key), label).Padding(0, 12).Cursor(ui.CursorPointer)
				if loginBtn.Clicked() {
					if loginMsg != "" {
						logoutPlugin(state, pm, configMgr, name, invalidate)
					} else {
						beginPluginLogin(state, pm, configMgr, name, invalidate)
					}
				}
				if loginMsg != "" && configMgr != nil && pm.SupportsAccountData(name) {
					useAccount := components.Button(c.Key("use_account_"+name), "使用账号").Padding(0, 12).Cursor(ui.CursorPointer)
					if useAccount.Clicked() {
						_ = configMgr.UpdateAsync(func(cfg *store.AppConfig) {
							cfg.AccountPlatform = name
							cfg.LibraryMode = "account"
						})
						requestLoginRedraw(invalidate)
					}
				}
			}
			if !isBuiltin {
				remove := ui.ButtonBase(c.Key("del_"+name)).Label("卸载"+name).Size(32, 32).Radius(8).Center().Cursor(ui.CursorPointer).Tooltip("卸载插件")
				if remove.Hovered() {
					remove.Background(th.HoverBg)
				}
				remove.Children(func() { ui.Icon(c, icons.Trash).Size(14, 14).TextColor(th.TextMuted) })
				if remove.Clicked() && pm != nil {
					go func() {
						err := pm.RemovePlugin(name)
						if err != nil {
							state.SetStatus("卸载失败："+err.Error(), true)
						} else {
							state.SetStatus("已卸载 "+name, false)
						}
						if invalidate != nil {
							invalidate()
						}
					}()
				}
			}
		})
		if status, tested, broken := state.TestStatus(name); tested {
			color := th.TextSub
			if broken {
				color = th.Danger
			}
			ui.Text(c, status).FontSize(11).TextColor(color).SingleLine().Ellipsis("…")
		}
	})
}

// installLocalPlugin loads a plugin from a file on disk, off the interface
// thread.
func installLocalPlugin(state *PluginManagerViewState, pm *plugin.Manager, path string) {
	if pm == nil {
		state.SetStatus("插件管理器未就绪", true)
		return
	}
	state.SetStatus("正在安装本地插件: "+path, false)

	meta, err := pm.LoadPluginFile(path)
	if err != nil {
		state.SetStatus("解析并运行插件失败: "+err.Error(), true)
		return
	}
	state.SetStatus(fmt.Sprintf("成功安装插件: %s (v%s)", meta.Platform, meta.Version), false)
}

func installFromRemoteURL(state *PluginManagerViewState, pm *plugin.Manager, rawURL string) {
	if pm == nil {
		state.SetStatus("插件管理器未就绪", true)
		return
	}
	state.SetInstalling(true)
	state.SetStatus("正在从网络下载插件...", false)

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()

		req, err := http.NewRequestWithContext(ctx, "GET", rawURL, nil)
		if err != nil {
			state.SetInstalling(false)
			state.SetStatus("无效的插件 URL: "+err.Error(), true)
			return
		}

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			state.SetInstalling(false)
			state.SetStatus("下载插件失败: "+err.Error(), true)
			return
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			state.SetInstalling(false)
			state.SetStatus(fmt.Sprintf("下载插件失败: 服务器返回 %s", resp.Status), true)
			return
		}

		data, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
		if err != nil {
			state.SetInstalling(false)
			state.SetStatus("读取插件内容失败: "+err.Error(), true)
			return
		}

		tmpFile := filepath.Join(os.TempDir(), fmt.Sprintf("plugin_%d.js", time.Now().UnixNano()))
		if err := os.WriteFile(tmpFile, data, 0644); err != nil {
			state.SetInstalling(false)
			state.SetStatus("写入临时插件文件失败: "+err.Error(), true)
			return
		}
		defer os.Remove(tmpFile)

		meta, err := pm.LoadPluginFile(tmpFile)
		state.SetInstalling(false)
		if err != nil {
			state.SetStatus("解析并运行插件失败: "+err.Error(), true)
			return
		}
		state.SetShowNetModal(false)
		state.SetNetworkURL("")
		state.SetStatus(fmt.Sprintf("成功安装插件: %s (v%s)", meta.Platform, meta.Version), false)
	}()
}
