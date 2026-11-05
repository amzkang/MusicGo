package player

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"musicgo/internal/plugin"
)

var errCacheBypass = errors.New("audio cache unavailable")

// AudioResource shares one sequential transfer. Separate cursors may request
// remote ranges beyond its prefix; those bytes never promote a partial file.
type AudioResource struct {
	mu                 sync.Mutex
	ctx                context.Context
	cancel             context.CancelFunc
	cache              *AudioCache
	file               *os.File
	key, path          string
	generation         uint64
	headers            map[string]string
	raw, contentType   string
	size, n, reserved  int64
	done, kept, closed bool
	err                error
	changed            chan struct{}
	ready, finished    chan struct{}
	server             *http.Server
	endpoint           string
	closeOnce          sync.Once
}

func (c *AudioCache) Open(ctx context.Context, song plugin.MusicItem, quality, raw string, headers map[string]string) (*AudioResource, error) {
	f, gen, err := c.createPart(ctx)
	if err != nil {
		return nil, err
	}
	rctx, cancel := context.WithCancel(ctx)
	r := &AudioResource{ctx: rctx, cancel: cancel, cache: c, file: f, key: audioCacheKey(song, quality), path: f.Name(), generation: gen, raw: raw, size: -1,
		headers: make(map[string]string), changed: make(chan struct{}), ready: make(chan struct{}), finished: make(chan struct{})}
	for k, v := range headers {
		r.headers[k] = v
	}
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		cancel()
		_ = f.Close()
		_ = os.Remove(f.Name())
		return nil, err
	}
	var token [24]byte
	if _, err = rand.Read(token[:]); err != nil {
		cancel()
		_ = listener.Close()
		_ = f.Close()
		_ = os.Remove(f.Name())
		return nil, err
	}
	route := "/" + hex.EncodeToString(token[:])
	r.endpoint = "http://" + listener.Addr().String() + route
	r.server = &http.Server{ReadHeaderTimeout: 5 * time.Second, Handler: http.HandlerFunc(func(w http.ResponseWriter, q *http.Request) {
		if q.URL.Path != route {
			http.NotFound(w, q)
			return
		}
		r.serve(w, q)
	})}
	go func() { _ = r.server.Serve(listener) }()
	c.transferChanged(1)
	go r.download()
	select {
	case <-r.ready:
	case <-ctx.Done():
		r.Close()
		return nil, ctx.Err()
	}
	r.mu.Lock()
	err = r.err
	r.mu.Unlock()
	if err != nil {
		r.Close()
		return nil, err
	}
	return r, nil
}
func (r *AudioResource) URL() string { return r.endpoint }
func (r *AudioResource) wakeLocked() { close(r.changed); r.changed = make(chan struct{}) }
func (r *AudioResource) download() {
	defer close(r.finished)
	defer r.cache.transferChanged(-1)
	ready := false
	finish := func(err error) {
		r.mu.Lock()
		r.done, r.err = true, err
		r.wakeLocked()
		r.mu.Unlock()
		if !ready {
			close(r.ready)
			ready = true
		}
	}
	request, err := http.NewRequestWithContext(r.ctx, http.MethodGet, r.raw, nil)
	if err != nil {
		finish(err)
		return
	}
	for k, v := range r.headers {
		request.Header.Set(k, v)
	}
	request.Header.Set("Accept-Encoding", "identity")
	client := &http.Client{Transport: &http.Transport{Proxy: http.ProxyFromEnvironment, DialContext: (&net.Dialer{Timeout: 15 * time.Second, KeepAlive: 30 * time.Second}).DialContext, ResponseHeaderTimeout: 20 * time.Second, DisableCompression: true}}
	defer client.CloseIdleConnections()
	resp, err := client.Do(request)
	if err != nil {
		finish(err)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		finish(fmt.Errorf("audio download: %s", resp.Status))
		return
	}
	content := strings.ToLower(resp.Header.Get("Content-Type"))
	if strings.Contains(content, "mpegurl") || strings.Contains(content, "dash+xml") || resp.ContentLength == 0 {
		finish(errCacheBypass)
		return
	}
	r.mu.Lock()
	r.size, r.contentType = resp.ContentLength, resp.Header.Get("Content-Type")
	r.mu.Unlock()
	if resp.ContentLength > 0 {
		r.cache.mu.Lock()
		tooLarge := resp.ContentLength > r.cache.limit/4
		r.cache.mu.Unlock()
		if tooLarge {
			finish(errCacheBypass)
			return
		}
	}
	close(r.ready)
	ready = true
	buf := make([]byte, 64<<10)
	digest := sha256.New()
	for {
		n, readErr := resp.Body.Read(buf)
		if n > 0 {
			if !r.cache.reserve(r.reserved, int64(n)) {
				finish(errCacheBypass)
				return
			}
			r.reserved += int64(n)
			r.mu.Lock()
			written, writeErr := r.file.WriteAt(buf[:n], r.n)
			r.n += int64(written)
			r.wakeLocked()
			r.mu.Unlock()
			if writeErr != nil || written != n {
				if writeErr == nil {
					writeErr = io.ErrShortWrite
				}
				finish(writeErr)
				return
			}
			_, _ = digest.Write(buf[:n])
		}
		if readErr == io.EOF {
			r.mu.Lock()
			total, expected := r.n, r.size
			r.mu.Unlock()
			if expected >= 0 && total != expected {
				finish(io.ErrUnexpectedEOF)
				return
			}
			if total == 0 {
				finish(io.ErrUnexpectedEOF)
				return
			}
			r.mu.Lock()
			if r.size < 0 {
				r.size = total
			}
			// Close readers before renaming: Windows cannot rename an open file.
			_ = r.file.Close()
			r.kept = r.cache.keep(r.key, r.path, total, hex.EncodeToString(digest.Sum(nil)), r.generation)
			if r.kept {
				r.path = r.cache.path(r.key)
				r.reserved = 0
			}
			r.file, err = os.Open(r.path)
			r.mu.Unlock()
			finish(err)
			return
		}
		if readErr != nil {
			finish(readErr)
			return
		}
	}
}
func (r *AudioResource) Failure() error { r.mu.Lock(); defer r.mu.Unlock(); return r.err }
func (r *AudioResource) NewReader(ctx context.Context) io.ReadSeekCloser {
	child, cancel := context.WithCancel(r.ctx)
	stop := context.AfterFunc(ctx, cancel)
	return &resourceReader{r: r, ctx: child, cancel: func() { stop(); cancel() }}
}
func (r *AudioResource) Close() {
	r.closeOnce.Do(func() {
		r.cancel()
		_ = r.server.Close()
		<-r.finished
		r.mu.Lock()
		r.closed = true
		r.wakeLocked()
		if r.file != nil {
			_ = r.file.Close()
		}
		kept, path, n := r.kept, r.path, r.reserved
		r.mu.Unlock()
		if kept {
			r.cache.release(r.key)
		} else {
			_ = os.Remove(path)
			r.cache.discard(n)
		}
	})
}

