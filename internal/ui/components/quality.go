package components

import (
	"github.com/egoist/mygo/ui"
	"musicgo/internal/player"
	"musicgo/internal/store"
	"musicgo/internal/ui/theme"
)

var playbackQualities = []struct{ value, label string }{
	{"low", "普通"}, {"standard", "高品"}, {"high", "无损"}, {"super", "Hi-Res"},
}

// RenderQualityControl selects the preferred quality, not a guarantee about the stream.
func RenderQualityControl(c *ui.Context, th *theme.Theme, eng *player.Engine, config *store.ConfigManager) {
	requested, source := eng.Quality()
	label := "高品"
	for _, choice := range playbackQualities {
		if choice.value == requested {
			label = choice.label
		}
	}
	tip := "请求音质：" + label
	if source != "" {
		tip += "；实际音质：" + source
		if player.KnownQualityDowngrade(requested, source) {
			label += " ↓"
			tip += "（已降级）"
		}
	}
	button := ui.ButtonBase(c).Label("选择播放音质").Tooltip(tip).Padding(5, 8).Radius(7).Center().Cursor(ui.CursorPointer)
	if button.Hovered() {
		button.Background(th.HoverBg)
	}
	button.Children(func() { ui.Text(c, label).FontSize(12).TextColor(th.TextMain) })
	button.Menu(func(menu *ui.Menu) {
		for _, choice := range playbackQualities {
			choice := choice
			name := choice.label
			if choice.value == requested {
				name = "✓ " + name
			}
			if menu.Item(name).Chosen() {
				eng.SetQuality(choice.value)
				if config != nil {
					_ = config.UpdateAsync(func(cfg *store.AppConfig) { cfg.PlaybackQuality = choice.value })
				}
			}
		}
	})
}
