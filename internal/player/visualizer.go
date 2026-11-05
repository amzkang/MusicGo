package player

import (
	"math"
	"sync"
)

const (
	// fftSize is the length of the analysis window, in samples.
	fftSize = 1024
	// fftHop is how many new samples trigger a new analysis.
	fftHop = 128
	// defaultSampleRate is assumed until the decoder reports its own.
	defaultSampleRate = 44100
	// spectrumFloor keeps a band from ever reading as fully silent, so the
	// UI shows a resting line instead of nothing at all.
	spectrumFloor = 0.02
)

// Visualizer turns the audio the engine plays into smoothed frequency
// bars for the GPU UI. It runs a radix-2 FFT over the samples the decoder
// produces, so the bars follow the music instead of guessing.
type Visualizer struct {
	mu       sync.Mutex
	barCount int
	bars     []float32
	decay    float32

	sampleRate int

	buf     []float32 // the analysis window, the newest samples last
	valid   int       // how many of buf hold samples
	pending int       // new samples since the last analysis

	re      []float32
	im      []float32
	win     []float32
	bitRev  []int
	twiddle []float32
}

// NewVisualizer creates a visualizer with the given number of spectrum bars
func NewVisualizer(barCount int) *Visualizer {
	if barCount <= 0 {
		barCount = 24
	}
	v := &Visualizer{
		barCount:   barCount,
		bars:       make([]float32, barCount),
		decay:      0.72,
		sampleRate: defaultSampleRate,
		buf:        make([]float32, fftSize),
		re:         make([]float32, fftSize),
		im:         make([]float32, fftSize),
		win:        make([]float32, fftSize),
		bitRev:     make([]int, fftSize),
		twiddle:    make([]float32, fftSize/2),
	}
	for i := 0; i < fftSize/2; i++ {
		v.twiddle[i] = float32(-2 * math.Pi * float64(i) / float64(fftSize))
	}
	// The bit reversal table the FFT permutes by.
	bits := 0
	for 1<<bits < fftSize {
		bits++
	}
	for i := 0; i < fftSize; i++ {
		x := i
		r := 0
		for j := 0; j < bits; j++ {
			r = (r << 1) | (x & 1)
			x >>= 1
		}
		v.bitRev[i] = r
	}
	// A Hann window, to keep the leakage of the FFT in check.
	for i := range v.win {
		v.win[i] = float32(0.5 - 0.5*math.Cos(2*math.Pi*float64(i)/float64(fftSize)))
	}
	return v
}

// SetSampleRate tells the visualizer how fast the samples it gets run, so
// the bands it reports stand for the right frequencies.
func (v *Visualizer) SetSampleRate(rate int) {
	if rate <= 0 {
		return
	}
	v.mu.Lock()
	v.sampleRate = rate
	v.mu.Unlock()
}

// SampleRate returns the sample rate the visualizer assumes
func (v *Visualizer) SampleRate() int {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.sampleRate
}

// ProcessSamples feeds mono samples to the visualizer.
func (v *Visualizer) ProcessSamples(samples []float32) []float32 {
	return v.ProcessInterleaved(samples, 1)
}

// ProcessInterleaved feeds interleaved samples of the given channel count to
// the visualizer, mixing them down to mono before the analysis.
func (v *Visualizer) ProcessInterleaved(samples []float32, channels int) []float32 {
	v.mu.Lock()
	defer v.mu.Unlock()

	if channels < 1 {
		channels = 1
	}

	if len(samples) == 0 {
		for i := range v.bars {
			v.bars[i] *= v.decay
			if v.bars[i] < spectrumFloor {
				v.bars[i] = spectrumFloor
			}
		}
		return v.barsCopyLocked()
	}

	mono := make([]float32, 0, len(samples)/channels+1)
	if channels == 1 {
		mono = append(mono, samples...)
	} else {
		for i := 0; i+channels-1 < len(samples); i += channels {
			var sum float32
			for ch := 0; ch < channels; ch++ {
				sum += samples[i+ch]
			}
			mono = append(mono, sum/float32(channels))
		}
	}

	// Keep the newest fftSize samples in the window.
	if len(mono) >= fftSize {
		copy(v.buf, mono[len(mono)-fftSize:])
		v.valid = fftSize
	} else {
		if v.valid+len(mono) > fftSize {
			drop := v.valid + len(mono) - fftSize
			copy(v.buf, v.buf[drop:v.valid])
			v.valid -= drop
		}
		copy(v.buf[v.valid:], mono)
		v.valid += len(mono)
	}
	v.pending += len(mono)

	if v.pending >= fftHop {
		v.pending %= fftHop
		v.analyzeLocked()
	}

	return v.barsCopyLocked()
}

