package player

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"musicgo/internal/plugin"
)

type cacheEntry struct {
	Size   int64     `json:"size"`
	SHA256 string    `json:"sha256"`
	Used   time.Time `json:"used"`
	users  int
	remove bool
}

// AudioCache owns only disposable audio files, independently of downloads.
// Its filesystem methods are called by loaders/settings workers, never paint.
type AudioCache struct {
	stats          atomic.Value
	loaded         bool
	mu             sync.Mutex
	dir            string
	limit, partial int64
	transfers      int
	entries        map[string]*cacheEntry
	ready          chan struct{}
	generation     uint64
	lastError      string
}

type AudioCacheStats struct {
	UsedBytes, LimitBytes int64
	Ready                 bool
	Downloading           bool
	Error                 string
}

func NewAudioCache(dir string, mb int) *AudioCache {
	c := &AudioCache{dir: dir, limit: int64(max(0, mb)) << 20, entries: make(map[string]*cacheEntry), ready: make(chan struct{})}
	c.stats.Store(AudioCacheStats{LimitBytes: c.limit})
	go c.scan()
	return c
}
func audioCacheKey(song plugin.MusicItem, quality string) string {
	h := sha256.Sum256([]byte(string(song.Key()) + "\x00" + quality))
	return hex.EncodeToString(h[:])
}
func (c *AudioCache) path(key string) string { return filepath.Join(c.dir, key+".audio") }
func (c *AudioCache) scan() {
	c.mu.Lock()
	defer func() { c.loaded = true; c.publishLocked(); c.mu.Unlock(); close(c.ready) }()
	if err := os.MkdirAll(c.dir, 0700); err != nil {
		c.lastError = err.Error()
		return
	}
	files, _ := os.ReadDir(c.dir)
	for _, f := range files {
		if f.IsDir() {
			continue
		}
		name := f.Name()
		ext := filepath.Ext(name)
		key := name[:len(name)-len(ext)]
		if ext == ".part" {
			_ = os.Remove(filepath.Join(c.dir, name))
			continue
		}
		if ext != ".json" || len(key) != 64 {
			continue
		}
		var entry cacheEntry
		b, err := os.ReadFile(filepath.Join(c.dir, name))
		info, statErr := os.Stat(c.path(key))
		if err != nil || json.Unmarshal(b, &entry) != nil || entry.Size <= 0 || len(entry.SHA256) != 64 || statErr != nil || info.Size() != entry.Size {
			_ = os.Remove(filepath.Join(c.dir, name))
			_ = os.Remove(c.path(key))
			continue
		}
		c.entries[key] = &entry
	}
	for _, f := range files {
		if filepath.Ext(f.Name()) == ".audio" {
			key := f.Name()[:len(f.Name())-6]
			if c.entries[key] == nil {
				_ = os.Remove(filepath.Join(c.dir, f.Name()))
			}
		}
	}
	c.pruneLocked(0)
}
func (c *AudioCache) wait(ctx context.Context) error {
	select {
	case <-c.ready:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
func (c *AudioCache) Stats() AudioCacheStats {
	if c == nil {
		return AudioCacheStats{Ready: true}
	}
	return c.stats.Load().(AudioCacheStats)
}
func (c *AudioCache) publishLocked() {
	s := AudioCacheStats{UsedBytes: c.partial, LimitBytes: c.limit, Ready: c.loaded, Error: c.lastError, Downloading: c.transfers > 0}
	for _, e := range c.entries {
		s.UsedBytes += e.Size
	}
	c.stats.Store(s)
}
func (c *AudioCache) SetLimit(mb int) {
	_ = c.wait(context.Background())
	c.mu.Lock()
	defer c.mu.Unlock()
	defer c.publishLocked()
	c.limit = int64(max(mb, 0)) << 20
	c.pruneLocked(0)
}
func (c *AudioCache) Clear() {
	_ = c.wait(context.Background())
	c.mu.Lock()
	defer c.mu.Unlock()
	defer c.publishLocked()
	c.generation++
	for key, e := range c.entries {
		e.remove = true
		if e.users == 0 {
			c.removeLocked(key)
		}
	}
}
func (c *AudioCache) removeLocked(key string) {
	_ = os.Remove(c.path(key))
	_ = os.Remove(filepath.Join(c.dir, key+".json"))
	delete(c.entries, key)
}
func (c *AudioCache) pruneLocked(extra int64) bool {
	total := c.partial + extra
	type item struct {
		key  string
		used time.Time
	}
	var idle []item
	for key, e := range c.entries {
		total += e.Size
		if e.users == 0 {
			idle = append(idle, item{key, e.Used})
		}
	}
	sort.Slice(idle, func(i, j int) bool { return idle[i].used.Before(idle[j].used) })
	for _, i := range idle {
		e := c.entries[i.key]
		if total <= c.limit && !e.remove {
			continue
		}
		total -= e.Size
		c.removeLocked(i.key)
	}
	return total <= c.limit
}

// Lookup pins the validated file until the playback context ends.
func (c *AudioCache) Lookup(ctx context.Context, song plugin.MusicItem, quality string) (string, func()) {
	if c == nil || c.wait(ctx) != nil {
		return "", func() {}
	}
	key := audioCacheKey(song, quality)
	c.mu.Lock()
	e := c.entries[key]
	if c.limit == 0 || e == nil || e.remove {
		c.mu.Unlock()
		return "", func() {}
	}
	e.users++
	c.mu.Unlock()
	var once sync.Once
	release := func() {
		once.Do(func() {
			c.mu.Lock()
			defer c.mu.Unlock()
			defer c.publishLocked()
			e.users--
			if e.users == 0 && e.remove {
				c.removeLocked(key)
			}
			c.pruneLocked(0)
		})
	}
	// Size parity is the cheap validity gate, the same one scan applies: the
	// SHA256 was checked when the entry was written, and a mid-file corruption
	// a stat cannot see fails the decoder and recovers from the source. Full
	// hashing here read the whole file on every hit and stalled playback start.
	info, err := os.Stat(c.path(key))
	if ctx.Err() != nil {
		release()
		return "", func() {}
	}
	if err != nil || info.Size() != e.Size {
		c.mu.Lock()
		e.remove = true
		c.mu.Unlock()
		release()
		return "", func() {}
	}
	c.mu.Lock()
	e.Used = time.Now()
	b, _ := json.Marshal(e)
	_ = os.WriteFile(filepath.Join(c.dir, key+".json"), b, 0600)
	c.mu.Unlock()
	return c.path(key), release
}

func (c *AudioCache) createPart(ctx context.Context) (*os.File, uint64, error) {
	if c == nil {
		return nil, 0, errors.New("audio cache disabled")
	}
	if err := c.wait(ctx); err != nil {
		return nil, 0, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	defer c.publishLocked()
	if c.limit <= 0 {
		return nil, 0, errors.New("audio cache disabled")
	}
	f, err := os.CreateTemp(c.dir, "audio-*.part")
	if err != nil {
		c.lastError = err.Error()
	}
	return f, c.generation, err
}
func (c *AudioCache) reserve(owned, n int64) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	defer c.publishLocked()
	if c.limit == 0 || owned+n > c.limit/4 || !c.pruneLocked(n) {
		return false
	}
	c.partial += n
	return true
}
func (c *AudioCache) discard(n int64) { c.mu.Lock(); c.partial -= n; c.publishLocked(); c.mu.Unlock() }
func (c *AudioCache) keep(key, path string, n int64, digest string, generation uint64) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	defer c.publishLocked()
	if generation != c.generation || c.limit == 0 || n > c.limit/4 || n <= 0 {
		return false
	}
	if e := c.entries[key]; e != nil {
		return false
	}
	entry := &cacheEntry{Size: n, SHA256: digest, Used: time.Now(), users: 1}
	meta, err := json.Marshal(entry)
	if err == nil {
		err = os.Rename(path, c.path(key))
	}
	if err == nil {
		err = os.WriteFile(filepath.Join(c.dir, key+".json"), meta, 0600)
	}
	if err != nil {
		c.lastError = err.Error()
		_ = os.Remove(c.path(key))
		return false
	}
	c.partial -= n
	c.entries[key] = entry
	return true
}
func (c *AudioCache) release(key string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	defer c.publishLocked()
	if e := c.entries[key]; e != nil {
		e.users--
		if e.users == 0 && e.remove {
			c.removeLocked(key)
		}
	}
	c.pruneLocked(0)
}

func (e *Engine) SetAudioCache(cache *AudioCache) { e.mu.Lock(); e.audioCache = cache; e.mu.Unlock() }
func (e *Engine) AudioCache() *AudioCache         { e.mu.RLock(); defer e.mu.RUnlock(); return e.audioCache }

func (c *AudioCache) transferChanged(delta int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.transfers += delta
	c.publishLocked()
}
