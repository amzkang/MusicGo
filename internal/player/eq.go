package player

import (
	"math"
	"sync"
	"sync/atomic"
)

// eqBands are the frequencies the graphic equalizer works at.
var eqBands = []struct {
	name string
	freq float64
	q    float64
}{
	{"bass", 60, 0.9},
	{"lowMid", 250, 1.0},
	{"mid", 1000, 1.0},
	{"treble", 4000, 1.1},
}

// Biquad is a second order section, applied to a stream of samples.
type Biquad struct {
	b0, b1, b2 float64
	a1, a2     float64
	x1, x2     float64
	y1, y2     float64
}

// NewPeakingBiquad builds a peaking equalizer section at freq, with the
// given gain in decibels and quality factor.
func NewPeakingBiquad(freq, q, gainDB float64, sampleRate int) *Biquad {
	if sampleRate <= 0 || freq <= 0 || freq >= float64(sampleRate)/2 {
		return &Biquad{b0: 1}
	}
	a := math.Pow(10, gainDB/40)
	w0 := 2 * math.Pi * freq / float64(sampleRate)
	alpha := math.Sin(w0) / (2 * q)
	cosw0 := math.Cos(w0)

	b0 := 1 + alpha*a
	b1 := -2 * cosw0
	b2 := 1 - alpha*a
	a0 := 1 + alpha/a
	a1 := -2 * cosw0
	a2 := 1 - alpha/a

	return &Biquad{
		b0: b0 / a0,
		b1: b1 / a0,
		b2: b2 / a0,
		a1: a1 / a0,
		a2: a2 / a0,
	}
}

// Process applies the filter to one sample, in place.
func (f *Biquad) Process(x float64) float64 {
	y := f.b0*x + f.b1*f.x1 + f.b2*f.x2 - f.a1*f.y1 - f.a2*f.y2
	f.x2 = f.x1
	f.x1 = x
	f.y2 = f.y1
	f.y1 = y
	return y
}

// Reset clears the filter's history, for a seek.
func (f *Biquad) Reset() {
	f.x1, f.x2, f.y1, f.y2 = 0, 0, 0, 0
}

// eqSnapshot is an immutable parameter set handed to the audio path. Filters
// carry mutable biquad state; each decoder owns a privateSnapshot copy, so
// one decoder's goroutine never touches another's filter history.
type eqSnapshot struct {
	channels int
	gen      uint64
	preamp   float64
	filters  [][]*Biquad
}

// Equalizer provides multi-band gain controls applied to the samples on
// their way to the speakers.
//
// Parameters reach the audio path as an immutable snapshot published via
// atomic.Pointer: setters rebuild the filters on their own goroutine, so
// Process (the audio callback path) only does two atomic loads and no lock
// or allocation once the channel count is known. The generation counter lets
// Process notice a stale snapshot without locking; the slow re-publish
// inside Process only runs on the first block and on rare channel-count
// changes.
type Equalizer struct {
	mu         sync.Mutex
	bands      map[string]float64 // band name -> gain (-12dB to +12dB)
	preamp     float64
	sampleRate int

	channels atomic.Int32
	gen      atomic.Uint64
	snap     atomic.Pointer[eqSnapshot]
}

// NewEqualizer creates a default flat equalizer
func NewEqualizer() *Equalizer {
	return &Equalizer{
		bands: map[string]float64{
			"bass":   0.0,
			"lowMid": 0.0,
			"mid":    0.0,
			"treble": 0.0,
		},
		preamp:     1.0,
		sampleRate: defaultSampleRate,
	}
}

// SetSampleRate tells the equalizer how fast the samples run, so its
// filters land on the right frequencies.
func (e *Equalizer) SetSampleRate(rate int) {
	if rate <= 0 {
		return
	}
	e.mu.Lock()
	if e.sampleRate == rate {
		e.mu.Unlock()
		return
	}
	e.sampleRate = rate
	e.gen.Add(1)
	e.mu.Unlock()
	e.publish(int(e.channels.Load()))
}

