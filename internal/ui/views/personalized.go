package views

import (
	"fmt"
	"strings"

	"github.com/egoist/mygo/ui"
	"musicgo/internal/download"
	"musicgo/internal/player"
	"musicgo/internal/plugin"
	"musicgo/internal/store"
	"musicgo/internal/ui/components"
	"musicgo/internal/ui/icons"
	"musicgo/internal/ui/theme"
)

// PersonalizedPageState keeps input and overlay state on the UI thread. Data
// arriving from plugins belongs to the locked PersonalizedViewState.
type PersonalizedPageState struct {
	Data            *PersonalizedViewState
	all             bool
	settingsOpen    bool
	manageExcluded  bool
	draft           PersonalizedSettings
	keywords        string
	seed            string
	settingsError   string
	actionNotice    string
	dismissedNotice string
	homeScroll      ui.ScrollState
	allScroll       ui.ScrollState
	artistScroll    ui.ScrollState
	artistScrollFor string
}

func NewPersonalizedPageState(cm *store.ConfigManager) *PersonalizedPageState {
	return &PersonalizedPageState{Data: NewPersonalizedViewState(cm)}
}

func (page *PersonalizedPageState) CanGoBack() bool {
	page.Data.mu.RLock()
	defer page.Data.mu.RUnlock()
	return page.all || page.Data.artist != nil
}

func (page *PersonalizedPageState) Back(redraw func()) {
	page.all = false
	page.Data.Back(redraw)
}

