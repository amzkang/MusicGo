package plugin

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/dop251/goja"
	"github.com/dop251/goja_nodejs/eventloop"
	"musicgo/internal/plugin/polyfill"
)

// Instance represents a running MusicFree JS plugin instance
type Instance struct {
	mu            sync.Mutex
	gate          chan struct{}
	session       *runtimeSession
	meta          PluginMeta
	methods       map[string]bool
	rawJS         string
	userVars      map[string]string
	storage       polyfill.Storage
	closed        bool
	closedCh      chan struct{}
	rebuildCancel context.CancelFunc
	filePath      string
}

type runtimeSession struct {
	loop     *eventloop.EventLoop
	vm       *goja.Runtime
	exports  *goja.Object
	ctx      context.Context
	cancel   context.CancelFunc
	ready    chan struct{}
	done     chan struct{}
	fault    chan error
	stopOnce sync.Once
}

func (s *runtimeSession) stop() {
	s.stopOnce.Do(func() {
		s.cancel()
		<-s.ready
		s.vm.Interrupt(context.Canceled)
		s.loop.Terminate()
		<-s.done
	})
}
func (s *runtimeSession) guard(fn func()) {
	defer func() {
		if p := recover(); p != nil {
			select {
			case s.fault <- fmt.Errorf("plugin host panic: %v", p):
			default:
			}
			s.cancel()
			s.loop.StopNoWait()
		}
	}()
	fn()
}
func (s *runtimeSession) protectTimers(vm *goja.Runtime) {
	for _, name := range []string{"setTimeout", "setInterval", "setImmediate"} {
		original, ok := goja.AssertFunction(vm.Get(name))
		if !ok {
			continue
		}
		_ = vm.Set(name, func(call goja.FunctionCall) goja.Value {
			args := append([]goja.Value(nil), call.Arguments...)
			if len(args) > 0 {
				if fn, ok := goja.AssertFunction(args[0]); ok {
					args[0] = vm.ToValue(func(inner goja.FunctionCall) goja.Value {
						result := goja.Undefined()
						s.guard(func() {
							var err error
							result, err = fn(inner.This, inner.Arguments...)
							if err != nil {
								select {
								case s.fault <- err:
								default:
								}
								s.cancel()
								s.loop.StopNoWait()
							}
						})
						return result
					})
				}
			}
			v, err := original(call.This, args...)
			if err != nil {
				panic(vm.NewGoError(err))
			}
			return v
		})
	}
}

// MethodError describes an absent/non-callable plugin export, not a network failure.
type MethodError struct{ Name string }

func (e *MethodError) Error() string { return "method '" + e.Name + "' is not exported by plugin" }

// NewInstance creates, compiles, and initializes a MusicFree plugin
func NewInstance(source string, userVars map[string]string) (*Instance, error) {
	return NewInstanceWithStorage(source, userVars, nil)
}

// NewInstanceWithStorage also attaches the plugin's persistent localStorage.
func NewInstanceWithStorage(source string, userVars map[string]string, storage polyfill.Storage) (*Instance, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return newInstance(ctx, source, userVars, storage)
}

