package views

import (
	"fmt"
	"time"

	"github.com/egoist/mygo/ui"
	"musicgo/internal/player"
	"musicgo/internal/store"
	"musicgo/internal/ui/theme"
)

func renderAudioCache(c *ui.Context, th *theme.Theme, state *SettingsViewState, eng *player.Engine, config *store.ConfigManager, current int) {
	cache := eng.AudioCache()
	if cache == nil {
		return
	}
	stats := cache.Stats()
	busy := state.Busy()
	if busy || !stats.Ready {
		c.After(100 * time.Millisecond)
	} else if stats.Downloading {
		c.After(500 * time.Millisecond)
	}
	ui.Row(c).FillWidth().Wrap().Gap(10).AlignItems(ui.Center).Children(func() {
		ui.Text(c, "音频缓存").FontSize(14).TextColor(th.TextSub)
		for _, mb := range []int{0, 256, 512, 1024, 2048} {
			label := "关闭"
			if mb > 0 {
				label = fmt.Sprintf("%d MiB", mb)
			}
			if choiceButton(c.Key(fmt.Sprintf("cache_limit_%d", mb)), label, current == mb).Disabled(busy).Clicked() && current != mb {
				_ = config.UpdateAsync(func(cfg *store.AppConfig) { cfg.AudioCacheMB = mb })
				state.SetBusy(true)
				go func() { cache.SetLimit(mb); state.SetBusy(false) }()
			}
		}
	})
	ui.Row(c).Gap(12).AlignItems(ui.Center).Children(func() {
		ui.Text(c, fmt.Sprintf("已用 %.1f MiB", float64(stats.UsedBytes)/(1<<20))).FontSize(12).TextColor(th.TextMuted)
		if ui.Button(c.Key("audio_cache_clear"), "清理缓存").Disabled(busy).Clicked() {
			state.SetBusy(true)
			go func() { cache.Clear(); state.SetBusy(false) }()
		}
	})
}
