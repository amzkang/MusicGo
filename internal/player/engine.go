package player

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"math/rand"
	"net/url"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/egoist/mygo/audio"
	_ "github.com/egoist/mygo/audio/mp3"
	_ "github.com/egoist/mygo/audio/vorbis"
	"musicgo/internal/plugin"
)

// State represents the current playback state
type State int

const (
	StateStopped State = iota
	StatePlaying
	StatePaused
	StateLoading
)

func (s State) String() string {
	switch s {
	case StatePlaying:
		return "playing"
	case StatePaused:
		return "paused"
	case StateLoading:
		return "loading"
	default:
		return "stopped"
	}
}

// Mode represents playback mode
type Mode int

const (
	ModeSequence Mode = iota
	ModeRepeatAll
	ModeRepeatOne
	ModeShuffle
)

func (m Mode) String() string {
	switch m {
	case ModeRepeatAll:
		return "repeat-all"
	case ModeRepeatOne:
		return "repeat-one"
	case ModeShuffle:
		return "shuffle"
	default:
		return "sequence"
	}
}

// Engine is the central music playback engine
type Engine struct {
	mu            sync.RWMutex
	state         State
	mode          Mode
	volume        float64
	speed         float64
	quality       string
	sourceQuality string
	currentSong   *plugin.MusicItem
	queue         []QueueEntry
	queueSerial   uint64
	nextEntries   []uint64
	queueIndex    int
	queueVersion  uint64
	player        *audio.Player
	offEnd        func()
	ffmpegDec     *FFmpegDecoder
	streamer      *HTTPStreamer
	localFile     *os.File
	inputReader   io.ReadSeekCloser
	visualizer    *Visualizer
	equalizer     *Equalizer
	pluginMgr     *plugin.Manager
	fade          volumeFader
	seeking       bool
	seekTarget    float64
	seekMu        sync.Mutex
	audioCache    *AudioCache
	inputResource *AudioResource
	cacheRecovery bool
	positionSec   float64
	resumeSec     float64
	sessionWake   chan struct{}
	durationSec   float64
	lastError     string

	// Concurrency & Playback epoch control
	playEpoch  uint64
	loadCancel context.CancelFunc

	// Event hooks
	onStateChange    []func(State)
	onSongChange     []func(*plugin.MusicItem)
	onPlaybackChange []func()

	closeOne         sync.Once
	closed           bool
	resourceCancel   context.CancelFunc
	pendingSeek      *float64
	shuffleHistory   []int
	shuffleCursor    int
	shuffleCandidate int
	prefetchCancel   context.CancelFunc
	prefetch         *prefetchedSource
	prefetchGen      uint64
	prefetchedPlay   bool
	prefetchSlot     chan struct{}
	spectrumUntil    atomic.Int64
	seekFailedSerial atomic.Uint64
	seekSerial       atomic.Uint64
	seekIssuing      atomic.Bool
}

type prefetchedSource struct {
	index    int
	source   *plugin.MediaSource
	at       time.Time
	revision uint64
}

// NewEngine initializes the audio player engine
func NewEngine(pm *plugin.Manager) *Engine {
	e := &Engine{
		state:            StateStopped,
		queueVersion:     1,
		mode:             ModeRepeatAll,
		volume:           0.8,
		speed:            1.0,
		quality:          "standard",
		visualizer:       NewVisualizer(24),
		equalizer:        NewEqualizer(),
		pluginMgr:        pm,
		sessionWake:      make(chan struct{}, 1),
		shuffleCursor:    -1,
		shuffleCandidate: -1,
		prefetchSlot:     make(chan struct{}, 1),
	}

	return e
}

// OnStateChange sets the callback for playback state changes. It replaces
// the callbacks set before it; use AddStateChangeListener to add one.
func (e *Engine) OnStateChange(fn func(State)) {
	e.mu.Lock()
	e.onStateChange = nil
	if fn != nil {
		e.onStateChange = []func(State){fn}
	}
	e.mu.Unlock()
}

// AddStateChangeListener adds a callback for playback state changes, keeping
// the ones already registered.
func (e *Engine) AddStateChangeListener(fn func(State)) {
	if fn == nil {
		return
	}
	e.mu.Lock()
	e.onStateChange = append(e.onStateChange, fn)
	e.mu.Unlock()
}

// OnSongChange sets the callback for current song changes. It replaces the
// callbacks set before it; use AddSongChangeListener to add one.
func (e *Engine) OnSongChange(fn func(*plugin.MusicItem)) {
	e.mu.Lock()
	e.onSongChange = nil
	if fn != nil {
		e.onSongChange = []func(*plugin.MusicItem){fn}
	}
	e.mu.Unlock()
}

// AddSongChangeListener adds a callback for current song changes, keeping
// the ones already registered.
func (e *Engine) AddSongChangeListener(fn func(*plugin.MusicItem)) {
	if fn == nil {
		return
	}
	e.mu.Lock()
	e.onSongChange = append(e.onSongChange, fn)
	e.mu.Unlock()
}

// AddPlaybackListener reports seeks and changes to playback controls.
// Continuous progress is read from MyGo's audible clock when it is drawn.
func (e *Engine) AddPlaybackListener(fn func()) {
	if fn == nil {
		return
	}
	e.mu.Lock()
	e.onPlaybackChange = append(e.onPlaybackChange, fn)
	e.mu.Unlock()
}

func (e *Engine) playbackChanged() {
	e.mu.RLock()
	callbacks := append([]func(){}, e.onPlaybackChange...)
	e.mu.RUnlock()
	for _, callback := range callbacks {
		go callback()
	}
}

