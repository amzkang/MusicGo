package ui

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"image"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const maxDiskArtworkBytes = 256 << 20
const maxDiskArtworkAge = 30 * 24 * time.Hour

// SetDiskCacheDir enables the optional persistent cache. All disk access is
// performed by the existing background artwork loaders.
func (ic *ImageCache) SetDiskCacheDir(dir string) {
	ic.mu.Lock()
	ic.diskDir = dir
	ic.mu.Unlock()
}

func (ic *ImageCache) artworkPath(rawURL string) string {
	ic.mu.Lock()
	dir := ic.diskDir
	ic.mu.Unlock()
	if dir == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(rawURL))
	return filepath.Join(dir, fmt.Sprintf("%x.image", sum))
}

func (ic *ImageCache) readArtwork(rawURL string) ([]byte, bool) {
	path := ic.artworkPath(rawURL)
	if path == "" {
		return nil, false
	}
	ic.diskMu.Lock()
	defer ic.diskMu.Unlock()
	if ic.ctx.Err() != nil {
		return nil, false
	}
	info, err := os.Stat(path)
	if err != nil || info.Size() <= 0 || info.Size() > maxImageBytes || time.Since(info.ModTime()) > maxDiskArtworkAge {
		if err == nil {
			_ = os.Remove(path)
		}
		return nil, false
	}
	data, err := os.ReadFile(path)
	if err != nil || len(data) == 0 || len(data) > maxImageBytes {
		return nil, false
	}
	return data, true
}

func (ic *ImageCache) removeArtwork(rawURL string) {
	if path := ic.artworkPath(rawURL); path != "" {
		ic.diskMu.Lock()
		_ = os.Remove(path)
		ic.diskMu.Unlock()
	}
}

func (ic *ImageCache) writeArtwork(rawURL string, data []byte) {
	path := ic.artworkPath(rawURL)
	if path == "" {
		return
	}
	ic.diskMu.Lock()
	defer ic.diskMu.Unlock()
	if ic.ctx.Err() != nil {
		return
	}
	dir := filepath.Dir(path)
	if os.MkdirAll(dir, 0700) != nil {
		return
	}
	file, err := os.CreateTemp(dir, ".artwork-*")
	if err != nil {
		return
	}
	tmp := file.Name()
	defer os.Remove(tmp)
	_, err = file.Write(data)
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	if err != nil || os.Rename(tmp, path) != nil {
		return
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	type entry struct {
		path string
		size int64
		mod  time.Time
	}
	var files []entry
	var total int64
	for _, item := range entries {
		if item.IsDir() || !strings.HasSuffix(item.Name(), ".image") {
			continue
		}
		info, err := item.Info()
		if err != nil || !info.Mode().IsRegular() {
			continue
		}
		files = append(files, entry{filepath.Join(dir, item.Name()), info.Size(), info.ModTime()})
		total += info.Size()
	}
	sort.Slice(files, func(i, j int) bool { return files[i].mod.Before(files[j].mod) })
	for _, file := range files {
		if total <= maxDiskArtworkBytes && time.Since(file.mod) <= maxDiskArtworkAge {
			break
		}
		if os.Remove(file.path) == nil {
			total -= file.size
		}
	}
}

func (ic *ImageCache) fetchArtwork(rawURL string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ic.ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36")
	host := strings.ToLower(req.URL.Hostname())
	if host == "qq.com" || strings.HasSuffix(host, ".qq.com") || host == "gtimg.cn" || strings.HasSuffix(host, ".gtimg.cn") {
		req.Header.Set("Referer", "https://y.qq.com/")
	}
	// Do not forward a source-specific Referer to a different host.
	client := *ic.client
	client.CheckRedirect = func(next *http.Request, via []*http.Request) error {
		if len(via) >= 10 {
			return fmt.Errorf("too many redirects")
		}
		if len(via) > 0 && !sameArtworkHost(next.URL, via[0].URL) {
			next.Header.Del("Referer")
		}
		return nil
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("artwork response: %s", resp.Status)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxImageBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) == 0 || len(data) > maxImageBytes {
		return nil, fmt.Errorf("invalid artwork size")
	}
	return data, nil
}

func sameArtworkHost(a, b *url.URL) bool {
	return strings.EqualFold(a.Hostname(), b.Hostname())
}

func decodeArtwork(data []byte) (image.Image, string, error) {
	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || cfg.Width <= 0 || cfg.Height <= 0 || int64(cfg.Width)*int64(cfg.Height) > maxImagePixels {
		return nil, "", fmt.Errorf("invalid artwork dimensions")
	}
	return image.Decode(bytes.NewReader(data))
}