func RenderPersonalized(c *ui.Context, th *theme.Theme, page *PersonalizedPageState, eng *player.Engine, pm *plugin.Manager, st *store.Store, dl *download.Manager, getImage func(string) *ui.Bitmap, redraw func(), navigate func(string)) {
	snap := page.Data.Snapshot()
	_, height := c.Size()
	compact := components.PageWidth(c) < 836
	padding, gap, heroHeight, titleSize := components.PagePadding(c), float32(16), float32(208), float32(34)
	if height < 800 {
		heroHeight = 160
		gap = 12
	}
	if compact {
		gap = 12
		heroHeight = 152
		titleSize = 28
	}
	viewKey := "home"
	scroll := &page.homeScroll
	if snap.Artist != nil {
		viewKey = "artist_" + snap.Artist.Name
		scroll = &page.artistScroll
		if page.artistScrollFor != snap.Artist.Name {
			page.artistScrollFor = snap.Artist.Name
			page.artistScroll = ui.ScrollState{}
		}
	} else if page.all {
		viewKey = "all"
		scroll = &page.allScroll
	}
	ui.Scroll(c.Key("personalized_scroll_"+viewKey)).TrackScroll(scroll).Grow(1).MinHeight(0).FillWidth().Padding(24, padding, 32, padding).Children(func() {
		ui.Column(c).FillWidth().Gap(gap).Children(func() {
			ui.Row(c).FillWidth().Height(36).AlignItems(ui.Center).Gap(12).Children(func() {
				ui.Spacer(c)
				sourceLabel := "全部音源"
				if snap.Source != "" {
					sourceLabel = shortPlatformName(snap.Source)
				}
				source := personalTextButton(c.Key("personal_source"), th, sourceLabel)
				source.Menu(func(m *ui.Menu) {
					if m.Item("全部音源").Checked(snap.Source == "").Chosen() {
						page.Data.SetSource("", pm, st, redraw)
					}
					for _, entry := range snap.Sources {
						if m.Item(entry.Name).Checked(snap.Source == entry.Name).Chosen() {
							page.Data.SetSource(entry.Name, pm, st, redraw)
						}
					}
				})
				settings := personalTextButton(c.Key("personal_settings"), th, "推荐设置")
				if settings.Clicked() {
					page.draft = snap.Settings
					page.draft.Keywords = append([]string(nil), snap.Settings.Keywords...)
					page.keywords = strings.Join(snap.Settings.Keywords, "、")
					page.settingsError = ""
					page.manageExcluded = false
					page.settingsOpen = true
				}
			})
			if snap.Artist != nil || page.all {
				page.renderFullList(c, th, snap, eng, pm, st, dl, getImage, redraw)
				return
			}
			stage := theme.Stage()
			heroBg, heroText, heroMuted := stage.Background, stage.TextMain, stage.TextSub
			needsSeed := snap.NeedsSeed && len(snap.Settings.Keywords) == 0 && !snap.Loading && !snap.Restoring && !snap.NoSources
			ui.Row(c).Label("今日推荐").FillWidth().Height(heroHeight).Shrink(0).Clip().Radius(16).Background(heroBg).Padding(0, 28).Gap(18).AlignItems(ui.Center).Children(func() {
				ui.Column(c).Grow(1).MinWidth(0).Gap(10).Children(func() {
					title, subtitle := "今天，听点喜欢的", personalSubtitle(snap)
					if needsSeed {
						title = "先选一位喜欢的歌手"
						subtitle = "从一位歌手开始，发现更多作品"
					}
					ui.Text(c, title).FontSize(titleSize).Bold().TextColor(heroText).SingleLine().Ellipsis("…")
					ui.Text(c, subtitle).FontSize(13).TextColor(heroMuted).SingleLine().Ellipsis("…")
					ui.Row(c).FillWidth().Margin(6, 0, 0, 0).Gap(10).AlignItems(ui.Center).Children(func() {
						if needsSeed {
							input := ui.TextInputBase(c.Key("personal_seed"), &page.seed).Label("喜欢的歌手").Placeholder("输入歌手名").Grow(1).MaxWidth(280).MinWidth(80).Height(40).Padding(6, 12).Radius(10).Background(heroText).TextColor(heroBg).FontSize(13)
							submit := components.HeroButton(c.Key("personal_generate"), stage, "生成推荐", true).Disabled(strings.TrimSpace(page.seed) == "")
							if (submit.Clicked() || input.Submitted()) && strings.TrimSpace(page.seed) != "" {
								page.Data.Generate(page.seed, pm, st, redraw)
							}
						} else {
							play := components.HeroButton(c.Key("personal_play"), stage, "播放推荐", true).Disabled(len(snap.Songs) == 0 || eng == nil)
							if play.Clicked() && eng != nil && len(snap.Songs) > 0 {
								eng.SetQueue(snap.Songs, 0)
							}
							label := "换一批"
							if snap.Loading || snap.Restoring {
								label = "正在更新"
							}
							refresh := components.HeroButton(c.Key("personal_refresh"), stage, label, false).Disabled(snap.Loading || snap.Restoring || snap.NoSources)
							if refresh.Clicked() {
								page.actionNotice = ""
								page.dismissedNotice = ""
								page.Data.Refresh(pm, st, redraw)
							}
						}
					})
				})
				sleeveWidth, sleeveSize := float32(270), float32(122)
				if heroHeight < 190 {
					sleeveSize = 100
				}
				if compact {
					sleeveWidth = 160
					sleeveSize = 82
				}
				// On the smallest window the seed input gets the space it needs.
				if !needsSeed || !compact {
					renderPersonalSleeves(c, th, snap.Batch, sleeveWidth, sleeveSize, getImage)
				}
			})
			page.renderStatus(c, th, snap, pm, st, redraw)
			if snap.NoSources {
				ui.Column(c).FillWidth().Gap(14).Padding(12, 0).Children(func() {
					ui.Text(c, "启用一个音源，开始发现歌曲").FontSize(18).Bold().TextColor(th.TextMain)
					if components.Button(c.Key("personal_plugins"), "管理插件").Clicked() && navigate != nil {
						navigate("plugins")
					}
				})
				if len(snap.Candidates) == 0 {
					return
				}
			}
			if needsSeed {
				ui.Column(c).FillWidth().Gap(14).Children(func() {
					ui.Text(c, "也可以从热门歌曲开始").FontSize(18).Bold().TextColor(th.TextMain)
					if components.Button(c.Key("personal_discover"), "浏览热门歌曲").Clicked() && navigate != nil {
						navigate("recommend")
					}
				})
				return
			}
			if len(snap.Candidates) == 0 {
				if snap.Loading || snap.Restoring {
					renderSongSkeleton(c, th)
				} else if snap.Error == "" {
					components.EmptyState(c, th, icons.Note, "暂时没有更多推荐，试试其他歌手或探索关键词")
				}
				return
			}
			ui.Column(c).FillWidth().Gap(12).Children(func() {
				ui.Row(c).FillWidth().AlignItems(ui.Center).Children(func() {
					ui.Text(c, "为你挑选").FontSize(18).Bold().TextColor(th.TextMain)
					ui.Spacer(c)
					if personalTextButton(c.Key("personal_all"), th, "查看全部").Clicked() {
						page.all = true
					}
				})
				count := min(6, len(snap.Candidates))
				if components.PageContentWidth(c) < 800 {
					ui.Column(c).FillWidth().Gap(4).Children(func() {
						for i := 0; i < count; i++ {
							page.renderSong(c.Key(fmt.Sprintf("personal_preview_%d", i)), th, snap.Candidates[i], snap.Songs, i, eng, st, dl, getImage, redraw)
						}
					})
				} else {
					ui.Row(c).FillWidth().Gap(24).Children(func() {
						for col := 0; col < 2; col++ {
							col := col
							ui.Column(c).Basis(0).Grow(1).MinWidth(0).Gap(4).Children(func() {
								for i := col * 3; i < min(col*3+3, count); i++ {
									page.renderSong(c.Key(fmt.Sprintf("personal_preview_%d", i)), th, snap.Candidates[i], snap.Songs, i, eng, st, dl, getImage, redraw)
								}
							})
						}
					})
				}
			})
			if len(snap.Artists) > 0 {
				ui.Column(c).FillWidth().Gap(14).Children(func() {
					ui.Text(c, "从喜欢的歌手听起").FontSize(18).Bold().TextColor(th.TextMain)
					ui.Grid(c).FillWidth().Columns(max(1, min(3, int((components.PageContentWidth(c)+12)/204)))).Gap(12).Children(func() {
						for i, artist := range snap.Artists {
							if i >= 3 {
								break
							}
							artist := artist
							b := ui.ButtonBase(c.Key("personal_artist_"+artist.Name)).Label("歌手："+artist.Name).Basis(0).Grow(1).MinWidth(0).Height(68).Radius(10).Background(th.CardBg).Padding(10, 12).Cursor(ui.CursorPointer)
							if b.Hovered() {
								b.Background(th.HoverBg)
							}
							b.Children(func() {
								ui.Row(c).FillWidth().Gap(10).AlignItems(ui.Center).Children(func() {
									ui.Box(c).Size(34, 34).Shrink(0).Radius(17).Background(th.ActiveBg).Center().Children(func() {
										name := []rune(artist.Name)
										if len(name) > 0 {
											ui.Text(c, string(name[0])).FontSize(15).TextColor(th.TextSub)
										}
									})
									ui.Column(c).Grow(1).MinWidth(0).Gap(3).Children(func() {
										ui.Text(c, artist.Name).FontSize(14).Bold().TextColor(th.TextMain).SingleLine().Ellipsis("…")
										if !compact {
											ui.Text(c, "听听其他作品").FontSize(11).TextColor(th.TextSub)
										}
									})
									ui.Icon(c, icons.ChevronRight).Size(12, 12).Shrink(0).TextColor(th.TextSub)
								})
							})
							if b.Clicked() {
								page.Data.OpenArtist(artist.Name, pm, st, redraw)
							}
						}
					})
				})
			}
		})
	})
	page.renderSettings(c, th, snap, redraw)
}