// CurrentSong returns a copy of the currently active song, so the caller
// never shares memory with the engine.
func (e *Engine) CurrentSong() *plugin.MusicItem {
	e.mu.RLock()
	defer e.mu.RUnlock()
	if e.currentSong == nil {
		return nil
	}
	song := e.currentSong.Clone()
	return &song
}

// IsPlaying reports whether the engine is currently playing
func (e *Engine) IsPlaying() bool {
	return e.State() == StatePlaying
}

// ToggleMode advances playback repeat mode
func (e *Engine) ToggleMode() {
	defer e.sessionChanged()
	e.mu.Lock()
	defer e.mu.Unlock()
	defer func() {
		if e.state == StatePlaying {
			e.startPrefetchLocked()
		}
	}()
	e.invalidatePrefetchLocked()
	e.shuffleHistory = nil
	e.shuffleCursor = -1
	switch e.mode {
	case ModeSequence:
		e.mode = ModeRepeatAll
	case ModeRepeatAll:
		e.mode = ModeRepeatOne
	case ModeRepeatOne:
		e.mode = ModeShuffle
	case ModeShuffle:
		e.mode = ModeSequence
	default:
		e.mode = ModeSequence
	}
}

// State returns current state
func (e *Engine) State() State {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.state
}

// Mode returns current mode
func (e *Engine) Mode() Mode {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.mode
}

// SetMode sets playback mode
func (e *Engine) SetMode(m Mode) {
	defer e.sessionChanged()
	defer e.playbackChanged()
	e.mu.Lock()
	if e.mode != m {
		e.invalidatePrefetchLocked()
		e.shuffleHistory = nil
		e.shuffleCursor = -1
	}
	e.mode = m
	if e.state == StatePlaying && e.prefetch == nil && e.prefetchCancel == nil {
		e.startPrefetchLocked()
	}
	e.mu.Unlock()
}

// Volume returns current volume (0.0 to 1.0)
func (e *Engine) Volume() float64 {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.volume
}

// SetVolume sets playback volume (0.0 to 1.0)
func (e *Engine) SetVolume(v float64) {
	if v < 0 {
		v = 0
	}
	if v > 1.0 {
		v = 1.0
	}
	e.mu.Lock()
	e.volume = v
	e.fade.setVolume(v)
	e.mu.Unlock()
	e.playbackChanged()
}

// Quality is the requested playback quality; plugins may return a different one.
func (e *Engine) Quality() (requested, source string) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.quality, e.sourceQuality
}

func (e *Engine) SetQuality(quality string) {
	switch quality {
	case "low", "standard", "high", "super":
	default:
		quality = "standard"
	}
	e.mu.Lock()
	if e.closed || e.quality == quality {
		e.mu.Unlock()
		return
	}
	e.quality, e.sourceQuality = quality, ""
	e.invalidatePrefetchLocked()
	state := e.state
	var song *plugin.MusicItem
	if e.currentSong != nil && e.currentSong.Platform != "local" {
		copy := e.currentSong.Clone()
		song = &copy
	}
	pos := e.positionSec
	if e.seeking {
		pos = e.seekTarget
	} else if e.player != nil {
		pos = e.player.Position().Seconds()
	}
	var cleanup func()
	if song != nil && state == StatePaused {
		e.playEpoch++
		cleanup = e.cleanupCurrentPlayerLocked()
		e.pendingSeek = nil
		e.positionSec, e.resumeSec = pos, pos
	} else if song != nil && (state == StatePlaying || state == StateLoading) {
		e.playEpoch++
		e.positionSec, e.resumeSec = pos, pos
		e.setState(StateLoading)
	}
	epoch := e.playEpoch
	e.mu.Unlock()
	if cleanup != nil {
		go cleanup()
	}
	if song != nil && (state == StatePlaying || state == StateLoading) {
		go e.playSongAt(*song, &epoch, nil, pos)
	}
	e.playbackChanged()
}

// Speed returns the playback speed, 1 being the normal one.
func (e *Engine) Speed() float64 {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.speed
}

// SetSpeed sets the playback speed, from 0.5 to 2.
func (e *Engine) SetSpeed(s float64) {
	if s < 0.5 {
		s = 0.5
	}
	if s > 2 {
		s = 2
	}
	e.mu.Lock()
	e.speed = s
	if e.player != nil {
		e.player.SetSpeed(s)
	}
	e.mu.Unlock()
	e.playbackChanged()
}

// Equalizer returns the engine's equalizer, whose bands shape the sound.
func (e *Engine) Equalizer() *Equalizer {
	return e.equalizer
}

// Position returns current playback position in seconds
func (e *Engine) Position() float64 {
	e.mu.RLock()
	p := e.player
	pos := e.positionSec
	if e.seeking {
		pos = e.seekTarget
		p = nil
	}
	e.mu.RUnlock()
	if p != nil {
		return p.Position().Seconds()
	}
	return pos
}

// Duration returns total duration in seconds
func (e *Engine) Duration() float64 {
	e.mu.RLock()
	p := e.player
	song := e.currentSong
	fallback := e.durationSec
	e.mu.RUnlock()

	if p != nil {
		dur := p.Duration().Seconds()
		if dur > 0 {
			return dur
		}
	}
	if song != nil && song.Duration > 0 {
		return song.Duration
	}
	return fallback
}

// LastError returns the most recent error message
func (e *Engine) LastError() string {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.lastError
}

// Spectrum returns frequency spectrum bars for animation
func (e *Engine) Spectrum() []float32 {
	e.spectrumUntil.Store(time.Now().Add(time.Second).UnixNano())
	return e.visualizer.GetBars()
}

