package plugin

import (
	"encoding/json"
	"net/url"
	"path/filepath"
	"strings"
)

// SongKey is a host identity. It never replaces the ID passed to plugins.
type SongKey string

func (m MusicItem) Key() SongKey {
	var parts []string
	if m.Platform == "local" && m.URL != "" {
		path := m.URL
		if u, err := url.Parse(path); err == nil && u.Scheme == "file" {
			path = u.Path
		}
		if abs, err := filepath.Abs(path); err == nil {
			path = abs
		}
		parts = []string{"local", filepath.Clean(path)}
	} else if m.ID != "" {
		parts = []string{"id", m.Platform, m.ID}
	} else {
		parts = []string{"meta", m.Platform, strings.TrimSpace(m.Title), strings.TrimSpace(m.Artist), strings.TrimSpace(m.Album)}
	}
	data, _ := json.Marshal(parts)
	return SongKey(data)
}

// Clone detaches maps and slices returned by plugins from shared snapshots.
func (m MusicItem) Clone() MusicItem {
	m.Raw = cloneFields(m.Raw)
	m.Extra = cloneFields(m.Extra)
	return m
}
func cloneFields(m map[string]interface{}) map[string]interface{} {
	if m == nil {
		return nil
	}
	out := make(map[string]interface{}, len(m))
	for k, v := range m {
		out[k] = cloneValue(v)
	}
	return out
}
func cloneValue(v interface{}) interface{} {
	switch x := v.(type) {
	case map[string]interface{}:
		return cloneFields(x)
	case []interface{}:
		out := make([]interface{}, len(x))
		for i, v := range x {
			out[i] = cloneValue(v)
		}
		return out
	case []byte:
		return append([]byte(nil), x...)
	default:
		return v
	}
}
