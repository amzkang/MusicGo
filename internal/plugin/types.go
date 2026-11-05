package plugin

import (
	"encoding/json"
	"fmt"
)

// MusicItem represents a music track in MusicFree
type MusicItem struct {
	ID       string                 `json:"id"`
	Platform string                 `json:"platform,omitempty"`
	Title    string                 `json:"title"`
	Artist   string                 `json:"artist,omitempty"`
	Album    string                 `json:"album,omitempty"`
	Duration float64                `json:"duration,omitempty"` // in seconds
	Artwork  string                 `json:"artwork,omitempty"`  // cover image URL
	URL      string                 `json:"url,omitempty"`
	Lyric    string                 `json:"lyric,omitempty"`
	RawLrc   string                 `json:"rawLrc,omitempty"`
	LxLrc    string                 `json:"lxLrc,omitempty"`
	Extra    map[string]interface{} `json:"extra,omitempty"`
	Raw      map[string]interface{} `json:"-"`
}

func (m *MusicItem) UnmarshalJSON(b []byte) error {
	var raw map[string]interface{}
	if err := json.Unmarshal(b, &raw); err != nil {
		return err
	}
	m.Raw = raw

	// ID can be string or float64/int/json.Number
	if idVal, ok := raw["id"]; ok && idVal != nil {
		switch v := idVal.(type) {
		case float64:
			m.ID = fmt.Sprintf("%.0f", v)
		case int:
			m.ID = fmt.Sprintf("%d", v)
		case int64:
			m.ID = fmt.Sprintf("%d", v)
		default:
			m.ID = fmt.Sprintf("%v", v)
		}
	}
	if platVal, ok := raw["platform"]; ok && platVal != nil {
		m.Platform = fmt.Sprintf("%v", platVal)
	}
	if titleVal, ok := raw["title"]; ok && titleVal != nil {
		m.Title = fmt.Sprintf("%v", titleVal)
	}
	if artistVal, ok := raw["artist"]; ok && artistVal != nil {
		m.Artist = fmt.Sprintf("%v", artistVal)
	}
	if albumVal, ok := raw["album"]; ok && albumVal != nil {
		m.Album = fmt.Sprintf("%v", albumVal)
	}
	if durVal, ok := raw["duration"]; ok && durVal != nil {
		switch v := durVal.(type) {
		case float64:
			m.Duration = v
		case int:
			m.Duration = float64(v)
		case string:
			var f float64
			if _, err := fmt.Sscanf(v, "%f", &f); err == nil {
				m.Duration = f
			}
		}
	}
	if artVal, ok := raw["artwork"]; ok && artVal != nil {
		m.Artwork = fmt.Sprintf("%v", artVal)
	}
	if urlVal, ok := raw["url"]; ok && urlVal != nil {
		m.URL = fmt.Sprintf("%v", urlVal)
	}
	if lrcVal, ok := raw["lrc"]; ok && lrcVal != nil {
		m.Lyric = fmt.Sprintf("%v", lrcVal)
	}
	if rawLrcVal, ok := raw["rawLrc"]; ok && rawLrcVal != nil {
		m.RawLrc = fmt.Sprintf("%v", rawLrcVal)
	}
	if lxLrcVal, ok := raw["lxLrc"]; ok && lxLrcVal != nil {
		m.LxLrc = fmt.Sprintf("%v", lxLrcVal)
	}
	if m.Extra == nil {
		m.Extra = make(map[string]interface{})
	}
	for k, v := range raw {
		m.Extra[k] = v
	}
	return nil
}

func (m MusicItem) MarshalJSON() ([]byte, error) {
	out := make(map[string]interface{})
	if m.Raw != nil {
		for k, v := range m.Raw {
			out[k] = v
		}
	}
	if m.Extra != nil {
		for k, v := range m.Extra {
			out[k] = v
		}
	}
	if m.ID != "" {
		// Keep the plugin's original ID type when the host identity is unchanged.
		// Some plugins distinguish a numeric ID from its string representation.
		original, exists := out["id"]
		if !exists || identityID(original) != m.ID {
			out["id"] = m.ID
		}
	}
	if m.Platform != "" {
		out["platform"] = m.Platform
	}
	if m.Title != "" {
		out["title"] = m.Title
	}
	if m.Artist != "" {
		out["artist"] = m.Artist
	}
	if m.Album != "" {
		out["album"] = m.Album
	}
	if m.Duration > 0 {
		out["duration"] = m.Duration
	}
	if m.Artwork != "" {
		out["artwork"] = m.Artwork
	}
	if m.URL != "" {
		out["url"] = m.URL
	}
	if m.RawLrc != "" {
		out["rawLrc"] = m.RawLrc
	}
	if m.LxLrc != "" {
		out["lxLrc"] = m.LxLrc
	}
	if m.Lyric != "" {
		out["lrc"] = m.Lyric
	}
	return json.Marshal(out)
}

