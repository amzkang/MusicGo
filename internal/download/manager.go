package download

import (
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"musicgo/internal/plugin"
	"musicgo/internal/store"
)

type taskRun struct {
	task               Task
	epoch              uint64
	ctx                context.Context
	cancel             context.CancelFunc
	deleteFiles        bool
	final, part, lyric string // owned exclusively by the worker
}
type persistOperation struct {
	record   store.DownloadRecord
	deleteID string
}
type Manager struct {
	mu                                    sync.RWMutex
	tasks                                 []*Task
	taskMap                               map[string]*Task
	active                                map[string]*taskRun
	available                             map[string]bool
	epoch                                 uint64
	pluginMgr                             *plugin.Manager
	store                                 *store.Store
	configMgr                             *store.ConfigManager
	client                                *http.Client
	listeners                             []func()
	closed                                bool
	closeOnce                             sync.Once
	workers                               sync.WaitGroup
	pathMu                                sync.Mutex
	reserved                              map[string]string
	pending                               []persistOperation
	persistWake, persistStop, persistDone chan struct{}
	lastError                             string
}

func NewManager(pm *plugin.Manager, st *store.Store, cm *store.ConfigManager) *Manager {
	m := &Manager{taskMap: make(map[string]*Task), active: make(map[string]*taskRun), available: make(map[string]bool), reserved: make(map[string]string), pluginMgr: pm, store: st, configMgr: cm, persistWake: make(chan struct{}, 1), persistStop: make(chan struct{}), persistDone: make(chan struct{}), client: &http.Client{Transport: &http.Transport{Proxy: http.ProxyFromEnvironment, DialContext: (&net.Dialer{Timeout: 15 * time.Second}).DialContext, ResponseHeaderTimeout: 30 * time.Second, IdleConnTimeout: 60 * time.Second}}}
	if st != nil {
		for _, r := range st.ListDownloads() {
			status := r.Status
			if status == StatusDownloading || status == StatusQueued {
				status = StatusPaused
			}
			song := r.SongData.Clone()
			if song.ID == "" {
				song.ID = r.SongID
			}
			if song.Platform == "" {
				song.Platform = r.Platform
			}
			if song.Title == "" {
				song.Title = r.Title
			}
			if song.Artist == "" {
				song.Artist = r.Artist
			}
			t := &Task{ID: r.ID, Song: song, Quality: r.Quality, Status: status, Progress: r.Progress, DownloadedBytes: r.DownloadedBytes, TotalBytes: r.TotalBytes, FilePath: r.FilePath, LyricPath: r.LyricPath, ErrorMsg: r.ErrorMsg, CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt}
			m.tasks = append(m.tasks, t)
			m.taskMap[t.ID] = t
			m.available[t.ID], _ = st.DownloadFileAvailability(t.ID, t.FilePath)
			if t.FilePath != "" {
				m.reserved[t.FilePath] = t.ID
			}
		}
	}
	go m.persistLoop()
	return m
}
func (m *Manager) OnUpdate(fn func()) {
	if fn != nil {
		m.mu.Lock()
		m.listeners = append(m.listeners, fn)
		m.mu.Unlock()
	}
}
func (m *Manager) notifyUpdate() {
	m.mu.RLock()
	callbacks := append([]func(){}, m.listeners...)
	m.mu.RUnlock()
	for _, fn := range callbacks {
		fn()
	}
}
func (m *Manager) PersistenceError() string { m.mu.RLock(); defer m.mu.RUnlock(); return m.lastError }
func taskCopy(t *Task) Task                 { copy := *t; copy.Song = copy.Song.Clone(); return copy }
func (m *Manager) Tasks() []Task {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]Task, len(m.tasks))
	for i, t := range m.tasks {
		out[i] = taskCopy(t)
		out[i].FileAvailable = t.Status == StatusCompleted && m.available[t.ID]
	}
	return out
}
func (m *Manager) GetTask(id string) (Task, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	t, ok := m.taskMap[id]
	if !ok {
		return Task{}, false
	}
	copy := taskCopy(t)
	copy.FileAvailable = t.Status == StatusCompleted && m.available[t.ID]
	return copy, true
}
func (m *Manager) ActiveCount() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	n := 0
	for _, t := range m.tasks {
		if t.Status == StatusQueued || t.Status == StatusDownloading {
			n++
		}
	}
	return n
}
func (m *Manager) IsDownloading(platform, id string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, t := range m.tasks {
		if t.Song.Platform == platform && t.Song.ID == id && (t.Status == StatusQueued || t.Status == StatusDownloading) {
			return true
		}
	}
	return false
}
func (m *Manager) IsDownloaded(platform, id string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, t := range m.tasks {
		if t.Song.Platform == platform && t.Song.ID == id && t.Status == StatusCompleted && m.available[t.ID] {
			return true
		}
	}
	return false
}

