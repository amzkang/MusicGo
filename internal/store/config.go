package store

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"musicgo/internal/plugin"
)

// AppConfig represents software settings and state
type AppConfig struct {
	EnableTrackNotifications bool                 `json:"enableTrackNotifications"`
	Theme                    string               `json:"theme"`
	Volume                   float64              `json:"volume"`
	AudioCacheMB             int                  `json:"audioCacheMB"`
	SearchHistory            []string             `json:"searchHistory,omitempty"`
	PlaybackMode             int                  `json:"playbackMode"`
	PlaybackQuality          string               `json:"playbackQuality,omitempty"`
	PlaybackQueue            *PlaybackQueueConfig `json:"playbackQueue,omitempty"`
	Speed                    float64              `json:"speed"`
	Equalizer                map[string]float64   `json:"equalizer,omitempty"`
	DesktopLyric             DesktopLyricCfg      `json:"desktopLyric"`
	Hotkeys                  HotkeyConfig         `json:"hotkeys"`
	// EnableGlobalShortcuts binds the shortcuts to the system, so the
	// player answers them while it is in the background. It is off by
	// default: on Wayland the desktop binds them through a portal, which
	// asks the user to confirm them, so the application does not ask for
	// them before the user wants them.
	EnableGlobalShortcuts bool              `json:"enableGlobalShortcuts"`
	UserVariables         map[string]string `json:"userVariables"`
	// PluginVars holds the user variables of each plugin, keyed by platform.
	// The QR-login flow writes the variables a plugin reports on confirm, and
	// the plugin is reloaded with them.
	PluginVars map[string]map[string]string `json:"pluginVars,omitempty"`
	// LibraryMode tells whose library「我的音乐」shows: "local" keeps the
	// on-device favorites and playlists, "account" the logged-in plugin's
	// sheets. It falls back to "local" whenever no account is logged in.
	LibraryMode    string          `json:"libraryMode,omitempty"`
	EnabledPlugins []string        `json:"enabledPlugins"`
	CustomTheme    *CustomThemeCfg `json:"customTheme,omitempty"`
	// Nil preserves legacy precedence: custom colors apply outside dark mode.
	// An explicit choice can select a built-in theme without deleting its preset.
	UseCustomTheme *bool          `json:"useCustomTheme,omitempty"`
	Download       DownloadConfig `json:"download"`
}

func (c AppConfig) UsesCustomTheme() bool {
	if c.CustomTheme == nil {
		return false
	}
	if c.UseCustomTheme != nil {
		return *c.UseCustomTheme
	}
	return c.Theme != "dark"
}

// SelectTheme updates the choice while retaining the custom palette.
func (c *AppConfig) SelectTheme(mode string) {
	if mode != "light" && mode != "dark" && mode != "custom" {
		return
	}
	custom := mode == "custom" && c.CustomTheme != nil
	c.UseCustomTheme = &custom
	if mode != "custom" {
		c.Theme = mode
	}
}

// PlaybackQueueConfig keeps queue order and selection across restarts.
// A nil config means no queue has been saved; an empty Songs slice is intentional.
type PlaybackQueueConfig struct {
	Songs            []plugin.MusicItem `json:"songs"`
	Index            int                `json:"index"`
	PositionMs       int64              `json:"positionMs,omitempty"`
	ShuffleHistory   []int              `json:"shuffleHistory,omitempty"`
	ShuffleCursor    int                `json:"shuffleCursor,omitempty"`
	ShuffleCandidate *int               `json:"shuffleCandidate,omitempty"`
	NextIndices      []int              `json:"nextIndices,omitempty"`
}

// DownloadConfig holds song download settings
type DownloadConfig struct {
	DownloadPath       string `json:"downloadPath"`
	MaxConcurrent      int    `json:"maxConcurrent"`      // default: 3
	AutoDownloadLyrics bool   `json:"autoDownloadLyrics"` // default: true
	FileNamingPattern  string `json:"fileNamingPattern"`  // default: "{artist} - {title}"
	DefaultQuality     string `json:"defaultQuality"`     // default: "standard"
}

// DesktopLyricCfg holds desktop lyric window settings
type DesktopLyricCfg struct {
	Enabled       bool    `json:"enabled"`
	FontSize      float64 `json:"fontSize"`
	ColorPrimary  string  `json:"colorPrimary"`
	ColorInactive string  `json:"colorInactive"`
	Background    string  `json:"background"`
	Opacity       float64 `json:"opacity"`
	Lock          bool    `json:"lock"`
	X             int     `json:"x"`
	Y             int     `json:"y"`
	Width         int     `json:"width"`
	Height        int     `json:"height"`
}

