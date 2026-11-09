package views

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"golang.org/x/text/unicode/norm"
	"musicgo/internal/plugin"
	"musicgo/internal/store"
)

type PersonalizedSettings = store.RecommendationSettings
type PersonalizedCandidate = store.RecommendationCandidate
type PersonalizedArtist = store.RecommendationArtist

type PersonalizedSource struct {
	Name   string
	Count  int
	Failed bool
	Error  string
}

type PersonalizedArtistSnapshot struct {
	Name       string
	Candidates []PersonalizedCandidate
	Songs      []plugin.MusicItem
	Loading    bool
	Error      string
}

// PersonalizedSnapshot owns all its slices and song metadata. Songs and
// Candidates describe the visible page; Batch always describes the hero's batch.
// Source filtering applies to all three in one snapshot.
type PersonalizedSnapshot struct {
	Candidates                      []PersonalizedCandidate
	Songs                           []plugin.MusicItem
	Batch                           []PersonalizedCandidate
	Artists                         []PersonalizedArtist
	Settings                        PersonalizedSettings
	Sources                         []PersonalizedSource
	Excluded                        []plugin.MusicItem
	Source                          string
	Loading, Restoring, Loaded      bool
	NeedsSeed, NoSources, CanUndo   bool
	Notice, Error, PersistenceError string
	Date                            string
	Artist                          *PersonalizedArtistSnapshot
}

// PersonalizedViewState is independent of discovery's RecommendViewState.
// Public methods only change memory or launch background work, including Save.
type PersonalizedViewState struct {
	mu                                 sync.RWMutex
	disk                               *store.RecommendationStore
	wake                               chan struct{}
	done                               chan struct{}
	workerDone                         chan struct{}
	closed, restoring, loaded, loading bool
	settings                           PersonalizedSettings
	settingsTouched                    bool
	sourceTouched, seedTouched         bool
	excluded                           []plugin.MusicItem
	// Edits made while the initial file is loading win over restored values.
	exclusionEdits                   map[plugin.SongKey]*plugin.MusicItem
	clearRestoredExclusions          bool
	undo                             *personalizedUndo
	cache                            store.RecommendationCache
	artist                           *PersonalizedArtistSnapshot
	sources                          []PersonalizedSource
	failures                         map[string]string
	needsSeed                        bool
	notice, errMsg, persistenceError string

	pm                   *plugin.Manager
	st                   *store.Store
	redraw               func()
	bound                bool
	seq, artistSeq       uint64
	cancel, artistCancel context.CancelFunc
	requestRevision      uint64
	requestDate          string
	requested            bool
	pendingForce         bool
	dirty, saved         uint64
}

type personalizedUndo struct {
	item            plugin.MusicItem
	candidate       *PersonalizedCandidate
	index           int
	artistCandidate *PersonalizedCandidate
	artistIndex     int
	artistName      string
}

func NewPersonalizedViewState(cm *store.ConfigManager) *PersonalizedViewState {
	s := &PersonalizedViewState{
		disk: store.NewRecommendationStore(cm), wake: make(chan struct{}, 1), done: make(chan struct{}), workerDone: make(chan struct{}),
		restoring: true, settings: PersonalizedSettings{Mix: "balanced"},
		failures: make(map[string]string), exclusionEdits: make(map[plugin.SongKey]*plugin.MusicItem),
	}
	go s.persistenceWorker()
	return s
}

func clonePersonalizedCandidates(items []PersonalizedCandidate) []PersonalizedCandidate {
	out := make([]PersonalizedCandidate, len(items))
	for i, c := range items {
		out[i] = c
		out[i].Item = c.Item.Clone()
	}
	return out
}

func personalizedSongs(items []PersonalizedCandidate) []plugin.MusicItem {
	out := make([]plugin.MusicItem, len(items))
	for i, c := range items {
		out[i] = c.Item.Clone()
	}
	return out
}

func filterPersonalized(items []PersonalizedCandidate, source string) []PersonalizedCandidate {
	out := make([]PersonalizedCandidate, 0, len(items))
	for _, c := range items {
		if source == "" || c.Item.Platform == source {
			out = append(out, c)
		}
	}
	return clonePersonalizedCandidates(out)
}