// expected rejects deferred commands whose playback intent has since changed.
func (e *Engine) playSong(song plugin.MusicItem, expected *uint64) {
	e.playSongSource(song, expected, nil)
}

func (e *Engine) playSongSource(song plugin.MusicItem, expected *uint64, cached *plugin.MediaSource) {
	e.playSongAt(song, expected, cached, 0)
}

func (e *Engine) playSongAt(song plugin.MusicItem, expected *uint64, cached *plugin.MediaSource, start float64) {
	e.playSongInput(song, expected, cached, start, false)
}

func (e *Engine) playSongInput(song plugin.MusicItem, expected *uint64, cached *plugin.MediaSource, start float64, direct bool) {
	song = song.Clone()
	e.mu.Lock()
	if e.closed || expected != nil && *expected != e.playEpoch {
		e.mu.Unlock()
		return
	}
	e.playEpoch++
	epoch := e.playEpoch
	e.seekSerial.Add(1)
	e.invalidatePrefetchLocked()
	cleanup := e.cleanupCurrentPlayerLocked()
	e.pendingSeek = nil
	if start > 0 {
		e.pendingSeek = &start
	}
	e.resumeSec = 0
	e.prefetchedPlay = cached != nil && cached.URL != ""
	e.positionSec = start
	e.durationSec = song.Duration
	e.currentSong = &song
	e.sourceQuality = ""
	quality := e.quality
	e.cacheRecovery = false
	cache := e.audioCache
	e.lastError = ""
	e.setState(StateLoading)
	callbacks := append([]func(*plugin.MusicItem){}, e.onSongChange...)
	ctx, cancel := context.WithCancel(context.Background())
	e.loadCancel = cancel
	e.resourceCancel = cancel
	e.mu.Unlock()
	go cleanup()
	for _, fn := range callbacks {
		if fn != nil {
			copy := song.Clone()
			fn(&copy)
		}
	}
	go func() {
		if !direct && cache != nil && song.Platform != "local" {
			if path, release := cache.Lookup(ctx, song, quality); path != "" {
				context.AfterFunc(ctx, release)
				e.mu.Lock()
				if e.playEpoch == epoch {
					e.cacheRecovery = true
				}
				e.mu.Unlock()
				e.loadAndPlay(ctx, epoch, path, nil, song.Duration)
				return
			}
		}
		actualSong := song
		var mediaURL string
		var headers map[string]string
		var sourceQuality string
		if cached != nil {
			mediaURL, headers = cached.URL, cached.Headers
			sourceQuality = cached.Quality
		}
		if mediaURL == "" && e.pluginMgr != nil && song.Platform != "" && song.Platform != "local" {
			if src, err := e.pluginMgr.GetMediaSource(ctx, song, quality); err == nil && src != nil {
				mediaURL = src.URL
				headers = src.Headers
				sourceQuality = src.Quality
			}
		}
		if ctx.Err() != nil {
			return
		}
		if mediaURL == "" {
			mediaURL = song.URL
		}
		if mediaURL == "" && e.pluginMgr != nil && song.Title != "" && song.Platform != "local" {
			if source, alternative := e.fallbackMediaSource(ctx, song, quality); source != nil {
				if alternative != nil {
					actualSong = alternative.Clone()
				}
				mediaURL = source.URL
				headers = source.Headers
				sourceQuality = source.Quality
			}
		}
		if ctx.Err() != nil {
			return
		}
		if mediaURL == "" {
			e.setError(epoch, "无法获取播放源 (可能为VIP专享或音源失效)")
			return
		}
		e.mu.Lock()
		if e.playEpoch == epoch {
			e.sourceQuality = sourceQuality
		}
		e.mu.Unlock()
		e.playbackChanged()
		// A known downgrade must not occupy the requested-quality cache entry.
		downgraded := actualSong.Platform == "酷狗概念版" && quality != "low" && sourceQuality == "128"
		if !direct && !downgraded && cache != nil && isRemoteURL(mediaURL) && !strings.Contains(strings.ToLower(mediaURL), ".m3u") && !strings.Contains(strings.ToLower(mediaURL), ".mpd") {
			if actualSong.Key() != song.Key() {
				if path, release := cache.Lookup(ctx, actualSong, quality); path != "" {
					context.AfterFunc(ctx, release)
					e.mu.Lock()
					if e.playEpoch == epoch {
						e.cacheRecovery = true
					}
					e.mu.Unlock()
					e.loadAndPlay(ctx, epoch, path, nil, song.Duration)
					return
				}
			}
			if resource, err := cache.Open(ctx, actualSong, quality, mediaURL, headers); err == nil {
				context.AfterFunc(ctx, resource.Close)
				e.mu.Lock()
				if e.playEpoch == epoch && !e.closed {
					e.inputResource = resource
					e.cacheRecovery = true
				}
				e.mu.Unlock()
				mediaURL, headers = resource.URL(), nil
			}
		}
		if ctx.Err() != nil {
			return
		}
		e.loadAndPlay(ctx, epoch, mediaURL, headers, song.Duration)
	}()
}

