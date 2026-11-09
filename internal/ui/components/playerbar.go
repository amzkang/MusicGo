package components

import (
	"fmt"
	"time"

	"github.com/egoist/mygo/ui"
	"musicgo/internal/player"
	"musicgo/internal/plugin"
	"musicgo/internal/store"
	"musicgo/internal/ui/anim"
	"musicgo/internal/ui/icons"
	"musicgo/internal/ui/theme"
)

// artworkKey names the artwork of a song, so that the cover of the player
// bar and the one of the lyric page are the same picture, moving from one
// place to the other.
func artworkKey(song *plugin.MusicItem) string {
	if song == nil {
		return ""
	}
	return string(song.Key())
}

// PlayerBarState holds persistent state for player controls
type PlayerBarState struct {
	Volume       float64
	VolumePushed float64
	LastVolume   float64
	DragProgress float64
	SeekRatio    float64
	SeekPending  bool
	SeekDragging bool
	SeekRevision uint64
	ShowLyrics   bool
	// ArtworkHidden reserves the cover's layout while the lyric cover is
	// travelling above the page, so only one artwork is visible.
	ArtworkHidden        bool
	DesktopLyricsVisible bool
	Queue                QueueDrawerState
}

// Position is the shared cursor for the time label and immersive lyrics. While
// dragging it previews the target; the decoder is moved only when released.
func (s *PlayerBarState) Position(eng *player.Engine) float64 {
	if s.SeekPending {
		return s.SeekRatio * eng.Duration()
	}
	return eng.Position()
}

