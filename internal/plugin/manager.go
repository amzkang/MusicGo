package plugin

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// Manager coordinates all installed MusicFree plugins
type Manager struct {
	mu        sync.RWMutex
	fileMu    sync.Mutex // serializes file loads, removal, and variable reloads
	plugins   map[string]*Instance
	pluginDir string
	revision  uint64
	// userVarsProvider gives the stored user variables of a platform. It is
	// injected from the outside, so the plugin layer does not depend on the
	// store.
	userVarsProvider func(platform string) map[string]string
	// storageProvider gives a platform's persistent localStorage, injected
	// the same way.
	storageProvider func(platform string) Storage
}

// NewManager creates a new plugin manager
func NewManager(pluginDir string) *Manager {
	if pluginDir != "" {
		_ = os.MkdirAll(pluginDir, 0755)
	}
	return &Manager{
		plugins:   make(map[string]*Instance),
		pluginDir: pluginDir,
	}
}

// SetUserVarsProvider wires the stored user variables in. It must be called
// before plugins are loaded, so instances are created with their variables.
func (m *Manager) SetUserVarsProvider(fn func(platform string) map[string]string) {
	m.mu.Lock()
	m.userVarsProvider = fn
	m.mu.Unlock()
}

func (m *Manager) storedUserVars(platform string) map[string]string {
	m.mu.RLock()
	fn := m.userVarsProvider
	m.mu.RUnlock()
	if fn == nil {
		return nil
	}
	return fn(platform)
}

// SetStorageProvider wires the per-plugin persistent storage in. It must be
// called before plugins are loaded, so instances are created with it.
func (m *Manager) SetStorageProvider(fn func(platform string) Storage) {
	m.mu.Lock()
	m.storageProvider = fn
	m.mu.Unlock()
}

func (m *Manager) storedStorage(platform string) Storage {
	m.mu.RLock()
	fn := m.storageProvider
	m.mu.RUnlock()
	if fn == nil {
		return nil
	}
	return fn(platform)
}

// StoredUserVars gives the stored user variables of a platform, so callers
// that know a plugin's platform up front can inject them in one load.
func (m *Manager) StoredUserVars(platform string) map[string]string {
	return m.storedUserVars(platform)
}

// LoadFromDir scans and loads all .js plugins in the directory
func (m *Manager) LoadFromDir() error {
	if m.pluginDir == "" {
		return nil
	}
	entries, err := os.ReadDir(m.pluginDir)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if !entry.IsDir() && filepath.Ext(entry.Name()) == ".js" {
			fullPath := filepath.Join(m.pluginDir, entry.Name())
			_, _ = m.LoadPluginFile(fullPath)
		}
	}
	return nil
}

// LoadPluginFile loads a plugin from a .js file on disk
func (m *Manager) LoadPluginFile(path string) (*PluginMeta, error) {
	m.fileMu.Lock()
	defer m.fileMu.Unlock()

	bytesData, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read plugin file failed: %w", err)
	}
	source := string(bytesData)
	inst, meta, err := createPluginInstance(source, nil, nil)
	if err != nil {
		return nil, err
	}

	// The platform is known only after initialization. Keep the discovery
	// instance private until the instance with stored variables and storage
	// is ready, so module-level localStorage reads see the persisted state.
	vars := m.storedUserVars(meta.Platform)
	storage := m.storedStorage(meta.Platform)
	if len(vars) > 0 || storage != nil {
		inst.Close()
		inst, meta, err = createPluginInstance(source, vars, storage)
		if err != nil {
			return nil, err
		}
	}

	if m.pluginDir != "" {
		targetFile, err := m.savePluginFile(path, meta.Platform, bytesData)
		if err != nil {
			inst.Close()
			return nil, fmt.Errorf("save plugin file failed: %w", err)
		}
		inst.filePath = targetFile
	}
	m.publishPlugin(inst, meta.Platform)
	return &meta, nil
}

