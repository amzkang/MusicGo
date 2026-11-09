package player

import (
	"math/rand"

	"musicgo/internal/plugin"
)

// IDs identify queue occurrences, including repeated copies of the same song.
// They live for this engine session; saved queues retain their original format.
type QueueEntry struct {
	ID   uint64
	Song plugin.MusicItem
}

type QueueSnapshot struct {
	Entries []QueueEntry
	Current int
	NextID  uint64
	Version uint64
	Mode    Mode
	State   State
}

// PreviousSongPreview follows Prev, including shuffle playback history.
func (e *Engine) PreviousSongPreview() *plugin.MusicItem {
	e.mu.RLock()
	defer e.mu.RUnlock()
	if e.closed || len(e.queue) == 0 {
		return nil
	}
	index := e.queueIndex - 1
	if e.mode == ModeShuffle {
		if e.shuffleCursor <= 0 || e.shuffleCursor >= len(e.shuffleHistory) || e.shuffleHistory[e.shuffleCursor] != e.queueIndex {
			return nil
		}
		index = e.shuffleHistory[e.shuffleCursor-1]
	} else if index < 0 {
		index = len(e.queue) - 1
	}
	if index < 0 || index >= len(e.queue) {
		return nil
	}
	song := e.queue[index].Song.Clone()
	return &song
}

// NextSongPreview follows the manual Next command, including a stable shuffle pick.
func (e *Engine) NextSongPreview() *plugin.MusicItem {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closed || len(e.queue) == 0 {
		return nil
	}
	index := e.queueIndex + 1
	if len(e.nextEntries) > 0 {
		index = e.queueEntryIndexLocked(e.nextEntries[0])
	} else {
		switch e.mode {
		case ModeRepeatOne, ModeRepeatAll:
			index %= len(e.queue)
		case ModeShuffle:
			e.ensureShuffleCurrentLocked()
			if e.shuffleCursor+1 < len(e.shuffleHistory) {
				index = e.shuffleHistory[e.shuffleCursor+1]
			} else if len(e.queue) == 1 {
				index = 0
			} else {
				if e.shuffleCandidate < 0 || e.shuffleCandidate >= len(e.queue) || e.shuffleCandidate == e.queueIndex {
					e.shuffleCandidate = rand.Intn(len(e.queue) - 1)
					if e.shuffleCandidate >= e.queueIndex {
						e.shuffleCandidate++
					}
				}
				index = e.shuffleCandidate
			}
		}
	}
	if index < 0 || index >= len(e.queue) {
		return nil
	}
	song := e.queue[index].Song.Clone()
	return &song
}

// QueueSnapshot always reads controls together. Entries are copied only when
// previous differs from Version; otherwise the caller retains its prior copy.
func (e *Engine) QueueSnapshot(previous uint64) QueueSnapshot {
	e.mu.RLock()
	defer e.mu.RUnlock()
	s := QueueSnapshot{Current: e.queueIndex, Version: e.queueVersion, Mode: e.mode, State: e.state}
	if len(e.nextEntries) > 0 {
		s.NextID = e.nextEntries[0]
	}
	if previous != s.Version {
		s.Entries = make([]QueueEntry, len(e.queue))
		for i, entry := range e.queue {
			s.Entries[i] = QueueEntry{ID: entry.ID, Song: entry.Song.Clone()}
		}
	}
	return s
}

func (e *Engine) newQueueEntryLocked(song plugin.MusicItem) QueueEntry {
	e.queueSerial++
	return QueueEntry{ID: e.queueSerial, Song: song.Clone()}
}

func (e *Engine) queueEntryIndexLocked(id uint64) int {
	for i := range e.queue {
		if e.queue[i].ID == id {
			return i
		}
	}
	return -1
}

func (e *Engine) touchQueueLocked() {
	e.queueVersion++
	e.sessionChanged()
	go e.playbackChanged()
}

// SetQueue replaces the queue and plays its selection. An empty queue stops.
func (e *Engine) SetQueue(songs []plugin.MusicItem, index int) {
	e.replaceQueue(songs, index, true)
}

// LoadQueue replaces the queue without opening a decoder or audio device.
func (e *Engine) LoadQueue(songs []plugin.MusicItem, index int) {
	e.replaceQueue(songs, index, false)
}

// ClearQueue cancels loading and detaches audio as well as clearing the list.
func (e *Engine) ClearQueue() {
	e.replaceQueue(nil, 0, false)
}