func (e *Engine) loadAndPlay(ctx context.Context, epoch uint64, mediaURL string, headers map[string]string, totalSec float64) {
	if ctx.Err() != nil {
		return
	}
	var p *audio.Player
	var fDec *FFmpegDecoder
	var streamer *HTTPStreamer
	var localFile *os.File
	var inputReader io.ReadSeekCloser
	var err error
	committed := false
	defer func() {
		if !committed {
			if inputReader != nil {
				_ = inputReader.Close()
			}
			closePlaybackResources(p, fDec, streamer, localFile, nil)
		}
	}()

	// How long the track is, as ffprobe knows it: the length a plugin
	// reports is often a guess, and the progress bar needs the real one.
	// The probe opens its own connection and is usually slower than the
	// decoder's first frame, so it must never gate playback start; the
	// result is applied once it arrives, for this epoch only.
	probeCh := make(chan float64, 1)
	go func() {
		probeCh <- ProbeDurationContext(ctx, mediaURL, headers)
	}()

	// 1. First priority: Universal FFmpegDecoder
	fDec, err = NewFFmpegDecoderContext(ctx, mediaURL, headers, totalSec)
	if err == nil && fDec != nil {
		p = e.wrapPlayerAtEpoch(fDec, epoch)
	}
	go func() {
		probed := <-probeCh
		if probed <= 0 || ctx.Err() != nil {
			return
		}
		if fDec != nil {
			fDec.SetDuration(probed)
		}
		e.mu.Lock()
		if e.playEpoch == epoch {
			e.durationSec = probed
		}
		e.mu.Unlock()
		e.playbackChanged()
	}()

	// 2. Fallback: built-in streamer if ffmpeg failed or is unavailable
	if p == nil {
		parsed, parseErr := url.Parse(mediaURL)
		isRemote := parseErr == nil && (parsed.Scheme == "http" || parsed.Scheme == "https")

		e.mu.RLock()
		resource := e.inputResource
		validResource := epoch == e.playEpoch
		e.mu.RUnlock()
		if resource != nil && validResource {
			inputReader = resource.NewReader(ctx)
			p, err = e.openDecodedPlayerAtEpoch(inputReader, epoch)
			if err != nil {
				e.setError(epoch, fmt.Sprintf("解码失败: %v", err))
				return
			}
		} else if isRemote {
			streamer, err = NewHTTPStreamerContext(ctx, mediaURL, headers)
			if err != nil {
				e.setError(epoch, fmt.Sprintf("网络连接失败: %v", err))
				return
			}
			p, err = e.openDecodedPlayerAtEpoch(streamer, epoch)
			if err != nil {
				_ = streamer.Close()
				e.setError(epoch, fmt.Sprintf("解码失败: %v", err))
				return
			}
		} else {
			filePath := mediaURL
			if strings.HasPrefix(filePath, "file://") {
				filePath = strings.TrimPrefix(filePath, "file://")
			}
			localFile, err = os.Open(filePath)
			if err != nil {
				e.setError(epoch, fmt.Sprintf("打开本地文件失败: %v", err))
				return
			}
			p, err = e.openDecodedPlayerAtEpoch(localFile, epoch)
			if err != nil {
				_ = localFile.Close()
				e.setError(epoch, fmt.Sprintf("本地音频格式不支持: %v", err))
				return
			}
		}
	}

	e.mu.Lock()
	defer e.mu.Unlock()

	if epoch != e.playEpoch || e.closed || ctx.Err() != nil {
		return
	}
	committed = true
	e.loadCancel = nil

	e.player = p
	e.offEnd = p.OnEnd(func() { e.trackEnded(p, epoch) })
	e.ffmpegDec = fDec
	e.streamer = streamer
	e.localFile = localFile
	e.inputReader = inputReader
	e.lastError = ""
	e.durationSec = totalSec
	level := float64(1)
	if e.pendingSeek != nil {
		level = 0
	}
	e.fade.attach(p, e.volume, level)
	p.SetSpeed(e.speed)
	if e.pendingSeek != nil {
		sec := *e.pendingSeek
		if totalSec > 0 && sec > totalSec {
			sec = totalSec
		}
		e.seekIssuing.Store(true)
		e.seekSerial.Add(1)
		p.Seek(time.Duration(sec * float64(time.Second)))
		e.seekIssuing.Store(false)
		e.positionSec = sec
		e.pendingSeek = nil
	}
	p.Play()
	if level == 0 {
		e.fade.ramp(1, resumeFade, nil)
	}
	e.setState(StatePlaying)
	e.startPrefetchLocked()
}

// setError records a playback failure for the given epoch.
func (e *Engine) setError(epoch uint64, msg string) {
	e.mu.Lock()
	if epoch != e.playEpoch || e.closed {
		e.mu.Unlock()
		return
	}
	if e.cacheRecovery && e.currentSong != nil {
		song, start := e.currentSong.Clone(), e.positionSec
		if e.seeking {
			start = e.seekTarget
		} else if e.player != nil {
			start = e.player.Position().Seconds()
		}
		e.cacheRecovery = false
		e.mu.Unlock()
		go e.playSongInput(song, &epoch, nil, start, true)
		return
	}
	if e.prefetchedPlay && e.currentSong != nil {
		song := e.currentSong.Clone()
		e.prefetchedPlay = false
		e.mu.Unlock()
		go e.playSong(song, &epoch)
		return
	}
	e.lastError = msg
	e.setState(StateStopped)
	cancel := e.loadCancel
	e.loadCancel = nil
	e.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}
func (e *Engine) decoderError(epoch uint64, err error) {
	e.handleDecoderError(epoch, err, nil)
}

func (e *Engine) seekError(epoch, serial uint64, err error) {
	e.handleDecoderError(epoch, fmt.Errorf("跳转失败: %w", err), &serial)
}

