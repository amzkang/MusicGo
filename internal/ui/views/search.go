package views

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/egoist/mygo/ui"
	"musicgo/internal/download"
	"musicgo/internal/player"
	"musicgo/internal/plugin"
	"musicgo/internal/store"
	"musicgo/internal/ui/components"
	"musicgo/internal/ui/icons"
	"musicgo/internal/ui/theme"
)

// SearchViewState owns background requests under mu; list and filter input
// state remain on the UI thread.
type SearchViewState struct {
	mu               sync.RWMutex
	query            string
	activeTab        int
	isSearching      bool
	results          []plugin.MusicItem
	errMsg           string
	seq              uint64
	cancel           context.CancelFunc
	closed           bool
	detail           *CollectionSnapshot
	detailSeq        uint64
	detailCancel     context.CancelFunc
	List             ui.ListState
	CollectionList   ui.ListState
	CollectionFilter string
	CollectionNotice string
}

type SearchSnapshot struct {
	Query      string
	Tab        int
	Searching  bool
	Error      string
	Total      int
	Items      []plugin.MusicItem
	Collection *CollectionSnapshot
}

func NewSearchViewState() *SearchViewState { return &SearchViewState{} }

// Snapshot makes one generation of query, results and source counts visible.
// The same normalized source name is used for counting and filtering.
func (s *SearchViewState) Snapshot() SearchSnapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := SearchSnapshot{Query: s.query, Tab: s.activeTab, Searching: s.isSearching, Error: s.errMsg, Total: len(s.results)}
	if s.detail != nil {
		detail := *s.detail
		detail.Item = detail.Item.Clone()
		detail.Items = cloneMusicItems(detail.Items)
		out.Collection = &detail
		return out
	}
	for _, item := range s.results {
		out.Items = append(out.Items, item.Clone())
	}
	return out
}

// Close cancels work before plugins are shut down and rejects late results.
func (s *SearchViewState) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
	s.seq++
	s.detailSeq++
	s.isSearching = false
	if s.cancel != nil {
		s.cancel()
		s.cancel = nil
	}
	if s.detailCancel != nil {
		s.detailCancel()
		s.detailCancel = nil
	}
}

func (s *SearchViewState) PerformSearch(pm *plugin.Manager, query string, tab int, invalidate func()) {
	query = strings.TrimSpace(query)
	if query == "" {
		return
	}
	var revision uint64
	if pm != nil {
		revision = pm.Revision()
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	if s.cancel != nil {
		s.cancel()
	}
	if s.detailCancel != nil {
		s.detailCancel()
		s.detailCancel = nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	s.cancel = cancel
	s.detail = nil
	s.detailSeq++
	s.query, s.activeTab = query, tab
	s.isSearching, s.errMsg, s.results = true, "", nil
	s.List = ui.ListState{}
	s.seq++
	seq := s.seq
	s.mu.Unlock()
	if invalidate != nil {
		invalidate()
	}
	go func() {
		defer cancel()
		var items []plugin.MusicItem
		var err error
		if pm != nil {
			items, err = pm.Search(ctx, query, 1, searchTypeForTab(tab))
		}
		s.mu.Lock()
		if s.closed || seq != s.seq {
			s.mu.Unlock()
			return
		}
		s.cancel = nil
		s.isSearching = false
		switch {
		case pm == nil:
			s.errMsg = "音源插件未加载"
		case revision != pm.Revision():
			s.errMsg = "音源已更新，请重试搜索"
		case err != nil && len(items) == 0:
			if ctx.Err() == context.DeadlineExceeded {
				s.errMsg = "搜索超时，请重试"
			} else if len(pm.ListPlugins()) == 0 {
				s.errMsg = "未安装可用的音源插件，请先到插件管理安装"
			} else {
				s.errMsg = "搜索失败，请重试或更换音源"
			}
		default:
			s.results = cloneMusicItems(items)
		}
		s.mu.Unlock()
		if invalidate != nil {
			invalidate()
		}
	}()
}

// searchTypeForTab maps a tab of the search view to the kind of search the
// plugins are asked for.
func searchTypeForTab(tab int) string {
	switch tab {
	case 1:
		return "album"
	case 2:
		return "sheet"
	case 3:
		return "artist"
	default:
		return "music"
	}
}

// RenderSearch renders only a snapshot; requests run in the background.
func RenderSearch(c *ui.Context, th *theme.Theme, state *SearchViewState, pm *plugin.Manager, eng *player.Engine, st *store.Store, dlMgr *download.Manager, getImage func(string) *ui.Bitmap, invalidate func()) {
	snap := state.Snapshot()
	if snap.Collection != nil {
		renderCollection(c, th, state, *snap.Collection, pm, eng, st, dlMgr, getImage, invalidate)
		return
	}
	ui.Column(c).Fill().Padding(24, components.PagePadding(c), 32, components.PagePadding(c)).Gap(14).Children(func() {
		ui.Row(c).FillWidth().AlignItems(ui.Center).Children(func() {
			components.PageTitle(c, th, "搜索："+snap.Query).Grow(1)
			ui.Text(c, fmt.Sprintf("找到 %d 条结果", snap.Total)).FontSize(12).TextColor(th.TextMuted)
		})
		activeTab := snap.Tab
		if renderTabs(c.Key("search_tabs"), &activeTab, "单曲", "专辑", "歌单", "歌手").Changed() {
			state.PerformSearch(pm, snap.Query, activeTab, invalidate)
		}
		if snap.Searching {
			renderSongSkeleton(c, th)
			return
		}
		if snap.Error != "" {
			ui.Column(c).FillWidth().Gap(12).Children(func() {
				ui.Text(c, snap.Error).FontSize(13).TextColor(th.Danger)
				if components.Button(c.Key("search_retry"), "重试").AlignSelf(ui.Start).Disabled(pm == nil).Clicked() {
					state.PerformSearch(pm, snap.Query, snap.Tab, invalidate)
				}
			})
			return
		}
		if len(snap.Items) == 0 {
			message := "未找到相关结果"
			if snap.Query == "" {
				message = "输入歌曲、歌手或专辑开始搜索"
			}
			components.EmptyState(c, th, icons.Search, message)
			return
		}
		if snap.Tab != 0 {
			renderCollectionResults(c, th, state, snap.Items, snap.Tab, pm, invalidate)
			return
		}
		var current *plugin.MusicItem
		if eng != nil {
			current = eng.CurrentSong()
		}
		ui.List(c, &state.List, len(snap.Items), func(index int) {
			item := snap.Items[index]
			var play func()
			if eng != nil {
				play = func() { eng.SetQueue(snap.Items, index) }
			}
			renderMusicRow(c.Key(fmt.Sprintf("song_%d", index)), th, item, songRowOptions{Index: index, Current: current != nil && current.Key() == item.Key(), Play: play}, eng, st, dlMgr, getImage, invalidate, nil, nil)
		}).Grow(1).MinHeight(0)
	})
}