func personalSubtitle(s PersonalizedSnapshot) string {
	if s.Restoring {
		return "正在恢复推荐"
	}
	if s.NoSources {
		return "启用音源后生成推荐"
	}
	if len(s.Batch) == 0 {
		if s.Loading {
			return "正在为你挑选歌曲"
		}
		return "从喜欢的歌手，找到下一首"
	}
	prefix := "根据你收藏的歌曲"
	hasFavorite, hasHistory, hasSeed := false, false, false
	for _, entry := range s.Batch {
		hasFavorite = hasFavorite || entry.Kind == "favorite"
		hasHistory = hasHistory || entry.Kind == "history"
		hasSeed = hasSeed || entry.Kind == "seed"
	}
	if !hasFavorite {
		if hasSeed {
			prefix = "从你选择的歌手出发"
		} else if hasHistory {
			prefix = "从你最近选择的歌曲出发"
		} else {
			prefix = "根据你选择的探索关键词"
		}
	}
	return fmt.Sprintf("%s，挑选 %d 首", prefix, len(s.Batch))
}

func personalTextButton(c *ui.Context, th *theme.Theme, label string) ui.Element {
	b := ui.ButtonBase(c).Label(label).Height(36).MaxWidth(180).Padding(0, 8).Radius(10).Center().Cursor(ui.CursorPointer)
	color := th.TextSub
	if b.Hovered() || b.FocusVisible() {
		color = th.TextMain
		b.Background(th.HoverBg)
	}
	b.Children(func() { ui.Text(c, label).FontSize(12).TextColor(color).SingleLine().Ellipsis("…") })
	return b
}