func (s *PersonalizedViewState) Snapshot() PersonalizedSnapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := PersonalizedSnapshot{
		Settings: s.settings, Source: s.settings.Source, Restoring: s.restoring,
		Loading: s.loading, Loaded: s.loaded, NeedsSeed: s.needsSeed,
		NoSources: len(s.sources) == 0, CanUndo: s.undo != nil,
		Notice: s.notice, Error: s.errMsg, PersistenceError: s.persistenceError, Date: s.cache.Date,
		Artists: append([]PersonalizedArtist(nil), s.cache.Artists...), Excluded: cloneMusicItems(s.excluded),
		Sources: append([]PersonalizedSource(nil), s.sources...),
	}
	out.Settings.Keywords = append([]string(nil), s.settings.Keywords...)
	source := ""
	if s.pm != nil {
		source = s.pm.DiscoveryPlatform()
	}
	visible := func(items []PersonalizedCandidate) []PersonalizedCandidate {
		var allowed []PersonalizedCandidate
		for _, item := range items {
			for _, entry := range s.sources {
				if item.Item.Platform == entry.Name {
					allowed = append(allowed, item)
					break
				}
			}
		}
		return filterPersonalized(allowed, source)
	}
	out.Batch = visible(s.cache.Candidates)
	out.Candidates = clonePersonalizedCandidates(out.Batch)
	if s.artist != nil {
		detail := *s.artist
		detail.Candidates = visible(detail.Candidates)
		detail.Songs = personalizedSongs(detail.Candidates)
		out.Artist = &detail
		out.Candidates = clonePersonalizedCandidates(detail.Candidates)
	}
	out.Songs = personalizedSongs(out.Candidates)
	for i := range out.Sources {
		out.Sources[i].Error = s.failures[out.Sources[i].Name]
		out.Sources[i].Failed = out.Sources[i].Error != ""
		for _, c := range s.cache.Candidates {
			if c.Item.Platform == out.Sources[i].Name {
				out.Sources[i].Count++
			}
		}
	}
	return out
}

func personalizedNotify(redraw func()) {
	if redraw != nil {
		redraw()
	}
}

func searchablePersonalizedSources(pm *plugin.Manager) ([]PersonalizedSource, string) {
	if pm == nil {
		return nil, ""
	}
	var out []PersonalizedSource
	var signature []string
	for _, meta := range pm.DiscoveryPlugins() {
		inst := pm.GetPlugin(meta.Platform)
		if inst == nil || !inst.HasMethod("search") {
			continue
		}
		music := len(meta.SupportedSearchType) == 0
		for _, kind := range meta.SupportedSearchType {
			if kind == "music" {
				music = true
			}
		}
		if !music {
			continue
		}
		out = append(out, PersonalizedSource{Name: meta.Platform})
		signature = append(signature, meta.Platform+"@"+meta.Version)
	}
	return out, strings.Join(signature, "\n")
}

// Ensure is safe on every frame. It restores once, then updates only once per
// local date/plugin revision. Failed attempts need an explicit Retry.
func (s *PersonalizedViewState) Ensure(pm *plugin.Manager, st *store.Store, redraw func()) {
	sources, signature := searchablePersonalizedSources(pm)
	revision := uint64(0)
	if pm != nil {
		revision = pm.Revision()
	}
	date := time.Now().Format("2006-01-02")
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.pm, s.st, s.redraw, s.bound = pm, st, redraw, true
	s.sources = sources
	if s.restoring {
		s.mu.Unlock()
		return
	}
	if s.needsSeed && !s.loading && st != nil {
		// These methods only clone the store's memory cache. An empty first visit
		// must become usable after the user favorites a song elsewhere in the app.
		favorites, _ := st.ListFavorites()
		history, _ := st.ListHistory()
		hasArtist := false
		for _, item := range append(favorites, history...) {
			for _, name := range personalizedArtistParts(item.Artist) {
				key := personalizedNormalize(name)
				if key != "" && !personalizedUnknownArtist(key) {
					hasArtist = true
					break
				}
			}
			if hasArtist {
				break
			}
		}
		if hasArtist {
			s.requested = false
		}
	}
	if s.requested && s.requestRevision == revision && s.requestDate == date && !s.pendingForce {
		s.mu.Unlock()
		return
	}
	// A current offline cache is useful even when none of its plugins is installed.
	validCache := s.cache.Date == date && s.cache.Source == s.settings.Source &&
		s.cache.SourceSignature == signature && len(s.cache.Candidates) > 0
	if !s.requested && validCache && !s.pendingForce {
		s.requested, s.requestRevision, s.requestDate, s.loaded = true, revision, date, true
		s.mu.Unlock()
		return
	}
	s.mu.Unlock()
	s.begin(pm, st, redraw, false, "", true)
}

// Refresh generates a new batch, excluding every song in the previous batch.
func (s *PersonalizedViewState) Refresh(pm *plugin.Manager, st *store.Store, redraw func()) {
	s.begin(pm, st, redraw, false, "")
}

func (s *PersonalizedViewState) SetSource(source string, pm *plugin.Manager, st *store.Store, redraw func()) {
	s.mu.Lock()
	if s.closed || s.settings.Source == source {
		s.mu.Unlock()
		return
	}
	s.settings.Source, s.sourceTouched, s.pendingForce = source, true, true
	s.backLocked()
	s.markDirtyLocked()
	s.mu.Unlock()
	s.begin(pm, st, redraw, false, "")
}

func normalizePersonalizedSettings(settings PersonalizedSettings) PersonalizedSettings {
	switch settings.Mix {
	case "familiar", "fresh", "balanced":
	default:
		settings.Mix = "balanced"
	}
	seen := make(map[string]bool)
	var keywords []string
	for _, keyword := range settings.Keywords {
		keyword = strings.TrimSpace(keyword)
		if keyword == "" || seen[personalizedNormalize(keyword)] {
			continue
		}
		if len([]rune(keyword)) > 80 {
			keyword = string([]rune(keyword)[:80])
		}
		seen[personalizedNormalize(keyword)] = true
		keywords = append(keywords, keyword)
		if len(keywords) == 6 {
			break
		}
	}
	settings.Keywords = keywords
	settings.SeedArtist = strings.TrimSpace(settings.SeedArtist)
	if len([]rune(settings.SeedArtist)) > 100 {
		settings.SeedArtist = string([]rune(settings.SeedArtist)[:100])
	}
	return settings
}