func newInstance(ctx context.Context, source string, userVars map[string]string, storage polyfill.Storage) (*Instance, error) {
	vars := make(map[string]string, len(userVars))
	for key, value := range userVars {
		vars[key] = value
	}
	userVars = vars
	codeToRun, err := transpilePlugin(source)
	if err != nil {
		return nil, fmt.Errorf("plugin transpilation failed: %w", err)
	}

	loop := eventloop.NewEventLoop()

	lifetime, cancel := context.WithCancel(context.Background())
	session := &runtimeSession{loop: loop, ctx: lifetime, cancel: cancel, ready: make(chan struct{}), done: make(chan struct{}), fault: make(chan error, 1)}
	inst := &Instance{session: session, gate: make(chan struct{}, 1), closedCh: make(chan struct{}), rawJS: source, userVars: userVars, storage: storage}

	type initResult struct {
		meta PluginMeta
		err  error
	}
	ch := make(chan initResult, 1)

	loop.RunOnLoop(func(vm *goja.Runtime) {
		session.vm = vm
		close(session.ready)
		session.guard(func() {
			polyfill.SetupEnvironmentWithBoundary(vm, loop, userVars, storage, session.guard, session.ctx)
			session.protectTimers(vm)

			_ = vm.Set("__rawPluginCode", codeToRun)
			wrappedCode := `
			var exports = {};
			var module = { exports: exports };
			var pluginFn = new Function('require', '__musicfree_require', 'module', 'exports', 'console', 'env', 'process', __rawPluginCode);
			pluginFn(require, __musicfree_require, module, exports, console, env, process);
			(function() {
				return module.exports.default || module.exports;
			})();
		`

			val, err := vm.RunString(wrappedCode)
			if err != nil {
				ch <- initResult{err: fmt.Errorf("plugin execution error: %w", err)}
				return
			}

			exportsObj := val.ToObject(vm)
			session.exports = exportsObj

			// Extract metadata
			meta := PluginMeta{}
			if p := exportsObj.Get("platform"); p != nil && !goja.IsUndefined(p) {
				meta.Platform = p.String()
			}
			if a := exportsObj.Get("author"); a != nil && !goja.IsUndefined(a) {
				meta.Author = a.String()
			}
			if v := exportsObj.Get("version"); v != nil && !goja.IsUndefined(v) {
				meta.Version = v.String()
			}
			if av := exportsObj.Get("appVersion"); av != nil && !goja.IsUndefined(av) {
				meta.AppVersion = av.String()
			}
			if s := exportsObj.Get("srcUrl"); s != nil && !goja.IsUndefined(s) {
				meta.SrcURL = s.String()
			}
			if desc := exportsObj.Get("description"); desc != nil && !goja.IsUndefined(desc) {
				meta.Description = desc.String()
			}

			if st := exportsObj.Get("supportedSearchType"); st != nil && !goja.IsUndefined(st) {
				if arr, ok := st.Export().([]interface{}); ok {
					for _, item := range arr {
						meta.SupportedSearchType = append(meta.SupportedSearchType, fmt.Sprint(item))
					}
				}
			}

			if pk := exportsObj.Get("primaryKey"); pk != nil && !goja.IsUndefined(pk) {
				if arr, ok := pk.Export().([]interface{}); ok {
					for _, item := range arr {
						meta.PrimaryKey = append(meta.PrimaryKey, fmt.Sprint(item))
					}
				}
			}

			if uv := exportsObj.Get("userVariables"); uv != nil && !goja.IsUndefined(uv) {
				if arr, ok := uv.Export().([]interface{}); ok {
					for _, item := range arr {
						if m, ok := item.(map[string]interface{}); ok {
							key, _ := m["key"].(string)
							name, _ := m["name"].(string)
							hint, _ := m["hint"].(string)
							defVal, _ := m["default"].(string)
							meta.UserVariables = append(meta.UserVariables, UserVariable{
								Key:     key,
								Name:    name,
								Hint:    hint,
								Default: defVal,
							})
						}
					}
				}
			}

			if h := exportsObj.Get("hints"); h != nil && !goja.IsUndefined(h) {
				if m, ok := h.Export().(map[string]interface{}); ok {
					meta.Hints = m
				}
			}

			inst.methods = make(map[string]bool)
			for _, name := range []string{"search", "getMediaSource", "getLyric", "getTopLists", "getTopListDetail", "getAlbumInfo", "getMusicSheetInfo", "getArtistWorks", "importMusicSheet", "getLoginQR", "pollLoginQR", "getLoginStatus", "getUserProfile", "getUserSheets", "getSheetSongs", "getDailyRecommend"} {
				_, ok := goja.AssertFunction(exportsObj.Get(name))
				inst.methods[name] = ok
			}
			inst.meta = meta
			ch <- initResult{meta: meta}
		})
	})
	go func() { defer close(session.done); loop.StartInForeground() }()

	select {
	case failure := <-session.fault:
		session.stop()
		return nil, failure
	case res := <-ch:
		if res.err != nil {
			session.stop()
			return nil, res.err
		}
		return inst, nil
	case <-ctx.Done():
		session.stop()
		return nil, fmt.Errorf("timeout initializing plugin: %w", ctx.Err())
	}
}