func renderPersonalSleeves(c *ui.Context, th *theme.Theme, items []PersonalizedCandidate, width, size float32, getImage func(string) *ui.Bitmap) {
	ui.Box(c).Label("推荐封套").Width(width).Height(size + 16).Shrink(0).Children(func() {
		for i := 2; i >= 0; i-- {
			idx := i
			x := float32(i) * (width - size - 8) / 2
			ui.Box(c.Key(fmt.Sprintf("personal_sleeve_%d", i))).Absolute().Left(x).Top(float32(i)*6).Size(size, size).Radius(5).Clip().Border(2, theme.Stage().Background).Background(th.ActiveBg).Children(func() {
				if idx < len(items) && getImage != nil && items[idx].Item.Artwork != "" {
					if bm := getImage(items[idx].Item.Artwork); bm != nil {
						ui.Image(c, bm).Fill().Fit(ui.Cover)
						return
					}
				}
				// A quiet native sleeve placeholder; the real artwork replaces it on load.
				colors := []ui.Color{ui.Hex("#607C86"), ui.Hex("#617360"), ui.Hex("#7B695C")}
				ui.Box(c).Fill().Background(colors[idx].Mix(ui.Hex("#FFFFFF"), 0.34)).Draw(func(p *ui.Painter, r ui.Rect) {
					p.Fill(ui.Rect{X: r.X + r.W*.67, Y: r.Y + r.H*.14, W: r.W * .18, H: r.H * .18}, ui.Hex("#DEDCCD"), r.W*.09)
					p.Fill(ui.Rect{X: r.X, Y: r.Y + r.H*.66, W: r.W, H: r.H * .34}, colors[idx], 0)
					for j := float32(0); j < 3; j++ {
						p.Fill(ui.Rect{X: r.X + r.W*.1, Y: r.Y + r.H*(.83+j*.035), W: r.W * .29, H: 0.7}, ui.Hex("#D5D9D4"), 0)
					}
				})
			})
		}
	})
}

func (page *PersonalizedPageState) renderStatus(c *ui.Context, th *theme.Theme, s PersonalizedSnapshot, pm *plugin.Manager, st *store.Store, redraw func()) {
	messages := []string{s.Error, s.Notice, s.PersistenceError, page.actionNotice}
	seen := make(map[string]bool)
	for _, message := range messages {
		if message == "" || seen[message] || message == s.Notice && (s.CanUndo || page.actionNotice != "" || message == page.dismissedNotice) || s.NoSources && message == s.Error {
			continue
		}
		seen[message] = true
		if message != "" {
			ui.Row(c).FillWidth().Gap(12).AlignItems(ui.Center).Children(func() {
				color := th.TextSub
				if message == s.Error || message == s.PersistenceError {
					color = th.Danger
				}
				ui.Text(c, message).Grow(1).MinWidth(0).FontSize(12).TextColor(color)
				if message == s.Error && !s.Loading {
					if personalTextButton(c.Key("personal_retry"), th, "重试").Clicked() {
						page.Data.Retry(pm, st, redraw)
					}
				}
				if message == page.actionNotice {
					if personalTextButton(c.Key("personal_dismiss"), th, "关闭提示").Clicked() {
						page.dismissedNotice = s.Notice
						page.actionNotice = ""
					}
				}
			})
		}
	}
	if s.CanUndo {
		ui.Row(c).FillWidth().Gap(12).AlignItems(ui.Center).Children(func() {
			ui.Text(c, "已暂不推荐这首歌曲").FontSize(12).TextColor(th.TextSub)
			if personalTextButton(c.Key("personal_undo"), th, "撤销").Clicked() {
				page.Data.UndoExclude(redraw)
			}
		})
	}
	failed := false
	for _, src := range s.Sources {
		failed = failed || src.Failed
	}
	if failed {
		b := personalTextButton(c.Key("personal_source_failures"), th, "部分音源未更新")
		b.Menu(func(m *ui.Menu) {
			for _, src := range s.Sources {
				if src.Failed && m.Item("重试 "+src.Name).Chosen() {
					page.Data.RetrySource(src.Name, pm, st, redraw)
				}
			}
		})
	}
}