// HotkeyConfig holds keyboard shortcuts
type HotkeyConfig struct {
	PlayPause  string `json:"playPause"`
	NextTrack  string `json:"nextTrack"`
	PrevTrack  string `json:"prevTrack"`
	VolumeUp   string `json:"volumeUp"`
	VolumeDown string `json:"volumeDown"`
}

// CustomThemeCfg holds color palette for theme v2
type CustomThemeCfg struct {
	Name            string  `json:"name"`
	BackgroundImage string  `json:"backgroundImage,omitempty"`
	BackgroundBlur  float64 `json:"backgroundBlur,omitempty"`
	ColorPrimary    string  `json:"colorPrimary"`
	ColorBg         string  `json:"colorBg"`
	ColorPanel      string  `json:"colorPanel"`
	ColorTextMain   string  `json:"colorTextMain"`
	ColorTextSub    string  `json:"colorTextSub"`
	ColorBorder     string  `json:"colorBorder"`
}

// ConfigManager handles loading and saving configuration
type ConfigManager struct {
	mu            sync.RWMutex
	saveMu        sync.Mutex
	timer         *time.Timer
	lastSaveError error
	loadError     error
	loadWarning   string
	filePath      string
	config        AppConfig
}

// DefaultConfig returns default application configuration
func DefaultConfig() AppConfig {
	return AppConfig{
		AudioCacheMB:    512,
		Theme:           "light",
		Volume:          0.8,
		PlaybackMode:    1, // RepeatAll
		PlaybackQuality: "standard",
		Speed:           1.0,
		Equalizer: map[string]float64{
			"bass":   0,
			"lowMid": 0,
			"mid":    0,
			"treble": 0,
		},
		DesktopLyric: DesktopLyricCfg{
			Enabled:       false,
			FontSize:      24,
			ColorPrimary:  "#FA586A",
			ColorInactive: "#FFFFFF",
			Background:    "#000000",
			Opacity:       0.6,
			Lock:          false,
			Width:         800,
			Height:        120,
		},
		Hotkeys: HotkeyConfig{
			PlayPause:  "Ctrl+Alt+Space",
			NextTrack:  "Ctrl+Alt+Right",
			PrevTrack:  "Ctrl+Alt+Left",
			VolumeUp:   "Ctrl+Alt+Up",
			VolumeDown: "Ctrl+Alt+Down",
		},
		UserVariables:  make(map[string]string),
		PluginVars:     make(map[string]map[string]string),
		EnabledPlugins: []string{},
		Download: DownloadConfig{
			DownloadPath:       defaultDownloadDir(),
			MaxConcurrent:      3,
			AutoDownloadLyrics: true,
			FileNamingPattern:  "{artist} - {title}",
			DefaultQuality:     "standard",
		},
	}
}

func defaultDownloadDir() string {
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		return filepath.Join(home, "Music", "MusicGo")
	}
	return filepath.Join(".data", "downloads")
}

// NewConfigManager initializes config from filePath
func NewConfigManager(filePath string) *ConfigManager {
	cm := &ConfigManager{
		filePath: filePath,
		config:   DefaultConfig(),
	}
	_ = cm.Load()
	return cm
}

