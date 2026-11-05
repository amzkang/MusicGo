package ui

import (
	"bytes"
	"container/list"
	"context"
	"image"
	"net/http"
	"sync"
	"time"

	"github.com/egoist/mygo/ui"
	"golang.org/x/image/draw"
)

// ImageCache asynchronously fetches, decodes, and caches artwork images as *ui.Bitmap
type ImageCache struct {
	mu        sync.Mutex
	cache     map[string]*ui.Bitmap
	backdrops map[string]*ui.Bitmap
	sources   map[string]image.Image
	effects   map[string]bool
	sizes     map[string]int64
	bytes     int64
	loads     chan struct{}
	// order holds the cached pictures that are not pinned, the least
	// recently shown first, so that the ones the interface keeps drawing
	// outlive the ones that nothing shows any more.
	order    *list.List
	at       map[string]*list.Element
	loading  map[string]bool
	failures map[string]time.Time
	// shown and frame protect covers requested by the current page.
	shown map[string]bool
	frame map[string]bool
	// pinned keeps the playing song artwork across page changes.
	pinned     string
	client     *http.Client
	diskDir    string
	diskMu     sync.Mutex
	Invalidate func()
	ctx        context.Context
	cancel     context.CancelFunc
	closed     bool
	tasks      sync.WaitGroup
}

// maxCachedImages is the idle LRU budget. The current page and the playing
// artwork may exceed it; leaving a page releases its protection.
const maxCachedImages = 192

const maxCachedImageBytes = 48 << 20
const maxImagePixels = 16 << 20
const maxImageLoads = 4

// failRetry is how long a picture a source would not give is left alone
// before it is asked for again: the interface asks on every frame, and a
// source that says no should not be asked that often.
const failRetry = 30 * time.Second

// maxImageBytes is the size of the biggest picture that is fetched.
const maxImageBytes = 8 << 20

// NewImageCache creates an ImageCache
func NewImageCache(invalidate func()) *ImageCache {
	ctx, cancel := context.WithCancel(context.Background())
	return &ImageCache{
		ctx: ctx, cancel: cancel,
		cache:      make(map[string]*ui.Bitmap),
		backdrops:  make(map[string]*ui.Bitmap),
		sources:    make(map[string]image.Image),
		effects:    make(map[string]bool),
		sizes:      make(map[string]int64),
		loads:      make(chan struct{}, maxImageLoads),
		order:      list.New(),
		at:         make(map[string]*list.Element),
		loading:    make(map[string]bool),
		failures:   make(map[string]time.Time),
		Invalidate: invalidate,
		client: &http.Client{
			Timeout: 6 * time.Second, Transport: http.DefaultTransport.(*http.Transport).Clone(),
		},
	}
}

// SetInvalidate sets the function that draws the interface again, when a
// picture has been fetched.
func (ic *ImageCache) SetInvalidate(fn func()) {
	ic.mu.Lock()
	ic.Invalidate = fn
	ic.mu.Unlock()
}

// invalidateFunc returns the function that draws the interface again.
func (ic *ImageCache) invalidateFunc() func() {
	ic.mu.Lock()
	defer ic.mu.Unlock()
	if ic.closed {
		return nil
	}
	return ic.Invalidate
}

// Close runs off the UI thread. Marking closed under mu prevents a new Add
// while Wait is draining loads, including loads whose goroutine has not started.
func (ic *ImageCache) Close() {
	ic.mu.Lock()
	ic.closed = true
	ic.Invalidate = nil
	ic.mu.Unlock()
	ic.cancel()
	ic.tasks.Wait()
	ic.client.CloseIdleConnections()
}

// Reserve capacity before creating a goroutine. A full pool leaves the image
// eligible for the next frame, rather than queuing work for pages already left.
func (ic *ImageCache) beginLoad() bool {
	if ic.closed {
		return false
	}
	select {
	case ic.loads <- struct{}{}:
		ic.tasks.Add(1)
		return true
	default:
		return false
	}
}

func (ic *ImageCache) finishLoad() {
	defer ic.tasks.Done()
	<-ic.loads
	// Release capacity before redraw: visible images that were skipped must
	// be able to claim the slot even when this was the last active load.
	if fn := ic.invalidateFunc(); fn != nil {
		fn()
	}
}