// samePluginFile also recognizes relative paths, symlinks, and hard links.
func samePluginFile(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	absA, errA := filepath.Abs(a)
	absB, errB := filepath.Abs(b)
	if errA == nil && errB == nil && absA == absB {
		return true
	}
	infoA, errA := os.Stat(a)
	infoB, errB := os.Stat(b)
	return errA == nil && errB == nil && os.SameFile(infoA, infoB)
}

// savePluginFile preserves an existing platform's path on updates, and uses
// exclusive creation for new files so unrelated files are never overwritten.
func (m *Manager) savePluginFile(source, platform string, data []byte) (string, error) {
	m.mu.RLock()
	old := m.plugins[platform]
	oldPath := ""
	if old != nil {
		oldPath = old.filePath
	}
	shared := false
	for p, inst := range m.plugins {
		if p != platform && samePluginFile(oldPath, inst.filePath) {
			shared = true
			break
		}
	}
	m.mu.RUnlock()
	// Sources already in the plugin directory are discovered as they stand.
	sourceDir, _ := filepath.Abs(filepath.Dir(source))
	pluginDir, _ := filepath.Abs(m.pluginDir)
	if sourceDir != pluginDir && oldPath != "" && !shared {
		if !samePluginFile(source, oldPath) {
			if err := os.WriteFile(oldPath, data, 0644); err != nil {
				return "", err
			}
		}
		return oldPath, nil
	}
	base := filepath.Base(source)
	ext := filepath.Ext(base)
	stem := strings.TrimSuffix(base, ext)
	for n := 0; ; n++ {
		name := base
		if n > 0 {
			name = fmt.Sprintf("%s-%d%s", stem, n, ext)
		}
		target := filepath.Join(m.pluginDir, name)
		owned, shared := false, false
		m.mu.RLock()
		for p, inst := range m.plugins {
			if samePluginFile(inst.filePath, target) {
				if p == platform {
					owned = true
				} else {
					shared = true
				}
			}
		}
		m.mu.RUnlock()
		if shared {
			continue
		}
		if samePluginFile(source, target) {
			return target, nil
		}
		if owned {
			if err := os.WriteFile(target, data, 0644); err != nil {
				return "", err
			}
			return target, nil
		}
		file, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0644)
		if os.IsExist(err) {
			continue
		}
		if err != nil {
			return "", err
		}
		_, writeErr := file.Write(data)
		closeErr := file.Close()
		if writeErr != nil || closeErr != nil {
			_ = os.Remove(target)
			if writeErr != nil {
				return "", writeErr
			}
			return "", closeErr
		}
		return target, nil
	}
}

// LoadPluginSource loads a plugin from JavaScript source string
func (m *Manager) LoadPluginSource(source string, userVars map[string]string) (*PluginMeta, error) {
	m.fileMu.Lock()
	defer m.fileMu.Unlock()
	inst, meta, err := createPluginInstance(source, userVars, nil)
	if err != nil {
		return nil, err
	}
	if storage := m.storedStorage(meta.Platform); storage != nil {
		inst.Close()
		inst, meta, err = createPluginInstance(source, userVars, storage)
		if err != nil {
			return nil, err
		}
	}
	m.publishPlugin(inst, meta.Platform)
	return &meta, nil
}

func createPluginInstance(source string, userVars map[string]string, storage Storage) (*Instance, PluginMeta, error) {
	inst, err := NewInstanceWithStorage(source, userVars, storage)
	if err != nil {
		return nil, PluginMeta{}, err
	}
	meta := inst.Meta()
	if meta.Platform == "" {
		inst.Close()
		return nil, PluginMeta{}, fmt.Errorf("plugin did not export a platform name")
	}
	return inst, meta, nil
}

func (m *Manager) publishPlugin(inst *Instance, platform string) {
	m.mu.Lock()
	old := m.plugins[platform]
	m.plugins[platform] = inst
	m.revision++
	m.mu.Unlock()
	if old != nil {
		old.Close()
	}
}