// Meta returns the plugin's metadata
func (inst *Instance) Meta() PluginMeta {
	return inst.meta
}

// Close shuts down the plugin's event loop
func (inst *Instance) Close() {
	inst.mu.Lock()
	if inst.closed {
		inst.mu.Unlock()
		return
	}
	inst.closed = true
	close(inst.closedCh)
	session := inst.session
	cancelRebuild := inst.rebuildCancel
	inst.mu.Unlock()
	if cancelRebuild != nil {
		cancelRebuild()
	}
	session.stop()
	// An in-flight rebuild owns another VM until it can publish its session.
	// Wait for that call to dispose of the canceled VM as well.
	inst.gate <- struct{}{}
	<-inst.gate
}

// callAsync invokes an exported function on the plugin instance, handling both sync values and Promises
func (inst *Instance) callAsync(ctx context.Context, fnName string, args ...interface{}) (interface{}, error) {
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
	}
	select {
	case inst.gate <- struct{}{}:
		defer func() { <-inst.gate }()
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-inst.closedCh:
		return nil, fmt.Errorf("plugin instance is closed")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	inst.mu.Lock()
	closed := inst.closed
	session := inst.session
	inst.mu.Unlock()
	if closed {
		return nil, fmt.Errorf("plugin instance is closed")
	}
	if session.ctx.Err() != nil {
		session.stop()
		initCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		inst.mu.Lock()
		if inst.closed {
			inst.mu.Unlock()
			cancel()
			return nil, context.Canceled
		}
		inst.rebuildCancel = cancel
		inst.mu.Unlock()
		fresh, err := newInstance(initCtx, inst.rawJS, inst.userVars, inst.storage)
		cancel()
		inst.mu.Lock()
		inst.rebuildCancel = nil
		if err != nil {
			inst.mu.Unlock()
			return nil, err
		}
		if inst.closed {
			inst.mu.Unlock()
			fresh.Close()
			return nil, fmt.Errorf("plugin instance is closed")
		}
		inst.session = fresh.session
		session = fresh.session
		inst.mu.Unlock()
	}

	type resultHolder struct {
		val interface{}
		err error
	}
	ch := make(chan resultHolder, 1)
	var delivered sync.Once
	send := func(r resultHolder) { delivered.Do(func() { ch <- r }) }

	session.loop.RunOnLoop(func(vm *goja.Runtime) {
		session.guard(func() {
			fnVal := session.exports.Get(fnName)
			if fnVal == nil || goja.IsUndefined(fnVal) || goja.IsNull(fnVal) {
				send(resultHolder{err: &MethodError{Name: fnName}})
				return
			}
			callable, ok := goja.AssertFunction(fnVal)
			if !ok {
				send(resultHolder{err: &MethodError{Name: fnName}})
				return
			}

			var jsArgs []goja.Value
			for _, arg := range args {
				if arg == nil {
					jsArgs = append(jsArgs, goja.Null())
					continue
				}
				switch v := arg.(type) {
				case string, int, int64, float64, bool:
					jsArgs = append(jsArgs, vm.ToValue(v))
				default:
					b, err := json.Marshal(arg)
					if err == nil {
						var val interface{}
						_ = json.Unmarshal(b, &val)
						jsArgs = append(jsArgs, vm.ToValue(val))
					} else {
						jsArgs = append(jsArgs, vm.ToValue(arg))
					}
				}
			}

			ret, err := callable(session.exports, jsArgs...)
			if err != nil {
				send(resultHolder{err: fmt.Errorf("error calling %s: %w", fnName, err)})
				return
			}

			// Check if returned value is a Promise (has a callable "then" property)
			if obj, ok := ret.(*goja.Object); ok {
				thenVal := obj.Get("then")
				if thenVal != nil && !goja.IsUndefined(thenVal) {
					if thenFn, ok := goja.AssertFunction(thenVal); ok {
						onResolve := vm.ToValue(func(call goja.FunctionCall) goja.Value {
							var res interface{}
							if len(call.Arguments) > 0 {
								res = call.Arguments[0].Export()
							}
							send(resultHolder{val: res})
							return goja.Undefined()
						})
						onReject := vm.ToValue(func(call goja.FunctionCall) goja.Value {
							var rErr error
							if len(call.Arguments) > 0 {
								arg := call.Arguments[0]
								if obj, ok := arg.(*goja.Object); ok {
									msg := obj.Get("message")
									if msg != nil && !goja.IsUndefined(msg) && msg.String() != "" {
										rErr = fmt.Errorf("%s", msg.String())
									} else {
										rErr = fmt.Errorf("%s", obj.String())
									}
								} else {
									rErr = fmt.Errorf("%v", arg.Export())
								}
							} else {
								rErr = fmt.Errorf("promise rejected")
							}
							send(resultHolder{err: rErr})
							return goja.Undefined()
						})
						_, _ = thenFn(obj, onResolve, onReject)
						return
					}
				}
			}

			// Plain synchronous return
			send(resultHolder{val: ret.Export()})
		})
	})

	select {
	case res := <-ch:
		return res.val, res.err
	case <-ctx.Done():
		session.stop()
		return nil, ctx.Err()
	case failure := <-session.fault:
		session.stop()
		return nil, failure
	case <-session.ctx.Done():
		session.stop()
		return nil, fmt.Errorf("plugin execution stopped")
	}
}

