package components

import (
	"github.com/egoist/mygo/ui"
	"musicgo/internal/ui/theme"
	"strings"
)

func renderSearchInput(c *ui.Context, th *theme.Theme, state *HeaderState, plate ui.Element, onSearch func(string)) {
	cb := ui.ComboboxBase(c.Key("header_search"), &state.SearchQuery)
	input := cb.Input.Grow(1).MinWidth(0).Label("搜索歌曲、歌手、专辑").Placeholder("搜索歌曲、歌手、专辑").FontSize(13).TextColor(th.TextMain)
	if input.Focused() {
		plate.BorderColor(th.Primary)
	}
	query := strings.TrimSpace(state.SearchQuery)
	if query == "" && len(state.SearchHistory) == 0 {
		cb.SetOpen(false)
	}
	cb.Popup(func(panel ui.Element) {
		panel.Label("搜索建议").AttachTo(plate, ui.AnchorBottomStart, ui.AnchorTopStart).Width(320).MaxHeight(360).
			Padding(6).Margin(8, 0, 0, 0).Radius(12).Background(th.PanelBg).Border(1, th.Border).Shadow(0, 6, 20, 0, ui.RGBA(0, 0, 0, 0.14))
		option := func(key, text string) {
			item := cb.Item(key).Label(text).FillWidth().MinHeight(34).Padding(8, 10).Radius(7).Cursor(ui.CursorPointer)
			if item.Highlighted() {
				item.Background(th.ActiveBg)
			} else if item.Hovered() {
				item.Background(th.HoverBg)
			}
			item.Children(func() { ui.Text(c, text).FontSize(13).TextColor(th.TextMain).SingleLine().Ellipsis("…") })
		}
		if query != "" {
			option("query", "搜索“"+query+"”")
		}
		if len(state.SearchHistory) > 0 {
			ui.Row(c).FillWidth().Padding(3, 8).AlignItems(ui.Center).Children(func() {
				ui.Text(c, "搜索历史").Grow(1).FontSize(11).TextColor(th.TextMuted)
				clear := ui.ButtonBase(c.Key("clear_search_history")).Label("清空搜索历史").Padding(4, 6).KeepFocus().Cursor(ui.CursorPointer)
				clear.Children(func() { ui.Text(c, "清空").FontSize(11).TextColor(th.TextSub) })
				if clear.Clicked() {
					state.SearchHistory = nil
					if state.ClearSearchHistory != nil {
						state.ClearSearchHistory()
					}
					if query == "" {
						cb.SetOpen(false)
					}
				}
			})
			ui.Scroll(c).MaxHeight(270).FillWidth().Children(func() {
				for _, term := range state.SearchHistory {
					if query == "" || strings.Contains(term, query) {
						if term != query || query == "" {
							option("history:"+term, term)
						}
					}
				}
			})
		}
	})
	submit := func(query string) {
		query = strings.TrimSpace(query)
		if query == "" {
			return
		}
		cb.SetOpen(false)
		state.SearchQuery = query
		if onSearch != nil {
			onSearch(query)
		}
	}
	if key, ok := cb.Chosen(); ok {
		if key == "query" {
			submit(query)
		} else if strings.HasPrefix(key, "history:") {
			submit(strings.TrimPrefix(key, "history:"))
		}
	} else if input.Submitted() {
		submit(query)
	}
}