// Load reads config from disk
func (cm *ConfigManager) Load() error {
	cm.saveMu.Lock()
	defer cm.saveMu.Unlock()
	cm.mu.Lock()
	defer cm.mu.Unlock()

	cm.loadWarning = ""
	if cm.filePath == "" {
		cm.loadError = nil
		return nil
	}

	data, err := os.ReadFile(cm.filePath)
	if err != nil {
		if os.IsNotExist(err) {
			cm.loadError = nil
		} else {
			cm.loadError = fmt.Errorf("read config: %w", err)
			cm.loadWarning = fmt.Sprintf("无法读取配置文件 %s，配置保存已禁用：%v", cm.filePath, err)
		}
		return err
	}

	cfg := DefaultConfig()
	var object map[string]json.RawMessage
	err = json.Unmarshal(data, &object)
	if err == nil && object == nil {
		err = fmt.Errorf("config must be a JSON object")
	}
	if err == nil {
		err = json.Unmarshal(data, &cfg)
	}
	if err != nil {
		// Never replace unreadable configuration unless its original bytes have
		// first been preserved in a separate, exclusively created file.
		backup, backupErr := os.CreateTemp(filepath.Dir(cm.filePath), filepath.Base(cm.filePath)+".corrupt-*.bak")
		if backupErr == nil {
			_, backupErr = backup.Write(data)
			if backupErr == nil {
				backupErr = backup.Sync()
			}
			if closeErr := backup.Close(); backupErr == nil {
				backupErr = closeErr
			}
			if backupErr != nil {
				_ = os.Remove(backup.Name())
			}
		}
		if backupErr != nil {
			cm.loadError = fmt.Errorf("preserve corrupt config: %w", backupErr)
			cm.loadWarning = fmt.Sprintf("配置文件 %s 损坏且无法备份，配置保存已禁用：%v", cm.filePath, backupErr)
			return fmt.Errorf("invalid config: %v; %w", err, cm.loadError)
		}
		cm.loadError = nil
		cm.loadWarning = fmt.Sprintf("配置文件损坏，原文件已备份至 %s", backup.Name())
		return fmt.Errorf("invalid config (backed up to %s): %w", backup.Name(), err)
	}
	cm.loadError = nil
	if cfg.Download.DownloadPath == "" {
		cfg.Download.DownloadPath = defaultDownloadDir()
	}
	if cfg.Download.MaxConcurrent <= 0 {
		cfg.Download.MaxConcurrent = 3
	}
	if cfg.Download.FileNamingPattern == "" {
		cfg.Download.FileNamingPattern = "{artist} - {title}"
	}
	if cfg.Download.DefaultQuality == "" {
		cfg.Download.DefaultQuality = "standard"
	}
	cfg.AudioCacheMB = min(2048, max(0, cfg.AudioCacheMB))
	cfg.SearchHistory = normalizeSearchHistory(cfg.SearchHistory)
	cm.config = cfg
	return nil
}

// Save writes config to disk
func (cm *ConfigManager) Save() error {
	cm.saveMu.Lock()
	defer cm.saveMu.Unlock()
	cm.mu.RLock()
	var data []byte
	err := cm.loadError
	if err == nil {
		data, err = json.MarshalIndent(cm.config, "", "  ")
	}
	path := cm.filePath
	cm.mu.RUnlock()
	if err == nil && path != "" {
		err = os.MkdirAll(filepath.Dir(path), 0755)
		if err == nil {
			var f *os.File
			f, err = os.CreateTemp(filepath.Dir(path), ".musicgo-config-*")
			if err == nil {
				tmp := f.Name()
				defer os.Remove(tmp)
				if _, err = f.Write(data); err == nil {
					err = f.Sync()
				}
				closeErr := f.Close()
				if err == nil {
					err = closeErr
				}
				if err == nil {
					err = os.Rename(tmp, path)
				}
			}
		}
	}
	cm.mu.Lock()
	cm.lastSaveError = err
	cm.mu.Unlock()
	return err
}

// UpdateAsync publishes memory immediately and coalesces disk writes.
func (cm *ConfigManager) UpdateAsync(fn func(*AppConfig)) error {
	cm.mu.Lock()
	defer cm.mu.Unlock()
	fn(&cm.config)
	if cm.timer != nil {
		cm.timer.Stop()
	}
	cm.timer = time.AfterFunc(300*time.Millisecond, func() { _ = cm.Save() })
	return nil
}
func (cm *ConfigManager) Flush(ctx context.Context) error {
	cm.mu.Lock()
	if cm.timer != nil {
		cm.timer.Stop()
	}
	cm.mu.Unlock()
	done := make(chan error, 1)
	go func() { done <- cm.Save() }()
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}
func (cm *ConfigManager) LastSaveError() error {
	cm.mu.RLock()
	defer cm.mu.RUnlock()
	return cm.lastSaveError
}

// LoadWarning reports a preserved corrupt configuration without exposing its contents.
func (cm *ConfigManager) LoadWarning() string {
	cm.mu.RLock()
	defer cm.mu.RUnlock()
	return cm.loadWarning
}

// Get returns current config snapshot
func (cm *ConfigManager) Get() AppConfig {
	cm.mu.RLock()
	defer cm.mu.RUnlock()
	return cm.config.clone()
}

// SettingsSnapshot returns a detached configuration without the playback queue.
// It is suitable for settings views that never inspect the saved queue.
func (cm *ConfigManager) SettingsSnapshot() AppConfig {
	cm.mu.RLock()
	defer cm.mu.RUnlock()
	cfg := cm.config
	cfg.PlaybackQueue = nil
	return cfg.clone()
}

// DownloadSettings returns the download settings without copying the playback queue.
func (cm *ConfigManager) DownloadSettings() DownloadConfig {
	cm.mu.RLock()
	defer cm.mu.RUnlock()
	return cm.config.Download
}

