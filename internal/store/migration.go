package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"musicgo/internal/plugin"
	"os"
	"time"
)

func (s *Store) migrateIdentity(path string) error {
	rows, err := s.db.Query("PRAGMA table_info(favorites)")
	if err != nil {
		return err
	}
	migrated := false
	for rows.Next() {
		var n, notnull, pk int
		var name, typ string
		var def interface{}
		if err = rows.Scan(&n, &name, &typ, &notnull, &def, &pk); err != nil {
			rows.Close()
			return err
		}
		if name == "song_key" {
			migrated = true
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil || migrated {
		return err
	}
	if path != ":memory:" {
		// VACUUM INTO produces a consistent snapshot, including WAL contents.
		var count int
		if err = s.db.QueryRow("SELECT (SELECT count(*) FROM favorites)+(SELECT count(*) FROM playlist_songs)+(SELECT count(*) FROM history)").Scan(&count); err != nil {
			return err
		}
		if count > 0 {
			backup := fmt.Sprintf("%s.before-identity-%d.bak", path, time.Now().UnixNano())
			if _, err = s.db.Exec("VACUUM INTO ?", backup); err != nil {
				return fmt.Errorf("backup before migration: %w", err)
			}
		}
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, q := range []string{
		"CREATE TABLE favorites_v2(song_key TEXT PRIMARY KEY,song_id TEXT NOT NULL,song_data TEXT NOT NULL,created_at INTEGER)",
		"CREATE TABLE playlist_songs_v2(playlist_id TEXT NOT NULL,song_key TEXT NOT NULL,song_id TEXT NOT NULL,song_data TEXT NOT NULL,sort_order INTEGER NOT NULL,PRIMARY KEY(playlist_id,song_key))",
		"ALTER TABLE history ADD COLUMN song_key TEXT NOT NULL DEFAULT ''",
	} {
		if _, err = tx.Exec(q); err != nil {
			return err
		}
	}
	type row struct {
		id            int64
		playlist, raw string
		at            sql.NullInt64
		song          plugin.MusicItem
	}
	for _, table := range []string{"favorites", "playlist_songs", "history"} {
		query := "SELECT rowid,'',song_data,created_at FROM favorites"
		if table == "playlist_songs" {
			query = "SELECT rowid,playlist_id,song_data,sort_order FROM playlist_songs"
		}
		if table == "history" {
			query = "SELECT id,'',song_data,played_at FROM history"
		}
		rows, e := tx.Query(query)
		if e != nil {
			return e
		}
		var batch []row
		for rows.Next() {
			var r row
			if e = rows.Scan(&r.id, &r.playlist, &r.raw, &r.at); e != nil {
				rows.Close()
				return e
			}
			if e = json.Unmarshal([]byte(r.raw), &r.song); e != nil {
				rows.Close()
				// 脏行只跳过不中断：迁移失败会让应用永远无法启动，而坏 JSON 行本就不可用。
				log.Printf("migrate %s row %d: skip corrupt song_data: %v", table, r.id, e)
				continue
			}
			batch = append(batch, r)
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			return e
		}
		for _, r := range batch {
			switch table {
			// 新表按 song_key 去重，旧数据里的重复身份行保留首条即可。
			case "favorites":
				_, err = tx.Exec("INSERT OR IGNORE INTO favorites_v2 VALUES(?,?,?,?)", songKey(r.song), r.song.ID, r.raw, r.at)
			case "playlist_songs":
				_, err = tx.Exec("INSERT OR IGNORE INTO playlist_songs_v2 VALUES(?,?,?,?,?)", r.playlist, songKey(r.song), r.song.ID, r.raw, r.at)
			case "history":
				_, err = tx.Exec("UPDATE history SET song_key=? WHERE id=?", songKey(r.song), r.id)
			}
			if err != nil {
				return err
			}
		}
	}
	for _, q := range []string{"DROP TABLE favorites", "ALTER TABLE favorites_v2 RENAME TO favorites", "DROP TABLE playlist_songs", "ALTER TABLE playlist_songs_v2 RENAME TO playlist_songs", "CREATE INDEX idx_history_song_key ON history(song_key)", "PRAGMA user_version=1"} {
		if _, err = tx.Exec(q); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func clonePlaylist(p Playlist) Playlist {
	p.Songs = append([]plugin.MusicItem(nil), p.Songs...)
	for i := range p.Songs {
		p.Songs[i] = p.Songs[i].Clone()
	}
	return p
}
func downloadFileExists(r DownloadRecord) bool {
	if r.Status != DownloadStatusCompleted || r.FilePath == "" {
		return false
	}
	info, err := os.Stat(r.FilePath)
	return err == nil && !info.IsDir() && info.Size() > 0
}
func downloadValidity(records []DownloadRecord) map[string]bool {
	out := make(map[string]bool, len(records))
	paths := make(map[string]bool)
	for _, r := range records {
		if r.Status != DownloadStatusCompleted || r.FilePath == "" {
			continue
		}
		valid, ok := paths[r.FilePath]
		if !ok {
			valid = downloadFileExists(r)
			paths[r.FilePath] = valid
		}
		out[r.ID] = valid
	}
	return out
}
func (s *Store) reindexDownloadsLocked() {
	s.downloadMap = make(map[string]*DownloadRecord, len(s.downloads)*2)
	for i := range s.downloads {
		r := &s.downloads[i]
		s.downloadMap[r.ID] = r
		s.downloadMap[r.Platform+":"+r.SongID] = r
	}
}
func (s *Store) watchDownloadFiles() {
	defer close(s.done)
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-s.stop:
			return
		case <-ticker.C:
			s.mu.RLock()
			records := make([]DownloadRecord, 0, len(s.downloads))
			for _, r := range s.downloads {
				if r.Status == DownloadStatusCompleted && r.FilePath != "" {
					records = append(records, DownloadRecord{ID: r.ID, Status: r.Status, FilePath: r.FilePath})
				}
			}
			s.mu.RUnlock()
			valid := downloadValidity(records)
			s.mu.Lock()
			for _, r := range records {
				if current, ok := s.downloadMap[r.ID]; ok && current.FilePath == r.FilePath && current.Status == r.Status {
					s.validDownloads[r.ID] = valid[r.ID]
				}
			}
			s.mu.Unlock()
		}
	}
}