func (e *Engine) handleDecoderError(epoch uint64, err error, serial *uint64) {
	e.mu.Lock()
	if epoch != e.playEpoch || e.closed || serial != nil && *serial != e.seekSerial.Load() {
		e.mu.Unlock()
		return
	}
	if e.cacheRecovery && e.currentSong != nil {
		song, start := e.currentSong.Clone(), e.positionSec
		if e.seeking {
			start = e.seekTarget
		} else if e.player != nil {
			start = e.player.Position().Seconds()
		}
		e.cacheRecovery = false
		e.mu.Unlock()
		go e.playSongInput(song, &epoch, nil, start, true)
		return
	}
	if e.prefetchedPlay && e.currentSong != nil {
		song := e.currentSong.Clone()
		e.prefetchedPlay = false
		e.mu.Unlock()
		go e.playSong(song, &epoch)
		return
	}
	e.playEpoch++
	cleanup := e.cleanupCurrentPlayerLocked()
	e.lastError = err.Error()
	e.setState(StateStopped)
	e.mu.Unlock()
	go cleanup()
}

// wrapPlayerAtEpoch feeds the visualizer and equalizer from decoded samples.
func (e *Engine) wrapPlayerAtEpoch(dec audio.Decoder, epoch uint64) *audio.Player {
	e.visualizer.SetSampleRate(dec.SampleRate())
	e.equalizer.SetSampleRate(dec.SampleRate())
	return audio.NewPlayer(&shapedDecoder{Decoder: dec, eng: e, epoch: epoch})
}

// openDecodedPlayer opens a stream with the decoders MyGo knows, behind the
// visualizer and the equalizer.
func (e *Engine) openDecodedPlayerAtEpoch(r interface {
	Read(p []byte) (int, error)
	Seek(offset int64, whence int) (int64, error)
}, epoch uint64) (*audio.Player, error) {
	dec, _, err := audio.NewDecoder(r)
	if err != nil {
		return nil, err
	}
	return e.wrapPlayerAtEpoch(dec, epoch), nil
}

// shapedDecoder passes the samples through the equalizer and hands them to
// the visualizer.
type shapedDecoder struct {
	audio.Decoder
	eng   *Engine
	epoch uint64
	// eq is this decoder's private filter chain (privateSnapshot): mutable
	// biquad state is only touched by this decoder's goroutine, so a track
	// switch's overlapping old decode goroutine can never race it.
	eq *eqSnapshot
}

// eqApply runs the equalizer over interleaved samples through this decoder's
// private chain. Parameters follow the published snapshot's generation (an
// EQ change re-clones, which resets filter history exactly like the old
// shared republish did); the state itself never leaves this goroutine.
func (d *shapedDecoder) eqApply(samples []float32, channels int) {
	eq := d.eng.equalizer
	if channels < 1 {
		channels = 1
	}
	eq.channels.Store(int32(channels))
	gen := eq.gen.Load()
	if d.eq == nil || d.eq.channels != channels || d.eq.gen != gen {
		d.eq = eq.privateSnapshot(channels)
	}
	d.eq.apply(samples, channels)
}

func (d *shapedDecoder) Read(buf []float32) (int, error) {
	n, err := d.Decoder.Read(buf)
	if err != nil {
		d.eng.mu.RLock()
		resource := d.eng.inputResource
		d.eng.mu.RUnlock()
		if resource != nil {
			if failure := resource.Failure(); failure != nil {
				err = failure
			}
		}
	}
	if n > 0 {
		channels := d.Decoder.Channels()
		d.eqApply(buf[:n], channels)
		d.eng.mu.Lock()
		if d.epoch == d.eng.playEpoch {
			d.eng.prefetchedPlay = false
		}
		d.eng.mu.Unlock()
		if time.Now().UnixNano() < d.eng.spectrumUntil.Load() {
			d.eng.visualizer.ProcessInterleaved(buf[:n], channels)
		}
	}
	if err != nil && err != io.EOF && err != io.ErrUnexpectedEOF && !errors.Is(err, errReadInterrupted) {
		go d.eng.decoderError(d.epoch, err)
	}
	return n, err
}

func (d *shapedDecoder) SetPosition(frame int64) error {
	if d.eq != nil {
		d.eq.reset()
	}
	// serial 在 seekMu 下快照，与 apply 的签发窗口（同样持 seekMu）串行；
	// pendingSeek 的自增先于入队，decode goroutine 只会在入队后醒来，
	// 快照必然是其签发序号。
	d.eng.seekMu.Lock()
	serial := d.eng.seekSerial.Load()
	d.eng.seekMu.Unlock()
	err := d.Decoder.SetPosition(frame)
	if err != nil && !errors.Is(err, errReadInterrupted) && !errors.Is(err, context.Canceled) {
		// 失败判定放在错误发生时：签发窗口此时早已关闭，SetPosition 之前的
		// issuing 快照不可靠——旧实现据此清零 serial，会把窗口内的失败整体
		// 吞掉，流被标记 EOF 后经 OnEnd 无声跳到下一首。
		d.eng.seekMu.Lock()
		if serial == d.eng.seekSerial.Load() && !d.eng.seekIssuing.Load() {
			for old := d.eng.seekFailedSerial.Load(); old < serial; old = d.eng.seekFailedSerial.Load() {
				if d.eng.seekFailedSerial.CompareAndSwap(old, serial) {
					break
				}
			}
			go d.eng.seekError(d.epoch, serial, err)
		}
		d.eng.seekMu.Unlock()
	}
	return err
}

func closePlaybackResources(p *audio.Player, dec *FFmpegDecoder, stream *HTTPStreamer, file *os.File, cancel context.CancelFunc) {
	if cancel != nil {
		cancel()
	}
	if dec != nil {
		dec.Close()
	}
	if stream != nil {
		stream.Close()
	}
	if p != nil {
		p.Close()
	}
	if file != nil {
		file.Close()
	}
}

