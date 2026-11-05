package ui

import "strings"

func (app *App) submitSearch(query string) {
	query = strings.TrimSpace(query)
	if query == "" {
		return
	}
	app.ConfigMgr.RememberSearch(query)
	app.HeaderState.SearchQuery = query
	app.SelectedNav = "search"
	app.SearchState.PerformSearch(app.PluginMgr, query, 0, app.requestRedraw)
}
