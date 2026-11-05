package plugin

import (
	"context"
	"encoding/json"
	"fmt"
)

type CollectionResult struct {
	MusicList []MusicItem `json:"musicList"`
	IsEnd     bool        `json:"isEnd"`
}

func (inst *Instance) collection(ctx context.Context, method string, item MusicItem, page int) (*CollectionResult, error) {
	var value interface{}
	var err error
	switch method {
	case "getArtistWorks":
		value, err = inst.callAsync(ctx, method, item, page, "music")
	case "getMusicSheetInfo":
		if !inst.HasMethod(method) && inst.HasMethod("importMusicSheet") {
			ref := item.URL
			if ref == "" {
				ref = item.ID
			}
			items, e := inst.ImportMusicSheet(ctx, ref)
			return &CollectionResult{MusicList: items, IsEnd: true}, e
		}
		value, err = inst.callAsync(ctx, method, item, page)
	default:
		value, err = inst.callAsync(ctx, method, item, page)
	}
	if err != nil {
		return nil, err
	}
	if value == nil {
		return &CollectionResult{IsEnd: true}, nil
	}
	data, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	var raw struct {
		MusicList []MusicItem `json:"musicList"`
		Data      []MusicItem `json:"data"`
		IsEnd     *bool       `json:"isEnd"`
	}
	var items []MusicItem
	end := true
	if err = json.Unmarshal(data, &items); err != nil {
		if err = json.Unmarshal(data, &raw); err != nil {
			return nil, err
		}
		items = raw.MusicList
		if items == nil {
			items = raw.Data
		}
		if raw.IsEnd != nil {
			end = *raw.IsEnd
		}
	}
	for i := range items {
		if items[i].Platform == "" {
			items[i].Platform = inst.meta.Platform
		}
	}
	return &CollectionResult{MusicList: items, IsEnd: end}, nil
}
func (inst *Instance) GetAlbumInfo(ctx context.Context, item MusicItem, page int) (*CollectionResult, error) {
	return inst.collection(ctx, "getAlbumInfo", item, page)
}
func (inst *Instance) GetMusicSheetInfo(ctx context.Context, item MusicItem, page int) (*CollectionResult, error) {
	return inst.collection(ctx, "getMusicSheetInfo", item, page)
}
func (inst *Instance) GetDailyRecommend(ctx context.Context) (*CollectionResult, error) {
	return inst.collection(ctx, "getDailyRecommend", MusicItem{}, 1)
}
func (inst *Instance) GetArtistWorks(ctx context.Context, item MusicItem, page int) (*CollectionResult, error) {
	return inst.collection(ctx, "getArtistWorks", item, page)
}
func (m *Manager) collection(ctx context.Context, kind string, item MusicItem, page int) (*CollectionResult, error) {
	inst := m.GetPlugin(item.Platform)
	if inst == nil {
		return nil, fmt.Errorf("音源插件未加载: %s", item.Platform)
	}
	switch kind {
	case "album":
		return inst.GetAlbumInfo(ctx, item, page)
	case "sheet":
		return inst.GetMusicSheetInfo(ctx, item, page)
	case "artist":
		return inst.GetArtistWorks(ctx, item, page)
	}
	return nil, fmt.Errorf("unsupported collection type: %s", kind)
}
func (m *Manager) GetAlbumInfo(ctx context.Context, item MusicItem, page int) (*CollectionResult, error) {
	return m.collection(ctx, "album", item, page)
}
func (m *Manager) GetMusicSheetInfo(ctx context.Context, item MusicItem, page int) (*CollectionResult, error) {
	return m.collection(ctx, "sheet", item, page)
}
func (m *Manager) GetArtistWorks(ctx context.Context, item MusicItem, page int) (*CollectionResult, error) {
	return m.collection(ctx, "artist", item, page)
}