// The caller owns e.mu; closing subprocesses and streams is deferred outside it.
func (e *Engine) cleanupCurrentPlayerLocked() func() {
	p, dec, stream, file, cancel := e.player, e.ffmpegDec, e.streamer, e.localFile, e.resourceCancel
	if e.offEnd != nil {
		e.offEnd()
		e.offEnd = nil
	}
	reader := e.inputReader
	e.fade.attach(nil, e.volume, 0)
	e.seeking = false
	e.inputReader = nil
	e.player = nil
	e.ffmpegDec = nil
	e.streamer = nil
	e.localFile = nil
	e.resourceCancel = nil
	e.inputResource = nil
	e.cacheRecovery = false
	e.loadCancel = nil
	return func() {
		if reader != nil {
			_ = reader.Close()
		}
		closePlaybackResources(p, dec, stream, file, cancel)
	}
}

// Play resumes playback
func (e *Engine) Play() {
	e.play(nil)
}

func (e *Engine) play(expected *uint64) {
	e.mu.Lock()
	if e.closed || expected != nil && *expected != e.playEpoch || e.state == StatePlaying || e.state == StateLoading {
		e.mu.Unlock()
		return
	}
	if e.player != nil {
		if !e.seeking {
			e.player.Play()
			e.fade.ramp(1, resumeFade, nil)
		}
		e.setState(StatePlaying)
		e.mu.Unlock()
		return
	}
	var song *plugin.MusicItem
	if e.currentSong != nil {
		copy := e.currentSong.Clone()
		song = &copy
	} else if len(e.queue) > 0 {
		qIdx := e.queueIndex
		if qIdx < 0 || qIdx >= len(e.queue) {
			qIdx = 0
		}
		copy := e.queue[qIdx].Song.Clone()
		song = &copy
	}
	epoch := e.playEpoch
	start := e.resumeSec
	e.mu.Unlock()
	if song != nil {
		e.playSongAt(*song, &epoch, nil, start)
	}
}

// Pause pauses playback immediately and cancels pending loading
func (e *Engine) Pause() {
	e.pause(nil)
}

func (e *Engine) pause(expected *uint64) {
	e.mu.Lock()
	if e.closed || expected != nil && *expected != e.playEpoch {
		e.mu.Unlock()
		return
	}
	var cleanup func()
	if e.loadCancel != nil || e.state == StateLoading && e.player != nil {
		e.resumeSec = e.positionSec
		e.pendingSeek = nil
		e.playEpoch++
		cleanup = e.cleanupCurrentPlayerLocked()
	}
	if p := e.player; p != nil {
		epoch := e.playEpoch
		e.fade.ramp(0, pauseFade, func(gen uint64) {
			e.mu.Lock()
			defer e.mu.Unlock()
			if e.player == p && e.playEpoch == epoch && e.state == StatePaused && e.fade.current(gen) {
				p.Pause()
			}
		})
	}
	e.setState(StatePaused)
	e.mu.Unlock()
	if cleanup != nil {
		go cleanup()
	}
}

// TogglePlay toggles between Play and Pause
func (e *Engine) TogglePlay() {
	e.mu.Lock()
	st := e.state
	e.mu.Unlock()

	if st == StatePlaying || st == StateLoading {
		e.Pause()
	} else {
		e.Play()
	}
}

// Stop stops playback completely
func (e *Engine) Stop() {
	e.stop(nil)
}

func (e *Engine) stop(expected *uint64) {
	e.mu.Lock()
	if expected != nil && *expected != e.playEpoch {
		e.mu.Unlock()
		return
	}
	e.playEpoch++
	e.invalidatePrefetchLocked()
	e.pendingSeek = nil
	cleanup := e.cleanupCurrentPlayerLocked()
	e.setState(StateStopped)
	e.positionSec = 0
	e.resumeSec = 0
	e.mu.Unlock()
	go cleanup()
}

// Seek seeks to a target time in seconds
func (e *Engine) Seek(sec float64) {
	if math.IsNaN(sec) || math.IsInf(sec, 0) {
		return
	}
	dur := e.Duration()
	sec = max(0, sec)
	if dur > 0 {
		sec = min(sec, dur)
	}
	e.mu.Lock()
	if e.closed {
		e.mu.Unlock()
		return
	}
	p, epoch := e.player, e.playEpoch
	serial := e.seekSerial.Add(1)
	if p == nil && e.state == StateLoading {
		value := sec
		e.pendingSeek = &value
	}
	e.positionSec = sec
	if p == nil && e.state != StateLoading {
		e.resumeSec = sec
	}
	delay := p != nil && (e.state == StatePlaying || e.seeking)
	if p != nil {
		e.seeking = true
		e.seekTarget = sec
		if delay {
			p.Pause()
		}
	}
	e.mu.Unlock()
	apply := func() {
		e.seekMu.Lock()
		defer e.seekMu.Unlock()
		e.mu.Lock()
		if e.closed || e.playEpoch != epoch || e.player != p || e.seekSerial.Load() != serial || p == nil {
			e.mu.Unlock()
			return
		}
		stream, dec, reader := e.streamer, e.ffmpegDec, e.inputReader
		e.mu.Unlock()
		var streamGen, decGen uint64
		shared, sharedOK := reader.(interface {
			readGeneration() uint64
			interruptRead(uint64)
		})
		var sharedGen uint64
		if sharedOK {
			sharedGen = shared.readGeneration()
		}
		if stream != nil {
			streamGen = stream.readGeneration()
		}
		if dec != nil {
			decGen = dec.readGeneration()
		}
		e.seekIssuing.Store(true)
		p.Seek(time.Duration(sec * float64(time.Second)))
		e.seekIssuing.Store(false)
		if stream != nil {
			stream.interruptRead(streamGen)
		}
		if dec != nil {
			dec.interruptRead(decGen)
		}
		if sharedOK {
			shared.interruptRead(sharedGen)
		}
		e.mu.Lock()
		if e.closed || e.playEpoch != epoch || e.player != p || e.seekSerial.Load() != serial {
			e.mu.Unlock()
			return
		}
		e.seeking = false
		// 暂停态 seek 期间按播放时，play() 因 seeking 跳过了 p.Play()，
		// 由这里统一恢复，避免出现状态 Playing 而底层仍在暂停的假播放。
		if e.state == StatePlaying {
			p.Play()
			e.fade.ramp(1, resumeFade, nil)
		}
		e.sessionChanged()
		e.mu.Unlock()
		e.playbackChanged()
	}
	if p != nil {
		wait := time.Duration(0)
		if delay {
			wait = 30 * time.Millisecond
		}
		time.AfterFunc(wait, apply)
	}

	e.sessionChanged()
	e.playbackChanged()
}