// GetPlugin retrieves a plugin instance by platform name
func (m *Manager) GetPlugin(platform string) *Instance {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.plugins[platform]
}

// ListPlugins returns metadata for all loaded plugins, deterministically sorted by platform
func (m *Manager) ListPlugins() []PluginMeta {
	m.mu.RLock()
	defer m.mu.RUnlock()
	res := make([]PluginMeta, 0, len(m.plugins))
	for _, inst := range m.plugins {
		res = append(res, inst.Meta())
	}
	sort.Slice(res, func(i, j int) bool {
		return res[i].Platform < res[j].Platform
	})
	return res
}

// Revision changes whenever the installed plugin instances change.
func (m *Manager) Revision() uint64 {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.revision
}

// UninstallPlugin removes a plugin and cleans up its file on disk
func (m *Manager) UninstallPlugin(platform string) { _ = m.RemovePlugin(platform) }

// Search searches across specified platforms or all platforms concurrently
func (m *Manager) Search(ctx context.Context, keyword string, page int, searchType string, platforms ...string) ([]MusicItem, error) {
	m.mu.RLock()
	var targets []*Instance
	if len(platforms) > 0 {
		for _, p := range platforms {
			if inst, ok := m.plugins[p]; ok {
				targets = append(targets, inst)
			}
		}
	} else {
		for _, inst := range m.plugins {
			targets = append(targets, inst)
		}
	}
	m.mu.RUnlock()

	if len(targets) == 0 {
		return nil, fmt.Errorf("没有已安装的插件，无法搜索")
	}

	var wg sync.WaitGroup
	var outMu sync.Mutex
	var allItems []MusicItem
	var failures []string

	for _, inst := range targets {
		wg.Add(1)
		go func(target *Instance) {
			defer wg.Done()
			res, err := target.Search(ctx, keyword, page, searchType)
			outMu.Lock()
			defer outMu.Unlock()
			if err != nil {
				failures = append(failures, fmt.Sprintf("%s: %v", target.Meta().Platform, err))
				return
			}
			if res == nil || len(res.Data) == 0 {
				return
			}
			meta := target.Meta()
			for i := range res.Data {
				if res.Data[i].Platform == "" && meta.Platform != "" {
					res.Data[i].Platform = meta.Platform
				}
			}
			allItems = append(allItems, res.Data...)
		}(inst)
	}

	wg.Wait()
	if len(allItems) == 0 && len(failures) > 0 {
		return nil, fmt.Errorf("every plugin failed to search: %s", strings.Join(failures, "; "))
	}
	return allItems, nil
}

// GetMediaSource gets media source from the item's platform plugin
func (m *Manager) GetMediaSource(ctx context.Context, item MusicItem, quality string) (*MediaSource, error) {
	m.mu.RLock()
	inst, ok := m.plugins[item.Platform]
	m.mu.RUnlock()

	if !ok {
		return nil, fmt.Errorf("plugin for platform '%s' not found", item.Platform)
	}

	return inst.GetMediaSource(ctx, item, quality)
}

// GetLyric gets lyrics from the item's platform plugin
func (m *Manager) GetLyric(ctx context.Context, item MusicItem) (*LyricResult, error) {
	m.mu.RLock()
	inst, ok := m.plugins[item.Platform]
	m.mu.RUnlock()

	if ok {
		res, err := inst.GetLyric(ctx, item)
		if err == nil && res != nil && (res.RawLrc != "" || res.LxLrc != "") {
			return res, nil
		}
	}

	// If the item platform has no lyric, try lyric-dedicated plugins (like 歌词千寻)
	m.mu.RLock()
	var lyricPlugins []*Instance
	for _, inst := range m.plugins {
		for _, t := range inst.Meta().SupportedSearchType {
			if t == "lyric" {
				lyricPlugins = append(lyricPlugins, inst)
				break
			}
		}
	}
	m.mu.RUnlock()

	for _, lp := range lyricPlugins {
		searchRes, err := lp.Search(ctx, item.Title+" "+item.Artist, 1, "lyric")
		if err == nil && searchRes != nil && len(searchRes.Data) > 0 {
			firstItem := searchRes.Data[0]
			res, err := lp.GetLyric(ctx, firstItem)
			if err == nil && res != nil && (res.RawLrc != "" || res.LxLrc != "") {
				return res, nil
			}
		}
	}

	return &LyricResult{}, nil
}

