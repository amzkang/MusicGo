package sys

import (
	"fmt"
	"sync"
	"time"

	"github.com/egoist/mygo"
	"musicgo/internal/player"
	"musicgo/internal/plugin"
)

// NotificationSender is a function that delivers notifications
type NotificationSender func(title, body string)

// MediaController publishes the engine through MyGo's native media controls.
// Commands and notification callbacks run on its worker, outside the UI thread.
type MediaController struct {
	mu       sync.Mutex
	eng      *player.Engine
	lastKey  plugin.SongKey
	enabled  func() bool
	notifier NotificationSender
	wake     chan struct{}
	done     chan struct{}
	finished chan struct{}
	commands chan mediaCommand
	off      func()
	once     sync.Once
}

type mediaCommand struct {
	command  mygo.MediaCommand
	position time.Duration
}

// NewMediaController creates a new media controller
func NewMediaController(eng *player.Engine) *MediaController {
	mc := &MediaController{
		eng:      eng,
		wake:     make(chan struct{}, 1),
		done:     make(chan struct{}),
		finished: make(chan struct{}),
		commands: make(chan mediaCommand, 32),
	}
	eng.AddStateChangeListener(func(player.State) { mc.notify() })
	eng.AddSongChangeListener(func(*plugin.MusicItem) { mc.notify() })
	eng.AddPlaybackListener(mc.notify)
	mc.off = mygo.NowPlaying.OnCommand(func(command mygo.MediaCommand, position time.Duration) {
		select {
		case <-mc.done:
			return
		default:
		}
		select {
		case mc.commands <- mediaCommand{command, position}:
		default:
		}
	})
	go mc.run()
	mc.notify()
	return mc
}

func (mc *MediaController) notify() {
	select {
	case mc.wake <- struct{}{}:
	default:
	}
}

func (mc *MediaController) Close() {
	mc.once.Do(func() {
		mc.off()
		close(mc.done)
	})
	<-mc.finished
}

func (mc *MediaController) run() {
	defer close(mc.finished)
	defer mygo.NowPlaying.Clear()
	var key plugin.SongKey
	var rate float64
	var duration time.Duration
	for {
		select {
		case <-mc.done:
			return
		case command := <-mc.commands:
			switch command.command {
			case mygo.MediaPlay:
				mc.eng.Play()
			case mygo.MediaPause:
				mc.eng.Pause()
			case mygo.MediaStop:
				mc.eng.Stop()
			case mygo.MediaNext:
				mc.eng.Next()
			case mygo.MediaPrevious:
				mc.eng.Prev()
			case mygo.MediaSeek:
				mc.eng.Seek(command.position.Seconds())
			}
		case <-mc.wake:
		}
		song := mc.eng.CurrentSong()
		if song == nil {
			mygo.NowPlaying.Clear()
			key = ""
			continue
		}
		state := mygo.PlaybackStopped
		switch mc.eng.State() {
		case player.StatePlaying:
			state = mygo.PlaybackPlaying
		case player.StatePaused, player.StateLoading:
			state = mygo.PlaybackPaused
		}
		position := time.Duration(mc.eng.Position() * float64(time.Second))
		nextDuration := time.Duration(mc.eng.Duration() * float64(time.Second))
		nextRate := mc.eng.Speed()
		if song.Key() != key || nextRate != rate || nextDuration != duration {
			commands := mygo.MediaPlay | mygo.MediaPause | mygo.MediaStop | mygo.MediaNext | mygo.MediaPrevious
			if nextDuration > 0 {
				commands |= mygo.MediaSeek
			}
			mygo.NowPlaying.Set(mygo.NowPlayingInfo{
				Title: song.Title, Artist: song.Artist, Album: song.Album,
				State: state, Position: position, Duration: nextDuration, Rate: nextRate, Commands: commands,
			})
			key, rate, duration = song.Key(), nextRate, nextDuration
		} else {
			mygo.NowPlaying.SetPlayback(state, position)
		}
		if state == mygo.PlaybackPlaying {
			mc.UpdateNowPlaying(song)
		}
	}
}

// SetNotifier supplies the optional track notification sender.
func (mc *MediaController) SetNotifier(sender NotificationSender) {
	mc.mu.Lock()
	defer mc.mu.Unlock()
	mc.notifier = sender
}

// UpdateNowPlaying updates the system now playing information and displays notification
func (mc *MediaController) UpdateNowPlaying(song *plugin.MusicItem) {
	if song == nil {
		return
	}

	mc.mu.Lock()
	enabled, notifier := mc.enabled, mc.notifier
	mc.mu.Unlock()
	if notifier == nil || enabled != nil && !enabled() {
		return
	}
	mc.mu.Lock()
	key := song.Key()
	if key == mc.lastKey {
		mc.mu.Unlock()
		return
	}
	mc.lastKey = key
	mc.mu.Unlock()

	body := song.Artist
	if song.Album != "" {
		body += " - " + song.Album
	}
	if body == "" {
		body = "正在播放"
	}

	if notifier != nil {
		notifier(fmt.Sprintf("正在播放: %s", song.Title), body)
	}
}

func (mc *MediaController) SetEnabled(check func() bool) {
	mc.mu.Lock()
	mc.enabled = check
	mc.mu.Unlock()
}