// Next plays the next song according to current mode
func (e *Engine) Next() {
	e.next(nil)
}

func (e *Engine) next(expected *uint64) {
	e.mu.Lock()
	if e.closed || len(e.queue) == 0 || expected != nil && *expected != e.playEpoch {
		e.mu.Unlock()
		return
	}
	if expected != nil && (e.state != StatePlaying || e.seeking || e.player == nil || e.player.Playing()) {
		e.mu.Unlock()
		return
	}

	var nextIdx int
	if len(e.nextEntries) > 0 {
		nextIdx = e.queueEntryIndexLocked(e.nextEntries[0])
		e.nextEntries = e.nextEntries[1:]
		if e.mode == ModeShuffle {
			e.ensureShuffleCurrentLocked()
			e.shuffleHistory = append(e.shuffleHistory[:e.shuffleCursor+1], nextIdx)
			e.shuffleCursor++
		}
	} else {
		switch e.mode {
		case ModeShuffle:
			e.ensureShuffleCurrentLocked()
			if e.shuffleCursor+1 < len(e.shuffleHistory) {
				e.shuffleCursor++
				nextIdx = e.shuffleHistory[e.shuffleCursor]
			} else if e.shuffleCandidate >= 0 && e.shuffleCandidate < len(e.queue) && e.shuffleCandidate != e.queueIndex {
				nextIdx = e.shuffleCandidate
			} else if len(e.queue) > 1 {
				for {
					nextIdx = rand.Intn(len(e.queue))
					if nextIdx != e.queueIndex {
						break
					}
				}
			} else {
				nextIdx = 0
			}
			if e.shuffleCursor == len(e.shuffleHistory)-1 && nextIdx != e.shuffleHistory[e.shuffleCursor] {
				e.shuffleHistory = append(e.shuffleHistory, nextIdx)
				e.shuffleCursor++
			}
		case ModeRepeatOne:
			nextIdx = e.queueIndex
			if expected == nil {
				// A manual skip advances even when natural endings repeat one.
				nextIdx = (e.queueIndex + 1) % len(e.queue)
			}
		default: // Sequence or RepeatAll
			nextIdx = e.queueIndex + 1
			if nextIdx >= len(e.queue) {
				if e.mode == ModeRepeatAll {
					nextIdx = 0
				} else {
					epoch := e.playEpoch
					e.mu.Unlock()
					e.stop(&epoch)
					return
				}
			}
		}
	}

	e.queueIndex = nextIdx
	e.touchQueueLocked()
	song := e.queue[nextIdx].Song.Clone()
	var cached *plugin.MediaSource
	if e.prefetch != nil && e.pluginMgr != nil && e.prefetch.index == nextIdx && time.Since(e.prefetch.at) < 30*time.Second && e.prefetch.revision == e.pluginMgr.Revision() {
		cached = e.prefetch.source
	}
	e.playEpoch++
	epoch := e.playEpoch
	e.mu.Unlock()

	e.playSongSource(song, &epoch, cached)
}

// Prev plays the previous song in the queue
func (e *Engine) Prev() {
	e.mu.Lock()
	if e.closed || len(e.queue) == 0 {
		e.mu.Unlock()
		return
	}

	prevIdx := e.queueIndex - 1
	if e.mode == ModeShuffle {
		e.ensureShuffleCurrentLocked()
		if e.shuffleCursor == 0 {
			e.mu.Unlock()
			return
		}
		e.shuffleCursor--
		prevIdx = e.shuffleHistory[e.shuffleCursor]
	} else if prevIdx < 0 {
		prevIdx = len(e.queue) - 1
	}

	e.forgetNextLocked(e.queue[prevIdx].ID)
	e.queueIndex = prevIdx
	e.touchQueueLocked()
	song := e.queue[prevIdx].Song.Clone()
	e.playEpoch++
	epoch := e.playEpoch
	e.mu.Unlock()

	e.playSong(song, &epoch)
}

func (e *Engine) setState(st State) {
	if e.state != st {
		e.state = st
		e.sessionChanged()
		cbs := append([]func(State){}, e.onStateChange...)
		for _, cb := range cbs {
			if cb != nil {
				go cb(st)
			}
		}
	}
}