func (s *PersonalizedViewState) UpdateSettings(settings PersonalizedSettings, redraw func()) error {
	settings = normalizePersonalizedSettings(settings)
	if settings.Mix == "fresh" && len(settings.Keywords) == 0 {
		return fmt.Errorf("新鲜一些需要先选择探索关键词")
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	settings.Source = s.settings.Source
	settings.SeedArtist = s.settings.SeedArtist
	s.settings, s.settingsTouched = settings, true
	s.notice = "推荐设置已保存，将用于下一批"
	if s.needsSeed && len(settings.Keywords) > 0 {
		s.needsSeed = false
	}
	s.markDirtyLocked()
	s.mu.Unlock()
	personalizedNotify(redraw)
	return nil
}

func (s *PersonalizedViewState) Generate(seedArtist string, pm *plugin.Manager, st *store.Store, redraw func()) {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.settings.SeedArtist = normalizePersonalizedSettings(PersonalizedSettings{SeedArtist: seedArtist}).SeedArtist
	s.seedTouched, s.pendingForce = true, true
	s.markDirtyLocked()
	s.mu.Unlock()
	s.begin(pm, st, redraw, false, "")
}

// Retry keeps successful-source candidates while retrying the selected source(s).
func (s *PersonalizedViewState) Retry(pm *plugin.Manager, st *store.Store, redraw func()) {
	s.begin(pm, st, redraw, true, "")
}
func (s *PersonalizedViewState) RetrySource(source string, pm *plugin.Manager, st *store.Store, redraw func()) {
	s.begin(pm, st, redraw, true, source)
}

type personalizedRequest struct {
	seq, revision   uint64
	date, signature string
	settings        PersonalizedSettings
	sources         []string
	previous        []PersonalizedCandidate
	excluded        []plugin.MusicItem
	retry           bool
	retrySource     string
}

func (s *PersonalizedViewState) begin(pm *plugin.Manager, st *store.Store, redraw func(), retry bool, retrySource string, automatic ...bool) {
	sources, signature := searchablePersonalizedSources(pm)
	revision := uint64(0)
	if pm != nil {
		revision = pm.Revision()
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.pm, s.st, s.redraw, s.bound = pm, st, redraw, true
	s.sources = sources
	if s.restoring {
		s.pendingForce = true
		s.mu.Unlock()
		personalizedNotify(redraw)
		return
	}
	// Restore and the next render may both reach Ensure at once. Recheck the
	// attempt latch while holding the lock before replacing any active request.
	date := time.Now().Format("2006-01-02")
	if len(automatic) > 0 && automatic[0] && s.requested && s.requestRevision == revision && s.requestDate == date && !s.pendingForce {
		s.mu.Unlock()
		return
	}
	if s.cancel != nil {
		s.cancel()
	}
	s.seq++
	if s.artist != nil && s.requestRevision != revision {
		if s.artistCancel != nil {
			s.artistCancel()
			s.artistCancel = nil
		}
		s.artistSeq++
		s.artist.Loading, s.artist.Error = false, "音源已改变，请重试"
	}
	s.loading, s.errMsg, s.notice, s.pendingForce = true, "", "", false
	s.requested, s.requestRevision, s.requestDate = true, revision, date
	ctx, cancel := context.WithTimeout(context.Background(), 24*time.Second)
	s.cancel = cancel
	req := personalizedRequest{seq: s.seq, revision: revision, date: s.requestDate, signature: signature,
		settings: s.settings, previous: clonePersonalizedCandidates(s.cache.Candidates), excluded: cloneMusicItems(s.excluded),
		retry: retry, retrySource: retrySource}
	req.settings.Keywords = append([]string(nil), s.settings.Keywords...)
	for _, source := range sources {
		if retrySource != "" && source.Name != retrySource {
			continue
		}
		req.sources = append(req.sources, source.Name)
	}
	s.mu.Unlock()
	personalizedNotify(redraw)
	go s.generate(ctx, cancel, req, pm, st, redraw)
}

type personalizedQuery struct{ text, kind, artist, artwork string }
type personalizedSearchResult struct {
	candidates []PersonalizedCandidate
	failures   map[string]string
	succeeded  int
}

func personalizedSeeds(st *store.Store, settings PersonalizedSettings) ([]personalizedQuery, []PersonalizedArtist, []plugin.MusicItem, []plugin.MusicItem) {
	var favorites, history []plugin.MusicItem
	if st != nil {
		favorites, _ = st.ListFavorites()
		history, _ = st.ListHistory()
	}
	type artistSeed struct {
		name, artwork string
		count, order  int
	}
	byArtist := make(map[string]*artistSeed)
	var seeds []*artistSeed
	for _, item := range favorites {
		for _, name := range personalizedArtistParts(item.Artist) {
			key := personalizedNormalize(name)
			if key == "" || personalizedUnknownArtist(key) {
				continue
			}
			seed := byArtist[key]
			if seed == nil {
				seed = &artistSeed{name: name, artwork: item.Artwork, order: len(seeds)}
				byArtist[key] = seed
				seeds = append(seeds, seed)
			}
			seed.count++
		}
	}
	sort.SliceStable(seeds, func(i, j int) bool { return seeds[i].count > seeds[j].count })
	var queries []personalizedQuery
	var artists []PersonalizedArtist
	seen := make(map[string]bool)
	for _, seed := range seeds {
		if len(queries) == 6 {
			break
		}
		seen[personalizedNormalize(seed.name)] = true
		queries = append(queries, personalizedQuery{text: seed.name, artist: seed.name, kind: "favorite", artwork: seed.artwork})
		if len(artists) < 3 {
			artists = append(artists, PersonalizedArtist{Name: seed.name, Artwork: seed.artwork, Reason: "来自你喜欢的歌手"})
		}
	}
	if settings.SeedArtist != "" && !seen[personalizedNormalize(settings.SeedArtist)] {
		queries = append([]personalizedQuery{{text: settings.SeedArtist, artist: settings.SeedArtist, kind: "seed"}}, queries...)
		seen[personalizedNormalize(settings.SeedArtist)] = true
	}
	for _, item := range history {
		if len(queries) >= 9 {
			break
		}
		for _, name := range personalizedArtistParts(item.Artist) {
			key := personalizedNormalize(name)
			if key == "" || seen[key] || personalizedUnknownArtist(key) {
				continue
			}
			seen[key] = true
			queries = append(queries, personalizedQuery{text: name, artist: name, kind: "history"})
			if len(queries) >= 9 {
				break
			}
		}
	}
	for _, keyword := range settings.Keywords {
		queries = append(queries, personalizedQuery{text: keyword, kind: "keyword"})
	}
	return queries, artists, favorites, history
}

// Search work is bounded both globally and per plugin: at most three source
// workers, sequential queries per source, eight seconds per call and a shared deadline.
func searchPersonalized(ctx context.Context, pm *plugin.Manager, sources []string, queries []personalizedQuery, page int) personalizedSearchResult {
	out := personalizedSearchResult{failures: make(map[string]string)}
	if pm == nil || len(queries) == 0 {
		return out
	}
	type sourceResult struct {
		index      int
		candidates []PersonalizedCandidate
		failure    string
		succeeded  bool
	}
	results := make(chan sourceResult, len(sources))
	jobs := make(chan int, len(sources))
	for i := range sources {
		jobs <- i
	}
	close(jobs)
	workers := min(3, len(sources))
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for index := range jobs {
				res := sourceResult{index: index}
				inst := pm.GetPlugin(sources[index])
				if inst == nil {
					res.failure = "音源已移除"
					results <- res
					continue
				}
				var errors []string
				for _, query := range queries {
					if ctx.Err() != nil {
						errors = append(errors, "请求超时或已取消")
						break
					}
					callCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
					found, err := inst.Search(callCtx, query.text, page, "music")
					cancel()
					if err != nil {
						errors = append(errors, query.text+": "+err.Error())
						continue
					}
					res.succeeded = true
					if found == nil {
						continue
					}
					for _, item := range found.Data[:min(len(found.Data), 100)] {
						// Routing must stay with the plugin that produced the result.
						if item.Platform == "" {
							item.Platform = sources[index]
						}
						if item.Platform != sources[index] || strings.TrimSpace(item.Title) == "" || item.ID == "" {
							continue
						}
						if query.artist != "" && !personalizedArtistMatches(item.Artist, query.artist) {
							continue
						}
						candidate := PersonalizedCandidate{Item: item.Clone(), Kind: query.kind, Artist: query.artist}
						switch query.kind {
						case "favorite":
							candidate.Reason = "来自你喜欢的歌手"
						case "history":
							candidate.Reason = "来自最近选择的歌手"
						case "seed":
							candidate.Reason = "来自你选择的歌手"
						case "keyword":
							candidate.Keyword = query.text
							candidate.Reason = "来自你选择的「" + query.text + "」"
						}
						res.candidates = append(res.candidates, candidate)
					}
				}
				res.failure = strings.Join(errors, "; ")
				results <- res
			}
		}()
	}
	wg.Wait()
	close(results)
	ordered := make([]sourceResult, len(sources))
	for res := range results {
		ordered[res.index] = res
	}
	// Round-robin sources avoids a fast or alphabetically early source owning the batch.
	for _, res := range ordered {
		if res.failure != "" {
			out.failures[sources[res.index]] = res.failure
		}
		if res.succeeded {
			out.succeeded++
		}
	}
	for index := 0; ; index++ {
		added := false
		for _, res := range ordered {
			if index < len(res.candidates) {
				out.candidates = append(out.candidates, res.candidates[index])
				added = true
			}
		}
		if !added {
			break
		}
	}
	return out
}