func (e *Engine) replaceQueue(songs []plugin.MusicItem, index int, play bool) {
	e.mu.Lock()
	if e.closed {
		e.mu.Unlock()
		return
	}
	e.queue = make([]QueueEntry, len(songs))
	for i := range songs {
		e.queue[i] = e.newQueueEntryLocked(songs[i])
	}
	if index < 0 || index >= len(songs) {
		index = 0
	}
	e.queueIndex = index
	e.nextEntries = nil
	e.resetQueueTrackingLocked()
	cleanup := e.resetQueueSelectionLocked()
	song, epoch := e.currentSong, e.playEpoch
	e.touchQueueLocked()
	e.mu.Unlock()
	go cleanup()
	if play && song != nil {
		e.playSong(*song, &epoch)
	} else if song == nil {
		e.notifyQueueSelection(epoch)
	}
}

// The queue selection has changed. Discard the old stream before exposing the
// new paused selection; slow decoder/network cleanup runs after releasing mu.
func (e *Engine) resetQueueSelectionLocked() func() {
	e.playEpoch++
	e.invalidatePrefetchLocked()
	cleanup := e.cleanupCurrentPlayerLocked()
	e.pendingSeek = nil
	e.positionSec, e.resumeSec, e.durationSec = 0, 0, 0
	e.lastError, e.sourceQuality = "", ""
	e.currentSong = nil
	if e.queueIndex >= 0 && e.queueIndex < len(e.queue) {
		song := e.queue[e.queueIndex].Song.Clone()
		e.currentSong = &song
		e.durationSec = song.Duration
		e.setState(StatePaused)
	} else {
		e.setState(StateStopped)
	}
	return cleanup
}

func (e *Engine) notifyQueueSelection(epoch uint64) {
	e.mu.RLock()
	if e.closed || e.playEpoch != epoch {
		e.mu.RUnlock()
		return
	}
	var song *plugin.MusicItem
	if e.currentSong != nil {
		copy := e.currentSong.Clone()
		song = &copy
	}
	callbacks := append([]func(*plugin.MusicItem){}, e.onSongChange...)
	e.mu.RUnlock()
	for _, callback := range callbacks {
		if callback != nil {
			var copy *plugin.MusicItem
			if song != nil {
				item := song.Clone()
				copy = &item
			}
			callback(copy)
		}
	}
}

// ActivateQueueEntry toggles the current entry, or plays another occurrence.
// Commands from a removed/replaced row are harmless, even with an open menu.
func (e *Engine) ActivateQueueEntry(id uint64) {
	e.activateQueueEntry(id, true)
}

func (e *Engine) activateQueueEntry(id uint64, toggle bool) {
	e.mu.Lock()
	index := e.queueEntryIndexLocked(id)
	if e.closed || index < 0 {
		e.mu.Unlock()
		return
	}
	if index == e.queueIndex {
		epoch, state := e.playEpoch, e.state
		e.mu.Unlock()
		if toggle && (state == StatePlaying || state == StateLoading) {
			e.pause(&epoch)
		} else {
			e.play(&epoch)
		}
		return
	}
	if e.mode == ModeShuffle {
		e.ensureShuffleCurrentLocked()
		e.shuffleHistory = append(e.shuffleHistory[:e.shuffleCursor+1], index)
		e.shuffleCursor++
	}
	e.forgetNextLocked(id)
	e.queueIndex = index
	e.playEpoch++
	song, epoch := e.queue[index].Song.Clone(), e.playEpoch
	e.touchQueueLocked()
	e.mu.Unlock()
	e.playSong(song, &epoch)
}

// PlaySong selects an existing occurrence, or appends a new one. Direct local
// playback participates in the same queue as list playback.
func (e *Engine) PlaySong(song plugin.MusicItem) {
	e.mu.Lock()
	if e.closed {
		e.mu.Unlock()
		return
	}
	id := uint64(0)
	key := song.Key()
	if len(e.queue) > 0 && e.queue[e.queueIndex].Song.Key() == key {
		id = e.queue[e.queueIndex].ID
	} else {
		for _, entry := range e.queue {
			if entry.Song.Key() == key {
				id = entry.ID
				break
			}
		}
	}
	if id == 0 {
		entry := e.newQueueEntryLocked(song)
		e.queue = append(e.queue, entry)
		id = entry.ID
		e.touchQueueLocked()
	}
	e.mu.Unlock()
	e.activateQueueEntry(id, false)
}

// PlayNext reuses a matching queued song rather than repeatedly duplicating it.
// A copy of the current entry is allowed, for an explicit request to repeat it.
func (e *Engine) PlayNext(song plugin.MusicItem) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closed {
		return
	}
	index := -1
	key := song.Key()
	for i, entry := range e.queue {
		if i != e.queueIndex && entry.Song.Key() == key {
			index = i
			break
		}
	}
	if index < 0 {
		e.queue = append(e.queue, e.newQueueEntryLocked(song))
		index = len(e.queue) - 1
	}
	if len(e.queue) == 1 {
		e.queueIndex = 0
		current := e.queue[0].Song.Clone()
		e.currentSong = &current
		e.durationSec = current.Duration
		e.setState(StatePaused)
		e.touchQueueLocked()
		return
	}
	e.queueNextLocked(index)
}

