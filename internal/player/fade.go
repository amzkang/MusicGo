package player

import (
	"github.com/egoist/mygo/audio"
	"math"
	"sync"
	"time"
)

const pauseFade = 180 * time.Millisecond
const resumeFade = 260 * time.Millisecond

// volumeFader changes only the output gain. The user's volume stays in Engine.
// Workers never redraw windows and an overtaking command cancels the old ramp.
type volumeFader struct {
	mu               sync.Mutex
	out              *audio.Player
	volume, from, to float64
	start            time.Time
	duration         time.Duration
	generation       uint64
}

func (f *volumeFader) level(now time.Time) float64 {
	t := 1.0
	if f.duration > 0 {
		t = min(1, max(0, now.Sub(f.start).Seconds()/f.duration.Seconds()))
	}
	return f.from + (f.to-f.from)*(0.5-0.5*math.Cos(math.Pi*t))
}
func (f *volumeFader) apply(now time.Time) {
	if f.out != nil {
		f.out.SetVolume(f.volume * f.level(now))
	}
}
func (f *volumeFader) attach(out *audio.Player, volume, level float64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.generation++
	f.out, f.volume = out, volume
	f.from, f.to, f.duration = level, level, 0
	f.apply(time.Now())
}
func (f *volumeFader) setVolume(volume float64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.volume = volume
	f.apply(time.Now())
}
func (f *volumeFader) current(gen uint64) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return gen == f.generation
}
func (f *volumeFader) ramp(to float64, d time.Duration, done func(uint64)) {
	f.mu.Lock()
	now := time.Now()
	from := f.level(now)
	f.generation++
	gen := f.generation
	f.from, f.to, f.start, f.duration = from, to, now, time.Duration(float64(d)*math.Abs(to-from))
	f.mu.Unlock()
	go func() {
		tick := time.NewTicker(8 * time.Millisecond)
		defer tick.Stop()
		for {
			f.mu.Lock()
			if gen != f.generation {
				f.mu.Unlock()
				return
			}
			now := time.Now()
			f.apply(now)
			finished := now.Sub(f.start) >= f.duration
			f.mu.Unlock()
			if finished {
				if done != nil {
					done(gen)
				}
				return
			}
			<-tick.C
		}
	}()
}