func (s *PersonalizedViewState) generate(ctx context.Context, cancel context.CancelFunc, req personalizedRequest, pm *plugin.Manager, st *store.Store, redraw func()) {
	defer cancel()
	queries, artists, favorites, history := personalizedSeeds(st, req.settings)
	result := searchPersonalized(ctx, pm, req.sources, queries, 1)
	// Repeating Refresh may reach beyond the first search page without reusing the
	// batch it replaces. There is no claim that these results are new releases.
	if !req.retry && len(result.candidates) > 0 && len(selectPersonalized(result.candidates, favorites, history, req.excluded, req.previous, req.settings, false)) == 0 && ctx.Err() == nil {
		more := searchPersonalized(ctx, pm, req.sources, queries, 2)
		result.candidates = append(result.candidates, more.candidates...)
		for source, err := range more.failures {
			result.failures[source] = err
		}
	}
	if ctx.Err() == context.Canceled {
		return
	}
	if req.retry {
		// Keep successful-source candidates first. A retry repairs this batch;
		// only Refresh promises to replace it with songs not previously shown.
		var retained []PersonalizedCandidate
		for _, candidate := range req.previous {
			if req.retrySource == "" || candidate.Item.Platform != req.retrySource {
				retained = append(retained, candidate)
			}
		}
		result.candidates = append(retained, result.candidates...)
	}
	batch := selectPersonalized(result.candidates, favorites, history, req.excluded, req.previous, req.settings, req.retry)
	s.mu.Lock()
	if s.closed || req.seq != s.seq {
		s.mu.Unlock()
		return
	}
	if pm != nil && pm.Revision() != req.revision {
		s.loading, s.requested = false, false
		s.mu.Unlock()
		personalizedNotify(redraw)
		return
	}
	s.loading, s.loaded, s.cancel = false, true, nil
	s.needsSeed = len(queries) == 0
	if req.retrySource == "" {
		s.failures = result.failures
	} else {
		delete(s.failures, req.retrySource)
		for source, err := range result.failures {
			s.failures[source] = err
		}
	}
	// An exclusion made during network work wins over the request snapshot.
	batch = filterPersonalizedExclusions(batch, s.excluded)
	switch {
	case len(req.sources) == 0:
		s.errMsg = "启用一个音源，开始发现歌曲"
		if len(s.cache.Candidates) > 0 {
			s.notice = "正在显示上次推荐"
		}
	case len(queries) == 0:
		// NeedsSeed drives the single onboarding prompt in the hero.
	case result.succeeded == 0:
		s.errMsg = "暂时无法获取推荐"
		if len(s.cache.Candidates) > 0 {
			s.notice = "更新失败，正在显示上次推荐"
		}
	case len(batch) == 0:
		s.notice = "暂时没有更多推荐，可更换音源或探索关键词"
	default:
		s.cache = store.RecommendationCache{Date: req.date, Source: req.settings.Source, Sources: append([]string(nil), req.sources...),
			SourceSignature: req.signature, Candidates: batch, Artists: artists}
		s.undo = nil
		if len(result.failures) > 0 {
			s.notice = "部分音源更新失败，已显示可用结果"
		}
		s.markDirtyLocked()
	}
	s.mu.Unlock()
	personalizedNotify(redraw)
}