func identityID(value interface{}) string {
	switch value := value.(type) {
	case float64:
		return fmt.Sprintf("%.0f", value)
	default:
		return fmt.Sprint(value)
	}
}

// MediaSource represents the resolved playable media source
type MediaSource struct {
	URL      string            `json:"url"`
	Headers  map[string]string `json:"headers,omitempty"`
	UserVars map[string]string `json:"userVariables,omitempty"`
	Quality  string            `json:"quality,omitempty"`
}

// LyricResult represents the lyrics returned by plugin
type LyricResult struct {
	RawLrc      string `json:"rawLrc,omitempty"`
	Translation string `json:"translation,omitempty"`
	LxLrc       string `json:"lxLrc,omitempty"`
}

// SearchResult represents search return data
type SearchResult struct {
	IsEnd bool        `json:"isEnd"`
	Data  []MusicItem `json:"data"`
}

// PluginMeta represents plugin metadata
type PluginMeta struct {
	Platform            string         `json:"platform"`
	Author              string         `json:"author,omitempty"`
	Version             string         `json:"version,omitempty"`
	AppVersion          string         `json:"appVersion,omitempty"`
	SrcURL              string         `json:"srcUrl,omitempty"`
	Description         string         `json:"description,omitempty"`
	SupportedSearchType []string       `json:"supportedSearchType,omitempty"`
	PrimaryKey          []string       `json:"primaryKey,omitempty"`
	Hints               map[string]any `json:"hints,omitempty"`
	UserVariables       []UserVariable `json:"userVariables,omitempty"`
}

// UserVariable represents user configurable variables in plugin
type UserVariable struct {
	Key     string `json:"key"`
	Name    string `json:"name"`
	Hint    string `json:"hint,omitempty"`
	Default string `json:"default,omitempty"`
}

// LoginQR is what a plugin's getLoginQR hook returns: the text to render as
// a QR code, plus the opaque state the host passes back to pollLoginQR.
type LoginQR struct {
	QRText  string `json:"qrText"`
	State   string `json:"state,omitempty"`
	Message string `json:"message,omitempty"`
}

// LoginPollResult is what a plugin's pollLoginQR hook returns. Status is one
// of "waiting", "scanned", "confirmed" or "expired"; on "confirmed" the
// UserVariables are what the host persists for the plugin.
type LoginPollResult struct {
	Status        string            `json:"status"`
	Message       string            `json:"message,omitempty"`
	UserVariables map[string]string `json:"userVariables,omitempty"`
}

// LoginStatus tells whether a plugin's stored user variables still mean a
// logged-in session.
type LoginStatus struct {
	LoggedIn bool   `json:"loggedIn"`
	Message  string `json:"message,omitempty"`
}

// UserProfile is what a plugin's getUserProfile hook returns: the account's
// public identity, for the header chip and the account view.
type UserProfile struct {
	Nickname string `json:"nickname"`
	Avatar   string `json:"avatar,omitempty"`
	VipLabel string `json:"vipLabel,omitempty"`
}

// UserSheet is one of the account's own playlists, as the plugin's
// getUserSheets hook lists them. IsFavorite marks the default liked-music
// sheet the host pins first.
type UserSheet struct {
	ID         string `json:"id"`
	Title      string `json:"title"`
	CoverImg   string `json:"coverImg,omitempty"`
	Count      int    `json:"count,omitempty"`
	IsFavorite bool   `json:"isFavorite,omitempty"`
}

// SheetSongs is one page of a sheet's songs, as the plugin's getSheetSongs
// hook returns them.
type SheetSongs struct {
	IsEnd bool        `json:"isEnd"`
	Data  []MusicItem `json:"data"`
}

// TopListItem is one leaderboard, as the plugins of MusicFree describe it.
type TopListItem struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	Description string `json:"description,omitempty"`
	CoverImg    string `json:"coverImg,omitempty"`
	// Platform tells which plugin the leaderboard comes from.
	Platform string `json:"platform,omitempty"`
	// Extra keeps the fields the host does not know, so that the plugin
	// gets the item back as it gave it.
	Extra map[string]interface{} `json:"extra,omitempty"`
}

// TopListGroup is a group of leaderboards, as the plugins group them.
type TopListGroup struct {
	Title string        `json:"title"`
	Data  []TopListItem `json:"data"`
}

// TopListDetail is a leaderboard with the songs it holds.
type TopListDetail struct {
	TopListItem
	MusicList []MusicItem `json:"musicList"`
}