type resourceReader struct {
	mu           sync.Mutex
	activeCancel context.CancelFunc
	generation   uint64
	interrupted  bool
	r            *AudioResource
	ctx          context.Context
	cancel       context.CancelFunc
	pos          int64
	remote       *HTTPStreamer
}

func (v *resourceReader) Read(p []byte) (int, error) {
	readCtx, cancel := context.WithCancel(v.ctx)
	v.mu.Lock()
	if v.interrupted {
		v.interrupted = false
		v.mu.Unlock()
		cancel()
		return 0, errReadInterrupted
	}
	v.activeCancel = cancel
	v.mu.Unlock()
	defer func() { cancel(); v.mu.Lock(); v.activeCancel = nil; v.mu.Unlock() }()

	if len(p) == 0 {
		return 0, nil
	}
	for {
		if err := v.ctx.Err(); err != nil {
			return 0, err
		}
		if readCtx.Err() != nil {
			return 0, errReadInterrupted
		}
		r := v.r
		r.mu.Lock()
		if r.closed {
			r.mu.Unlock()
			return 0, io.ErrClosedPipe
		}
		if r.file == nil {
			r.mu.Unlock()
			return 0, errCacheBypass
		}
		if v.pos < r.n {
			count := min(int64(len(p)), r.n-v.pos)
			n, err := r.file.ReadAt(p[:int(count)], v.pos)
			v.pos += int64(n)
			r.mu.Unlock()
			return n, err
		}
		if r.done && r.err != nil {
			err := r.err
			r.mu.Unlock()
			return 0, err
		}
		if r.done || r.size >= 0 && v.pos >= r.size {
			r.mu.Unlock()
			return 0, io.EOF
		}
		if v.pos > r.n {
			r.mu.Unlock()
			v.mu.Lock()
			remote := v.remote
			v.mu.Unlock()
			if remote == nil {
				var err error
				remote, err = NewHTTPStreamerContext(v.ctx, r.raw, r.headers)
				if err != nil {
					return 0, err
				}
				v.mu.Lock()
				if v.ctx.Err() != nil {
					v.mu.Unlock()
					_ = remote.Close()
					return 0, v.ctx.Err()
				}
				v.remote = remote
				v.mu.Unlock()
			}
			if _, err := remote.Seek(v.pos, io.SeekStart); err != nil {
				return 0, err
			}
			gen := remote.readGeneration()
			stop := context.AfterFunc(readCtx, func() { remote.interruptRead(gen) })
			n, err := remote.Read(p)
			stop()
			v.pos += int64(n)
			return n, err
		}
		changed := r.changed
		r.mu.Unlock()
		select {
		case <-changed:
		case <-readCtx.Done():
			if v.ctx.Err() != nil {
				return 0, v.ctx.Err()
			}
			return 0, errReadInterrupted
		}
	}
}
func (v *resourceReader) Seek(offset int64, whence int) (int64, error) {
	pos := offset
	switch whence {
	case io.SeekStart:
	case io.SeekCurrent:
		pos += v.pos
	case io.SeekEnd:
		v.r.mu.Lock()
		size := v.r.size
		v.r.mu.Unlock()
		if size < 0 {
			return 0, errors.New("unknown stream length")
		}
		pos += size
	default:
		return 0, errors.New("invalid whence")
	}
	if pos < 0 {
		return 0, errors.New("negative seek position")
	}
	v.mu.Lock()
	remote := v.remote
	v.remote = nil
	v.generation++
	v.interrupted = false
	v.mu.Unlock()
	if remote != nil {
		_ = remote.Close()
	}
	v.pos = pos
	return pos, nil
}
func (v *resourceReader) Close() error {
	v.cancel()
	v.mu.Lock()
	remote := v.remote
	v.remote = nil
	v.mu.Unlock()
	if remote != nil {
		return remote.Close()
	}
	return nil
}