// Metadata normalization intentionally keeps version words, bracketed suffixes,
// and punctuation. A Live/cover/instrumental/remaster never becomes its base title.
func personalizedNormalize(value string) string {
	return strings.ToLower(strings.Join(strings.Fields(norm.NFKC.String(value)), " "))
}
func personalizedUnknownArtist(value string) bool {
	switch value {
	case "未知歌手", "unknown", "unknown artist", "various artists", "群星":
		return true
	}
	return false
}
func personalizedArtistParts(value string) []string {
	// Do not split a plain ampersand: it may be part of a band's real name.
	replacer := strings.NewReplacer(" feat. ", "、", " feat ", "、", " ft. ", "、", " featuring ", "、", " / ", "、", "/", "、", " & ", "、", ";", "、", "；", "、", "，", "、")
	value = replacer.Replace(value)
	var out []string
	for _, name := range strings.Split(value, "、") {
		if name = strings.TrimSpace(name); name != "" {
			out = append(out, name)
		}
	}
	return out
}
func personalizedArtistMatches(metadata, requested string) bool {
	want := personalizedNormalize(requested)
	if want == "" {
		return false
	}
	if personalizedNormalize(metadata) == want {
		return true
	}
	for _, name := range personalizedArtistParts(metadata) {
		if personalizedNormalize(name) == want {
			return true
		}
	}
	return false
}