// ImportMusicSheet imports a sheet from the given platform
func (m *Manager) ImportMusicSheet(ctx context.Context, platform string, urlOrId string) ([]MusicItem, error) {
	m.mu.RLock()
	inst, ok := m.plugins[platform]
	m.mu.RUnlock()

	if !ok {
		return nil, fmt.Errorf("plugin for platform '%s' not found", platform)
	}

	return inst.ImportMusicSheet(ctx, urlOrId)
}

// TopListPlatform is one plugin that gives leaderboards.
type TopListPlatform struct {
	// Platform is the name of the plugin.
	Platform string
	// Groups are the leaderboards of the plugin, grouped as it groups them.
	Groups []TopListGroup
}

// ListTopLists gathers the leaderboards of every plugin that gives them.
// The plugins are asked at the same time, and one that fails does not keep
// the others from showing up.
func (m *Manager) ListTopLists(ctx context.Context) ([]TopListPlatform, error) {
	m.mu.RLock()
	var targets []*Instance
	for _, inst := range m.plugins {
		if inst.HasTopLists() {
			targets = append(targets, inst)
		}
	}
	m.mu.RUnlock()

	if len(targets) == 0 {
		return nil, fmt.Errorf("没有安装支持排行榜的音源插件")
	}

	var wg sync.WaitGroup
	var outMu sync.Mutex
	var results []TopListPlatform
	var failures []string

	for _, inst := range targets {
		wg.Add(1)
		go func(target *Instance) {
			defer wg.Done()
			groups, err := target.GetTopLists(ctx)
			if err != nil {
				outMu.Lock()
				failures = append(failures, fmt.Sprintf("%s: %v", target.Meta().Platform, err))
				outMu.Unlock()
				return
			}
			if len(groups) == 0 {
				return
			}
			outMu.Lock()
			results = append(results, TopListPlatform{
				Platform: target.Meta().Platform,
				Groups:   groups,
			})
			outMu.Unlock()
		}(inst)
	}

	wg.Wait()

	// The plugins are asked in the order they were taken, which is not the
	// order they answer in: they are sorted, so the page does not jump.
	sort.Slice(results, func(i, j int) bool {
		return results[i].Platform < results[j].Platform
	})

	if len(results) == 0 {
		if len(failures) > 0 {
			return nil, fmt.Errorf("所有插件都未能返回排行榜: %s", strings.Join(failures, "; "))
		}
		return nil, fmt.Errorf("没有安装支持排行榜的音源插件")
	}
	return results, nil
}

// GetTopListDetail gets the songs of one leaderboard, from the plugin that
// gave it.
func (m *Manager) GetTopListDetail(ctx context.Context, item TopListItem) (*TopListDetail, error) {
	m.mu.RLock()
	inst, ok := m.plugins[item.Platform]
	m.mu.RUnlock()

	if !ok {
		return nil, fmt.Errorf("plugin for platform '%s' not found", item.Platform)
	}

	return inst.GetTopListDetail(ctx, item)
}

