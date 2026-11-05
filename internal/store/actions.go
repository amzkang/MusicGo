package store

import "musicgo/internal/plugin"

// RunAsync keeps UI mutations ordered and flushes accepted work before Close.
func (s *Store) RunAsync(fn func() error, redraw func()) {
	s.actionMu.Lock()
	if s.actionsClosed {
		s.actionMu.Unlock()
		return
	}
	s.actions = append(s.actions, func() {
		err := fn()
		s.mu.Lock()
		s.lastWriteError = err
		s.mu.Unlock()
		if redraw != nil {
			redraw()
		}
	})
	select {
	case s.actionWake <- struct{}{}:
	default:
	}
	s.actionMu.Unlock()
}
func (s *Store) LastWriteError() error { s.mu.RLock(); defer s.mu.RUnlock(); return s.lastWriteError }
func (s *Store) actionLoop() {
	defer close(s.actionDone)
	for {
		stopping := false
		select {
		case <-s.actionWake:
		case <-s.actionStop:
			stopping = true
		}
		for {
			s.actionMu.Lock()
			batch := s.actions
			s.actions = nil
			s.actionMu.Unlock()
			if len(batch) == 0 {
				break
			}
			for _, fn := range batch {
				fn()
			}
		}
		if stopping {
			return
		}
	}
}
func (s *Store) ToggleFavoriteAsync(song plugin.MusicItem, redraw func()) {
	song = song.Clone()
	s.RunAsync(func() error {
		if s.IsFavorite(song.Key()) {
			return s.RemoveFavorite(song.Key())
		}
		return s.AddFavorite(song)
	}, redraw)
}