func personalizedEquivalent(a, b plugin.MusicItem) bool {
	if a.Key() == b.Key() {
		return true
	}
	if a.Platform == b.Platform {
		return false
	}
	if a.Title == "" || a.Artist == "" || b.Title == "" || b.Artist == "" {
		return false
	}
	if personalizedNormalize(a.Title) != personalizedNormalize(b.Title) || personalizedNormalize(a.Artist) != personalizedNormalize(b.Artist) {
		return false
	}
	// Album and duration are corroborating evidence, not synthesized metadata.
	// Missing album/duration is deliberately too ambiguous for cross-source merging.
	if a.Album == "" || b.Album == "" || personalizedNormalize(a.Album) != personalizedNormalize(b.Album) {
		return false
	}
	if a.Duration <= 0 || b.Duration <= 0 {
		return false
	}
	delta := a.Duration - b.Duration
	return delta >= -2 && delta <= 2 && personalizedVersion(a) == personalizedVersion(b)
}
func personalizedVersion(item plugin.MusicItem) string {
	var parts []string
	for _, key := range []string{"version", "versionName", "subtitle", "subTitle", "songType", "type"} {
		value := item.Extra[key]
		if value == nil {
			value = item.Raw[key]
		}
		if value != nil {
			parts = append(parts, key+"="+personalizedNormalize(fmt.Sprint(value)))
		}
	}
	return strings.Join(parts, "|")
}
func personalizedContains(items []plugin.MusicItem, item plugin.MusicItem) bool {
	for _, existing := range items {
		if personalizedEquivalent(existing, item) {
			return true
		}
	}
	return false
}

func selectPersonalized(pool []PersonalizedCandidate, favorites, history, excluded []plugin.MusicItem, previous []PersonalizedCandidate, settings PersonalizedSettings, retry bool) []PersonalizedCandidate {
	blocked := append(cloneMusicItems(favorites), excluded...)
	if !retry {
		for _, c := range previous {
			blocked = append(blocked, c.Item)
		}
	}
	var familiar, exploration []PersonalizedCandidate
	var seen []plugin.MusicItem
	// Stable weak ordering: recent selections move later, without inferring listens.
	sort.SliceStable(pool, func(i, j int) bool {
		a, b := personalizedContains(history, pool[i].Item), personalizedContains(history, pool[j].Item)
		if a != b {
			return !a
		}
		return personalizedKindRank(pool[i].Kind) < personalizedKindRank(pool[j].Kind)
	})
	for _, c := range pool {
		if c.Item.ID == "" || strings.TrimSpace(c.Item.Title) == "" || personalizedContains(blocked, c.Item) || personalizedContains(seen, c.Item) {
			continue
		}
		seen = append(seen, c.Item)
		if c.Kind == "keyword" {
			exploration = append(exploration, c)
		} else {
			familiar = append(familiar, c)
		}
	}
	// Interleave artist groups before allocating the requested mix.
	familiar = personalizedRoundRobinArtists(familiar)
	exploration = personalizedRoundRobinArtists(exploration)
	target := 6
	switch settings.Mix {
	case "familiar":
		target = 2
	case "fresh":
		target = 12
	}
	if len(settings.Keywords) == 0 {
		target = 0
	}
	counts := make(map[string]int)
	var out []PersonalizedCandidate
	add := func(c PersonalizedCandidate) bool {
		keys := personalizedArtistParts(c.Item.Artist)
		if len(keys) == 0 {
			keys = []string{"未知歌手"}
		}
		for _, artist := range keys {
			if counts[personalizedNormalize(artist)] >= 3 {
				return false
			}
		}
		for _, artist := range keys {
			counts[personalizedNormalize(artist)]++
		}
		out = append(out, c)
		return true
	}
	fi, ei, addedF, addedE := 0, 0, 0, 0
	// Distribute both portions through the list so its first six are representative.
	for len(out) < 20 && (fi < len(familiar) || ei < len(exploration)) {
		chooseE := ei < len(exploration) && (fi >= len(familiar) || addedF >= 20-target || addedE < target && float64(addedE+1)/float64(target+1) <= float64(addedF+1)/float64(21-target))
		if chooseE {
			if add(exploration[ei]) {
				addedE++
			}
			ei++
		} else if fi < len(familiar) {
			if add(familiar[fi]) {
				addedF++
			}
			fi++
		} else {
			break
		}
	}
	return clonePersonalizedCandidates(out)
}
func personalizedKindRank(kind string) int {
	switch kind {
	case "favorite":
		return 0
	case "seed":
		return 1
	case "history":
		return 2
	}
	return 3
}
func personalizedRoundRobinArtists(items []PersonalizedCandidate) []PersonalizedCandidate {
	var order []string
	groups := make(map[string][]PersonalizedCandidate)
	for _, c := range items {
		key := personalizedNormalize(c.Item.Artist)
		if _, exists := groups[key]; !exists {
			order = append(order, key)
		}
		groups[key] = append(groups[key], c)
	}
	out := make([]PersonalizedCandidate, 0, len(items))
	for i := 0; len(out) < len(items); i++ {
		for _, key := range order {
			if i < len(groups[key]) {
				out = append(out, groups[key][i])
			}
		}
	}
	return out
}

func filterPersonalizedExclusions(items []PersonalizedCandidate, excluded []plugin.MusicItem) []PersonalizedCandidate {
	var out []PersonalizedCandidate
	for _, c := range items {
		if !personalizedContains(excluded, c.Item) {
			out = append(out, c)
		}
	}
	return out
}