func (page *PersonalizedPageState) renderSong(c *ui.Context, th *theme.Theme, candidate PersonalizedCandidate, songs []plugin.MusicItem, index int, eng *player.Engine, st *store.Store, dl *download.Manager, getImage func(string) *ui.Bitmap, redraw func()) {
	item := candidate.Item
	var current *plugin.MusicItem
	var play func()
	if eng != nil {
		current = eng.CurrentSong()
		play = func() { eng.SetQueue(songs, index) }
	}
	preview := !page.CanGoBack()
	width := components.PageContentWidth(c)
	if preview && width >= 800 {
		width = (width - 24) / 2
	}
	options := songRowOptions{Index: index, Current: current != nil && current.Key() == item.Key(), Preview: preview, Reason: candidate.Reason, Width: width, Play: play}
	renderMusicRow(c, th, item, options, eng, st, dl, getImage, redraw, func(message string) { page.actionNotice = message }, func(m *ui.Menu) {
		if m.Item("暂不推荐").Chosen() {
			page.Data.Exclude(item, redraw)
		}
	})
}

func (page *PersonalizedPageState) renderFullList(c *ui.Context, th *theme.Theme, s PersonalizedSnapshot, eng *player.Engine, pm *plugin.Manager, st *store.Store, dl *download.Manager, getImage func(string) *ui.Bitmap, redraw func()) {
	ui.Row(c).FillWidth().Gap(12).AlignItems(ui.Center).Children(func() {
		if components.BackButton(c.Key("personal_back"), th, "返回推荐").Clicked() {
			page.Back(redraw)
		}
		title := "本批推荐"
		if s.Artist != nil {
			title = s.Artist.Name + "的其他作品"
		}
		components.PageTitle(c, th, title).Grow(1)
		if components.Button(c.Key("personal_full_play"), "播放全部").Disabled(len(s.Songs) == 0 || eng == nil).Clicked() && eng != nil {
			eng.SetQueue(s.Songs, 0)
		}
	})
	page.renderStatus(c, th, s, pm, st, redraw)
	if s.Artist != nil {
		if s.Artist.Loading {
			ui.Text(c, "正在查找其他作品").FontSize(13).TextColor(th.TextSub)
		}
		if s.Artist.Error != "" {
			ui.Text(c, s.Artist.Error).FontSize(13).TextColor(th.Danger)
			if components.Button(c.Key("personal_artist_retry"), "重试").Clicked() {
				page.Data.OpenArtist(s.Artist.Name, pm, st, redraw)
			}
		}
	}
	if len(s.Candidates) == 0 {
		components.EmptyState(c, th, icons.Note, "暂无可展示的歌曲")
	}
	ui.Column(c).FillWidth().Gap(4).Children(func() {
		for i, candidate := range s.Candidates {
			page.renderSong(c.Key(fmt.Sprintf("personal_full_%d", i)), th, candidate, s.Songs, i, eng, st, dl, getImage, redraw)
		}
	})
}

