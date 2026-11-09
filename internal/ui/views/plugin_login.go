package views

import (
	"bytes"
	"context"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"net/http"
	"net/url"
	"time"

	qrcode "github.com/skip2/go-qrcode"

	"github.com/egoist/mygo/ui"
	"musicgo/internal/plugin"
	"musicgo/internal/store"
	"musicgo/internal/ui/components"
	"musicgo/internal/ui/icons"
	"musicgo/internal/ui/theme"
)

// Login phases shown in the login modal.
const (
	loginPhaseLoading = "loading"
	loginPhaseQR      = "qr"
	loginPhaseScanned = "scanned"
	loginPhaseExpired = "expired"
	loginPhaseError   = "error"
)

// OpenLogin opens the login modal for the platform and invalidates any flow
// already running. It returns the new sequence number the flow must carry.
func (s *PluginManagerViewState) OpenLogin(platform string) uint64 {
	s.mu.Lock()
	s.loginSeq++
	s.ShowLoginModal = true
	s.LoginPlatform = platform
	s.LoginPhase = loginPhaseLoading
	s.LoginMsg = ""
	s.LoginQR = nil
	s.loginCloseReq = false
	seq := s.loginSeq
	s.mu.Unlock()
	return seq
}

// CloseLoginUI closes the modal and stops the running flow. It is called on
// the UI thread.
func (s *PluginManagerViewState) CloseLoginUI() {
	s.mu.Lock()
	s.loginSeq++
	s.ShowLoginModal = false
	s.loginCloseReq = false
	s.mu.Unlock()
}

// RequestLoginClose asks the UI thread to close the modal; background
// goroutines must not write ShowLoginModal directly.
func (s *PluginManagerViewState) RequestLoginClose() {
	s.mu.Lock()
	s.loginSeq++
	s.loginCloseReq = true
	s.mu.Unlock()
}

// LoginCloseRequested reports and clears a close request. UI thread only.
func (s *PluginManagerViewState) LoginCloseRequested() bool {
	s.mu.Lock()
	req := s.loginCloseReq
	if req {
		s.loginCloseReq = false
		s.ShowLoginModal = false
	}
	s.mu.Unlock()
	return req
}

// LoginOpen tells whether the modal is shown.
func (s *PluginManagerViewState) LoginOpen() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.ShowLoginModal
}

// LoginSeqCurrent tells whether seq is still the running flow.
func (s *PluginManagerViewState) LoginSeqCurrent(seq uint64) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.loginSeq == seq
}

// SetLoginPhase records the phase and message of the login flow.
func (s *PluginManagerViewState) SetLoginPhase(phase, msg string) {
	s.mu.Lock()
	s.LoginPhase = phase
	s.LoginMsg = msg
	s.mu.Unlock()
}

// SetLoginQR shows the QR bitmap and its caption.
func (s *PluginManagerViewState) SetLoginQR(bm *ui.Bitmap, caption string) {
	s.mu.Lock()
	s.LoginQR = bm
	s.LoginPhase = loginPhaseQR
	s.LoginMsg = caption
	s.mu.Unlock()
}

// LoginSnapshot returns what the modal draws.
func (s *PluginManagerViewState) LoginSnapshot() (platform, phase, msg string, qr *ui.Bitmap) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.LoginPlatform, s.LoginPhase, s.LoginMsg, s.LoginQR
}

// SetLoginState records a platform's login state message; empty clears it.
func (s *PluginManagerViewState) SetLoginState(platform, msg string) {
	s.mu.Lock()
	if s.LoginStates == nil {
		s.LoginStates = make(map[string]string)
	}
	s.LoginStates[platform] = msg
	s.mu.Unlock()
}

// LoginState returns a platform's login state message, empty when logged out
// or unknown.
func (s *PluginManagerViewState) LoginState(platform string) string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.LoginStates[platform]
}

// BeginLoginStatesRefresh starts a refresh for revision rev if the cached
// states are stale and no refresh is running. It tells whether it started.
func (s *PluginManagerViewState) BeginLoginStatesRefresh(rev uint64) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.loginStatesBusy || s.loginStatesRev == rev {
		return false
	}
	s.loginStatesBusy = true
	s.loginStatesRev = rev
	return true
}

// EndLoginStatesRefresh marks the refresh done.
func (s *PluginManagerViewState) EndLoginStatesRefresh() {
	s.mu.Lock()
	s.loginStatesBusy = false
	s.mu.Unlock()
}

// beginPluginLogin opens the modal and runs the QR login flow in the
// background, off the UI thread.
func beginPluginLogin(state *PluginManagerViewState, pm *plugin.Manager, configMgr *store.ConfigManager, platform string, invalidate func()) {
	seq := state.OpenLogin(platform)
	go runPluginLogin(state, pm, configMgr, platform, seq, invalidate)
}