func (s *PersonalizedViewState) Exclude(item plugin.MusicItem, redraw func()) {
	s.mu.Lock()
	if s.closed || personalizedContains(s.excluded, item) {
		s.mu.Unlock()
		return
	}
	item = item.Clone()
	u := &personalizedUndo{item: item, index: -1, artistIndex: -1}
	for i, c := range s.cache.Candidates {
		if personalizedEquivalent(c.Item, item) {
			copy := c
			u.candidate, u.index = &copy, i
			break
		}
	}
	if s.artist != nil {
		u.artistName = s.artist.Name
		for i, c := range s.artist.Candidates {
			if personalizedEquivalent(c.Item, item) {
				copy := c
				u.artistCandidate, u.artistIndex = &copy, i
				break
			}
		}
		s.artist.Candidates = filterPersonalizedExclusions(s.artist.Candidates, []plugin.MusicItem{item})
	}
	s.excluded = append(s.excluded, item)
	s.exclusionEdits[item.Key()] = &item
	s.cache.Candidates = filterPersonalizedExclusions(s.cache.Candidates, []plugin.MusicItem{item})
	s.undo, s.notice = u, "已暂不推荐这首歌曲，可撤销"
	s.markDirtyLocked()
	s.mu.Unlock()
	personalizedNotify(redraw)
}
func insertPersonalized(items []PersonalizedCandidate, c PersonalizedCandidate, index int) []PersonalizedCandidate {
	index = max(0, min(index, len(items)))
	items = append(items, PersonalizedCandidate{})
	copy(items[index+1:], items[index:])
	items[index] = c
	return items
}
func (s *PersonalizedViewState) UndoExclude(redraw func()) {
	s.mu.Lock()
	if s.closed || s.undo == nil {
		s.mu.Unlock()
		return
	}
	u := s.undo
	s.restoreExcludedLocked(u.item.Key())
	if u.candidate != nil && !personalizedContains(personalizedSongs(s.cache.Candidates), u.item) {
		s.cache.Candidates = insertPersonalized(s.cache.Candidates, *u.candidate, u.index)
	}
	if s.artist != nil && s.artist.Name == u.artistName && u.artistCandidate != nil && !personalizedContains(personalizedSongs(s.artist.Candidates), u.item) {
		s.artist.Candidates = insertPersonalized(s.artist.Candidates, *u.artistCandidate, u.artistIndex)
	}
	s.undo, s.notice = nil, "已恢复推荐"
	s.markDirtyLocked()
	s.mu.Unlock()
	personalizedNotify(redraw)
}
func (s *PersonalizedViewState) restoreExcludedLocked(key plugin.SongKey) {
	for i, item := range s.excluded {
		if item.Key() == key {
			s.excluded = append(s.excluded[:i], s.excluded[i+1:]...)
			break
		}
	}
	s.exclusionEdits[key] = nil
}
func (s *PersonalizedViewState) RestoreExcluded(key plugin.SongKey, redraw func()) {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.restoreExcludedLocked(key)
	s.undo, s.notice = nil, "已恢复，可在后续推荐中出现"
	s.markDirtyLocked()
	s.mu.Unlock()
	personalizedNotify(redraw)
}
func (s *PersonalizedViewState) ClearExcluded(redraw func()) {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.excluded, s.undo, s.clearRestoredExclusions = nil, nil, true
	s.exclusionEdits = make(map[plugin.SongKey]*plugin.MusicItem)
	s.notice = "已清空排除歌曲"
	s.markDirtyLocked()
	s.mu.Unlock()
	personalizedNotify(redraw)
}

func (s *PersonalizedViewState) backLocked() {
	if s.artistCancel != nil {
		s.artistCancel()
		s.artistCancel = nil
	}
	s.artistSeq++
	s.artist = nil
}
func (s *PersonalizedViewState) Back(redraw func()) {
	s.mu.Lock()
	s.backLocked()
	s.mu.Unlock()
	personalizedNotify(redraw)
}

