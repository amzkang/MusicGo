package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	_ "modernc.org/sqlite"
	"musicgo/internal/plugin"
)

// Playlist represents a music playlist (sheet)
type Playlist struct {
	ID        string             `json:"id"`
	Title     string             `json:"title"`
	Cover     string             `json:"cover,omitempty"`
	CreatedAt int64              `json:"createdAt"`
	UpdatedAt int64              `json:"updatedAt"`
	Songs     []plugin.MusicItem `json:"songs,omitempty"`
	SongCount int                `json:"songCount"`
}

// BackupData represents MusicFree exported backup structure

// DownloadStatus represents the state of a download task
type DownloadStatus int

const (
	DownloadStatusQueued DownloadStatus = iota
	DownloadStatusDownloading
	DownloadStatusPaused
	DownloadStatusCompleted
	DownloadStatusFailed
	DownloadStatusCanceled
)

func (s DownloadStatus) String() string {
	switch s {
	case DownloadStatusQueued:
		return "排队中"
	case DownloadStatusDownloading:
		return "下载中"
	case DownloadStatusPaused:
		return "已暂停"
	case DownloadStatusCompleted:
		return "已完成"
	case DownloadStatusFailed:
		return "下载失败"
	case DownloadStatusCanceled:
		return "已取消"
	default:
		return "未知状态"
	}
}

// DownloadRecord represents a persistent download entry in SQLite
type DownloadRecord struct {
	ID              string           `json:"id"`
	SongID          string           `json:"songId"`
	Platform        string           `json:"platform"`
	Title           string           `json:"title"`
	Artist          string           `json:"artist"`
	Album           string           `json:"album"`
	Duration        float64          `json:"duration"`
	Artwork         string           `json:"artwork"`
	SongData        plugin.MusicItem `json:"songData"`
	Status          DownloadStatus   `json:"status"`
	Progress        float64          `json:"progress"` // 0.0 to 1.0
	DownloadedBytes int64            `json:"downloadedBytes"`
	TotalBytes      int64            `json:"totalBytes"`
	FilePath        string           `json:"filePath"`
	LyricPath       string           `json:"lyricPath"`
	Quality         string           `json:"quality"`
	ErrorMsg        string           `json:"errorMsg"`
	CreatedAt       int64            `json:"createdAt"`
	UpdatedAt       int64            `json:"updatedAt"`
}

type BackupData struct {
	Version   string             `json:"version,omitempty"`
	Sheets    []Playlist         `json:"sheets,omitempty"`
	Favorites []plugin.MusicItem `json:"favorites,omitempty"`
	History   []plugin.MusicItem `json:"history,omitempty"`
	// PluginStorage maps a plugin's platform name to its localStorage
	// entries, so login states survive a backup round-trip.
	PluginStorage map[string]map[string]string `json:"pluginStorage,omitempty"`
}

// maxHistory is how many plays the history keeps.
const maxHistory = 200

// Store handles persistent SQLite storage.
//
// The data a view needs while it draws - the favourites, the history and the
// playlists - is kept in memory and refreshed when it changes, so drawing
// the interface never waits for the disk.
type Store struct {
	actionMu       sync.Mutex
	actions        []func()
	actionWake     chan struct{}
	actionStop     chan struct{}
	actionDone     chan struct{}
	actionsClosed  bool
	lastWriteError error

	mu             sync.RWMutex
	writeMu        sync.Mutex
	db             *sql.DB
	validDownloads map[string]bool
	stop           chan struct{}
	done           chan struct{}
	closeOnce      sync.Once

	favorites   []plugin.MusicItem
	favSet      map[string]bool
	history     []plugin.MusicItem
	playlists   []Playlist
	downloads   []DownloadRecord
	downloadMap map[string]*DownloadRecord

	// pluginStorage is every plugin's localStorage, kept in memory so plugin
	// method calls never wait for the disk. Guarded by mu.
	pluginStorage map[string]map[string]string
}