func (page *PersonalizedPageState) renderSettings(c *ui.Context, th *theme.Theme, s PersonalizedSnapshot, redraw func()) {
	width, height := c.Size()
	ui.DialogBase(c.Key("personal_settings_dialog"), &page.settingsOpen, func(backdrop, panel ui.Element) {
		backdrop.Background(ui.RGBA(0, 0, 0, .38))
		panel.Label("推荐设置面板").Width(min(float32(520), width-40)).MaxHeight(height - 40).Background(th.CardBg).Radius(16).Padding(24)
		ui.Column(c).FillWidth().Gap(14).Children(func() {
			ui.Row(c).FillWidth().AlignItems(ui.Center).Children(func() {
				ui.Text(c, "推荐设置").FontSize(20).Bold().TextColor(th.TextMain)
				ui.Spacer(c)
				if personalTextButton(c.Key("personal_settings_close"), th, "关闭").Clicked() {
					page.settingsOpen = false
				}
			})
			if page.manageExcluded {
				if personalTextButton(c.Key("personal_excluded_back"), th, "返回设置").Clicked() {
					page.manageExcluded = false
				}
				if len(s.Excluded) == 0 {
					ui.Text(c, "没有排除的歌曲").FontSize(13).TextColor(th.TextSub)
				}
				ui.Scroll(c).FillWidth().Height(min(float32(260), height-250)).Children(func() {
					ui.Column(c).FillWidth().Gap(8).Children(func() {
						for i, item := range s.Excluded {
							item := item
							ui.Row(c.Key(fmt.Sprintf("personal_excluded_%d", i))).FillWidth().Gap(10).AlignItems(ui.Center).Children(func() {
								ui.Text(c, item.Title+" / "+item.Artist).Grow(1).MinWidth(0).SingleLine().Ellipsis("…").FontSize(13).TextColor(th.TextMain)
								if components.Button(c, "恢复").Clicked() {
									page.Data.RestoreExcluded(item.Key(), redraw)
								}
							})
						}
					})
				})
				if components.Button(c.Key("personal_clear_excluded"), "恢复全部歌曲").Disabled(len(s.Excluded) == 0).Clicked() {
					page.Data.ClearExcluded(redraw)
				}
				return
			}
			ui.Text(c, "发现偏好").FontSize(13).Bold().TextColor(th.TextMain)
			ui.Row(c).FillWidth().Gap(8).Children(func() {
				for _, option := range []struct{ value, label string }{{"familiar", "熟悉一些"}, {"balanced", "均衡"}, {"fresh", "新鲜一些"}} {
					o := option
					b := ui.ButtonBase(c.Key("personal_mix_"+o.value)).Label(o.label).Grow(1).Basis(0).Height(36).Radius(9).Center().Border(1, th.Border).Cursor(ui.CursorPointer)
					if page.draft.Mix == o.value {
						b.Background(th.ActiveBg)
					} else if b.Hovered() {
						b.Background(th.HoverBg)
					}
					b.Children(func() { ui.Text(c, o.label).FontSize(13).TextColor(th.TextMain) })
					if b.Clicked() {
						page.draft.Mix = o.value
					}
				}
			})
			ui.Text(c, "探索关键词").FontSize(13).Bold().TextColor(th.TextMain)
			ui.TextInput(c.Key("personal_keywords"), &page.keywords).Label("探索关键词").Placeholder("例如：民谣、摇滚、纯音乐").FillWidth().Height(38)
			ui.Row(c).Wrap().Gap(8).Children(func() {
				for _, keyword := range []string{"民谣", "摇滚", "纯音乐", "爵士", "电子"} {
					word := keyword
					if personalTextButton(c.Key("personal_keyword_"+word), th, word).Clicked() {
						words := personalKeywords(page.keywords)
						found := false
						for _, v := range words {
							found = found || v == word
						}
						if !found {
							words = append(words, word)
							page.keywords = strings.Join(words, "、")
						}
					}
				}
			})
			ui.Text(c, "关键词会交给所选音源搜索；留空时从喜欢的歌手继续发现。").FontSize(12).TextColor(th.TextSub)
			if page.settingsError != "" {
				ui.Text(c, page.settingsError).FontSize(12).TextColor(th.Danger)
			}
			ui.Row(c).FillWidth().AlignItems(ui.Center).Children(func() {
				if personalTextButton(c.Key("personal_manage_excluded"), th, fmt.Sprintf("管理排除歌曲（%d）", len(s.Excluded))).Clicked() {
					page.manageExcluded = true
				}
				ui.Spacer(c)
				if components.PrimaryButton(c.Key("personal_settings_save"), "保存").Clicked() {
					page.draft.Keywords = personalKeywords(page.keywords)
					if err := page.Data.UpdateSettings(page.draft, redraw); err != nil {
						page.settingsError = err.Error()
					} else {
						page.settingsOpen = false
						page.actionNotice = "推荐偏好已保存，下次换批时生效"
					}
				}
			})
		})
	})
}

func personalKeywords(value string) []string {
	parts := strings.FieldsFunc(value, func(r rune) bool { return r == '、' || r == ',' || r == '，' || r == ';' || r == '；' || r == '\n' })
	var result []string
	seen := map[string]bool{}
	for _, part := range parts {
		word := strings.TrimSpace(part)
		if word != "" && !seen[word] {
			seen[word] = true
			result = append(result, word)
		}
	}
	return result
}
