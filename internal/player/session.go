package player

import (
	"math"

	"musicgo/internal/plugin"
)

// PlaybackSession is a detached, consistent snapshot. Random history may repeat
// indices: it records actual visits, not a permutation of the queue.
type PlaybackSession struct {
	Songs            []plugin.MusicItem
	Index            int
	Mode             Mode
	PositionMs       int64
	ShuffleHistory   []int
	ShuffleCursor    int
	ShuffleCandidate *int
	NextIndices      []int
}

func (e *Engine) SessionSnapshot() PlaybackSession {
	e.mu.RLock()
	defer e.mu.RUnlock()
	s := PlaybackSession{Index: e.queueIndex, Mode: e.mode, ShuffleCursor: e.shuffleCursor,
		Songs: make([]plugin.MusicItem, len(e.queue)), ShuffleHistory: append([]int(nil), e.shuffleHistory...)}
	for i := range e.queue {
		s.Songs[i] = e.queue[i].Song.Clone()
	}
	if len(e.nextEntries) > 0 {
		indices := make(map[uint64]int, len(e.queue))
		for i, entry := range e.queue {
			indices[entry.ID] = i
		}
		for _, id := range e.nextEntries {
			if index, ok := indices[id]; ok {
				s.NextIndices = append(s.NextIndices, index)
			}
		}
	}
	pos := e.positionSec
	if e.seeking {
		pos = e.seekTarget
	} else if e.player != nil {
		pos = e.player.Position().Seconds()
	}
	if !math.IsNaN(pos) && !math.IsInf(pos, 0) {
		s.PositionMs = int64(max(0, pos) * 1000)
	}
	if e.shuffleCandidate >= 0 {
		candidate := e.shuffleCandidate
		s.ShuffleCandidate = &candidate
	}
	return s
}

// RestoreSession sets memory only. The first explicit Play supplies the saved
// position with the load request, before playSong resets transient seek state.
func (e *Engine) RestoreSession(s PlaybackSession) {
	e.LoadQueue(s.Songs, s.Index)
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closed {
		return
	}
	if s.Mode >= ModeSequence && s.Mode <= ModeShuffle {
		e.mode = s.Mode
	}
	pos := max(0, float64(s.PositionMs)/1000)
	if e.currentSong != nil && e.currentSong.Duration > 0 {
		pos = min(pos, e.currentSong.Duration)
	}
	if len(e.queue) == 0 {
		pos = 0
	} else {
		e.setState(StatePaused)
	}
	e.positionSec, e.resumeSec = pos, pos
	valid := len(s.ShuffleHistory) > 0 && s.ShuffleCursor >= 0 && s.ShuffleCursor < len(s.ShuffleHistory)
	for _, idx := range s.ShuffleHistory {
		valid = valid && idx >= 0 && idx < len(e.queue)
	}
	if valid && s.ShuffleHistory[s.ShuffleCursor] == e.queueIndex {
		e.shuffleHistory = append([]int(nil), s.ShuffleHistory...)
		e.shuffleCursor = s.ShuffleCursor
	}
	if s.ShuffleCandidate != nil && *s.ShuffleCandidate >= 0 && *s.ShuffleCandidate < len(e.queue) {
		e.shuffleCandidate = *s.ShuffleCandidate
	}
	seen := make(map[int]bool)
	for _, index := range s.NextIndices {
		if index >= 0 && index < len(e.queue) && index != e.queueIndex && !seen[index] {
			e.nextEntries = append(e.nextEntries, e.queue[index].ID)
			seen[index] = true
		}
	}
}

// SessionChanges coalesces changes; one application persistence worker consumes it.
func (e *Engine) SessionChanges() <-chan struct{} { return e.sessionWake }

func (e *Engine) sessionChanged() {
	select {
	case e.sessionWake <- struct{}{}:
	default:
	}
}