// Get returns the decoded *ui.Bitmap if already cached, or initiates background loading.
// A picture that shows again becomes the most recently shown one, so that a
// page full of covers does not crowd out the cover of the song that plays.
func (ic *ImageCache) Get(rawURL string) *ui.Bitmap {
	if rawURL == "" {
		return nil
	}

	ic.mu.Lock()
	if ic.frame != nil {
		ic.frame[rawURL] = true
	}
	if bm, ok := ic.cache[rawURL]; ok {
		ic.touch(rawURL)
		ic.mu.Unlock()
		return bm
	}
	if ic.loading[rawURL] || time.Since(ic.failures[rawURL]) < failRetry {
		ic.mu.Unlock()
		return nil
	}
	if !ic.beginLoad() {
		ic.mu.Unlock()
		return nil
	}
	ic.loading[rawURL] = true
	ic.mu.Unlock()

	go func(targetURL string) {
		defer func() {
			ic.mu.Lock()
			delete(ic.loading, targetURL)
			ic.mu.Unlock()
			ic.finishLoad()
		}()
		if ic.ctx.Err() != nil {
			return
		}

		data, cached := ic.readArtwork(targetURL)
		if !cached {
			var err error
			data, err = ic.fetchArtwork(targetURL)
			if err != nil {
				ic.forget(targetURL)
				return
			}
		}
		src, format, err := decodeArtwork(data)
		if err != nil && cached {
			ic.removeArtwork(targetURL)
			data, err = ic.fetchArtwork(targetURL)
			if err == nil {
				src, format, err = decodeArtwork(data)
				cached = false
			}
		}
		if err != nil {
			ic.forget(targetURL)
			return
		}
		if ic.ctx.Err() != nil {
			return
		}
		if !cached {
			ic.writeArtwork(targetURL, data)
		}
		b := src.Bounds()
		longest := max(b.Dx(), b.Dy())
		w, h := max(1, b.Dx()*min(longest, 1024)/longest), max(1, b.Dy()*min(longest, 1024)/longest)
		rgba := image.NewRGBA(image.Rect(0, 0, w, h))
		draw.CatmullRom.Scale(rgba, rgba.Bounds(), src, b, draw.Src, nil)
		bm := ui.NewBitmap(rgba)
		// Preserve MyGo's camera orientation handling for JPEGs with EXIF.
		if format == "jpeg" && bytes.Contains(data, []byte("Exif\x00\x00")) {
			bm, err = ui.DecodeBitmap(data)
			if err != nil {
				ic.forget(targetURL)
				return
			}
		}

		ic.mu.Lock()
		delete(ic.failures, targetURL)
		// NewBitmap shares rgba's pixels. Keeping this read-only image lets
		// effects reuse them without retaining encoded data or decoding again.
		ic.sources[targetURL] = rgba
		bw, bh := bm.Size()
		size := int64(bw) * int64(bh) * 4
		if bw != w || bh != h || (format == "jpeg" && bytes.Contains(data, []byte("Exif\x00\x00"))) {
			size += int64(w) * int64(h) * 4
		}
		ic.sizes[targetURL] = size
		ic.bytes += size
		ic.keep(targetURL, bm)
		ic.mu.Unlock()
	}(rawURL)

	return nil
}

// GetBackdrop shares the cover's load, lifetime and LRU protection.
func (ic *ImageCache) GetBackdrop(rawURL string) *ui.Bitmap {
	return ic.getEffect(rawURL)
}

func (ic *ImageCache) getEffect(rawURL string) *ui.Bitmap {
	bm := ic.Get(rawURL)
	if bm == nil {
		return nil
	}
	ic.mu.Lock()
	result := ic.backdrops[rawURL]
	src := ic.sources[rawURL]
	if result != nil || ic.effects[rawURL] || src == nil {
		ic.mu.Unlock()
		return result
	}
	if !ic.beginLoad() {
		ic.mu.Unlock()
		return nil
	}
	ic.effects[rawURL] = true
	ic.mu.Unlock()
	go func() {
		defer ic.finishLoad()
		if ic.ctx.Err() != nil {
			ic.mu.Lock()
			delete(ic.effects, rawURL)
			ic.mu.Unlock()
			return
		}
		backdrop := prepareBackdrop(src)
		ic.mu.Lock()
		delete(ic.effects, rawURL)
		if ic.cache[rawURL] == bm {
			ic.backdrops[rawURL] = backdrop
			w, h := backdrop.Size()
			size := int64(w*h) * 4
			ic.sizes[rawURL] += size
			ic.bytes += size
			ic.trim()
		}
		ic.mu.Unlock()
	}()
	return nil
}