func (r *AudioResource) serve(w http.ResponseWriter, q *http.Request) {
	if q.Method != http.MethodGet && q.Method != http.MethodHead {
		w.WriteHeader(405)
		return
	}
	r.mu.Lock()
	size, content := r.size, r.contentType
	r.mu.Unlock()
	start, end := int64(0), size-1
	partial := false
	if value := q.Header.Get("Range"); value != "" && !(size < 0 && value == "bytes=0-") {
		valid := strings.HasPrefix(value, "bytes=") && !strings.Contains(value, ",") && size >= 0
		if valid {
			parts := strings.SplitN(strings.TrimPrefix(value, "bytes="), "-", 2)
			valid = len(parts) == 2
			if valid {
				if parts[0] == "" {
					suffix, err := strconv.ParseInt(parts[1], 10, 64)
					valid = err == nil && suffix > 0
					start = max(0, size-suffix)
				} else {
					var err error
					start, err = strconv.ParseInt(parts[0], 10, 64)
					valid = err == nil && start >= 0
					if parts[1] != "" {
						end, err = strconv.ParseInt(parts[1], 10, 64)
						valid = valid && err == nil
					}
				}
			}
		}
		if !valid || start >= size || end < start {
			w.Header().Set("Content-Range", fmt.Sprintf("bytes */%d", size))
			w.WriteHeader(416)
			return
		}
		end = min(end, size-1)
		partial = true
	}
	if size >= 0 {
		w.Header().Set("Accept-Ranges", "bytes")
	}
	if content != "" {
		w.Header().Set("Content-Type", content)
	}
	if size >= 0 {
		w.Header().Set("Content-Length", strconv.FormatInt(end-start+1, 10))
	}
	if partial {
		w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end, size))
		w.WriteHeader(206)
	}
	if q.Method == http.MethodHead {
		return
	}
	cursor := r.NewReader(q.Context())
	defer cursor.Close()
	if _, err := cursor.Seek(start, io.SeekStart); err != nil {
		return
	}
	var err error
	if size >= 0 {
		_, err = io.CopyN(w, cursor, end-start+1)
	} else {
		_, err = io.Copy(w, cursor)
	}
	if err != nil && q.Context().Err() == nil {
		panic(http.ErrAbortHandler)
	}
}

// A queued decoder seek may already have landed before the interruption is
// delivered. Its generation prevents canceling the new position's first read.
func (v *resourceReader) readGeneration() uint64 {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.generation
}
func (v *resourceReader) interruptRead(generation uint64) {
	v.mu.Lock()
	if v.generation != generation {
		v.mu.Unlock()
		return
	}
	v.interrupted = true
	cancel := v.activeCancel
	v.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}