func (m *Manager) AddTask(song plugin.MusicItem, quality string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return "", fmt.Errorf("download manager is closed")
	}
	if quality == "" && m.configMgr != nil {
		quality = m.configMgr.DownloadSettings().DefaultQuality
	}
	if quality == "" {
		quality = "standard"
	}
	for _, t := range m.tasks {
		if t.Song.Key() != song.Key() || t.Quality != quality {
			continue
		}
		if t.Status == StatusQueued || t.Status == StatusDownloading || t.Status == StatusCompleted && m.available[t.ID] {
			return t.ID, nil
		}
		// 剩余状态（Paused/Failed/Canceled 或文件已丢的 Completed）转为重新排队，
		// 与 ResumeTask 同一转换：另起新任务会经 reservePath 产生重复任务和重复文件。
		if t.Status == StatusCompleted {
			t.Progress, t.DownloadedBytes, t.TotalBytes = 0, 0, 0
		}
		t.Status = StatusQueued
		t.ErrorMsg = ""
		m.saveRecordLocked(t)
		return t.ID, nil
	}
	now := time.Now()
	m.epoch++
	id := fmt.Sprintf("dl_%d_%d", now.UnixNano(), m.epoch)
	t := &Task{ID: id, Song: song.Clone(), Quality: quality, Status: StatusQueued, CreatedAt: now.UnixMilli(), UpdatedAt: now.UnixMilli()}
	m.tasks = append([]*Task{t}, m.tasks...)
	m.taskMap[id] = t
	m.saveRecordLocked(t)
	m.scheduleLocked()
	go m.notifyUpdate()
	return id, nil
}
func (m *Manager) PauseTask(id string) {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return
	}
	if t := m.taskMap[id]; t != nil && (t.Status == StatusDownloading || t.Status == StatusQueued) {
		t.Status = StatusPaused
		t.Speed = 0
		if run := m.active[id]; run != nil {
			run.cancel()
		}
		m.saveRecordLocked(t)
	}
	m.scheduleLocked()
	m.mu.Unlock()
	m.notifyUpdate()
}
func (m *Manager) ResumeTask(id string) {
	m.mu.Lock()
	if t := m.taskMap[id]; t != nil && !m.closed && (t.Status == StatusPaused || t.Status == StatusFailed || t.Status == StatusCanceled || t.Status == StatusCompleted && !m.available[id]) {
		if t.Status == StatusCompleted {
			t.Progress, t.DownloadedBytes, t.TotalBytes = 0, 0, 0
		}
		t.Status = StatusQueued
		t.ErrorMsg = ""
		m.saveRecordLocked(t)
	}
	m.scheduleLocked()
	m.mu.Unlock()
	m.notifyUpdate()
}
func (m *Manager) RetryTask(id string)  { m.ResumeTask(id) }
func (m *Manager) CancelTask(id string) { m.PauseTask(id) }
func (m *Manager) DeleteTask(id string, deleteFile bool) {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return
	}
	t := m.taskMap[id]
	if t == nil {
		m.mu.Unlock()
		return
	}
	copy := taskCopy(t)
	run := m.active[id]
	if run != nil {
		run.deleteFiles = deleteFile
		run.cancel()
	}
	delete(m.taskMap, id)
	delete(m.available, id)
	for i, task := range m.tasks {
		if task.ID == id {
			m.tasks = append(m.tasks[:i], m.tasks[i+1:]...)
			break
		}
	}
	m.pending = append(m.pending, persistOperation{deleteID: id})
	m.wakePersistenceLocked()
	m.scheduleLocked()
	m.mu.Unlock()
	if deleteFile && run == nil {
		removeTaskFiles(copy)
	}
	if run == nil {
		m.releasePaths(id)
	}
	m.notifyUpdate()
}