func prepareBackdrop(src image.Image) *ui.Bitmap {
	b := src.Bounds()
	longest := max(b.Dx(), b.Dy())
	w, h := max(1, b.Dx()*min(longest, 96)/longest), max(1, b.Dy()*min(longest, 96)/longest)
	a := image.NewRGBA(image.Rect(0, 0, w, h))
	draw.CatmullRom.Scale(a, a.Bounds(), src, b, draw.Src, nil)
	z := image.NewRGBA(a.Bounds())
	// Three small separable box passes approximate the original soft blur.
	// Clamp samples at the edges so stretching the result never exposes alpha.
	for pass := 0; pass < 6; pass++ {
		for y := 0; y < h; y++ {
			for x := 0; x < w; x++ {
				var sum [4]int
				for k := -3; k <= 3; k++ {
					sx, sy := x, y
					if pass%2 == 0 {
						sx = max(0, min(w-1, x+k))
					} else {
						sy = max(0, min(h-1, y+k))
					}
					i := sy*a.Stride + sx*4
					for ch := range sum {
						sum[ch] += int(a.Pix[i+ch])
					}
				}
				i := y*z.Stride + x*4
				for ch := range sum {
					z.Pix[i+ch] = uint8(sum[ch] / 7)
				}
			}
		}
		a, z = z, a
	}
	return ui.NewBitmap(a)
}

// Pin keeps url in the cache until another takes its place: the artwork of
// the song that plays, which the player bar and the lyric page show, must
// never wait on the network again because the pages the user browses filled
// the cache with their pictures. An empty url pins none.
func (ic *ImageCache) Pin(url string) {
	ic.mu.Lock()
	defer ic.mu.Unlock()
	if ic.pinned == url {
		return
	}
	if old := ic.pinned; old != "" {
		// The picture that showed until now is one of the rest again, and
		// the most recently shown of them.
		if _, ok := ic.cache[old]; ok && ic.at[old] == nil {
			ic.at[old] = ic.order.PushBack(old)
		}
	}
	ic.pinned = url
	if el, ok := ic.at[url]; ok {
		ic.order.Remove(el)
		delete(ic.at, url)
	}
}

// touch makes url the most recently shown of the cached pictures.
func (ic *ImageCache) touch(url string) {
	if el, ok := ic.at[url]; ok {
		ic.order.MoveToBack(el)
	}
}

// keep holds bm for url, and forgets the least recently shown pictures
// beyond the cache's size. The pinned picture is never one of them. The
// caller holds the lock.
func (ic *ImageCache) keep(url string, bm *ui.Bitmap) {
	ic.cache[url] = bm
	if ic.pinned == url {
		return
	}
	if el, ok := ic.at[url]; ok {
		ic.order.MoveToBack(el)
		return
	}
	ic.at[url] = ic.order.PushBack(url)
	ic.trim()
}

// BeginFrame protects the previous frame until the new frame has requested its
// covers. A scroll view builds off-screen rows too, so its working set can be
// larger than the idle LRU limit. Evicting that working set causes fetch loops.
func (ic *ImageCache) BeginFrame() {
	ic.mu.Lock()
	ic.frame = make(map[string]bool)
	ic.mu.Unlock()
}

func (ic *ImageCache) EndFrame() {
	ic.mu.Lock()
	ic.shown, ic.frame = ic.frame, nil
	ic.trim()
	ic.mu.Unlock()
}

// trim bounds unused image count and base pixels while retaining every cover
// used by the page. MyGo may additionally retain display-sized mipmap levels.
// The caller holds the lock. Once a page is left, its covers can be evicted.
func (ic *ImageCache) trim() {
	for el := ic.order.Front(); el != nil && (ic.order.Len() > maxCachedImages || ic.bytes > maxCachedImageBytes); {
		next := el.Next()
		key := el.Value.(string)
		if !ic.shown[key] && !ic.frame[key] {
			ic.order.Remove(el)
			delete(ic.at, key)
			delete(ic.cache, key)
			delete(ic.backdrops, key)
			delete(ic.sources, key)
			ic.bytes -= ic.sizes[key]
			delete(ic.sizes, key)
		}
		el = next
	}
}

// forget remembers that targetURL gave no picture, for a while.
func (ic *ImageCache) forget(targetURL string) {
	ic.mu.Lock()
	ic.failures[targetURL] = time.Now()
	if len(ic.failures) > maxCachedImages {
		for url, at := range ic.failures {
			if time.Since(at) >= failRetry {
				delete(ic.failures, url)
			}
		}
	}
	ic.mu.Unlock()
}