// SetGain sets gain for a specific band in dB
func (e *Equalizer) SetGain(band string, gainDB float64) {
	if gainDB < -12 {
		gainDB = -12
	}
	if gainDB > 12 {
		gainDB = 12
	}
	e.mu.Lock()
	e.bands[band] = gainDB
	e.gen.Add(1)
	e.mu.Unlock()
	e.publish(int(e.channels.Load()))
}

// GetGain gets gain for a band in dB
func (e *Equalizer) GetGain(band string) float64 {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.bands[band]
}

// Gains returns a copy of every band's gain, in dB.
func (e *Equalizer) Gains() map[string]float64 {
	e.mu.Lock()
	defer e.mu.Unlock()
	res := make(map[string]float64, len(e.bands))
	for k, v := range e.bands {
		res[k] = v
	}
	return res
}

// SetPreamp sets the gain applied to the signal before the bands, from 0.5
// to 2.
func (e *Equalizer) SetPreamp(g float64) {
	if g < 0.5 {
		g = 0.5
	}
	if g > 2 {
		g = 2
	}
	e.mu.Lock()
	e.preamp = g
	e.gen.Add(1)
	e.mu.Unlock()
	e.publish(int(e.channels.Load()))
}

// Preamp returns the gain applied before the bands.
func (e *Equalizer) Preamp() float64 {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.preamp
}

// Enabled reports whether any band changes the signal.
func (e *Equalizer) Enabled() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, g := range e.bands {
		if g != 0 {
			return true
		}
	}
	return false
}

// publish rebuilds the filter snapshot for channels and stores it. Setters
// call it after changing parameters; Process calls it only when no snapshot
// matches yet (first block, channel change, or a race with a setter).
func (e *Equalizer) publish(channels int) *eqSnapshot {
	e.mu.Lock()
	defer e.mu.Unlock()
	gen := e.gen.Load()
	if snap := e.snap.Load(); snap != nil && snap.channels == channels && snap.gen == gen {
		return snap
	}
	if channels < 1 {
		channels = 1
	}
	filters := make([][]*Biquad, channels)
	for ch := range filters {
		for _, b := range eqBands {
			filters[ch] = append(filters[ch], NewPeakingBiquad(b.freq, b.q, e.bands[b.name], e.sampleRate))
		}
	}
	snap := &eqSnapshot{channels: channels, gen: gen, preamp: e.preamp, filters: filters}
	e.snap.Store(snap)
	return snap
}

// apply runs the snapshot's filters over interleaved samples, in place.
// The steady-state path touches no locks.
func (s *eqSnapshot) apply(samples []float32, channels int) {
	preamp := s.preamp
	filters := s.filters
	for i := 0; i+channels <= len(samples); i += channels {
		for ch := 0; ch < channels; ch++ {
			value := float64(samples[i+ch]) * preamp
			for _, f := range filters[ch] {
				value = f.Process(value)
			}
			samples[i+ch] = float32(math.Max(-1, math.Min(1, value)))
		}
	}
}

// reset clears the filters' history, for a seek.
func (s *eqSnapshot) reset() {
	for _, filters := range s.filters {
		for _, f := range filters {
			f.Reset()
		}
	}
}

// privateSnapshot returns the published parameters with fresh filter state,
// for one decoder. Two players overlap briefly on a track switch (the old
// one still drains until its async close lands), and shared mutable biquad
// history between them would be a data race; the state therefore belongs to
// one decoder's goroutine, while the parameters follow the shared snapshot.
func (e *Equalizer) privateSnapshot(channels int) *eqSnapshot {
	snap := e.publish(channels)
	filters := make([][]*Biquad, len(snap.filters))
	for ch, chain := range snap.filters {
		filters[ch] = make([]*Biquad, len(chain))
		for i, f := range chain {
			// Coefficients only: the source chain may carry a running
			// decoder's history.
			filters[ch][i] = &Biquad{b0: f.b0, b1: f.b1, b2: f.b2, a1: f.a1, a2: f.a2}
		}
	}
	return &eqSnapshot{channels: snap.channels, gen: snap.gen, preamp: snap.preamp, filters: filters}
}