// LibraryModeValue returns just the library mode, for the frames that only
// need to know whose library「我的音乐」shows: "local", "account", or "" when
// the user has not chosen yet (then the logged-in account decides).
func (cm *ConfigManager) LibraryModeValue() string {
	cm.mu.RLock()
	defer cm.mu.RUnlock()
	if cm.config.LibraryMode == "account" {
		return "account"
	}
	if cm.config.LibraryMode == "local" {
		return "local"
	}
	return ""
}

// Update modifies config and persists it
func (cm *ConfigManager) Update(fn func(*AppConfig)) error {
	cm.mu.Lock()
	fn(&cm.config)
	cm.mu.Unlock()
	return cm.Save()
}

// clone copies the configuration, so a caller that changes a map or a slice
// it got from Get does not change the configuration itself.
func (c AppConfig) clone() AppConfig {
	out := c
	out.SearchHistory = append([]string(nil), c.SearchHistory...)
	if c.UserVariables != nil {
		out.UserVariables = make(map[string]string, len(c.UserVariables))
		for k, v := range c.UserVariables {
			out.UserVariables[k] = v
		}
	}
	if c.PluginVars != nil {
		out.PluginVars = make(map[string]map[string]string, len(c.PluginVars))
		for platform, vars := range c.PluginVars {
			copied := make(map[string]string, len(vars))
			for k, v := range vars {
				copied[k] = v
			}
			out.PluginVars[platform] = copied
		}
	}
	if c.EnabledPlugins != nil {
		out.EnabledPlugins = append([]string{}, c.EnabledPlugins...)
	}
	if c.LibraryMode != "" {
		out.LibraryMode = c.LibraryMode
	}
	if c.Equalizer != nil {
		out.Equalizer = make(map[string]float64, len(c.Equalizer))
		for k, v := range c.Equalizer {
			out.Equalizer[k] = v
		}
	}
	if c.PlaybackQueue != nil {
		queue := *c.PlaybackQueue
		queue.ShuffleHistory = append([]int(nil), queue.ShuffleHistory...)
		queue.NextIndices = append([]int(nil), queue.NextIndices...)
		if queue.ShuffleCandidate != nil {
			candidate := *queue.ShuffleCandidate
			queue.ShuffleCandidate = &candidate
		}
		queue.Songs = make([]plugin.MusicItem, len(c.PlaybackQueue.Songs))
		for i := range queue.Songs {
			queue.Songs[i] = c.PlaybackQueue.Songs[i].Clone()
		}
		out.PlaybackQueue = &queue
	}
	if c.CustomTheme != nil {
		ct := *c.CustomTheme
		out.CustomTheme = &ct
	}
	if c.UseCustomTheme != nil {
		custom := *c.UseCustomTheme
		out.UseCustomTheme = &custom
	}
	return out
}

// UnmarshalJSON tolerates malformed optional session fields without discarding
// the legacy queue. Songs/index retain their original validation behavior.
func (q *PlaybackQueueConfig) UnmarshalJSON(data []byte) error {
	var raw struct {
		Songs            []plugin.MusicItem `json:"songs"`
		Index            int                `json:"index"`
		PositionMs       json.RawMessage    `json:"positionMs"`
		ShuffleHistory   json.RawMessage    `json:"shuffleHistory"`
		ShuffleCursor    json.RawMessage    `json:"shuffleCursor"`
		ShuffleCandidate json.RawMessage    `json:"shuffleCandidate"`
		NextIndices      json.RawMessage    `json:"nextIndices"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	*q = PlaybackQueueConfig{Songs: raw.Songs, Index: raw.Index}
	if len(raw.NextIndices) > 0 {
		if err := json.Unmarshal(raw.NextIndices, &q.NextIndices); err != nil {
			q.NextIndices = nil
		}
	}
	if len(raw.PositionMs) > 0 {
		if err := json.Unmarshal(raw.PositionMs, &q.PositionMs); err != nil {
			q.PositionMs = 0
		}
	}
	valid := true
	if len(raw.ShuffleHistory) > 0 {
		valid = json.Unmarshal(raw.ShuffleHistory, &q.ShuffleHistory) == nil && valid
	}
	if len(raw.ShuffleCursor) > 0 {
		valid = json.Unmarshal(raw.ShuffleCursor, &q.ShuffleCursor) == nil && valid
	}
	if len(raw.ShuffleCandidate) > 0 {
		valid = json.Unmarshal(raw.ShuffleCandidate, &q.ShuffleCandidate) == nil && valid
	}
	if !valid {
		q.ShuffleHistory = nil
		q.ShuffleCursor = 0
		q.ShuffleCandidate = nil
	}
	return nil
}