func (v *Visualizer) barsCopyLocked() []float32 {
	res := make([]float32, v.barCount)
	copy(res, v.bars)
	return res
}

// analyzeLocked runs the FFT over the window and maps its magnitudes onto
// the bands.
func (v *Visualizer) analyzeLocked() {
	if v.valid < 32 {
		return
	}

	for i := 0; i < fftSize; i++ {
		var s float32
		if i < v.valid {
			s = v.buf[i]
		}
		v.re[i] = s * v.win[i]
		v.im[i] = 0
	}

	v.fft(v.re, v.im)

	// The magnitude of each bin, up to the Nyquist frequency.
	half := fftSize / 2
	mags := make([]float32, half)
	for k := 0; k < half; k++ {
		re := float64(v.re[k])
		im := float64(v.im[k])
		mags[k] = float32(math.Sqrt(re*re + im*im))
	}

	binHz := float64(v.sampleRate) / float64(fftSize)
	const fMin = 40.0
	const fMax = 16000.0

	for band := 0; band < v.barCount; band++ {
		lo := fMin * math.Pow(fMax/fMin, float64(band)/float64(v.barCount))
		hi := fMin * math.Pow(fMax/fMin, float64(band+1)/float64(v.barCount))

		kLo := int(lo / binHz)
		kHi := int(hi / binHz)
		if kLo < 1 {
			kLo = 1 // the DC bin says nothing about the music
		}
		if kHi <= kLo {
			kHi = kLo + 1
		}
		if kHi > half {
			kHi = half
		}
		if kLo >= half {
			continue
		}

		var sum float64
		count := 0
		for k := kLo; k < kHi; k++ {
			sum += float64(mags[k])
			count++
		}
		if count == 0 {
			continue
		}

		amp := sum / float64(count) / float64(half)
		db := 20 * math.Log10(amp+1e-9)
		// -60dB to 0dB mapped onto 0 to 1.
		val := float32((db + 60) / 60)
		if val < 0 {
			val = 0
		}
		if val > 1 {
			val = 1
		}

		if val > v.bars[band] {
			v.bars[band] = val
		} else {
			v.bars[band] = v.bars[band]*v.decay + val*(1-v.decay)
		}
		if v.bars[band] < spectrumFloor {
			v.bars[band] = spectrumFloor
		}
	}
}

// fft runs an in-place radix-2 Cooley-Tukey transform on re and im.
func (v *Visualizer) fft(re, im []float32) {
	n := len(re)
	for i := 0; i < n; i++ {
		j := v.bitRev[i]
		if j > i {
			re[i], re[j] = re[j], re[i]
			im[i], im[j] = im[j], im[i]
		}
	}
	for size := 2; size <= n; size <<= 1 {
		half := size / 2
		step := n / size
		for i := 0; i < n; i += size {
			k := 0
			for j := i; j < i+half; j++ {
				ang := v.twiddle[k]
				wr := float32(math.Cos(float64(ang)))
				wi := float32(math.Sin(float64(ang)))
				tr := wr*re[j+half] - wi*im[j+half]
				ti := wr*im[j+half] + wi*re[j+half]
				re[j+half] = re[j] - tr
				im[j+half] = im[j] - ti
				re[j] += tr
				im[j] += ti
				k += step
			}
		}
	}
}

// GetBars returns the current spectrum bars
func (v *Visualizer) GetBars() []float32 {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.barsCopyLocked()
}