// Search calls the plugin's search method
func (inst *Instance) Search(ctx context.Context, keyword string, page int, searchType string) (*SearchResult, error) {
	val, err := inst.callAsync(ctx, "search", keyword, page, searchType)
	if err != nil {
		return nil, err
	}
	if val == nil {
		return &SearchResult{IsEnd: true, Data: nil}, nil
	}

	// Marshal and unmarshal to parse into SearchResult
	bytesData, err := json.Marshal(val)
	if err != nil {
		return nil, fmt.Errorf("failed to encode search result: %w", err)
	}

	var res SearchResult
	if err := json.Unmarshal(bytesData, &res); err == nil && len(res.Data) > 0 {
		for i := range res.Data {
			if res.Data[i].Platform == "" {
				res.Data[i].Platform = inst.meta.Platform
			}
		}
		return &res, nil
	}

	// Fallback: sometimes plugins return a raw array: [ { id, title, ... } ]
	var rawList []MusicItem
	if err := json.Unmarshal(bytesData, &rawList); err == nil {
		for i := range rawList {
			if rawList[i].Platform == "" {
				rawList[i].Platform = inst.meta.Platform
			}
		}
		return &SearchResult{IsEnd: true, Data: rawList}, nil
	}

	return &res, nil
}

// GetMediaSource calls the plugin's getMediaSource method
func (inst *Instance) GetMediaSource(ctx context.Context, item MusicItem, quality string) (*MediaSource, error) {
	val, err := inst.callAsync(ctx, "getMediaSource", item, quality)
	if err != nil {
		return nil, err
	}
	if val == nil {
		return nil, fmt.Errorf("no media source returned")
	}

	// If returned a plain string (the audio URL)
	if urlStr, ok := val.(string); ok {
		return &MediaSource{URL: urlStr}, nil
	}

	bytesData, err := json.Marshal(val)
	if err != nil {
		return nil, fmt.Errorf("failed to encode media source: %w", err)
	}

	var ms MediaSource
	if err := json.Unmarshal(bytesData, &ms); err != nil {
		return nil, fmt.Errorf("failed to decode media source: %w", err)
	}
	return &ms, nil
}