func (m *Manager) releasePaths(id string) {
	m.pathMu.Lock()
	defer m.pathMu.Unlock()
	for path, owner := range m.reserved {
		if owner == id {
			delete(m.reserved, path)
		}
	}
}
func removeTaskFiles(t Task) {
	for _, path := range []string{t.FilePath, t.LyricPath, partialPath(t)} {
		if path != "" {
			os.Remove(path)
		}
	}
}
func (m *Manager) PauseAll() {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return
	}
	for _, t := range m.tasks {
		if t.Status == StatusDownloading || t.Status == StatusQueued {
			t.Status = StatusPaused
			t.Speed = 0
			if run := m.active[t.ID]; run != nil {
				run.cancel()
			}
			m.saveRecordLocked(t)
		}
	}
	m.mu.Unlock()
	m.notifyUpdate()
}
func (m *Manager) ResumeAll() {
	m.mu.Lock()
	if !m.closed {
		for _, t := range m.tasks {
			if t.Status == StatusPaused || t.Status == StatusFailed || t.Status == StatusCanceled {
				t.Status = StatusQueued
				t.ErrorMsg = ""
				m.saveRecordLocked(t)
			}
		}
		m.scheduleLocked()
	}
	m.mu.Unlock()
	m.notifyUpdate()
}
func (m *Manager) ClearCompleted() {
	for _, t := range m.Tasks() {
		if t.Status == StatusCompleted {
			m.DeleteTask(t.ID, false)
		}
	}
}
func (m *Manager) Close() {
	m.closeOnce.Do(func() {
		m.mu.Lock()
		m.closed = true
		for _, t := range m.tasks {
			if t.Status == StatusQueued || t.Status == StatusDownloading {
				t.Status = StatusPaused
				t.Speed = 0
				m.saveRecordLocked(t)
			}
		}
		for _, run := range m.active {
			run.cancel()
		}
		m.mu.Unlock()
		m.workers.Wait()
		close(m.persistStop)
		<-m.persistDone
		m.client.CloseIdleConnections()
	})
}
func (m *Manager) scheduleLocked() {
	if m.closed {
		return
	}
	limit := 3
	if m.configMgr != nil {
		limit = m.configMgr.DownloadSettings().MaxConcurrent
	}
	if limit < 1 {
		limit = 3
	}
	for len(m.active) < limit {
		var next *Task
		for _, t := range m.tasks {
			if t.Status == StatusQueued && m.active[t.ID] == nil {
				next = t
				break
			}
		}
		if next == nil {
			return
		}
		m.epoch++
		ctx, cancel := context.WithCancel(context.Background())
		next.Status = StatusDownloading
		run := &taskRun{task: taskCopy(next), epoch: m.epoch, ctx: ctx, cancel: cancel}
		m.active[next.ID] = run
		m.saveRecordLocked(next)
		m.workers.Add(1)
		go m.runTask(run)
	}
}
func (m *Manager) updateRun(run *taskRun, fn func(*Task)) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	t := m.taskMap[run.task.ID]
	if m.closed || t == nil || m.active[t.ID] != run || run.ctx.Err() != nil || t.Status != StatusDownloading {
		return false
	}
	fn(t)
	t.UpdatedAt = time.Now().UnixMilli()
	return true
}
func (m *Manager) runTask(run *taskRun) {
	defer m.workers.Done()
	defer run.cancel()
	err := m.downloadSong(run)
	if err != nil && run.ctx.Err() == nil {
		m.updateRun(run, func(t *Task) { t.Status = StatusFailed; t.Speed = 0; t.ErrorMsg = err.Error(); m.saveRecordLocked(t) })
	}
	m.mu.Lock()
	if m.active[run.task.ID] == run {
		delete(m.active, run.task.ID)
	}
	deleted := run.deleteFiles
	task := m.taskMap[run.task.ID]
	if task != nil {
		m.saveRecordLocked(task)
	}
	m.scheduleLocked()
	m.mu.Unlock()
	if deleted {
		removeTaskFiles(Task{ID: run.task.ID, FilePath: run.final, LyricPath: run.lyric})
	}
	if task == nil {
		m.releasePaths(run.task.ID)
	}
	m.notifyUpdate()
}
func (m *Manager) wakePersistenceLocked() {
	select {
	case m.persistWake <- struct{}{}:
	default:
	}
}
func (m *Manager) saveRecordLocked(t *Task) {
	if m.store == nil {
		return
	}
	r := store.DownloadRecord{ID: t.ID, SongID: t.Song.ID, Platform: t.Song.Platform, Title: t.Song.Title, Artist: t.Song.Artist, Album: t.Song.Album, Duration: t.Song.Duration, Artwork: t.Song.Artwork, SongData: t.Song.Clone(), Quality: t.Quality, Status: t.Status, Progress: t.Progress, DownloadedBytes: t.DownloadedBytes, TotalBytes: t.TotalBytes, FilePath: t.FilePath, LyricPath: t.LyricPath, ErrorMsg: t.ErrorMsg, CreatedAt: t.CreatedAt, UpdatedAt: t.UpdatedAt}
	m.pending = append(m.pending, persistOperation{record: r})
	m.wakePersistenceLocked()
}
func (m *Manager) persistLoop() {
	defer close(m.persistDone)
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	retrying := false
	for {
		stopping := false
		select {
		case <-m.persistWake:
			if retrying {
				continue
			}
		case <-ticker.C:
			m.refreshFiles()
		case <-m.persistStop:
			stopping = true
		}
		retrying = false
		for {
			m.mu.Lock()
			batch := m.pending
			m.pending = nil
			m.mu.Unlock()
			if len(batch) == 0 {
				break
			}
			batch = compactPersistence(batch)
			for i, op := range batch {
				if m.store == nil {
					continue
				}
				var err error
				if op.deleteID != "" {
					err = m.store.DeleteDownload(op.deleteID)
				} else {
					err = m.store.SaveDownload(op.record)
				}
				if err != nil {
					m.mu.Lock()
					// Keep the failed operation and newer pending state. The last
					// operation per task wins, including a later deletion.
					m.pending = compactPersistence(append(batch[i:], m.pending...))
					changed := m.lastError != err.Error()
					m.lastError = err.Error()
					m.mu.Unlock()
					if changed {
						m.notifyUpdate()
					}
					retrying = true
					break
				}
			}
			if retrying {
				break
			}
		}
		if !retrying {
			m.mu.Lock()
			changed := m.lastError != ""
			m.lastError = ""
			m.mu.Unlock()
			if changed {
				m.notifyUpdate()
			}
		}
		if stopping {
			return
		}
	}
}