func (s *PersonalizedViewState) OpenArtist(name string, pm *plugin.Manager, st *store.Store, redraw func()) {
	name = strings.TrimSpace(name)
	if name == "" {
		return
	}
	sources, _ := searchablePersonalizedSources(pm)
	revision := uint64(0)
	if pm != nil {
		revision = pm.Revision()
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.backLocked()
	seq := s.artistSeq
	var selected []string
	for _, src := range sources {
		selected = append(selected, src.Name)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	s.artistCancel = cancel
	s.artist = &PersonalizedArtistSnapshot{Name: name, Loading: true}
	s.mu.Unlock()
	personalizedNotify(redraw)
	go func() {
		defer cancel()
		result := searchPersonalized(ctx, pm, selected, []personalizedQuery{{text: name, artist: name, kind: "favorite"}}, 1)
		if ctx.Err() == context.Canceled {
			return
		}
		var favorites []plugin.MusicItem
		if st != nil {
			favorites, _ = st.ListFavorites()
		}
		s.mu.Lock()
		if s.closed || s.artist == nil || seq != s.artistSeq {
			s.mu.Unlock()
			return
		}
		if pm != nil && pm.Revision() != revision {
			s.artist.Loading, s.artist.Error = false, "音源已改变，请重试"
			s.mu.Unlock()
			personalizedNotify(redraw)
			return
		}
		var candidates []PersonalizedCandidate
		var seen []plugin.MusicItem
		for _, c := range result.candidates {
			if personalizedContains(favorites, c.Item) || personalizedContains(s.excluded, c.Item) || personalizedContains(seen, c.Item) {
				continue
			}
			c.Reason = "「" + name + "」的其他作品"
			c.Kind = "artist"
			candidates = append(candidates, c)
			seen = append(seen, c.Item)
			if len(candidates) == 20 {
				break
			}
		}
		s.artist.Candidates, s.artist.Loading, s.artistCancel = candidates, false, nil
		if len(selected) == 0 {
			s.artist.Error = "启用一个音源，开始发现歌曲"
		} else if result.succeeded == 0 {
			s.artist.Error = "暂时无法获取歌手作品，请重试"
		} else if len(candidates) == 0 {
			s.artist.Error = "暂时没有其他作品"
		} else if len(result.failures) > 0 {
			s.artist.Error = "部分音源加载失败，可重试"
		}
		for source, err := range result.failures {
			s.failures[source] = err
		}
		s.mu.Unlock()
		personalizedNotify(redraw)
	}()
}

// Persistence has exactly one worker. It snapshots only after acquiring the state
// mutex, then writes without it; subsequent dirty versions are saved in order.
func (s *PersonalizedViewState) markDirtyLocked() {
	s.dirty++
	select {
	case s.wake <- struct{}{}:
	default:
	}
}
func (s *PersonalizedViewState) persistenceDataLocked() store.RecommendationData {
	data := store.RecommendationData{Version: 1, Settings: s.settings, Excluded: cloneMusicItems(s.excluded), Cache: s.cache}
	data.Settings.Keywords = append([]string(nil), s.settings.Keywords...)
	data.Cache.Sources = append([]string(nil), s.cache.Sources...)
	data.Cache.Artists = append([]store.RecommendationArtist(nil), s.cache.Artists...)
	data.Cache.Candidates = clonePersonalizedCandidates(s.cache.Candidates)
	return data
}
func (s *PersonalizedViewState) persistenceWorker() {
	defer close(s.workerDone)
	data, err := s.disk.Load()
	s.mu.Lock()
	if err != nil {
		s.persistenceError = err.Error()
	} else {
		restored := normalizePersonalizedSettings(data.Settings)
		if !s.settingsTouched {
			s.settings.Mix, s.settings.Keywords = restored.Mix, restored.Keywords
		}
		if !s.sourceTouched {
			s.settings.Source = restored.Source
		}
		if !s.seedTouched {
			s.settings.SeedArtist = restored.SeedArtist
		}
		if !s.clearRestoredExclusions {
			for _, item := range data.Excluded {
				if _, edited := s.exclusionEdits[item.Key()]; !edited && !personalizedContains(s.excluded, item) {
					s.excluded = append(s.excluded, item.Clone())
				}
			}
		}
		if s.cache.Date == "" {
			s.cache = data.Cache
			if len(s.cache.Candidates) > 20 {
				s.cache.Candidates = s.cache.Candidates[:20]
			}
			s.cache.Candidates = filterPersonalizedExclusions(s.cache.Candidates, s.excluded)
			s.loaded = len(s.cache.Candidates) > 0
		}
	}
	s.exclusionEdits = make(map[plugin.SongKey]*plugin.MusicItem)
	s.restoring = false
	pm, st, redraw, bound, closed := s.pm, s.st, s.redraw, s.bound, s.closed
	s.mu.Unlock()
	personalizedNotify(redraw)
	if bound && !closed {
		s.Ensure(pm, st, redraw)
	}
	for {
		select {
		case <-s.wake:
		case <-s.done:
		}
		for {
			s.mu.RLock()
			version, saved, closed := s.dirty, s.saved, s.closed
			if version == saved {
				s.mu.RUnlock()
				if closed {
					return
				}
				break
			}
			data := s.persistenceDataLocked()
			s.mu.RUnlock()
			err := s.disk.Save(data)
			s.mu.Lock()
			s.saved = version
			if err != nil {
				s.persistenceError = "推荐设置保存失败: " + err.Error()
			} else {
				s.persistenceError = ""
			}
			redraw := s.redraw
			s.mu.Unlock()
			personalizedNotify(redraw)
		}
	}
}

// Close cancels requests immediately and lets the single worker flush pending
// writes in the background. It never waits for disk or a plugin on the UI thread.
func (s *PersonalizedViewState) Close() {
	s.mu.Lock()
	if !s.closed {
		s.closed = true
		s.seq++
		if s.cancel != nil {
			s.cancel()
		}
		s.backLocked()
		close(s.done)
	}
	s.mu.Unlock()
}

// Wait waits for Close's final persistence write. Only shutdown/background code
// should call it; render and input handlers must use the nonblocking methods.
func (s *PersonalizedViewState) Wait(ctx context.Context) error {
	select {
	case <-s.workerDone:
		s.mu.RLock()
		defer s.mu.RUnlock()
		if s.persistenceError != "" {
			return fmt.Errorf("%s", s.persistenceError)
		}
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