// GetLyric calls the plugin's getLyric method
func (inst *Instance) GetLyric(ctx context.Context, item MusicItem) (*LyricResult, error) {
	val, err := inst.callAsync(ctx, "getLyric", item)
	if err != nil {
		return nil, err
	}
	if val == nil {
		return &LyricResult{}, nil
	}

	if rawStr, ok := val.(string); ok {
		return &LyricResult{RawLrc: rawStr}, nil
	}

	bytesData, err := json.Marshal(val)
	if err != nil {
		return nil, fmt.Errorf("failed to encode lyric: %w", err)
	}

	var lr LyricResult
	if err := json.Unmarshal(bytesData, &lr); err != nil {
		return nil, fmt.Errorf("failed to decode lyric: %w", err)
	}
	return &lr, nil
}

// ImportMusicSheet calls the plugin's importMusicSheet method
func (inst *Instance) ImportMusicSheet(ctx context.Context, urlOrId string) ([]MusicItem, error) {
	val, err := inst.callAsync(ctx, "importMusicSheet", urlOrId)
	if err != nil {
		return nil, err
	}
	if val == nil {
		return nil, nil
	}

	bytesData, err := json.Marshal(val)
	if err != nil {
		return nil, fmt.Errorf("failed to encode music sheet: %w", err)
	}

	var items []MusicItem
	if err := json.Unmarshal(bytesData, &items); err != nil {
		return nil, fmt.Errorf("failed to decode music sheet: %w", err)
	}
	for i := range items {
		if items[i].Platform == "" {
			items[i].Platform = inst.meta.Platform
		}
	}
	return items, nil
}

// HasLogin tells whether the plugin exports the QR-login hooks, so the host
// can offer a "scan to log in" entry for it.
func (inst *Instance) HasLogin() bool {
	return inst.HasMethod("getLoginQR") && inst.HasMethod("pollLoginQR")
}

// Source returns the raw JavaScript the instance was created from. It does
// not change over the instance's life, so it can rebuild a fresh instance.
func (inst *Instance) Source() string { return inst.rawJS }

// GetLoginQR calls the plugin's getLoginQR hook: the text to draw as a QR
// code plus the opaque state the host passes back to pollLoginQR.
func (inst *Instance) GetLoginQR(ctx context.Context) (*LoginQR, error) {
	val, err := inst.callAsync(ctx, "getLoginQR")
	if err != nil {
		return nil, err
	}
	if val == nil {
		return nil, fmt.Errorf("no login QR returned")
	}

	bytesData, err := json.Marshal(val)
	if err != nil {
		return nil, fmt.Errorf("failed to encode login QR: %w", err)
	}

	var qr LoginQR
	if err := json.Unmarshal(bytesData, &qr); err != nil {
		return nil, fmt.Errorf("failed to decode login QR: %w", err)
	}
	if qr.QRText == "" {
		return nil, fmt.Errorf("login QR text is empty")
	}
	return &qr, nil
}

// PollLoginQR calls the plugin's pollLoginQR hook with the state getLoginQR
// gave. On "confirmed" the result carries the user variables to persist.
func (inst *Instance) PollLoginQR(ctx context.Context, state string) (*LoginPollResult, error) {
	val, err := inst.callAsync(ctx, "pollLoginQR", state)
	if err != nil {
		return nil, err
	}
	if val == nil {
		return nil, fmt.Errorf("no login poll result returned")
	}

	bytesData, err := json.Marshal(val)
	if err != nil {
		return nil, fmt.Errorf("failed to encode login poll result: %w", err)
	}

	var res LoginPollResult
	if err := json.Unmarshal(bytesData, &res); err != nil {
		return nil, fmt.Errorf("failed to decode login poll result: %w", err)
	}
	return &res, nil
}