// Download records describe current state, not an event log. Coalesce retries
// so a slow/unavailable disk does not retain every progress snapshot forever.
func compactPersistence(operations []persistOperation) []persistOperation {
	positions := make(map[string]int, len(operations))
	out := operations[:0]
	for _, operation := range operations {
		id := operation.deleteID
		if id == "" {
			id = operation.record.ID
		}
		if index, found := positions[id]; found {
			out[index] = operation
		} else {
			positions[id] = len(out)
			out = append(out, operation)
		}
	}
	clear(operations[len(out):])
	return out
}
func (m *Manager) refreshFiles() {
	changed := false
	for _, snapshot := range m.Tasks() {
		if snapshot.Status != StatusCompleted {
			continue
		}
		var valid, found bool
		if m.store != nil {
			valid, found = m.store.DownloadFileAvailability(snapshot.ID, snapshot.FilePath)
		} else {
			info, err := os.Stat(snapshot.FilePath)
			valid = err == nil && !info.IsDir() && info.Size() > 0
			found = true
		}
		if !found {
			continue
		}
		m.mu.Lock()
		if t := m.taskMap[snapshot.ID]; t != nil && t.Status == snapshot.Status && t.FilePath == snapshot.FilePath {
			changed = changed || m.available[t.ID] != valid
			m.available[t.ID] = valid
		}
		m.mu.Unlock()
	}
	if changed {
		m.notifyUpdate()
	}
}
func partialPath(t Task) string {
	if t.FilePath == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(t.ID))
	return filepath.Join(filepath.Dir(t.FilePath), fmt.Sprintf(".musicgo-%x.part", sum[:12]))
}
func (m *Manager) reservePath(task Task, dir, base, ext string) (string, error) {
	m.pathMu.Lock()
	defer m.pathMu.Unlock()
	candidate := task.FilePath
	if candidate == "" {
		candidate = filepath.Join(dir, base+ext)
	}
	key := sha256.Sum256([]byte(string(task.Song.Key()) + "\x00" + task.Quality))
	suffix := fmt.Sprintf("%x", key[:5])
	for i := 0; ; i++ {
		owner := m.reserved[candidate]
		_, err := os.Stat(candidate)
		if os.IsNotExist(err) && (owner == "" || owner == task.ID) {
			m.reserved[candidate] = task.ID
			return candidate, nil
		}
		if err != nil && !os.IsNotExist(err) {
			return "", err
		}
		extra := ""
		if i > 0 {
			extra = fmt.Sprintf("-%d", i)
		}
		candidate = filepath.Join(dir, base+"-"+suffix+extra+ext)
	}
}
func (m *Manager) publish(run *taskRun, dir, base, ext string) error {
	m.pathMu.Lock()
	defer m.pathMu.Unlock()
	// Link is atomic and refuses to overwrite any existing destination.
	for i := 0; ; i++ {
		if err := run.ctx.Err(); err != nil {
			return err
		}
		err := os.Link(run.part, run.final)
		if err == nil {
			return os.Remove(run.part)
		}
		if os.IsExist(err) {
			for {
				run.final = filepath.Join(dir, fmt.Sprintf("%s-%s-%d%s", base, run.task.ID, i+1, ext))
				if owner := m.reserved[run.final]; owner == "" || owner == run.task.ID {
					break
				}
				i++
			}
			m.reserved[run.final] = run.task.ID
			continue
		}
		// Filesystems without hard links still use exclusive creation, never rename-overwrite.
		dst, e := os.OpenFile(run.final, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0644)
		if os.IsExist(e) {
			// 与 Link 冲突分支同样推进候选名，否则在不支持硬链接的文件系统上
			// Link 恒定非 EEXIST 失败、名字永不前进，这里会空转死循环。
			for {
				run.final = filepath.Join(dir, fmt.Sprintf("%s-%s-%d%s", base, run.task.ID, i+1, ext))
				if owner := m.reserved[run.final]; owner == "" || owner == run.task.ID {
					break
				}
				i++
			}
			m.reserved[run.final] = run.task.ID
			continue
		}
		if e != nil {
			return e
		}
		src, e := os.Open(run.part)
		if e != nil {
			dst.Close()
			os.Remove(run.final)
			return e
		}
		_, e = io.Copy(dst, src)
		src.Close()
		if e == nil {
			e = dst.Sync()
		}
		closeErr := dst.Close()
		if e == nil {
			e = closeErr
		}
		if e != nil {
			os.Remove(run.final)
			return e
		}
		return os.Remove(run.part)
	}
}