// RenderPlayerBar renders the native playback controls.
func RenderPlayerBar(
	c *ui.Context,
	th *theme.Theme,
	eng *player.Engine,
	state *PlayerBarState,
	getImage func(string) *ui.Bitmap,
	configMgr *store.ConfigManager,
	st *store.Store,
	redraw func(),
	onToggleDetail func(),
	onToggleDesktopLyric func(),
) {
	t := c.Theme()
	curSong := eng.CurrentSong()
	isPlaying := eng.IsPlaying()
	pos := eng.Position()
	dur := eng.Duration()
	if isPlaying {
		c.After(time.Second)
	}

	// The volume follows the engine, and the engine follows the slider.
	if state.Volume == state.VolumePushed {
		state.Volume = eng.Volume()
		state.VolumePushed = state.Volume
	}

	barSurface, barHeight := PlaybarSurface(th), float32(84)
	if state.ShowLyrics {
		barSurface = ui.Transparent
		barHeight = 96
	}
	bar := ui.Column(c).
		Label("pb_bar").
		FillWidth().
		Height(barHeight).Shrink(0).Background(barSurface).Border(0, th.Border)
	bar.Children(func() {
		// 1. Top Edge: Interactive Seek Progress Track
		seekRatio := state.SeekRatio
		if !state.SeekPending && dur > 0 {
			seekRatio = pos / dur
			if seekRatio > 1 {
				seekRatio = 1
			}
			state.SeekRatio = seekRatio
		}

		seekSlider := ui.SliderBase(c.Key("pb_seek_slider"), &seekRatio, 0, 1).
			Label("播放进度").
			FillWidth().
			Height(10).
			Padding(0, 0, 0, 0).
			Cursor(ui.CursorPointer).
			HandleInput(func(ev ui.InputEvent) bool {
				if ev.Button == 0 {
					switch ev.Kind {
					case ui.InputPointerDown:
						state.SeekDragging = true
					case ui.InputPointerUp:
						state.SeekDragging = false
					}
				}
				return false
			})

		seekSlider.Draw(func(p *ui.Painter, r ui.Rect) {
			ratio := state.SeekRatio
			if !state.SeekPending && dur > 0 {
				ratio = max(0, min(1, eng.Position()/dur))
			}
			if eng.IsPlaying() && !state.SeekDragging {
				p.AnimationFrameRate(30)
			}
			// The track thickens while it is dragged, so the hold reads.
			trackH := float32(2)
			if state.SeekDragging {
				trackH = 4
			}
			y := r.Y + (r.H-trackH)/2
			p.Fill(ui.Rect{X: r.X, Y: y, W: r.W, H: trackH}, th.Border, trackH/2)

			filled := r.W * float32(ratio)
			if filled > 0 {
				p.Fill(ui.Rect{X: r.X, Y: y, W: filled, H: trackH}, th.ProgressColor(), trackH/2)
			}
		})
		if x, _, over := seekSlider.PointerPosition(); over && dur > 0 {
			if width := seekSlider.Bounds().W; width > 0 {
				seconds := int(max(0, min(1, float64(x/width))) * dur)
				seekSlider.Tooltip(fmt.Sprintf("%02d:%02d", seconds/60, seconds%60))
			}
		}

		if dur > 0 {
			if seekSlider.Changed() {
				state.SeekRatio = seekRatio
				state.SeekPending = true
			}
			if state.SeekPending && !state.SeekDragging {
				state.SeekPending = false
				state.SeekRevision++
				state.DragProgress = seekRatio * dur
				eng.Seek(state.DragProgress)
				pos = state.DragProgress
			}
		}

		// 2. Main Player Bar Body
		ui.Row(c).
			Fill().
			Padding(0, 20).
			AlignItems(ui.Center).
			Children(func() {
				// Left Part: Track Information (width ~280px)
				left := ui.Row(c.Key("pb_left_info")).Basis(0).Grow(1).MinWidth(140).MaxWidth(280).AlignItems(ui.Center).Gap(12)
				if state.ShowLyrics {
					left.Basis(200).Grow(0).MinWidth(200).MaxWidth(200)
				}
				left.Children(func() {
					if state.ShowLyrics {
						shown := pos
						if state.SeekPending {
							shown = state.SeekRatio * dur
						}
						ui.Text(c, fmt.Sprintf("%02d:%02d / %02d:%02d", int(shown)/60, int(shown)%60, int(dur)/60, int(dur)%60)).FontSize(12).TextColor(th.TextSub)
						if eng.LastError() != "" {
							ui.Text(c, eng.LastError()).FontSize(11).TextColor(th.TextSub).SingleLine().Ellipsis("…")
						}
						return
					}
					// Keep the travelling artwork above the sliding lyric page,
					// including while it shrinks back into this placeholder.
					anchor := ui.Box(c.Key("pb_cover_slot")).Size(44, 44)
					if !state.ArtworkHidden {
						ui.Overlay(c, func() {
							coverBox := ui.ButtonBase(c.Key("pb_cover")).
								AttachTo(anchor, ui.AnchorCenter, ui.AnchorCenter).
								Label("歌曲封面").Transition(anim.Artwork(artworkKey(curSong))).
								Size(44, 44).
								Background(th.PanelBg).
								Radius(8).
								Center().
								Cursor(ui.CursorPointer)

							if coverBox.Hovered() {
								coverBox.Background(th.HoverBg)
							}
							// It grows a little under the pointer, so that the
							// click that opens the lyric page is invited.
							scale := float32(1)
							if coverBox.Hovered() {
								scale = 1.06
							}
							coverBox.AnimatePose(ui.Pose{Scale: scale}, anim.Hover)

							coverBox.Children(func() {
								if curSong != nil && curSong.Artwork != "" && getImage != nil {
									if bm := getImage(curSong.Artwork); bm != nil {
										// The picture fills the cover, so that it
										// grows with it on the way to the page.
										ui.Image(c, bm).Fill().Radius(8).Fit(ui.Cover)
										return
									}
								}
								ui.Icon(c, icons.Album).Size(22, 22).TextColor(th.TextSub)
							})
							if coverBox.Clicked() && onToggleDetail != nil {
								onToggleDetail()
							}
						})
					}

					// Text Info Column
					ui.Column(c).Grow(1).Gap(3).Children(func() {
						if curSong != nil && curSong.Title != "" {
							title := curSong.Title
							if len([]rune(title)) > 14 {
								title = string([]rune(title)[:13]) + "..."
							}
							ui.Row(c).AlignItems(ui.Center).Gap(6).Children(func() {
								ui.Text(c, title).
									FontSize(13).
									Bold().
									TextColor(th.TextMain).
									SingleLine().
									ContentTransition(ui.ContentTransition{})
							})

							artist := curSong.Artist
							if artist == "" {
								artist = "未知歌手"
							}
							if len([]rune(artist)) > 8 {
								artist = string([]rune(artist)[:7]) + "..."
							}

							shown := pos
							if state.SeekPending {
								shown = float64(state.SeekRatio) * dur
							}
							timeStr := fmt.Sprintf("%02d:%02d/%02d:%02d",
								int(shown)/60, int(shown)%60,
								int(dur)/60, int(dur)%60)

							ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
								if eng.LastError() != "" {
									errMsg := eng.LastError()
									if len([]rune(errMsg)) > 16 {
										errMsg = string([]rune(errMsg)[:15]) + "..."
									}
									ui.Text(c, errMsg).FontSize(11).TextColor(th.Danger)
								} else if eng.State() == player.StateLoading {
									ui.Text(c, "正在缓冲音源...").FontSize(11).TextColor(th.TextSub)
								} else {
									ui.Text(c, artist).FontSize(11).TextColor(th.TextSub)
									ui.Text(c, timeStr).FontSize(11).TextColor(th.TextMuted)
								}
							})
						} else {
							ui.Text(c, "暂无播放歌曲").FontSize(13).Bold().TextColor(th.TextMain)
							ui.Text(c, "双击歌曲开始播放").FontSize(11).TextColor(th.TextMuted)
						}
					})
				})

				renderMode := func() {
					// Playback Mode Button (Requirement 2: 支持切换单曲循环/随机播放/列表循环)
					modeIcon := icons.Repeat
					modeTip := "列表循环"
					mode := eng.Mode()
					modeColor := th.PrimaryText()
					if mode == player.ModeSequence {
						modeColor = th.TextMain
					}
					switch mode {
					case player.ModeRepeatAll:
						modeIcon = icons.Repeat
						modeTip = "列表循环"
					case player.ModeRepeatOne:
						modeIcon = icons.RepeatOne
						modeTip = "单曲循环"
					case player.ModeShuffle:
						modeIcon = icons.Shuffle
						modeTip = "随机播放"
					case player.ModeSequence:
						modeIcon = icons.Repeat
						modeTip = "顺序播放"
					}

					modeBtn := ui.ButtonBase(c.Key("pb_btn_mode")).
						Size(32, 32).
						Radius(16).
						Center().
						Cursor(ui.CursorPointer).
						Tooltip(modeTip).
						Transition(ui.ElementTransition{Colors: true, Duration: 150 * time.Millisecond, Ease: ui.EaseOut})

					HoverLights(modeBtn, th)
					Scales(modeBtn, 1.08, 0.92)
					modeBtn.Children(func() {
						ui.Icon(c, modeIcon).
							Size(16, 16).
							TextColor(modeColor).
							ContentTransition(ui.ContentTransition{Animation: &anim.Quick})
					})
					if modeBtn.Clicked() {
						eng.ToggleMode()
					}
				}
				// Center Part: Controller buttons
				ui.Row(c.Key("pb_center_ctrls")).
					Grow(1).
					Justify(ui.Center).
					AlignItems(ui.Center).
					Gap(16).
					Children(func() {
						if state.ShowLyrics {
							renderMode()
						}
						// Previous Track
						prevBtn := ui.ButtonBase(c.Key("btn_prev")).
							Size(32, 32).
							Radius(16).
							Center().
							Cursor(ui.CursorPointer).
							Tooltip("上一首").
							Transition(ui.ElementTransition{Colors: true, Duration: 150 * time.Millisecond, Ease: ui.EaseOut})

						HoverLights(prevBtn, th)
						Scales(prevBtn, 1.08, 0.92)
						prevBtn.Children(func() {
							ui.Icon(c, icons.SkipLeft).Size(16, 16).TextColor(th.TextMain)
						})
						if prevBtn.Clicked() {
							eng.Prev()
						}

						// Play / Pause: 40px circular primary button with hover feedback
						playIcon := icons.Play
						iconMarginLeft := float32(2)
						if isPlaying {
							playIcon = icons.Pause
							iconMarginLeft = 0
						}
						buttonTheme := th.PlaybackButtonTheme(t)
						playBackground, playForeground := buttonTheme.Accent, buttonTheme.AccentText
						playBtn := ui.ButtonBase(c.Key("btn_play_pause")).
							Label("btn_play_pause").
							Size(40, 40).
							Radius(20).
							Background(playBackground).
							Center().
							Cursor(ui.CursorPointer).
							Tooltip("播放 / 暂停").
							Transition(ui.ElementTransition{Colors: true, Duration: 150 * time.Millisecond, Ease: ui.EaseOut})

						if playBtn.Hovered() {
							playBtn.Background(buttonTheme.AccentHover)
						}
						// It leans into the pointer, and gives under it
						// while the click is held.
						Scales(playBtn, 1.05, 0.94)
						playBtn.Children(func() {
							// The play and pause glyphs cross over instead
							// of replacing one another at once.
							ui.Icon(c, playIcon).
								Size(18, 18).
								Margin(0, 0, 0, iconMarginLeft).
								TextColor(playForeground).
								ContentTransition(ui.ContentTransition{Animation: &anim.Quick})
						})
						if playBtn.Clicked() {
							eng.TogglePlay()
						}

						// Next Track
						nextBtn := ui.ButtonBase(c.Key("btn_next")).
							Size(32, 32).
							Radius(16).
							Center().
							Cursor(ui.CursorPointer).
							Tooltip("下一首").
							Transition(ui.ElementTransition{Colors: true, Duration: 150 * time.Millisecond, Ease: ui.EaseOut})

						HoverLights(nextBtn, th)
						Scales(nextBtn, 1.08, 0.92)
						nextBtn.Children(func() {
							ui.Icon(c, icons.SkipRight).Size(16, 16).TextColor(th.TextMain)
						})
						if nextBtn.Clicked() {
							eng.Next()
						}

						if !state.ShowLyrics {
							renderMode()
						} else {
							ui.Box(c).Size(32, 32)
						}

					})

				// Right Part: Volume, Lyrics Toggle, and Queue Drawer Popover
				ui.Row(c.Key("pb_right_tools")).
					Width(270).Shrink(0).
					Justify(ui.End).
					AlignItems(ui.Center).
					Gap(14).
					Children(func() {
						RenderQualityControl(c.Key("bar_quality"), th, eng, configMgr)
						// Volume Icon & Slider (clean spacing)
						ui.Row(c).AlignItems(ui.Center).Gap(6).HandleInput(func(ev ui.InputEvent) bool {
							if ev.Kind != ui.InputScroll || ev.DY == 0 {
								return false
							}
							step := 0.05
							if ev.DY > 0 {
								step = -step
							}
							state.Volume = max(0, min(1, eng.Volume()+step))
							state.VolumePushed = state.Volume
							eng.SetVolume(state.Volume)
							return true
						}).Children(func() {
							volIcon := icons.Volume
							if state.Volume <= 0.001 {
								volIcon = icons.VolumeMute
							}
							mute := ui.ButtonBase(c.Key("pb_mute")).Label("静音或恢复音量").Tooltip("静音 / 恢复音量").Size(24, 24).Center().Cursor(ui.CursorPointer)
							mute.Children(func() { ui.Icon(c, volIcon).Size(16, 16).TextColor(th.TextSub) })
							if mute.Clicked() {
								if state.Volume > 0.001 {
									state.LastVolume = state.Volume
									state.Volume = 0
								} else {
									state.Volume = state.LastVolume
									if state.Volume <= 0 {
										state.Volume = 0.8
									}
								}
								state.VolumePushed = state.Volume
								eng.SetVolume(state.Volume)
							}

							volSlider := ui.Slider(c.Key("pb_volume_slider"), &state.Volume, 0, 1).Width(74).Cursor(ui.CursorPointer)
							if volSlider.Changed() {
								eng.SetVolume(state.Volume)
								state.VolumePushed = state.Volume
							} else if engVol := eng.Volume(); engVol != state.VolumePushed {
								// The volume changed elsewhere, by a hotkey
								// or a media key: the slider follows.
								state.Volume = engVol
								state.VolumePushed = engVol
							}
						})

						// Lyrics Toggle Button
						lrcBtn := ui.ButtonBase(c.Key("pb_btn_lyric")).
							Label("pb_btn_lyric").
							Size(28, 28).
							Radius(6).
							Center().
							Cursor(ui.CursorPointer).
							Tooltip("桌面悬浮歌词").
							Transition(ui.ElementTransition{Colors: true, Duration: 150 * time.Millisecond, Ease: ui.EaseOut})

						HoverLights(lrcBtn, th)
						Scales(lrcBtn, 1, 0.9)
						lrcBtn.Children(func() {
							lrcCol := th.TextMain
							if state.DesktopLyricsVisible {
								lrcCol = th.PrimaryText()
							}
							ui.Text(c, "词").
								FontSize(16).Bold().
								TextColor(lrcBtn.AnimateColor("lrc_icon", lrcCol, anim.Quick))
						})
						if lrcBtn.Clicked() {
							if onToggleDesktopLyric != nil {
								onToggleDesktopLyric()
							}
						}
						// Queue Drawer Button & Popover (Requirement 4: 播放列表抽屉)
						queueBtn := ui.ButtonBase(c.Key("pb_btn_queue")).Label("播放队列").
							Size(32, 32).
							Radius(8).
							Center().
							Cursor(ui.CursorPointer).
							Tooltip("播放队列").
							Transition(ui.ElementTransition{Colors: true, Duration: 150 * time.Millisecond, Ease: ui.EaseOut})

						if queueBtn.Hovered() || state.Queue.Open {
							queueBtn.Background(th.HoverBg)
						}
						if queueBtn.Pressed() {
							queueBtn.Background(th.ActiveBg)
						}
						Scales(queueBtn, 1, 0.9)
						queueBtn.Children(func() {
							qCol := th.TextMain
							if state.Queue.Open {
								qCol = th.PrimaryText()
							}
							ui.Icon(c, icons.Playlist).Size(16, 16).TextColor(qCol)
						})
						if queueBtn.Clicked() {
							state.Queue.Open = !state.Queue.Open
						}

						queueTheme := th
						if state.ShowLyrics {
							palette := *th
							palette.CardBg = ui.Hex("#343434")
							queueTheme = &palette
						}
						renderQueueDrawer(c, queueTheme, queueBtn, eng, &state.Queue, getImage, st, redraw, barHeight)
					})
			})
	})
}