// RemovePlugin unloads a plugin and deletes its local file
func (m *Manager) RemovePlugin(platform string) error {
	m.fileMu.Lock()
	defer m.fileMu.Unlock()
	m.mu.Lock()
	inst, ok := m.plugins[platform]
	delete(m.plugins, platform)
	shared := false
	if ok {
		m.revision++
		for _, other := range m.plugins {
			if samePluginFile(inst.filePath, other.filePath) {
				shared = true
				break
			}
		}
	}
	m.mu.Unlock()
	if !ok {
		return fmt.Errorf("plugin '%s' not found", platform)
	}
	inst.Close()
	if inst.filePath != "" && !shared {
		if err := os.Remove(inst.filePath); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}

// SupportsLogin tells whether the plugin exports the QR-login hooks.
func (m *Manager) SupportsLogin(platform string) bool {
	m.mu.RLock()
	inst, ok := m.plugins[platform]
	m.mu.RUnlock()
	return ok && inst.HasLogin()
}

// GetLoginQR asks the plugin for a fresh login QR code.
func (m *Manager) GetLoginQR(ctx context.Context, platform string) (*LoginQR, error) {
	m.mu.RLock()
	inst, ok := m.plugins[platform]
	m.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("plugin for platform '%s' not found", platform)
	}
	return inst.GetLoginQR(ctx)
}

// PollLoginQR asks the plugin whether the QR code has been scanned.
func (m *Manager) PollLoginQR(ctx context.Context, platform string, state string) (*LoginPollResult, error) {
	m.mu.RLock()
	inst, ok := m.plugins[platform]
	m.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("plugin for platform '%s' not found", platform)
	}
	return inst.PollLoginQR(ctx, state)
}

// GetLoginStatus asks the plugin whether its stored variables still mean a
// logged-in session. A plugin without the hook yields a MethodError.
func (m *Manager) GetLoginStatus(ctx context.Context, platform string) (*LoginStatus, error) {
	m.mu.RLock()
	inst, ok := m.plugins[platform]
	m.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("plugin for platform '%s' not found", platform)
	}
	return inst.GetLoginStatus(ctx)
}

// SupportsAccountData tells whether the plugin exports the account-data
// hooks the account view needs.
func (m *Manager) SupportsAccountData(platform string) bool {
	m.mu.RLock()
	inst, ok := m.plugins[platform]
	m.mu.RUnlock()
	return ok && inst.HasMethod("getUserSheets") && inst.HasMethod("getSheetSongs")
}

// GetUserProfile asks the plugin for the account's public identity.
func (m *Manager) GetUserProfile(ctx context.Context, platform string) (*UserProfile, error) {
	m.mu.RLock()
	inst, ok := m.plugins[platform]
	m.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("plugin for platform '%s' not found", platform)
	}
	return inst.GetUserProfile(ctx)
}

// GetUserSheets asks the plugin for the account's own playlists.
func (m *Manager) GetUserSheets(ctx context.Context, platform string) ([]UserSheet, error) {
	m.mu.RLock()
	inst, ok := m.plugins[platform]
	m.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("plugin for platform '%s' not found", platform)
	}
	return inst.GetUserSheets(ctx)
}

// GetSheetSongs asks the plugin for one page of a sheet's songs.
func (m *Manager) GetSheetSongs(ctx context.Context, platform string, sheetID string, page int) (*SheetSongs, error) {
	m.mu.RLock()
	inst, ok := m.plugins[platform]
	m.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("plugin for platform '%s' not found", platform)
	}
	return inst.GetSheetSongs(ctx, sheetID, page)
}

// ApplyPluginVars replaces the plugin instance with one created from the
// same source and the given user variables. An empty map logs the plugin
// out; the replacement bumps the revision, so the UI refreshes on its own.
func (m *Manager) ApplyPluginVars(platform string, vars map[string]string) (*PluginMeta, error) {
	m.fileMu.Lock()
	defer m.fileMu.Unlock()
	m.mu.RLock()
	inst, ok := m.plugins[platform]
	m.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("plugin '%s' not found", platform)
	}
	replacement, meta, err := createPluginInstance(inst.Source(), vars, m.storedStorage(platform))
	if err != nil {
		return nil, err
	}
	if meta.Platform != platform {
		replacement.Close()
		return nil, fmt.Errorf("plugin platform changed from %q to %q", platform, meta.Platform)
	}
	replacement.filePath = inst.filePath
	m.publishPlugin(replacement, platform)
	return &meta, nil
}

