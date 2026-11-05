package store

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"musicgo/internal/plugin"
)

// RecommendationSettings affects the next generated batch, not the current one.
// Source is empty for all searchable plugins. Mix is familiar, balanced or fresh.
type RecommendationSettings struct {
	Mix        string   `json:"mix"`
	Keywords   []string `json:"keywords,omitempty"`
	SeedArtist string   `json:"seedArtist,omitempty"`
	Source     string   `json:"source,omitempty"`
}

// RecommendationCandidate retains both the plugin's metadata and its actual basis.
type RecommendationCandidate struct {
	Item    plugin.MusicItem `json:"item"`
	Reason  string           `json:"reason"`
	Kind    string           `json:"kind"`
	Artist  string           `json:"artist,omitempty"`
	Keyword string           `json:"keyword,omitempty"`
}

type RecommendationArtist struct {
	Name    string `json:"name"`
	Artwork string `json:"artwork,omitempty"`
	Reason  string `json:"reason"`
}

// RecommendationCache is one coherent batch. Sources identifies the plugins
// queried; SourceSignature also detects version changes after an application restart.
type RecommendationCache struct {
	Date            string                    `json:"date"`
	Source          string                    `json:"source,omitempty"`
	Sources         []string                  `json:"sources,omitempty"`
	SourceSignature string                    `json:"sourceSignature,omitempty"`
	Candidates      []RecommendationCandidate `json:"candidates,omitempty"`
	Artists         []RecommendationArtist    `json:"artists,omitempty"`
}

type RecommendationData struct {
	Version  int                    `json:"version"`
	Settings RecommendationSettings `json:"settings"`
	Excluded []plugin.MusicItem     `json:"excluded,omitempty"`
	Cache    RecommendationCache    `json:"cache"`
}

// RecommendationPath derives the separate recommendation file without disk I/O.
// An empty config path deliberately keeps recommendations memory-only as well.
func (cm *ConfigManager) RecommendationPath() string {
	if cm == nil {
		return ""
	}
	cm.mu.RLock()
	defer cm.mu.RUnlock()
	if cm.filePath == "" {
		return ""
	}
	return filepath.Join(filepath.Dir(cm.filePath), "recommendation.json")
}

// RecommendationStore performs synchronous I/O for a background state worker.
// Its caller serializes state snapshots, so an older write cannot overtake a newer one.
type RecommendationStore struct {
	path string
	mu   sync.Mutex
}

func NewRecommendationStore(cm *ConfigManager) *RecommendationStore {
	return &RecommendationStore{path: cm.RecommendationPath()}
}

func (s *RecommendationStore) Load() (RecommendationData, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	data := RecommendationData{Version: 1, Settings: RecommendationSettings{Mix: "balanced"}}
	if s.path == "" {
		return data, nil
	}
	b, err := os.ReadFile(s.path)
	if os.IsNotExist(err) {
		return data, nil
	}
	if err != nil {
		return data, err
	}
	if err := json.Unmarshal(b, &data); err != nil {
		return RecommendationData{}, fmt.Errorf("读取推荐缓存失败: %w", err)
	}
	if data.Version > 1 {
		return RecommendationData{}, fmt.Errorf("推荐缓存版本不受支持: %d", data.Version)
	}
	data.Version = 1
	return data, nil
}

func (s *RecommendationStore) Save(data RecommendationData) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.path == "" {
		return nil
	}
	data.Version = 1
	b, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return err
	}
	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, ".musicgo-recommendation-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if _, err = f.Write(b); err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}