// GetLoginStatus calls the plugin's optional getLoginStatus hook, which tells
// whether the stored user variables still mean a logged-in session.
func (inst *Instance) GetLoginStatus(ctx context.Context) (*LoginStatus, error) {
	val, err := inst.callAsync(ctx, "getLoginStatus")
	if err != nil {
		return nil, err
	}
	if val == nil {
		return nil, nil
	}

	bytesData, err := json.Marshal(val)
	if err != nil {
		return nil, fmt.Errorf("failed to encode login status: %w", err)
	}

	var status LoginStatus
	if err := json.Unmarshal(bytesData, &status); err != nil {
		return nil, fmt.Errorf("failed to decode login status: %w", err)
	}
	return &status, nil
}

// GetUserProfile calls the plugin's optional getUserProfile hook: the
// account's nickname, avatar and VIP label.
func (inst *Instance) GetUserProfile(ctx context.Context) (*UserProfile, error) {
	val, err := inst.callAsync(ctx, "getUserProfile")
	if err != nil {
		return nil, err
	}
	if val == nil {
		return nil, &MethodError{Name: "getUserProfile"}
	}

	bytesData, err := json.Marshal(val)
	if err != nil {
		return nil, fmt.Errorf("failed to encode user profile: %w", err)
	}

	var profile UserProfile
	if err := json.Unmarshal(bytesData, &profile); err != nil {
		return nil, fmt.Errorf("failed to decode user profile: %w", err)
	}
	return &profile, nil
}

// GetUserSheets calls the plugin's optional getUserSheets hook, which lists
// the account's own playlists.
func (inst *Instance) GetUserSheets(ctx context.Context) ([]UserSheet, error) {
	val, err := inst.callAsync(ctx, "getUserSheets")
	if err != nil {
		return nil, err
	}
	if val == nil {
		return nil, &MethodError{Name: "getUserSheets"}
	}

	bytesData, err := json.Marshal(val)
	if err != nil {
		return nil, fmt.Errorf("failed to encode user sheets: %w", err)
	}

	var sheets []UserSheet
	if err := json.Unmarshal(bytesData, &sheets); err != nil {
		return nil, fmt.Errorf("failed to decode user sheets: %w", err)
	}
	return sheets, nil
}

// GetSheetSongs calls the plugin's optional getSheetSongs hook with a
// one-based page number.
func (inst *Instance) GetSheetSongs(ctx context.Context, sheetID string, page int) (*SheetSongs, error) {
	val, err := inst.callAsync(ctx, "getSheetSongs", sheetID, page)
	if err != nil {
		return nil, err
	}
	if val == nil {
		return nil, &MethodError{Name: "getSheetSongs"}
	}

	bytesData, err := json.Marshal(val)
	if err != nil {
		return nil, fmt.Errorf("failed to encode sheet songs: %w", err)
	}

	var songs SheetSongs
	if err := json.Unmarshal(bytesData, &songs); err != nil {
		return nil, fmt.Errorf("failed to decode sheet songs: %w", err)
	}
	// Sheet songs arrive without a platform; the player routes playback by it.
	for i := range songs.Data {
		if songs.Data[i].Platform == "" {
			songs.Data[i].Platform = inst.meta.Platform
			songs.Data[i].Extra["platform"] = inst.meta.Platform
		}
	}
	return &songs, nil
}

// HasTopLists tells whether the plugin exports the leaderboard methods. A
// plugin that does not is left out of the leaderboard page.
func (inst *Instance) HasTopLists() bool          { return inst.HasMethod("getTopLists") }
func (inst *Instance) HasMethod(name string) bool { return inst.methods[name] }

