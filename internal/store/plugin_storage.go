package store

import (
	"database/sql"
	"log"
	"musicgo/internal/plugin"
	"sort"
)

// pluginStorage keeps every plugin's localStorage entries in memory (MusicFree
// plugin storage is KB-scale, so full residency costs nothing) and persists
// writes to SQLite. Drawing code never touches this table; only plugin method
// calls on background goroutines read and write it.

type pluginStorageScope struct {
	s  *Store
	id string
}

// PluginStorageScope returns the persistent key-value store of one plugin,
// scoped by the plugin's platform name.
func (s *Store) PluginStorageScope(pluginID string) plugin.Storage {
	return &pluginStorageScope{s: s, id: pluginID}
}

func (sc *pluginStorageScope) Get(key string) (string, bool) {
	sc.s.mu.RLock()
	defer sc.s.mu.RUnlock()
	m := sc.s.pluginStorage[sc.id]
	v, ok := m[key]
	return v, ok
}

// writeFailed surfaces a failed persistence through the log and
// LastWriteError so that a plugin losing its login token on restart is
// diagnosable instead of silent.
func (sc *pluginStorageScope) writeFailed(err error) {
	log.Printf("plugin storage %s: persist failed: %v", sc.id, err)
	sc.s.mu.Lock()
	sc.s.lastWriteError = err
	sc.s.mu.Unlock()
}

func (sc *pluginStorageScope) Set(key, value string) {
	sc.s.writeMu.Lock()
	defer sc.s.writeMu.Unlock()
	if _, err := sc.s.db.Exec("INSERT INTO plugin_storage(plugin_id,key,value) VALUES(?,?,?) ON CONFLICT(plugin_id,key) DO UPDATE SET value=excluded.value", sc.id, key, value); err != nil {
		sc.writeFailed(err)
		return
	}
	sc.s.mu.Lock()
	defer sc.s.mu.Unlock()
	if sc.s.pluginStorage == nil {
		sc.s.pluginStorage = make(map[string]map[string]string)
	}
	m := sc.s.pluginStorage[sc.id]
	if m == nil {
		m = make(map[string]string)
		sc.s.pluginStorage[sc.id] = m
	}
	m[key] = value
}

func (sc *pluginStorageScope) Delete(key string) {
	sc.s.writeMu.Lock()
	defer sc.s.writeMu.Unlock()
	if _, err := sc.s.db.Exec("DELETE FROM plugin_storage WHERE plugin_id=? AND key=?", sc.id, key); err != nil {
		sc.writeFailed(err)
		return
	}
	sc.s.mu.Lock()
	defer sc.s.mu.Unlock()
	if m := sc.s.pluginStorage[sc.id]; m != nil {
		delete(m, key)
	}
}

func (sc *pluginStorageScope) Keys() []string {
	sc.s.mu.RLock()
	keys := make([]string, 0, len(sc.s.pluginStorage[sc.id]))
	for k := range sc.s.pluginStorage[sc.id] {
		keys = append(keys, k)
	}
	sc.s.mu.RUnlock()
	sort.Strings(keys)
	return keys
}

func (sc *pluginStorageScope) Clear() {
	sc.s.writeMu.Lock()
	defer sc.s.writeMu.Unlock()
	if _, err := sc.s.db.Exec("DELETE FROM plugin_storage WHERE plugin_id=?", sc.id); err != nil {
		sc.writeFailed(err)
		return
	}
	sc.s.mu.Lock()
	defer sc.s.mu.Unlock()
	delete(sc.s.pluginStorage, sc.id)
}

// loadPluginStorageLocked fills the in-memory plugin storage cache from SQLite.
func (s *Store) loadPluginStorageLocked() error {
	rows, err := s.db.Query("SELECT plugin_id, key, value FROM plugin_storage")
	if err != nil {
		return err
	}
	defer rows.Close()

	storage := make(map[string]map[string]string)
	for rows.Next() {
		var pluginID, key string
		var value sql.NullString
		if err := rows.Scan(&pluginID, &key, &value); err != nil {
			return err
		}
		m := storage[pluginID]
		if m == nil {
			m = make(map[string]string)
			storage[pluginID] = m
		}
		m[key] = value.String
	}
	if err := rows.Err(); err != nil {
		return err
	}
	s.pluginStorage = storage
	return nil
}

// PluginStorageAll returns a copy of every plugin's stored entries, for
// backup export.
func (s *Store) PluginStorageAll() map[string]map[string]string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.pluginStorageAllLocked()
}

// The caller already holds mu. A backup must not recursively RLock while a
// plugin write is waiting, since RWMutex then blocks new readers as well.
func (s *Store) pluginStorageAllLocked() map[string]map[string]string {
	out := make(map[string]map[string]string, len(s.pluginStorage))
	for id, m := range s.pluginStorage {
		cm := make(map[string]string, len(m))
		for k, v := range m {
			cm[k] = v
		}
		out[id] = cm
	}
	return out
}