// resolveSource gives downloads the same recovery path as playback. Match both
// title and artist before switching providers so a failed download cannot
// silently become another performance of a song with the same title.
func (m *Manager) resolveSource(ctx context.Context, task Task) (*plugin.MediaSource, error) {
	song := task.Song
	var sourceErr error
	if m.pluginMgr != nil && song.Platform != "local" {
		resolveCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
		source, err := m.pluginMgr.GetMediaSource(resolveCtx, song, task.Quality)
		cancel()
		if err == nil && source != nil && source.URL != "" {
			return source, nil
		}
		sourceErr = err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if song.URL != "" {
		return &plugin.MediaSource{URL: song.URL}, nil
	}
	if m.pluginMgr != nil && song.Platform != "local" && strings.TrimSpace(song.Title) != "" {
		fallbackCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		var platforms []string
		for _, meta := range m.pluginMgr.ListPlugins() {
			if meta.Platform != song.Platform {
				platforms = append(platforms, meta.Platform)
			}
		}
		if len(platforms) > 0 {
			query := strings.TrimSpace(song.Title + " " + song.Artist)
			candidates, _ := m.pluginMgr.Search(fallbackCtx, query, 1, "music", platforms...)
			normalize := func(s string) string { return strings.ToLower(strings.Join(strings.Fields(s), " ")) }
			for _, candidate := range candidates {
				if candidate.Platform == song.Platform || candidate.Platform == "" || normalize(candidate.Title) != normalize(song.Title) {
					continue
				}
				if song.Artist != "" && song.Artist != "未知歌手" && normalize(candidate.Artist) != normalize(song.Artist) {
					continue
				}
				source, err := m.pluginMgr.GetMediaSource(fallbackCtx, candidate, task.Quality)
				if err == nil && source != nil && source.URL != "" {
					return source, nil
				}
			}
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if sourceErr != nil {
		return nil, fmt.Errorf("音源解析失败，其他音源未找到匹配歌曲: %w", sourceErr)
	}
	return nil, fmt.Errorf("未能解析到可用的音频下载链接")
}

func (m *Manager) downloadSong(run *taskRun) error {
	ctx, task := run.ctx, run.task
	if err := ctx.Err(); err != nil {
		return err
	}
	source, err := m.resolveSource(ctx, task)
	if err != nil {
		return err
	}
	raw, headers := source.URL, source.Headers
	cfg := store.DefaultConfig().Download
	if m.configMgr != nil {
		cfg = m.configMgr.DownloadSettings()
	}
	dir := cfg.DownloadPath
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	base, ext := formatFilename(cfg.FileNamingPattern, task.Song), detectExtension(raw)
	final, err := m.reservePath(task, dir, base, ext)
	if err != nil {
		return err
	}
	run.final = final
	task.FilePath = final
	run.part = partialPath(task)
	if !m.updateRun(run, func(t *Task) { t.FilePath = final; m.saveRecordLocked(t) }) {
		return context.Canceled
	}
	// Preserve unambiguous legacy partial files during the first resume.
	if task.FilePath == run.task.FilePath && task.FilePath != "" {
		if _, err = os.Stat(run.part); os.IsNotExist(err) {
			if _, e := os.Stat(final + ".part"); e == nil {
				if e = os.Rename(final+".part", run.part); e != nil {
					return e
				}
			}
		}
	}
	var existing int64
	if info, e := os.Stat(run.part); e == nil {
		existing = info.Size()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, raw, nil)
	if err != nil {
		return err
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	if existing > 0 {
		req.Header.Set("Range", fmt.Sprintf("bytes=%d-", existing))
	}
	resp, err := m.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 && resp.StatusCode != 206 {
		return fmt.Errorf("服务器响应异常状态码: %s", resp.Status)
	}
	total := resp.ContentLength
	flags := os.O_CREATE | os.O_WRONLY | os.O_TRUNC
	if resp.StatusCode == 206 {
		var start, end, size int64
		if _, err = fmt.Sscanf(resp.Header.Get("Content-Range"), "bytes %d-%d/%d", &start, &end, &size); err != nil || start != existing || end < start || size <= end || resp.ContentLength >= 0 && resp.ContentLength != end-start+1 {
			return fmt.Errorf("断点响应 Content-Range 无效")
		}
		total = size
		flags = os.O_CREATE | os.O_WRONLY | os.O_APPEND
	} else {
		existing = 0
	}
	file, err := os.OpenFile(run.part, flags, 0644)
	if err != nil {
		return err
	}
	defer file.Close()
	m.updateRun(run, func(t *Task) { t.TotalBytes = total; t.DownloadedBytes = existing })
	buf := make([]byte, 32*1024)
	last := time.Now()
	previous := existing
	for {
		if err = ctx.Err(); err != nil {
			return err
		}
		n, readErr := resp.Body.Read(buf)
		if n > 0 {
			if err = ctx.Err(); err != nil {
				return err
			}
			written, writeErr := file.Write(buf[:n])
			if writeErr != nil {
				return writeErr
			}
			if written != n {
				return io.ErrShortWrite
			}
			existing += int64(written)
			if elapsed := time.Since(last); elapsed >= 300*time.Millisecond {
				speed := int64(float64(existing-previous) / elapsed.Seconds())
				if !m.updateRun(run, func(t *Task) {
					t.DownloadedBytes = existing
					t.Speed = speed
					if total > 0 {
						t.Progress = float64(existing) / float64(total)
					}
					m.saveRecordLocked(t)
				}) {
					return context.Canceled
				}
				last = time.Now()
				previous = existing
				m.notifyUpdate()
			}
		}
		if readErr != nil {
			if readErr == io.EOF {
				break
			}
			return readErr
		}
	}
	if existing == 0 {
		return fmt.Errorf("服务器没有返回音频内容")
	}
	if total >= 0 && existing != total {
		return io.ErrUnexpectedEOF
	}
	if err = file.Sync(); err != nil {
		return err
	}
	if err = file.Close(); err != nil {
		return err
	}
	if err = m.publish(run, dir, base, ext); err != nil {
		return err
	}
	// Audio publication is the completion boundary. Optional lyrics must not
	// turn a finished file into a paused task that downloads it a second time.
	m.mu.Lock()
	t := m.taskMap[task.ID]
	if t == nil || m.active[task.ID] != run {
		m.mu.Unlock()
		return context.Canceled
	}
	t.Status = StatusCompleted
	t.Progress = 1
	t.DownloadedBytes = existing
	t.TotalBytes = existing
	t.Speed = 0
	t.FilePath = run.final
	t.UpdatedAt = time.Now().UnixMilli()
	m.available[t.ID] = true
	m.saveRecordLocked(t)
	m.mu.Unlock()
	m.notifyUpdate()
	if cfg.AutoDownloadLyrics && m.pluginMgr != nil && ctx.Err() == nil {
		if lyric, err := m.pluginMgr.GetLyric(ctx, task.Song); err == nil && lyric != nil && lyric.RawLrc != "" {
			path := strings.TrimSuffix(run.final, filepath.Ext(run.final)) + ".lrc"
			if f, e := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0644); e == nil {
				_, e = f.WriteString(lyric.RawLrc)
				ce := f.Close()
				if e == nil && ce == nil {
					run.lyric = path
				}
			}
		}
	}
	if run.lyric != "" {
		m.mu.Lock()
		if t := m.taskMap[task.ID]; t != nil && m.active[task.ID] == run && t.Status == StatusCompleted {
			t.LyricPath = run.lyric
			m.saveRecordLocked(t)
		}
		m.mu.Unlock()
	}
	return nil
}

// formatFilename replaces placeholders and strips filesystem-illegal characters
func formatFilename(pattern string, song plugin.MusicItem) string {
	artist := song.Artist
	if artist == "" {
		artist = "未知歌手"
	}
	title := song.Title
	if title == "" {
		title = "未知曲目"
	}
	album := song.Album
	if album == "" {
		album = "未知专辑"
	}

	r := strings.NewReplacer(
		"{artist}", artist,
		"{title}", title,
		"{album}", album,
		"{platform}", song.Platform,
	)
	name := r.Replace(pattern)

	// Replace illegal characters: \ / : * ? " < > |
	illegal := regexp.MustCompile(`[\\/:*?"<>|]`)
	name = illegal.ReplaceAllString(name, "_")
	name = strings.TrimSpace(name)
	if name == "" {
		name = fmt.Sprintf("music_%s", song.ID)
	}
	return name
}

// detectExtension infers audio extension from URL
func detectExtension(rawURL string) string {
	cleanURL := strings.Split(rawURL, "?")[0]
	lower := strings.ToLower(cleanURL)
	switch {
	case strings.HasSuffix(lower, ".flac"):
		return ".flac"
	case strings.HasSuffix(lower, ".m4a"):
		return ".m4a"
	case strings.HasSuffix(lower, ".ogg"):
		return ".ogg"
	case strings.HasSuffix(lower, ".wav"):
		return ".wav"
	case strings.HasSuffix(lower, ".aac"):
		return ".aac"
	default:
		return ".mp3"
	}
}