// NewStore initializes the SQLite database at dbPath (or in-memory if ":memory:")
func NewStore(dbPath string) (*Store, error) {
	if dbPath != ":memory:" && dbPath != "" {
		dir := filepath.Dir(dbPath)
		_ = os.MkdirAll(dir, 0755)
	} else if dbPath == "" {
		dbPath = ":memory:"
	}

	// Encode ordinary filenames as SQLite file URIs so '?' and '#' remain
	// filename characters rather than becoming URI query or fragment syntax.
	dsn := dbPath
	if dbPath != ":memory:" && !strings.HasPrefix(dbPath, "file:") {
		abs, err := filepath.Abs(dbPath)
		if err != nil {
			return nil, fmt.Errorf("resolve sqlite path: %w", err)
		}
		dsn = (&url.URL{Scheme: "file", Path: filepath.ToSlash(abs)}).String()
	}
	separator := "?"
	if strings.Contains(dsn, "?") {
		separator = "&"
	}
	// Connection pragmas also apply if database/sql replaces its connection.
	// _txlock=immediate makes every transaction take the write lock up front
	// instead of upgrading a read lock on first write, which fails with
	// SQLITE_BUSY when another connection holds the write lock. temp_store
	// keeps sort/hash spills in RAM; journal_size_limit bounds WAL growth.
	dsn += separator + "_txlock=immediate&_pragma=busy_timeout%285000%29&_pragma=journal_mode%28WAL%29&_pragma=synchronous%28NORMAL%29&_pragma=temp_store%28MEMORY%29&_pragma=journal_size_limit%284194304%29"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("failed to open sqlite db: %w", err)
	}

	// Set connection limits for SQLite concurrency
	db.SetMaxOpenConns(1)
	var filename, journalMode string
	if err = db.QueryRow("SELECT file FROM pragma_database_list WHERE name = 'main'").Scan(&filename); err == nil {
		err = db.QueryRow("PRAGMA journal_mode").Scan(&journalMode)
	}
	if err == nil && filename != "" && !strings.EqualFold(journalMode, "wal") {
		err = fmt.Errorf("sqlite journal mode is %q, expected WAL", journalMode)
	}
	if err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("configure sqlite: %w", err)
	}

	s := &Store{db: db, favSet: make(map[string]bool), downloadMap: make(map[string]*DownloadRecord), validDownloads: make(map[string]bool), stop: make(chan struct{}), done: make(chan struct{})}
	if err := s.initSchema(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("failed to init schema: %w", err)
	}

	if err := s.migrateIdentity(dbPath); err != nil {
		_ = db.Close()
		return nil, err
	}
	if err := s.reloadCache(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("failed to load data: %w", err)
	}

	s.actionWake = make(chan struct{}, 1)
	s.actionStop = make(chan struct{})
	s.actionDone = make(chan struct{})
	go s.actionLoop()
	go s.watchDownloadFiles()
	return s, nil
}