// GetTopLists calls the plugin's getTopLists method, which gives the
// leaderboards of the platform, grouped as the platform groups them.
func (inst *Instance) GetTopLists(ctx context.Context) ([]TopListGroup, error) {
	val, err := inst.callAsync(ctx, "getTopLists")
	if err != nil {
		return nil, err
	}
	if val == nil {
		return nil, nil
	}

	bytesData, err := json.Marshal(val)
	if err != nil {
		return nil, fmt.Errorf("failed to encode top lists: %w", err)
	}

	var rawGroups []struct {
		Title string                   `json:"title"`
		Data  []map[string]interface{} `json:"data"`
	}
	if err := json.Unmarshal(bytesData, &rawGroups); err != nil {
		return nil, fmt.Errorf("failed to decode top lists: %w", err)
	}

	groups := make([]TopListGroup, 0, len(rawGroups))
	for _, rg := range rawGroups {
		group := TopListGroup{Title: rg.Title}
		for _, raw := range rg.Data {
			group.Data = append(group.Data, topListItemFromRaw(raw, inst.meta.Platform))
		}
		groups = append(groups, group)
	}
	return groups, nil
}

// topListItemFromRaw reads a leaderboard the plugin gave, and keeps the
// fields the host does not know, so that the plugin finds them again when
// it is asked for the songs of the leaderboard.
func topListItemFromRaw(raw map[string]interface{}, platform string) TopListItem {
	item := TopListItem{
		ID:          stringOf(raw["id"]),
		Title:       stringOf(raw["title"]),
		Description: stringOf(raw["description"]),
		CoverImg:    stringOf(raw["coverImg"]),
		Platform:    platform,
	}
	if item.Platform == "" {
		item.Platform = stringOf(raw["platform"])
	}

	item.Extra = make(map[string]interface{})
	for k, v := range raw {
		switch k {
		case "id", "title", "description", "coverImg", "platform":
			continue
		}
		item.Extra[k] = v
	}
	return item
}

// stringOf reads a field that may be a string, a number or nothing.
func stringOf(v interface{}) string {
	switch s := v.(type) {
	case string:
		return s
	case float64:
		return fmt.Sprintf("%.0f", s)
	case bool:
		return fmt.Sprintf("%v", s)
	case nil:
		return ""
	default:
		return fmt.Sprintf("%v", s)
	}
}

// GetTopListDetail calls the plugin's getTopListDetail method, which gives
// the songs of one leaderboard. The item is given back to the plugin the way
// it gave it, so a plugin that reads more fields than the host knows still
// finds them.
func (inst *Instance) GetTopListDetail(ctx context.Context, item TopListItem) (*TopListDetail, error) {
	arg := map[string]interface{}{
		"id":          item.ID,
		"title":       item.Title,
		"description": item.Description,
		"coverImg":    item.CoverImg,
	}
	for k, v := range item.Extra {
		arg[k] = v
	}

	val, err := inst.callAsync(ctx, "getTopListDetail", arg)
	if err != nil {
		return nil, err
	}
	if val == nil {
		return nil, fmt.Errorf("no top list detail returned")
	}

	bytesData, err := json.Marshal(val)
	if err != nil {
		return nil, fmt.Errorf("failed to encode top list detail: %w", err)
	}

	var detail TopListDetail
	if err := json.Unmarshal(bytesData, &detail); err != nil {
		return nil, fmt.Errorf("failed to decode top list detail: %w", err)
	}
	if detail.ID == "" {
		detail.ID = item.ID
	}
	if detail.Title == "" {
		detail.Title = item.Title
	}
	if detail.CoverImg == "" {
		detail.CoverImg = item.CoverImg
	}
	if detail.Platform == "" {
		detail.Platform = item.Platform
	}
	for i := range detail.MusicList {
		if detail.MusicList[i].Platform == "" {
			detail.MusicList[i].Platform = inst.meta.Platform
		}
	}
	return &detail, nil
}