// trackEnded runs on MyGo's audio-end goroutine. Old players and seeks cannot
// advance a newer queue generation.
func (e *Engine) trackEnded(p *audio.Player, epoch uint64) {
	e.mu.RLock()
	valid := !e.closed && e.player == p && e.playEpoch == epoch && e.state == StatePlaying && !e.seeking &&
		(e.seekFailedSerial.Load() == 0 || e.seekFailedSerial.Load() != e.seekSerial.Load())
	empty := len(e.queue) == 0
	e.mu.RUnlock()
	if !valid {
		return
	}
	if empty {
		e.stop(&epoch)
	} else {
		e.next(&epoch)
	}
}

// Close terminates the engine and frees resources
func (e *Engine) Close() {
	e.closeOne.Do(func() {
		e.mu.Lock()
		e.closed = true
		e.invalidatePrefetchLocked()
		e.pendingSeek = nil
		e.playEpoch++
		cleanup := e.cleanupCurrentPlayerLocked()
		e.setState(StateStopped)
		e.mu.Unlock()
		cleanup()
	})
}

func (e *Engine) invalidatePrefetchLocked() {
	e.shuffleCandidate = -1
	e.prefetchGen++
	if e.prefetchCancel != nil {
		e.prefetchCancel()
		e.prefetchCancel = nil
	}
	e.prefetch = nil
}

func (e *Engine) resetQueueTrackingLocked() {
	e.invalidatePrefetchLocked()
	e.shuffleHistory = nil
	e.shuffleCursor = -1
}

func (e *Engine) ensureShuffleCurrentLocked() {
	if e.shuffleCursor < 0 || e.shuffleCursor >= len(e.shuffleHistory) || e.shuffleHistory[e.shuffleCursor] != e.queueIndex {
		e.shuffleHistory = []int{e.queueIndex}
		e.shuffleCursor = 0
	}
}

// Choose and resolve the next queue entry while the current one plays.
func (e *Engine) startPrefetchLocked() {
	if e.closed || len(e.queue) == 0 {
		return
	}
	idx := e.queueIndex + 1
	if len(e.nextEntries) > 0 {
		idx = e.queueEntryIndexLocked(e.nextEntries[0])
	} else {
		switch e.mode {
		case ModeRepeatOne:
			idx = e.queueIndex
		case ModeRepeatAll:
			idx %= len(e.queue)
		case ModeShuffle:
			e.ensureShuffleCurrentLocked()
			if e.shuffleCursor+1 < len(e.shuffleHistory) {
				idx = e.shuffleHistory[e.shuffleCursor+1]
			} else if len(e.queue) > 1 {
				if e.shuffleCandidate < 0 {
					idx = rand.Intn(len(e.queue) - 1)
					if idx >= e.queueIndex {
						idx++
					}
					e.shuffleCandidate = idx
				} else {
					idx = e.shuffleCandidate
				}
			} else {
				idx = 0
			}
		}
	}
	if e.pluginMgr == nil || idx < 0 || idx >= len(e.queue) {
		return
	}
	song := e.queue[idx].Song.Clone()
	if song.Platform == "" || song.Platform == "local" {
		return
	}
	select {
	case e.prefetchSlot <- struct{}{}:
	default:
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	e.prefetchCancel = cancel
	gen := e.prefetchGen
	revision := e.pluginMgr.Revision()
	cache := e.audioCache
	quality := e.quality
	go func() {
		var source *plugin.MediaSource
		var err error
		local := false
		if path, release := cache.Lookup(ctx, song, quality); path != "" {
			release()
			local = true
			source = &plugin.MediaSource{}
		} else {
			source, err = e.pluginMgr.GetMediaSource(ctx, song, quality)
		}
		e.mu.Lock()
		<-e.prefetchSlot
		if !e.closed && e.prefetchGen == gen {
			e.prefetchCancel = nil
			if err == nil && source != nil && (source.URL != "" || local) && ctx.Err() == nil && e.pluginMgr.Revision() == revision {
				e.prefetch = &prefetchedSource{index: idx, source: source, at: time.Now(), revision: revision}
			} else if e.pluginMgr.Revision() != revision && e.state == StatePlaying {
				e.startPrefetchLocked()
			}
		} else if !e.closed && e.state == StatePlaying && e.prefetchCancel == nil && e.prefetch == nil {
			e.startPrefetchLocked()
		}
		e.mu.Unlock()
		cancel()
	}()
}

// fallbackMediaSource attempts to resolve a playable source from other installed plugins
func (e *Engine) fallbackMediaSource(parent context.Context, song plugin.MusicItem, quality string) (*plugin.MediaSource, *plugin.MusicItem) {
	if e.pluginMgr == nil || song.Title == "" {
		return nil, nil
	}
	ctx, cancel := context.WithTimeout(parent, 8*time.Second)
	defer cancel()

	query := song.Title
	if song.Artist != "" && song.Artist != "未知歌手" {
		query += " " + song.Artist
	}

	results, err := e.pluginMgr.Search(ctx, query, 1, "music")
	if err != nil || len(results) == 0 {
		return nil, nil
	}

	for _, cand := range results {
		if cand.Platform == song.Platform || cand.Platform == "" {
			continue
		}
		cleanCand := strings.ToLower(strings.TrimSpace(cand.Title))
		cleanSong := strings.ToLower(strings.TrimSpace(song.Title))
		if !strings.Contains(cleanCand, cleanSong) && !strings.Contains(cleanSong, cleanCand) {
			continue
		}
		media, mErr := e.pluginMgr.GetMediaSource(ctx, cand, quality)
		if mErr == nil && media != nil && media.URL != "" {
			return media, &cand
		}
	}
	return nil, nil
}
