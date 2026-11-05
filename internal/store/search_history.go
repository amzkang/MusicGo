package store

import "strings"

const maxSearchHistory = 20

func (cm *ConfigManager) RememberSearch(query string) {
	query = strings.TrimSpace(query)
	if query == "" {
		return
	}
	_ = cm.UpdateAsync(func(c *AppConfig) {
		c.SearchHistory = normalizeSearchHistory(append([]string{query}, c.SearchHistory...))
	})
}
func normalizeSearchHistory(terms []string) []string {
	var history []string
	for _, term := range terms {
		term = strings.TrimSpace(term)
		if term == "" {
			continue
		}
		duplicate := false
		for _, existing := range history {
			if existing == term {
				duplicate = true
				break
			}
		}
		if !duplicate {
			history = append(history, term)
		}
		if len(history) == maxSearchHistory {
			break
		}
	}
	return history
}

func (cm *ConfigManager) ClearSearchHistory() {
	_ = cm.UpdateAsync(func(c *AppConfig) { c.SearchHistory = nil })
}
func (cm *ConfigManager) SearchHistorySnapshot() []string {
	cm.mu.RLock()
	defer cm.mu.RUnlock()
	return append([]string(nil), cm.config.SearchHistory...)
}