// QueueEntryNext moves this exact occurrence next, including duplicate songs.
func (e *Engine) QueueEntryNext(id uint64) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if index := e.queueEntryIndexLocked(id); !e.closed && index >= 0 && index != e.queueIndex {
		e.queueNextLocked(index)
	}
}

func (e *Engine) queueNextLocked(index int) {
	id := e.queue[index].ID
	e.invalidatePrefetchLocked()
	e.forgetNextLocked(id)
	to := e.queueIndex + 1
	if index < to {
		to--
	}
	e.moveQueueIndexLocked(index, to)
	e.nextEntries = append([]uint64{id}, e.nextEntries...)
	e.touchQueueLocked()
	if e.state == StatePlaying {
		e.startPrefetchLocked()
	}
}

func (e *Engine) forgetNextLocked(id uint64) {
	for i, next := range e.nextEntries {
		if next == id {
			e.nextEntries = append(e.nextEntries[:i], e.nextEntries[i+1:]...)
			return
		}
	}
}

func (e *Engine) RemoveQueueEntry(id uint64) {
	e.mu.Lock()
	index := e.queueEntryIndexLocked(id)
	if e.closed || index < 0 {
		e.mu.Unlock()
		return
	}
	current := index == e.queueIndex
	playing := e.state == StatePlaying || e.state == StateLoading
	e.invalidatePrefetchLocked()
	e.forgetNextLocked(id)
	history, cursor := e.shuffleHistory[:0], e.shuffleCursor
	for i, idx := range e.shuffleHistory {
		if idx == index {
			if i <= e.shuffleCursor {
				cursor--
			}
			continue
		}
		if idx > index {
			idx--
		}
		history = append(history, idx)
	}
	e.shuffleHistory, e.shuffleCursor = history, cursor
	copy(e.queue[index:], e.queue[index+1:])
	e.queue[len(e.queue)-1] = QueueEntry{}
	e.queue = e.queue[:len(e.queue)-1]
	if index < e.queueIndex {
		e.queueIndex--
	} else if e.queueIndex >= len(e.queue) {
		e.queueIndex = 0
	}
	var cleanup func()
	if current {
		if len(e.nextEntries) > 0 {
			e.queueIndex = e.queueEntryIndexLocked(e.nextEntries[0])
			e.nextEntries = e.nextEntries[1:]
		}
		cleanup = e.resetQueueSelectionLocked()
		if len(e.queue) > 0 && e.mode == ModeShuffle {
			e.shuffleHistory = append(e.shuffleHistory[:min(max(e.shuffleCursor+1, 0), len(e.shuffleHistory))], e.queueIndex)
			e.shuffleCursor = len(e.shuffleHistory) - 1
		}
	} else if e.state == StatePlaying {
		e.startPrefetchLocked()
	}
	song, epoch := e.currentSong, e.playEpoch
	e.touchQueueLocked()
	e.mu.Unlock()
	if cleanup != nil {
		go cleanup()
		if playing && song != nil {
			e.playSong(*song, &epoch)
		} else {
			e.notifyQueueSelection(epoch)
		}
	}
}

// MoveQueueEntry inserts before an entry ID, or at the end when before is zero.
// It never changes the current stream, position or paused/playing state.
func (e *Engine) MoveQueueEntry(id, before uint64) {
	e.mu.Lock()
	defer e.mu.Unlock()
	from := e.queueEntryIndexLocked(id)
	to := len(e.queue)
	if before != 0 {
		to = e.queueEntryIndexLocked(before)
	}
	if e.closed || from < 0 || to < 0 {
		return
	}
	if from < to {
		to--
	}
	if from == to {
		return
	}
	e.invalidatePrefetchLocked()
	e.moveQueueIndexLocked(from, to)
	e.touchQueueLocked()
	if e.state == StatePlaying {
		e.startPrefetchLocked()
	}
}

func (e *Engine) moveQueueIndexLocked(from, to int) {
	remap := func(index int) int {
		switch {
		case index == from:
			return to
		case from < to && index > from && index <= to:
			return index - 1
		case from > to && index >= to && index < from:
			return index + 1
		default:
			return index
		}
	}
	for i, index := range e.shuffleHistory {
		e.shuffleHistory[i] = remap(index)
	}
	entry := e.queue[from]
	if from < to {
		copy(e.queue[from:to], e.queue[from+1:to+1])
	} else {
		copy(e.queue[to+1:from+1], e.queue[to:from])
	}
	e.queue[to] = entry
	e.queueIndex = remap(e.queueIndex)
}