// runPluginLogin obtains the QR bitmap and starts polling in the background.
func runPluginLogin(state *PluginManagerViewState, pm *plugin.Manager, configMgr *store.ConfigManager, platform string, seq uint64, invalidate func()) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	qr, err := pm.GetLoginQR(ctx, platform)
	cancel()
	if !state.LoginSeqCurrent(seq) {
		return
	}
	if err != nil {
		state.SetLoginPhase(loginPhaseError, "获取二维码失败："+err.Error())
		requestLoginRedraw(invalidate)
		return
	}

	var picture image.Image
	if qr.QRText != "" {
		var code *qrcode.QRCode
		code, err = qrcode.New(qr.QRText, qrcode.Medium)
		if err == nil {
			picture = code.Image(460)
		}
	} else {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		picture, err = loadLoginQRImage(ctx, qr.QRImageURL)
		cancel()
	}
	if err != nil {
		if !state.LoginSeqCurrent(seq) {
			return
		}
		state.SetLoginPhase(loginPhaseError, "生成二维码失败："+err.Error())
		requestLoginRedraw(invalidate)
		return
	}
	bitmap := ui.NewBitmap(picture)
	bitmap.Pixelated()
	if !state.LoginSeqCurrent(seq) {
		return
	}
	caption := qr.Message
	if caption == "" {
		caption = "打开官方 App 扫码登录"
	}
	state.SetLoginQR(bitmap, caption)
	requestLoginRedraw(invalidate)

	pollPluginLogin(state, pm, configMgr, platform, qr.State, seq, invalidate)
}

func loadLoginQRImage(ctx context.Context, address string) (image.Image, error) {
	u, err := url.Parse(address)
	if err != nil || u.Scheme != "https" || u.Host == "" {
		return nil, fmt.Errorf("二维码图片地址无效")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Referer", u.Scheme+"://"+u.Host+"/")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("二维码图片请求失败：%s", resp.Status)
	}
	const maxQRBytes = 2 << 20
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxQRBytes+1))
	if err != nil || len(data) > maxQRBytes {
		return nil, fmt.Errorf("二维码图片读取失败或过大")
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || cfg.Width > 2048 || cfg.Height > 2048 {
		return nil, fmt.Errorf("二维码图片格式无效或尺寸过大")
	}
	picture, _, err := image.Decode(bytes.NewReader(data))
	return picture, err
}

// pollPluginLogin polls the plugin until the login is confirmed, the QR
// expires or the flow is superseded.
func pollPluginLogin(state *PluginManagerViewState, pm *plugin.Manager, configMgr *store.ConfigManager, platform, pollState string, seq uint64, invalidate func()) {
	failures := 0
	for {
		if !state.LoginSeqCurrent(seq) || !state.LoginOpen() {
			return
		}
		time.Sleep(2 * time.Second)
		if !state.LoginSeqCurrent(seq) || !state.LoginOpen() {
			return
		}

		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		res, err := pm.PollLoginQR(ctx, platform, pollState)
		cancel()
		if !state.LoginSeqCurrent(seq) {
			return
		}
		if err != nil {
			// One missed poll is not a failure: the network may blink. The
			// flow gives up after a few in a row.
			failures++
			if failures >= 3 {
				state.SetLoginPhase(loginPhaseError, "查询扫码状态失败："+err.Error())
				requestLoginRedraw(invalidate)
				return
			}
			continue
		}
		failures = 0

		switch res.Status {
		case "scanned":
			state.SetLoginPhase(loginPhaseScanned, "已扫码，请在手机上确认")
			requestLoginRedraw(invalidate)
		case "expired":
			state.SetLoginPhase(loginPhaseExpired, "二维码已过期，可刷新后重新扫码")
			requestLoginRedraw(invalidate)
			return
		case "confirmed":
			completePluginLogin(state, pm, configMgr, platform, res.UserVariables, invalidate)
			return
		}
	}
}

// completePluginLogin persists the variables the plugin reported, reloads the
// plugin with them and closes the modal.
func completePluginLogin(state *PluginManagerViewState, pm *plugin.Manager, configMgr *store.ConfigManager, platform string, vars map[string]string, invalidate func()) {
	accountData := pm.SupportsAccountData(platform)
	if configMgr != nil {
		_ = configMgr.UpdateAsync(func(cfg *store.AppConfig) {
			if cfg.PluginVars == nil {
				cfg.PluginVars = make(map[string]map[string]string)
			}
			cfg.PluginVars[platform] = vars
			if accountData {
				cfg.AccountPlatform = platform
				cfg.LibraryMode = "account"
			}
		})
	}
	if len(vars) > 0 {
		_, _ = pm.ApplyPluginVars(platform, vars)
	}
	msg := "已登录"
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	if status, err := pm.GetLoginStatus(ctx, platform); err == nil && status != nil && status.Message != "" {
		msg = status.Message
	}
	cancel()
	state.SetLoginState(platform, msg)
	state.SetStatus(platform+" 登录成功", false)
	state.RequestLoginClose()
	requestLoginRedraw(invalidate)
}

