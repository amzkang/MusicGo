package sys

import (
	"fmt"
	"sync"

	"github.com/egoist/mygo"
	"musicgo/internal/player"
	"musicgo/internal/store"
)

// HotkeyManager handles media actions and tray menu integration
type HotkeyManager struct {
	mu           sync.Mutex
	eng          *player.Engine
	tray         *mygo.Tray
	onToggleWin  func()
	onToggleDesk func()
	registered   []string
	lastErr      error
	enabled      bool
}

// NewHotkeyManager creates a hotkey and system tray manager
func NewHotkeyManager(
	eng *player.Engine,
	onToggleWin func(),
	onToggleDesk func(),
) *HotkeyManager {
	hm := &HotkeyManager{
		eng:          eng,
		onToggleWin:  onToggleWin,
		onToggleDesk: onToggleDesk,
	}
	return hm
}

// SetupTray configures the system tray menu
func (hm *HotkeyManager) SetupTray(iconBytes []byte) error {
	menu := mygo.NewMenu([]*mygo.MenuItem{
		{
			Label:    "MusicGo 原生播放器",
			Disabled: true,
		},
		mygo.Separator(),
		{
			Label:       "播放 / 暂停",
			Accelerator: "Space",
			Click: func(item *mygo.MenuItem, win *mygo.Window) {
				hm.eng.TogglePlay()
			},
		},
		{
			Label:       "下一首",
			Accelerator: "Right",
			Click: func(item *mygo.MenuItem, win *mygo.Window) {
				hm.eng.Next()
			},
		},
		{
			Label:       "上一首",
			Accelerator: "Left",
			Click: func(item *mygo.MenuItem, win *mygo.Window) {
				hm.eng.Prev()
			},
		},
		mygo.Separator(),
		{
			Label: "显示 / 隐藏主窗口",
			Click: func(item *mygo.MenuItem, win *mygo.Window) {
				if hm.onToggleWin != nil {
					hm.onToggleWin()
				}
			},
		},
		{
			Label: "切换桌面歌词",
			Click: func(item *mygo.MenuItem, win *mygo.Window) {
				if hm.onToggleDesk != nil {
					hm.onToggleDesk()
				}
			},
		},
		mygo.Separator(),
		{
			Label: "退出 MusicGo",
			Click: func(item *mygo.MenuItem, win *mygo.Window) {
				mygo.App.Quit()
			},
		},
	})

	tray, err := mygo.NewTray(mygo.TrayOptions{
		Title:   "",
		ToolTip: "MusicGo 原生播放器",
		Icon:    iconBytes,
		Menu:    menu,
	})
	if err != nil {
		return err
	}
	hm.tray = tray
	return nil
}

// SetupHotkeys registers the global shortcuts of the configuration, so the
// player answers even when it is in the background.
//
// The shortcuts are only bound when the user asked for them: on Wayland the
// desktop binds them through a portal, which asks the user to confirm them,
// so an application that binds them on every start makes the desktop ask
// again and again.
func (hm *HotkeyManager) SetupHotkeys(cfg store.HotkeyConfig) error {
	bindings := []struct {
		acc string
		fn  func()
	}{
		{cfg.PlayPause, hm.eng.TogglePlay},
		{cfg.NextTrack, hm.eng.Next},
		{cfg.PrevTrack, hm.eng.Prev},
		{cfg.VolumeUp, func() { hm.eng.SetVolume(hm.eng.Volume() + 0.05) }},
		{cfg.VolumeDown, func() { hm.eng.SetVolume(hm.eng.Volume() - 0.05) }},
	}

	var firstErr error
	var bound []string
	for _, b := range bindings {
		if b.acc == "" {
			continue
		}
		if mygo.GlobalShortcut.IsRegistered(b.acc) {
			continue
		}
		// Register waits for the main thread, which draws the interface
		// and asks for the status, so the lock must not be held here.
		if err := mygo.GlobalShortcut.Register(b.acc, b.fn); err != nil {
			if firstErr == nil {
				firstErr = fmt.Errorf("注册快捷键 %q 失败: %w", b.acc, err)
			}
			continue
		}
		bound = append(bound, b.acc)
	}

	hm.mu.Lock()
	hm.registered = append(hm.registered, bound...)
	hm.enabled = true
	hm.lastErr = firstErr
	hm.mu.Unlock()
	return firstErr
}

// Enable binds the global shortcuts of the configuration, when they are not
// bound yet. The user asked for them, so the desktop asking them to confirm
// them is expected.
func (hm *HotkeyManager) Enable(cfg store.HotkeyConfig) error {
	hm.mu.Lock()
	enabled := hm.enabled
	hm.mu.Unlock()
	if enabled {
		return nil
	}
	return hm.SetupHotkeys(cfg)
}

// Disable gives the global shortcuts back to the system, so the desktop
// stops asking for them and they no longer reach the player.
func (hm *HotkeyManager) Disable() {
	hm.mu.Lock()
	accs := hm.registered
	hm.registered = nil
	hm.enabled = false
	hm.lastErr = nil
	hm.mu.Unlock()

	for _, acc := range accs {
		mygo.GlobalShortcut.Unregister(acc)
	}
}

// The states of the global shortcuts, which the settings show.
const (
	// HotkeyOff means the user did not ask for the shortcuts.
	HotkeyOff = "off"
	// HotkeyOK means the shortcuts are bound.
	HotkeyOK = "ok"
	// HotkeyError means the system refused some of them.
	HotkeyError = "error"
)

// Status tells whether the global shortcuts are bound, and why they are not
// when they are not.
func (hm *HotkeyManager) Status() (message string, state string) {
	hm.mu.Lock()
	defer hm.mu.Unlock()

	if !hm.enabled {
		return "全局快捷键未启用", HotkeyOff
	}
	// A shortcut the system refused is reported first: the others may be
	// bound, but the player does not answer all the keys it should.
	if hm.lastErr != nil {
		return "全局快捷键不可用: " + hm.lastErr.Error(), HotkeyError
	}
	if len(hm.registered) > 0 {
		return fmt.Sprintf("已绑定 %d 个全局快捷键", len(hm.registered)), HotkeyOK
	}
	return "全局快捷键尚未绑定", HotkeyError
}