// PluginTestResult is the outcome of testing whether a plugin can still
// give out a playable source.
type PluginTestResult struct {
	Kind TestFailureKind
	// OK tells the plugin works.
	OK bool
	// Message describes the outcome, for the user.
	Message string
	// Elapsed is how long the test took.
	Elapsed time.Duration
	// Broken tells the plugin itself is at fault, so it may be removed.
	// A network that is down or a plugin that simply has no match only
	// makes the test fail.
	Broken bool
}

// TestPluginSource tests whether the plugin can search and return a working media URL
func (m *Manager) TestPluginSource(ctx context.Context, platform string) (bool, string, time.Duration) {
	res := m.TestPlugin(ctx, platform)
	return res.OK, res.Message, res.Elapsed
}

// TestPlugin tests whether the plugin can search and return a working media
// URL, and tells a broken plugin from one that only could not be reached.
func (m *Manager) TestPlugin(ctx context.Context, platform string) PluginTestResult {
	start := time.Now()
	m.mu.RLock()
	inst, ok := m.plugins[platform]
	m.mu.RUnlock()

	if !ok || inst == nil {
		return PluginTestResult{Message: "插件未加载", Elapsed: time.Since(start)}
	}

	// 1. Test Search
	searchRes, err := inst.Search(ctx, "周杰伦", 1, "music")
	if err != nil {
		// The plugin could not be reached: it may be the network, so the
		// plugin is not taken for broken.
		return PluginTestResult{
			Message: "搜索失败: " + err.Error(),
			Broken:  isStructural(err), Kind: classifyTestFailure(err),
			Elapsed: time.Since(start),
		}
	}
	if searchRes == nil || len(searchRes.Data) == 0 {
		return PluginTestResult{
			Message: "搜索无结果",
			Elapsed: time.Since(start),
		}
	}

	// 2. Test GetMediaSource on first song
	song := searchRes.Data[0]
	media, err := inst.GetMediaSource(ctx, song, "standard")
	elapsed := time.Since(start)
	if err != nil {
		return PluginTestResult{
			Message: "音源解析失败: " + err.Error(),
			Elapsed: elapsed, Broken: isStructural(err), Kind: classifyTestFailure(err),
		}
	}
	if media == nil || media.URL == "" {
		return PluginTestResult{
			Message: "音源无法解析或已失效",
			Elapsed: elapsed,
			Broken:  false,
			Kind:    TestUnavailable,
		}
	}

	return PluginTestResult{
		OK:      true,
		Message: fmt.Sprintf("正常 (%dms)", elapsed.Milliseconds()),
		Elapsed: elapsed,
	}
}

// CloseAll closes all plugin instances
func (m *Manager) CloseAll() {
	m.fileMu.Lock()
	defer m.fileMu.Unlock()
	m.mu.Lock()
	instances := m.plugins
	m.plugins = make(map[string]*Instance)
	if len(instances) > 0 {
		m.revision++
	}
	m.mu.Unlock()
	for _, inst := range instances {
		inst.Close()
	}
}

type TestFailureKind string

const (
	TestStructural  TestFailureKind = "structural"
	TestNetwork     TestFailureKind = "network"
	TestTimeout     TestFailureKind = "timeout"
	TestCanceled    TestFailureKind = "canceled"
	TestUnavailable TestFailureKind = "unavailable"
	TestExecution   TestFailureKind = "execution"
)

func isStructural(err error) bool { var missing *MethodError; return errors.As(err, &missing) }
func classifyTestFailure(err error) TestFailureKind {
	if isStructural(err) {
		return TestStructural
	}
	if errors.Is(err, context.Canceled) {
		return TestCanceled
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return TestTimeout
	}
	msg := strings.ToLower(err.Error())
	for _, part := range []string{"network", "connect", "dns", "http", "timeout", "unreachable", "econn"} {
		if strings.Contains(msg, part) {
			return TestNetwork
		}
	}
	return TestExecution
}