// logoutPlugin clears the stored variables of the platform and reloads the
// plugin without them.
func logoutPlugin(state *PluginManagerViewState, pm *plugin.Manager, configMgr *store.ConfigManager, platform string, invalidate func()) {
	go func() {
		if configMgr != nil {
			_ = configMgr.UpdateAsync(func(cfg *store.AppConfig) {
				delete(cfg.PluginVars, platform)
				if cfg.AccountPlatform == platform || cfg.AccountPlatform == "" {
					cfg.AccountPlatform = ""
					cfg.LibraryMode = "local"
				}
			})
		}
		_, _ = pm.ApplyPluginVars(platform, map[string]string{})
		state.SetLoginState(platform, "")
		state.SetStatus(platform+" 已退出登录", false)
		requestLoginRedraw(invalidate)
	}()
}

// maybeRefreshLoginStates refreshes the login state of every plugin that
// supports login, once per plugin revision.
func maybeRefreshLoginStates(state *PluginManagerViewState, pm *plugin.Manager, invalidate func()) {
	if pm == nil {
		return
	}
	if !state.BeginLoginStatesRefresh(pm.Revision()) {
		return
	}
	go func() {
		for _, meta := range pm.ListPlugins() {
			if !pm.SupportsLogin(meta.Platform) {
				continue
			}
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			status, err := pm.GetLoginStatus(ctx, meta.Platform)
			cancel()
			if err != nil || status == nil {
				state.SetLoginState(meta.Platform, "")
				continue
			}
			msg := ""
			if status.LoggedIn {
				msg = status.Message
				if msg == "" {
					msg = "已登录"
				}
			}
			state.SetLoginState(meta.Platform, msg)
		}
		state.EndLoginStatesRefresh()
		requestLoginRedraw(invalidate)
	}()
}

func requestLoginRedraw(invalidate func()) {
	if invalidate != nil {
		invalidate()
	}
}

// renderLoginModal draws the QR-login modal over the window.
func renderLoginModal(c *ui.Context, th *theme.Theme, state *PluginManagerViewState, pm *plugin.Manager, configMgr *store.ConfigManager, invalidate func()) {
	// A close asked for by a background goroutine lands here, on the UI
	// thread, which is the only writer of ShowLoginModal.
	if state.LoginCloseRequested() {
		requestLoginRedraw(invalidate)
	}
	if !state.LoginOpen() {
		return
	}

	ui.Modal(c, &state.ShowLoginModal, func() {
		platform, phase, msg, bitmap := state.LoginSnapshot()
		ui.Column(c.Key("plugin_login_modal")).Label("扫码登录弹窗").Width(280).Gap(16).Children(func() {
			ui.Text(c, "登录 "+platform).FillWidth().TextAlign(ui.Center).FontSize(18).Bold().TextColor(th.TextMain).SingleLine().Ellipsis("…")

			ui.Row(c).FillWidth().Justify(ui.Center).Children(func() {
				switch phase {
				case loginPhaseLoading:
					ui.Box(c).Size(220, 220).Radius(12).Background(th.HoverBg).Center().Children(func() {
						ui.Text(c, "正在获取二维码...").FontSize(13).TextColor(th.TextSub)
					})
				case loginPhaseError, loginPhaseExpired:
					ui.Box(c).Size(220, 220).Radius(12).Background(th.HoverBg).Center().Children(func() {
						ui.Icon(c, icons.Plugin).Size(32, 32).TextColor(th.TextMuted)
					})
				default:
					ui.Box(c).Size(220, 220).Radius(12).Background(ui.Hex("#ffffff")).Center().Children(func() {
						if bitmap != nil {
							ui.Image(c, bitmap).Size(204, 204).Fit(ui.Contain)
						}
					})
				}
			})

			statusCol := th.TextSub
			switch phase {
			case loginPhaseExpired, loginPhaseError:
				statusCol = th.Danger
			}
			if msg != "" {
				ui.Text(c, msg).FillWidth().TextAlign(ui.Center).FontSize(13).TextColor(statusCol).MaxLines(3).Ellipsis("…")
			}

			ui.Row(c).FillWidth().Justify(ui.Center).Gap(10).Children(func() {
				if phase != loginPhaseLoading {
					refresh := components.Button(c.Key("login_refresh"), "刷新二维码")
					if refresh.Clicked() && pm != nil {
						beginPluginLogin(state, pm, configMgr, platform, invalidate)
					}
				}
				cancel := components.Button(c.Key("login_cancel"), "取消")
				if cancel.Clicked() {
					state.CloseLoginUI()
					requestLoginRedraw(invalidate)
				}
			})
		})
	})
}

// LogoutAccount runs the plugin logout flow for callers outside the plugin
// page, such as the header account menu.
func LogoutAccount(state *PluginManagerViewState, pm *plugin.Manager, configMgr *store.ConfigManager, platform string, invalidate func()) {
	logoutPlugin(state, pm, configMgr, platform, invalidate)
}
