package ui

import (
	"time"

	"musicgo/internal/player"
	"musicgo/internal/store"
)

func (app *App) saveSession() {
	if app.Player == nil || app.ConfigMgr == nil {
		return
	}
	s := app.Player.SessionSnapshot()
	_ = app.ConfigMgr.UpdateAsync(func(c *store.AppConfig) {
		c.PlaybackQueue = &store.PlaybackQueueConfig{Songs: s.Songs, Index: s.Index, PositionMs: s.PositionMs,
			ShuffleHistory: s.ShuffleHistory, ShuffleCursor: s.ShuffleCursor, ShuffleCandidate: s.ShuffleCandidate, NextIndices: s.NextIndices}
		c.PlaybackMode = int(s.Mode)
	})
}

func (app *App) startSessionPersistence() {
	app.sessionStop, app.sessionDone = make(chan struct{}), make(chan struct{})
	go func() {
		defer close(app.sessionDone)
		tick := time.NewTicker(30 * time.Second)
		defer tick.Stop()
		for {
			select {
			case <-app.sessionStop:
				return
			case <-app.Player.SessionChanges():
				app.saveSession()
			case <-tick.C:
				if app.Player.State() == player.StatePlaying {
					app.saveSession()
				}
			}
		}
	}()
}