func (s *Store) initSchema() error {
	schema := `
	CREATE TABLE IF NOT EXISTS playlists (
		id TEXT PRIMARY KEY,
		title TEXT NOT NULL,
		cover TEXT,
		created_at INTEGER,
		updated_at INTEGER
	);

	CREATE TABLE IF NOT EXISTS playlist_songs (
		playlist_id TEXT NOT NULL,
		song_id TEXT NOT NULL,
		song_data TEXT NOT NULL,
		sort_order INTEGER NOT NULL,
		PRIMARY KEY (playlist_id, song_id)
	);

	CREATE TABLE IF NOT EXISTS favorites (
		song_id TEXT PRIMARY KEY,
		song_data TEXT NOT NULL,
		created_at INTEGER
	);

	CREATE TABLE IF NOT EXISTS history (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		song_id TEXT NOT NULL,
		song_data TEXT NOT NULL,
		played_at INTEGER
	);

	CREATE TABLE IF NOT EXISTS settings (
		key TEXT PRIMARY KEY,
		value TEXT NOT NULL
	);

	CREATE TABLE IF NOT EXISTS plugin_storage (
		plugin_id TEXT NOT NULL,
		key TEXT NOT NULL,
		value TEXT,
		PRIMARY KEY (plugin_id, key)
	);

	CREATE TABLE IF NOT EXISTS downloads (
		id TEXT PRIMARY KEY,
		song_id TEXT NOT NULL,
		platform TEXT NOT NULL,
		title TEXT NOT NULL,
		artist TEXT,
		album TEXT,
		duration REAL,
		artwork TEXT,
		song_data TEXT NOT NULL,
		status INTEGER NOT NULL,
		progress REAL NOT NULL DEFAULT 0,
		downloaded_bytes INTEGER NOT NULL DEFAULT 0,
		total_bytes INTEGER NOT NULL DEFAULT 0,
		file_path TEXT NOT NULL DEFAULT '',
		lyric_path TEXT NOT NULL DEFAULT '',
		quality TEXT NOT NULL DEFAULT 'standard',
		error_msg TEXT NOT NULL DEFAULT '',
		created_at INTEGER NOT NULL,
		updated_at INTEGER NOT NULL
	);
	CREATE INDEX IF NOT EXISTS idx_downloads_song ON downloads(platform, song_id);
	CREATE INDEX IF NOT EXISTS idx_downloads_status ON downloads(status);
	`
	_, err := s.db.Exec(schema)
	return err
}

// songKey is how a song is told apart from another: by its platform and its
// id, or by its title and artist when it has no id.
func songKey(song plugin.MusicItem) string { return string(song.Key()) }

// reloadCache reads everything the interface draws from the database.
func (s *Store) reloadCache() error {

	favs, err := s.loadFavoritesLocked()
	if err != nil {
		return err
	}
	hist, err := s.loadHistoryLocked()
	if err != nil {
		return err
	}
	pls, err := s.loadPlaylistsLocked()
	if err != nil {
		return err
	}

	dls, err := s.loadDownloadsLocked()
	if err != nil {
		return err
	}
	if err := s.loadPluginStorageLocked(); err != nil {
		return err
	}
	valid := downloadValidity(dls)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.favorites = favs
	s.favSet = make(map[string]bool, len(favs))
	for _, song := range favs {
		s.favSet[songKey(song)] = true
	}
	s.history = hist
	s.playlists = pls
	s.downloads = dls
	s.validDownloads = valid
	s.reindexDownloadsLocked()

	return nil
}

func (s *Store) loadFavoritesLocked() ([]plugin.MusicItem, error) {
	rows, err := s.db.Query("SELECT song_data FROM favorites ORDER BY created_at DESC, rowid DESC")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var songs []plugin.MusicItem
	seen := make(map[string]bool)
	for rows.Next() {
		var songJSON string
		if err := rows.Scan(&songJSON); err == nil {
			var song plugin.MusicItem
			if err := json.Unmarshal([]byte(songJSON), &song); err == nil {
				key := songKey(song)
				if !seen[key] {
					seen[key] = true
					songs = append(songs, song)
				}
			}
		}
	}
	return songs, rows.Err()
}

func (s *Store) loadHistoryLocked() ([]plugin.MusicItem, error) {
	rows, err := s.db.Query("SELECT song_data FROM history ORDER BY id DESC LIMIT ?", maxHistory)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var songs []plugin.MusicItem
	seen := make(map[string]bool)
	for rows.Next() {
		var songJSON string
		if err := rows.Scan(&songJSON); err == nil {
			var song plugin.MusicItem
			if err := json.Unmarshal([]byte(songJSON), &song); err == nil {
				key := songKey(song)
				if !seen[key] {
					seen[key] = true
					songs = append(songs, song)
				}
			}
		}
	}
	return songs, rows.Err()
}

