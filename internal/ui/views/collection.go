package views

import (
	"context"
	"errors"
	"fmt"
	"github.com/egoist/mygo/ui"
	"musicgo/internal/download"
	"musicgo/internal/player"
	"musicgo/internal/plugin"
	"musicgo/internal/store"
	"musicgo/internal/ui/components"
	"musicgo/internal/ui/icons"
	"musicgo/internal/ui/theme"
	"strings"
	"time"
)

type CollectionSnapshot struct {
	Item           plugin.MusicItem
	Kind           string
	Items          []plugin.MusicItem
	Page           int
	IsEnd, Loading bool
	Error          string
}

func cloneMusicItems(items []plugin.MusicItem) []plugin.MusicItem {
	out := make([]plugin.MusicItem, len(items))
	for i, item := range items {
		out[i] = item.Clone()
	}
	return out
}
func (s *SearchViewState) HasCollection() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.detail != nil
}

func (s *SearchViewState) BackToResults() {
	s.mu.Lock()
	if s.detailCancel != nil {
		s.detailCancel()
	}
	s.detail = nil
	s.detailSeq++
	s.mu.Unlock()
}
func (s *SearchViewState) OpenCollection(pm *plugin.Manager, item plugin.MusicItem, kind string, redraw func()) {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.CollectionFilter = ""
	s.CollectionNotice = ""
	s.CollectionList = ui.ListState{}
	if s.detailCancel != nil {
		s.detailCancel()
	}
	s.detailSeq++
	seq := s.detailSeq
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	s.detailCancel = cancel
	s.detail = &CollectionSnapshot{Item: item.Clone(), Kind: kind, Loading: true}
	s.mu.Unlock()
	if redraw != nil {
		redraw()
	}
	go func() {
		defer cancel()
		s.loadCollection(ctx, seq, 1, pm, item.Clone(), kind, redraw)
	}()
}
func (s *SearchViewState) LoadMore(pm *plugin.Manager, redraw func()) {
	s.mu.Lock()
	if s.closed || s.detail == nil || s.detail.Loading || s.detail.IsEnd {
		s.mu.Unlock()
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	s.detailCancel = cancel
	seq, page, item, kind := s.detailSeq, s.detail.Page+1, s.detail.Item.Clone(), s.detail.Kind
	s.detail.Loading = true
	s.detail.Error = ""
	s.mu.Unlock()
	if redraw != nil {
		redraw()
	}
	go func() {
		defer cancel()
		s.loadCollection(ctx, seq, page, pm, item, kind, redraw)
	}()
}
func (s *SearchViewState) loadCollection(ctx context.Context, seq uint64, page int, pm *plugin.Manager, item plugin.MusicItem, kind string, redraw func()) {
	var result *plugin.CollectionResult
	var err error
	if pm == nil {
		err = fmt.Errorf("音源插件未加载")
	} else {
		switch kind {
		case "album":
			result, err = pm.GetAlbumInfo(ctx, item, page)
		case "sheet":
			result, err = pm.GetMusicSheetInfo(ctx, item, page)
		case "artist":
			result, err = pm.GetArtistWorks(ctx, item, page)
		default:
			err = fmt.Errorf("不支持的搜索分类")
		}
	}
	s.mu.Lock()
	if s.closed || s.detail == nil || seq != s.detailSeq || errors.Is(ctx.Err(), context.Canceled) {
		s.mu.Unlock()
		return
	}
	d := s.detail
	d.Loading = false
	if err != nil {
		var unsupported *plugin.MethodError
		if errors.As(err, &unsupported) {
			d.Error = "此音源暂不支持这类详情"
		} else {
			d.Error = "加载失败: " + err.Error()
		}
	} else if result != nil {
		seen := make(map[plugin.SongKey]bool, len(d.Items))
		for _, song := range d.Items {
			seen[song.Key()] = true
		}
		for _, song := range result.MusicList {
			if !seen[song.Key()] {
				seen[song.Key()] = true
				d.Items = append(d.Items, song.Clone())
			}
		}
		d.Page = page
		d.IsEnd = result.IsEnd || len(result.MusicList) == 0
	}
	if s.detailCancel != nil {
		s.detailCancel()
		s.detailCancel = nil
	}
	s.mu.Unlock()
	if redraw != nil {
		redraw()
	}
}
func renderCollectionResults(c *ui.Context, th *theme.Theme, state *SearchViewState, items []plugin.MusicItem, tab int, pm *plugin.Manager, redraw func()) {
	kind := searchTypeForTab(tab)
	ui.Scroll(c).Grow(1).Children(func() {
		ui.Column(c).FillWidth().Gap(8).Children(func() {
			for i, item := range items {
				item := item
				row := ui.Row(c.Key(fmt.Sprintf("search_collection_%d", i))).FillWidth().Padding(12, 14).Gap(12).AlignItems(ui.Center).Background(th.CardBg).Radius(8)
				open := false
				row.Children(func() {
					ui.Column(c).Grow(1).Gap(4).Children(func() {
						ui.Text(c, item.Title).FontSize(15).Bold().TextColor(th.TextMain)
						if item.Artist != "" {
							ui.Text(c, item.Artist).FontSize(12).TextColor(th.TextSub)
						}
					})
					open = components.Button(c.Key(fmt.Sprintf("collection_open_%d", i)), "打开").Clicked()
				})
				if open || row.DoubleClicked() {
					state.OpenCollection(pm, item, kind, redraw)
				}
			}
		})
	})
}
func renderCollection(c *ui.Context, th *theme.Theme, state *SearchViewState, detail CollectionSnapshot, pm *plugin.Manager, eng *player.Engine, st *store.Store, dl *download.Manager, getImage func(string) *ui.Bitmap, redraw func()) {
	ui.Column(c).Fill().Padding(24, components.PagePadding(c), 32, components.PagePadding(c)).Gap(14).Children(func() {
		ui.Row(c).FillWidth().Wrap().Gap(14).AlignItems(ui.Center).Children(func() {
			if components.BackButton(c.Key("collection_back"), th, "返回搜索").Clicked() {
				state.BackToResults()
				if redraw != nil {
					redraw()
				}
			}
			components.PageTitle(c, th, detail.Item.Title).Grow(1)
			label := "播放全部"
			if !detail.IsEnd {
				label = "播放已加载"
			}
			if components.PrimaryButton(c.Key("collection_play_all"), th, label).Disabled(len(detail.Items) == 0 || eng == nil).Clicked() && eng != nil {
				eng.SetQueue(detail.Items, 0)
			}
		})
		if detail.Item.Artwork != "" && getImage != nil {
			if image := getImage(detail.Item.Artwork); image != nil {
				ui.Image(c, image).Size(96, 96).Radius(10).Fit(ui.Cover)
			}
		}
		if detail.Error != "" {
			ui.Text(c, detail.Error).TextColor(th.Danger)
		}
		if detail.Loading {
			ui.Text(c, "正在加载...").TextColor(th.TextSub)
		}
		if len(detail.Items) == 0 && !detail.Loading && detail.Error == "" {
			components.EmptyState(c, th, icons.Note, "暂无歌曲")
		}
		locate := -1
		ui.Row(c).FillWidth().Wrap().Gap(10).AlignItems(ui.Center).Children(func() {
			filter := ui.SearchField(c.Key("collection_filter"), &state.CollectionFilter).Width(220).MinWidth(120).Shrink(1).Placeholder("筛选已加载歌曲")
			if filter.Changed() {
				state.CollectionNotice = ""
				state.CollectionList = ui.ListState{}
			}
			if components.Button(c.Key("collection_locate"), "定位当前歌曲").Clicked() {
				state.CollectionNotice = "当前歌曲不在已加载范围"
				if eng != nil {
					if song := eng.CurrentSong(); song != nil {
						for i, item := range detail.Items {
							if item.Key() == song.Key() {
								locate = i
								state.CollectionFilter = ""
								state.CollectionNotice = ""
								break
							}
						}
					}
				}
			}
			if !detail.IsEnd {
				ui.Text(c, "仅筛选已加载歌曲").FontSize(12).TextColor(th.TextSub)
			}
			if state.CollectionNotice != "" {
				ui.Text(c, state.CollectionNotice).FontSize(12).TextColor(th.TextSub)
			}
		})
		if locate >= 0 {
			state.CollectionList.ScrollIntoView(locate)
		}
		filter := strings.ToLower(strings.TrimSpace(state.CollectionFilter))
		var currentSong *plugin.MusicItem
		if eng != nil {
			currentSong = eng.CurrentSong()
		}
		visible := make([]int, 0, len(detail.Items))
		for i, song := range detail.Items {
			if filter == "" || strings.Contains(strings.ToLower(song.Title+" "+song.Artist+" "+song.Album), filter) {
				visible = append(visible, i)
			}
		}
		state.CollectionList.Key = func(i int) any { return detail.Items[visible[i]].Key() }
		ui.List(c, &state.CollectionList, len(visible), func(i int) {
			index := visible[i]
			song := detail.Items[index]
			isCurrent := currentSong != nil && currentSong.Key() == song.Key()
			var play func()
			if eng != nil {
				play = func() { eng.SetQueue(detail.Items, index) }
			}
			renderMusicRow(c.Key(fmt.Sprintf("collection_song_%d", index)), th, song, songRowOptions{Index: index, Current: isCurrent, Play: play}, eng, st, dl, getImage, redraw, func(message string) { state.CollectionNotice = message }, nil)
		}).FillWidth().Grow(1).MinHeight(0)
		if !detail.IsEnd && !detail.Loading {
			label := "加载更多"
			if detail.Error != "" {
				label = "重试"
			}
			if components.Button(c.Key("collection_more"), label).Clicked() {
				state.LoadMore(pm, redraw)
			}
		}
	})
}
