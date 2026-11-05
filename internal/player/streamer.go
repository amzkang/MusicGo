package player

import (
	"context"
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
)

// HTTPStreamer serializes reads while allowing Seek and Close to cancel IO.
type HTTPStreamer struct {
	mu             sync.Mutex
	readMu         sync.Mutex
	url            string
	headers        map[string]string
	client         *http.Client
	ctx            context.Context
	cancel         context.CancelFunc
	streamCancel   context.CancelFunc
	pos, totalSize int64
	activeBody     io.ReadCloser
	cacheFile      *os.File
	cachedSize     int64
	closed         bool
	gen            uint64
	interruptGen   uint64
}

var errReadInterrupted = errors.New("stream read interrupted")

// InterruptRead wakes the decoder so its queued seek can run.
func (s *HTTPStreamer) readGeneration() uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.gen
}

func (s *HTTPStreamer) interruptRead(gen uint64) {
	s.mu.Lock()
	if s.gen != gen || s.closed {
		s.mu.Unlock()
		return
	}
	s.gen++
	s.interruptGen++
	body, cancel := s.activeBody, s.streamCancel
	s.activeBody, s.streamCancel = nil, nil
	s.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	if body != nil {
		go body.Close()
	}
}

func NewHTTPStreamer(raw string, headers map[string]string) (*HTTPStreamer, error) {
	return NewHTTPStreamerContext(context.Background(), raw, headers)
}
func NewHTTPStreamerContext(parent context.Context, raw string, headers map[string]string) (*HTTPStreamer, error) {
	ctx, cancel := context.WithCancel(parent)
	copied := make(map[string]string, len(headers))
	for k, v := range headers {
		copied[k] = v
	}
	s := &HTTPStreamer{url: raw, headers: copied, ctx: ctx, cancel: cancel, totalSize: -1, client: &http.Client{Transport: &http.Transport{
		Proxy: http.ProxyFromEnvironment, DialContext: (&net.Dialer{Timeout: 15 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
		ResponseHeaderTimeout: 20 * time.Second, ExpectContinueTimeout: 5 * time.Second, MaxIdleConns: 4, IdleConnTimeout: 60 * time.Second, DisableCompression: true,
	}}}
	s.cacheFile, _ = os.CreateTemp("", "musicgo-stream-*.cache")
	go s.probeSize()
	return s, nil
}
func (s *HTTPStreamer) request(ctx context.Context, method string, position int64) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, method, s.url, nil)
	if err != nil {
		return nil, err
	}
	for k, v := range s.headers {
		req.Header.Set(k, v)
	}
	if method == http.MethodGet && position > 0 {
		req.Header.Set("Range", fmt.Sprintf("bytes=%d-", position))
	}
	return s.client.Do(req)
}
func (s *HTTPStreamer) probeSize() {
	resp, err := s.request(s.ctx, http.MethodHead, 0)
	if err != nil {
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusOK && resp.ContentLength > 0 {
		s.mu.Lock()
		if !s.closed {
			s.totalSize = resp.ContentLength
		}
		s.mu.Unlock()
	}
}
func (s *HTTPStreamer) openStream(ctx context.Context, pos int64) (*http.Response, error) {
	resp, err := s.request(ctx, http.MethodGet, pos)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != 200 && resp.StatusCode != 206 {
		resp.Body.Close()
		return nil, fmt.Errorf("unexpected http status: %s", resp.Status)
	}
	if resp.StatusCode == 206 {
		var start, end, total int64
		if _, err = fmt.Sscanf(resp.Header.Get("Content-Range"), "bytes %d-%d/%d", &start, &end, &total); err != nil || start != pos {
			resp.Body.Close()
			return nil, fmt.Errorf("invalid Content-Range at %d", pos)
		}
	}
	if pos > 0 && resp.StatusCode == 200 {
		if _, err = io.CopyN(io.Discard, resp.Body, pos); err != nil {
			resp.Body.Close()
			return nil, err
		}
	}
	return resp, nil
}
func (s *HTTPStreamer) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	s.readMu.Lock()
	defer s.readMu.Unlock()
	for {
		s.mu.Lock()
		if s.closed {
			s.mu.Unlock()
			return 0, errors.New("streamer closed")
		}
		gen, interruptGen, pos := s.gen, s.interruptGen, s.pos
		if s.cacheFile != nil && pos < s.cachedSize {
			count := min(int64(len(p)), s.cachedSize-pos)
			n, err := s.cacheFile.ReadAt(p[:int(count)], pos)
			s.pos += int64(n)
			s.mu.Unlock()
			return n, err
		}
		body := s.activeBody
		if body == nil {
			ctx, cancel := context.WithCancel(s.ctx)
			s.streamCancel = cancel
			s.mu.Unlock()
			resp, err := s.openStream(ctx, pos)
			s.mu.Lock()
			if s.closed || s.gen != gen {
				closed := s.closed
				interrupted := interruptGen != s.interruptGen
				s.mu.Unlock()
				cancel()
				if resp != nil {
					resp.Body.Close()
				}
				if closed {
					return 0, errors.New("streamer closed")
				}
				if interrupted {
					return 0, errReadInterrupted
				}
				continue
			}
			if err != nil {
				s.streamCancel = nil
				s.mu.Unlock()
				cancel()
				return 0, err
			}
			body = resp.Body
			s.activeBody = body
			if s.totalSize <= 0 {
				parts := strings.Split(resp.Header.Get("Content-Range"), "/")
				if len(parts) == 2 {
					s.totalSize, _ = strconv.ParseInt(parts[1], 10, 64)
				} else if resp.ContentLength > 0 {
					s.totalSize = pos + resp.ContentLength
				}
			}
		}
		s.mu.Unlock()
		n, err := body.Read(p)
		s.mu.Lock()
		if s.closed || s.gen != gen {
			closed := s.closed
			interrupted := interruptGen != s.interruptGen
			s.mu.Unlock()
			if closed {
				return 0, errors.New("streamer closed")
			}
			if interrupted {
				return 0, errReadInterrupted
			}
			continue
		}
		if n > 0 {
			if s.cacheFile != nil && pos == s.cachedSize {
				if written, _ := s.cacheFile.WriteAt(p[:n], pos); written > 0 {
					s.cachedSize += int64(written)
				}
			}
			s.pos += int64(n)
		}
		if err != nil && err != io.EOF {
			s.activeBody = nil
			s.mu.Unlock()
			body.Close()
			return n, err
		}
		s.mu.Unlock()
		return n, err
	}
}
func (s *HTTPStreamer) Seek(offset int64, whence int) (int64, error) {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return 0, errors.New("streamer closed")
	}
	pos := offset
	switch whence {
	case io.SeekStart:
	case io.SeekCurrent:
		pos += s.pos
	case io.SeekEnd:
		if s.totalSize < 0 {
			s.mu.Unlock()
			return 0, errors.New("unknown stream length")
		}
		pos += s.totalSize
	default:
		s.mu.Unlock()
		return 0, errors.New("invalid whence")
	}
	if pos < 0 {
		s.mu.Unlock()
		return 0, errors.New("negative seek position")
	}
	if pos == s.pos {
		s.mu.Unlock()
		return pos, nil
	}
	s.gen++
	s.pos = pos
	body, cancel := s.activeBody, s.streamCancel
	s.activeBody = nil
	s.streamCancel = nil
	s.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	if body != nil {
		body.Close()
	}
	return pos, nil
}
func (s *HTTPStreamer) TotalSize() int64 { s.mu.Lock(); defer s.mu.Unlock(); return s.totalSize }
func (s *HTTPStreamer) Close() error {
	s.cancel()
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	s.gen++
	body, file := s.activeBody, s.cacheFile
	s.activeBody = nil
	s.cacheFile = nil
	s.mu.Unlock()
	if body != nil {
		body.Close()
	}
	if file != nil {
		path := file.Name()
		file.Close()
		os.Remove(path)
	}
	s.client.CloseIdleConnections()
	return nil
}