func (s *Store) loadPlaylistsLocked() ([]Playlist, error) {
	rows, err := s.db.Query(`
		SELECT p.id, p.title, p.cover, p.created_at, p.updated_at
		FROM playlists p
		ORDER BY p.created_at ASC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var res []Playlist
	for rows.Next() {
		var pl Playlist
		var cover sql.NullString
		if err := rows.Scan(&pl.ID, &pl.Title, &cover, &pl.CreatedAt, &pl.UpdatedAt); err != nil {
			return nil, err
		}
		if cover.Valid {
			pl.Cover = cover.String
		}
		res = append(res, pl)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	for i := range res {
		songs, err := s.loadPlaylistSongsLocked(res[i].ID)
		if err != nil {
			return nil, err
		}
		res[i].Songs = songs
		res[i].SongCount = len(songs)
	}
	return res, nil
}

func (s *Store) loadPlaylistSongsLocked(playlistID string) ([]plugin.MusicItem, error) {
	rows, err := s.db.Query("SELECT song_data FROM playlist_songs WHERE playlist_id = ? ORDER BY sort_order ASC", playlistID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var songs []plugin.MusicItem
	for rows.Next() {
		var songJSON string
		if err := rows.Scan(&songJSON); err == nil {
			var song plugin.MusicItem
			if err := json.Unmarshal([]byte(songJSON), &song); err == nil {
				songs = append(songs, song)
			}
		}
	}
	return songs, rows.Err()
}

// Close closes the database connection
func (s *Store) Close() error {
	s.closeOnce.Do(func() {
		s.actionMu.Lock()
		s.actionsClosed = true
		close(s.actionStop)
		s.actionMu.Unlock()
		<-s.actionDone
		close(s.stop)
		<-s.done
	})
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	return s.db.Close()
}

// ---------------- Playlists ----------------

// CreatePlaylist creates a new playlist
func (s *Store) CreatePlaylist(title, cover string) (*Playlist, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	now := time.Now().Unix()
	id := fmt.Sprintf("pl_%d", time.Now().UnixNano())

	_, err := s.db.Exec("INSERT INTO playlists (id, title, cover, created_at, updated_at) VALUES (?, ?, ?, ?, ?)",
		id, title, cover, now, now)
	if err != nil {
		return nil, err
	}

	pl := Playlist{
		ID:        id,
		Title:     title,
		Cover:     cover,
		CreatedAt: now,
		UpdatedAt: now,
		Songs:     []plugin.MusicItem{},
		SongCount: 0,
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.playlists = append(s.playlists, pl)

	out := pl
	return &out, nil
}

// ListPlaylists returns all playlists with their song counts
func (s *Store) ListPlaylists() ([]Playlist, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	res := make([]Playlist, len(s.playlists))
	for i, pl := range s.playlists {
		res[i] = clonePlaylist(pl)
	}
	return res, nil
}

// GetPlaylist returns a playlist along with its songs
func (s *Store) GetPlaylist(id string) (*Playlist, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	for i := range s.playlists {
		if s.playlists[i].ID == id {
			pl := s.playlists[i]
			pl.Songs = make([]plugin.MusicItem, len(s.playlists[i].Songs))
			for j, song := range s.playlists[i].Songs {
				pl.Songs[j] = song.Clone()
			}
			return &pl, nil
		}
	}
	return nil, fmt.Errorf("playlist %q not found", id)
}

// AddSongToPlaylist appends a song to a playlist
func (s *Store) AddSongToPlaylist(playlistID string, song plugin.MusicItem) error {
	song = song.Clone()
	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	data, err := json.Marshal(song)
	if err != nil {
		return err
	}

	// Get current max sort_order
	var maxOrder sql.NullInt64
	_ = s.db.QueryRow("SELECT MAX(sort_order) FROM playlist_songs WHERE playlist_id = ?", playlistID).Scan(&maxOrder)
	newOrder := int64(0)
	if maxOrder.Valid {
		newOrder = maxOrder.Int64 + 1
	}

	_, err = s.db.Exec(`
		INSERT INTO playlist_songs (playlist_id, song_key, song_id, song_data, sort_order)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(playlist_id, song_key) DO UPDATE SET song_data = excluded.song_data
	`, playlistID, songKey(song), song.ID, string(data), newOrder)
	if err != nil {
		return err
	}

	now := time.Now().Unix()
	if _, err = s.db.Exec("UPDATE playlists SET updated_at = ? WHERE id = ?", now, playlistID); err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.playlists {
		if s.playlists[i].ID != playlistID {
			continue
		}
		replaced := false
		for j := range s.playlists[i].Songs {
			if s.playlists[i].Songs[j].Key() == song.Key() {
				s.playlists[i].Songs[j] = song
				replaced = true
				break
			}
		}
		if !replaced {
			s.playlists[i].Songs = append(s.playlists[i].Songs, song)
		}
		s.playlists[i].SongCount = len(s.playlists[i].Songs)
		s.playlists[i].UpdatedAt = now
		break
	}
	return nil
}

// RemoveSongFromPlaylist removes a song from a playlist
func (s *Store) RemoveSongFromPlaylist(playlistID string, key plugin.SongKey) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	if _, err := s.db.Exec("DELETE FROM playlist_songs WHERE playlist_id = ? AND song_key = ?", playlistID, string(key)); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.playlists {
		if s.playlists[i].ID != playlistID {
			continue
		}
		songs := s.playlists[i].Songs[:0]
		for _, song := range s.playlists[i].Songs {
			if song.Key() != key {
				songs = append(songs, song)
			}
		}
		s.playlists[i].Songs = songs
		s.playlists[i].SongCount = len(songs)
		break
	}
	return nil
}

// DeletePlaylist deletes a playlist and its song associations
func (s *Store) DeletePlaylist(id string) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	if _, err := s.db.Exec("DELETE FROM playlist_songs WHERE playlist_id = ?", id); err != nil {
		return err
	}
	if _, err := s.db.Exec("DELETE FROM playlists WHERE id = ?", id); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.playlists {
		if s.playlists[i].ID == id {
			s.playlists = append(s.playlists[:i], s.playlists[i+1:]...)
			break
		}
	}
	return nil
}

// ---------------- Favorites ----------------

// AddFavorite adds a song to favorites
func (s *Store) AddFavorite(song plugin.MusicItem) error {
	song = song.Clone()
	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	data, err := json.Marshal(song)
	if err != nil {
		return err
	}

	_, err = s.db.Exec(`
		INSERT INTO favorites (song_key, song_id, song_data, created_at)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(song_key) DO UPDATE SET song_data = excluded.song_data
	`, songKey(song), song.ID, string(data), time.Now().Unix())
	if err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	key := songKey(song)
	s.favSet[key] = true
	for i := range s.favorites {
		if songKey(s.favorites[i]) == key {
			s.favorites = append(s.favorites[:i], s.favorites[i+1:]...)
			break
		}
	}
	s.favorites = append([]plugin.MusicItem{song}, s.favorites...)
	return nil
}

// RemoveFavorite removes a song from favorites
func (s *Store) RemoveFavorite(key plugin.SongKey) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	if _, err := s.db.Exec("DELETE FROM favorites WHERE song_key = ?", string(key)); err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.favorites {
		if s.favorites[i].Key() == key {
			s.favorites = append(s.favorites[:i], s.favorites[i+1:]...)
			break
		}
	}
	delete(s.favSet, string(key))
	return nil
}

// IsFavorite checks whether a song is in favorites
func (s *Store) IsFavorite(key plugin.SongKey) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.favSet[string(key)]
}

// ListFavorites returns all favorite songs
func (s *Store) ListFavorites() ([]plugin.MusicItem, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	res := make([]plugin.MusicItem, len(s.favorites))
	for i, song := range s.favorites {
		res[i] = song.Clone()
	}
	return res, nil
}

// ---------------- History ----------------

// AddHistory records a song play in history (keeps up to 200 items, deduplicating previous occurrence)
func (s *Store) AddHistory(song plugin.MusicItem) error {
	song = song.Clone()
	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	data, err := json.Marshal(song)
	if err != nil {
		return err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.Exec("DELETE FROM history WHERE song_key = ?", songKey(song)); err != nil {
		return err
	}

	if _, err = tx.Exec("INSERT INTO history (song_key, song_id, song_data, played_at) VALUES (?, ?, ?, ?)",
		songKey(song), song.ID, string(data), time.Now().Unix()); err != nil {
		return err
	}

	// Trim older history beyond the limit
	if _, err = tx.Exec(`
		DELETE FROM history WHERE id NOT IN (
			SELECT id FROM history ORDER BY id DESC LIMIT ?
		)
	`, maxHistory); err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	key := songKey(song)
	for i := range s.history {
		if songKey(s.history[i]) == key {
			s.history = append(s.history[:i], s.history[i+1:]...)
			break
		}
	}
	s.history = append([]plugin.MusicItem{song}, s.history...)
	if len(s.history) > maxHistory {
		s.history = s.history[:maxHistory]
	}
	return nil
}

// escapeLike escapes the wildcards of a LIKE pattern, so a title that holds
// one is matched literally.
func escapeLike(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return r.Replace(s)
}

// ListHistory returns playback history
func (s *Store) ListHistory() ([]plugin.MusicItem, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	res := make([]plugin.MusicItem, len(s.history))
	for i, song := range s.history {
		res[i] = song.Clone()
	}
	return res, nil
}

// ClearHistory deletes all history records
func (s *Store) ClearHistory() error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	if _, err := s.db.Exec("DELETE FROM history"); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.history = nil
	return nil
}

// ---------------- MusicFree Backup Import / Export ----------------

// ExportBackup exports playlists, favorites, and history as JSON
func (s *Store) ExportBackup() (*BackupData, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	pls := make([]Playlist, len(s.playlists))
	for i, pl := range s.playlists {
		pls[i] = clonePlaylist(pl)
	}
	favs := make([]plugin.MusicItem, len(s.favorites))
	for i, song := range s.favorites {
		favs[i] = song.Clone()
	}
	hist := make([]plugin.MusicItem, len(s.history))
	for i, song := range s.history {
		hist[i] = song.Clone()
	}

	return &BackupData{
		Version:       "0.1.0",
		Sheets:        pls,
		Favorites:     favs,
		History:       hist,
		PluginStorage: s.pluginStorageAllLocked(),
	}, nil
}

// ImportBackup imports MusicFree backup JSON data into the store
func (s *Store) ImportBackup(data *BackupData) error {
	if data == nil {
		return nil
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	now := time.Now().Unix()
	for n, sheet := range data.Sheets {
		id := fmt.Sprintf("pl_%d_%d", time.Now().UnixNano(), n)
		if _, err = tx.Exec("INSERT INTO playlists(id,title,cover,created_at,updated_at) VALUES(?,?,?,?,?)", id, sheet.Title, sheet.Cover, now, now); err != nil {
			return err
		}
		for i, song := range sheet.Songs {
			raw, e := json.Marshal(song)
			if e != nil {
				return e
			}
			if _, err = tx.Exec("INSERT INTO playlist_songs(playlist_id,song_key,song_id,song_data,sort_order) VALUES(?,?,?,?,?) ON CONFLICT(playlist_id,song_key) DO UPDATE SET song_data=excluded.song_data", id, songKey(song), song.ID, string(raw), i); err != nil {
				return err
			}
		}
	}
	for _, song := range data.Favorites {
		raw, e := json.Marshal(song)
		if e != nil {
			return e
		}
		if _, err = tx.Exec("INSERT INTO favorites(song_key,song_id,song_data,created_at) VALUES(?,?,?,?) ON CONFLICT(song_key) DO UPDATE SET song_data=excluded.song_data", songKey(song), song.ID, string(raw), now); err != nil {
			return err
		}
	}
	for i := len(data.History) - 1; i >= 0; i-- {
		song := data.History[i]
		raw, e := json.Marshal(song)
		if e != nil {
			return e
		}
		if _, err = tx.Exec("DELETE FROM history WHERE song_key=?", songKey(song)); err != nil {
			return err
		}
		if _, err = tx.Exec("INSERT INTO history(song_key,song_id,song_data,played_at) VALUES(?,?,?,?)", songKey(song), song.ID, string(raw), now); err != nil {
			return err
		}
	}
	if _, err = tx.Exec("DELETE FROM history WHERE id NOT IN (SELECT id FROM history ORDER BY id DESC LIMIT ?)", maxHistory); err != nil {
		return err
	}
	for pluginID, entries := range data.PluginStorage {
		for key, value := range entries {
			if _, err = tx.Exec("INSERT INTO plugin_storage(plugin_id,key,value) VALUES(?,?,?) ON CONFLICT(plugin_id,key) DO UPDATE SET value=excluded.value", pluginID, key, value); err != nil {
				return err
			}
		}
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	return s.reloadCache()
}

func (s *Store) loadDownloadsLocked() ([]DownloadRecord, error) {
	rows, err := s.db.Query(`SELECT id, song_id, platform, title, artist, album, duration, artwork, song_data,
		status, progress, downloaded_bytes, total_bytes, file_path, lyric_path, quality, error_msg,
		created_at, updated_at FROM downloads ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var list []DownloadRecord
	for rows.Next() {
		var rec DownloadRecord
		var songJSON string
		var statusInt int
		if err := rows.Scan(
			&rec.ID, &rec.SongID, &rec.Platform, &rec.Title, &rec.Artist, &rec.Album, &rec.Duration, &rec.Artwork,
			&songJSON, &statusInt, &rec.Progress, &rec.DownloadedBytes, &rec.TotalBytes, &rec.FilePath,
			&rec.LyricPath, &rec.Quality, &rec.ErrorMsg, &rec.CreatedAt, &rec.UpdatedAt,
		); err != nil {
			// 静默丢行会让用户看不见这条下载而重新添加，产生第二份文件。
			log.Printf("downloads: skip unreadable row: %v", err)
			continue
		}
		rec.Status = DownloadStatus(statusInt)
		_ = json.Unmarshal([]byte(songJSON), &rec.SongData)
		list = append(list, rec)
	}
	return list, rows.Err()
}

// SaveDownload inserts or updates a download record in database and in-memory cache
func (s *Store) SaveDownload(rec DownloadRecord) error {
	rec.SongData = rec.SongData.Clone()
	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	songJSON, err := json.Marshal(rec.SongData)
	if err != nil {
		return err
	}
	now := time.Now().UnixMilli()
	if rec.CreatedAt == 0 {
		rec.CreatedAt = now
	}
	rec.UpdatedAt = now

	query := `INSERT INTO downloads (
		id, song_id, platform, title, artist, album, duration, artwork, song_data,
		status, progress, downloaded_bytes, total_bytes, file_path, lyric_path, quality, error_msg,
		created_at, updated_at
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	ON CONFLICT(id) DO UPDATE SET
		status = excluded.status,
		progress = excluded.progress,
		downloaded_bytes = excluded.downloaded_bytes,
		total_bytes = excluded.total_bytes,
		file_path = excluded.file_path,
		lyric_path = excluded.lyric_path,
		quality = excluded.quality,
		error_msg = excluded.error_msg,
		updated_at = excluded.updated_at`

	_, err = s.db.Exec(query,
		rec.ID, rec.SongID, rec.Platform, rec.Title, rec.Artist, rec.Album, rec.Duration, rec.Artwork,
		string(songJSON), int(rec.Status), rec.Progress, rec.DownloadedBytes, rec.TotalBytes, rec.FilePath,
		rec.LyricPath, rec.Quality, rec.ErrorMsg, rec.CreatedAt, rec.UpdatedAt,
	)
	if err != nil {
		return err
	}

	s.mu.RLock()
	previous, exists := s.downloadMap[rec.ID]
	unchanged := exists && previous.ID == rec.ID && previous.Status == rec.Status && previous.FilePath == rec.FilePath
	valid := s.validDownloads[rec.ID]
	s.mu.RUnlock()
	if !unchanged {
		valid = downloadFileExists(rec)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.validDownloads[rec.ID] = valid
	// Update in-memory cache
	found := false
	for i := range s.downloads {
		if s.downloads[i].ID == rec.ID {
			old := s.downloads[i]
			s.downloads[i] = rec
			found = true
			if old.Platform != rec.Platform || old.SongID != rec.SongID {
				s.reindexDownloadsLocked()
			}
			break
		}
	}
	if !found {
		s.downloads = append([]DownloadRecord{rec}, s.downloads...)
		s.reindexDownloadsLocked()
	}
	return nil
}

// GetDownload retrieves a download record by task ID
func (s *Store) GetDownload(id string) (*DownloadRecord, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	rec, ok := s.downloadMap[id]
	if !ok || rec == nil {
		return nil, false
	}
	res := *rec
	res.SongData = res.SongData.Clone()
	return &res, true
}

// FindDownloadBySong retrieves a download record by platform and song ID
func (s *Store) FindDownloadBySong(platform, songID string) (*DownloadRecord, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	rec, ok := s.downloadMap[platform+":"+songID]
	if !ok || rec == nil {
		return nil, false
	}
	res := *rec
	res.SongData = res.SongData.Clone()
	return &res, true
}

// ListDownloads returns a copy of all download records
func (s *Store) ListDownloads() []DownloadRecord {
	s.mu.RLock()
	defer s.mu.RUnlock()
	res := make([]DownloadRecord, len(s.downloads))
	for i, r := range s.downloads {
		res[i] = r
		res[i].SongData = r.SongData.Clone()
	}
	return res
}

// DeleteDownload removes a download record from SQLite and in-memory cache
func (s *Store) DeleteDownload(id string) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	if _, err := s.db.Exec("DELETE FROM downloads WHERE id = ?", id); err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.validDownloads, id)
	for i := range s.downloads {
		if s.downloads[i].ID == id {
			s.downloads = append(s.downloads[:i], s.downloads[i+1:]...)
			break
		}
	}

	s.reindexDownloadsLocked()
	return nil
}

// DownloadFileAvailability returns cached validity when the persisted task matches.
func (s *Store) DownloadFileAvailability(id, path string) (bool, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	rec, ok := s.downloadMap[id]
	if !ok || rec.Status != DownloadStatusCompleted || rec.FilePath != path {
		return false, false
	}
	return s.validDownloads[id], true
}

// IsSongDownloaded reports whether the song is successfully downloaded and the file exists
func (s *Store) IsSongDownloaded(platform, songID string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	rec, ok := s.downloadMap[platform+":"+songID]
	if !ok || rec == nil || rec.Status != DownloadStatusCompleted || rec.FilePath == "" {
		return false
	}
	return s.validDownloads[rec.ID]
}

// GetDownloadedSongPath returns the file path of a downloaded song if it exists
func (s *Store) GetDownloadedSongPath(platform, songID string) string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	rec, ok := s.downloadMap[platform+":"+songID]
	if !ok || rec == nil || rec.Status != DownloadStatusCompleted || rec.FilePath == "" {
		return ""
	}
	if s.validDownloads[rec.ID] {
		return rec.FilePath
	}
	return ""
}
