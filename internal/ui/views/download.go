package views

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"

	"github.com/egoist/mygo/ui"
	"musicgo/internal/download"
	"musicgo/internal/player"
	"musicgo/internal/plugin"
	"musicgo/internal/ui/anim"
	"musicgo/internal/ui/components"
	"musicgo/internal/ui/icons"
	"musicgo/internal/ui/theme"
)

// DownloadViewState holds the active tab and interaction state for the download view
type DownloadViewState struct {
	mu            sync.RWMutex
	ActiveTab     int // 0: 正在下载, 1: 已下载
	ActiveList    ui.ListState
	CompletedList ui.ListState
}

// NewDownloadViewState creates an initialized DownloadViewState
func NewDownloadViewState() *DownloadViewState {
	return &DownloadViewState{ActiveTab: 0}
}

// RenderDownload renders the download management page
func RenderDownload(
	c *ui.Context,
	th *theme.Theme,
	state *DownloadViewState,
	dlMgr *download.Manager,
	eng *player.Engine,
	getImage func(string) *ui.Bitmap,
	requestRedraw func(),
) {
	if dlMgr == nil {
		ui.Column(c).Fill().Center().Children(func() {
			ui.Text(c, "下载服务未就绪").FontSize(14).TextColor(th.TextMuted)
		})
		return
	}

	allTasks := dlMgr.Tasks()
	var activeTasks []download.Task
	var completedTasks []download.Task

	for _, t := range allTasks {
		if t.Status == download.StatusCompleted {
			completedTasks = append(completedTasks, t)
		} else {
			activeTasks = append(activeTasks, t)
		}
	}

	ui.Column(c).Fill().Padding(24, components.PagePadding(c), 32, components.PagePadding(c)).Gap(14).Children(func() {
		components.PageTitle(c, th, "下载管理")
		if err := dlMgr.PersistenceError(); err != "" {
			ui.Text(c, "下载记录保存失败: "+err).TextColor(th.Danger)
		}
		// Tab selector
		ui.Row(c).FillWidth().AlignItems(ui.Center).Children(func() {
			tab1 := fmt.Sprintf("正在下载 (%d)", len(activeTasks))
			tab2 := fmt.Sprintf("已下载 (%d)", len(completedTasks))
			renderTabs(c.Key("download_tabs"), &state.ActiveTab, tab1, tab2)
		})

		ui.Box(c.Key(fmt.Sprintf("dl_panel_%d", state.ActiveTab))).
			FillWidth().
			Grow(1).
			Transition(anim.Tab()).
			Children(func() {
				if state.ActiveTab == 0 {
					renderDownloadingTab(c, th, state, dlMgr, activeTasks)
				} else {
					renderDownloadedTab(c, th, state, dlMgr, eng, completedTasks, getImage)
				}
			})
	})
}

func renderDownloadingTab(c *ui.Context, th *theme.Theme, state *DownloadViewState, dlMgr *download.Manager, tasks []download.Task) {
	ui.Column(c).Fill().Gap(12).Children(func() {
		// Action bar
		ui.Row(c).FillWidth().Wrap().AlignItems(ui.Center).Gap(10).Children(func() {
			pauseBtn := components.Button(c.Key("dl_pause_all"), "全部暂停").Cursor(ui.CursorPointer)
			if pauseBtn.Clicked() {
				dlMgr.PauseAll()
			}

			resumeBtn := components.Button(c.Key("dl_resume_all"), "全部继续").Cursor(ui.CursorPointer)
			if resumeBtn.Clicked() {
				dlMgr.ResumeAll()
			}

			clearBtn := components.Button(c.Key("dl_clear_comp"), "清空已完成").Cursor(ui.CursorPointer)
			if clearBtn.Clicked() {
				go dlMgr.ClearCompleted()
			}

			ui.Spacer(c)
			ui.Text(c, fmt.Sprintf("共 %d 个任务", len(tasks))).FontSize(12).TextColor(th.TextMuted)
		})

		if len(tasks) == 0 {
			components.EmptyState(c, th, icons.Download, "暂无正在下载的任务")
			return
		}

		state.ActiveList.Key = func(i int) any { return tasks[i].ID }
		ui.List(c, &state.ActiveList, len(tasks), func(i int) {
			t := tasks[i]

			ui.Box(c.Key("dl_task_"+t.ID)).
				FillWidth().
				Background(th.CardBg).
				Radius(12).
				Border(1, th.Border).
				Padding(12, 16).
				Children(func() {
					ui.Column(c).FillWidth().Gap(8).Children(func() {
						// Row 1: Title, Artist, Status, Actions
						ui.Row(c).FillWidth().AlignItems(ui.Center).Children(func() {
							ui.Column(c).Grow(1).MinWidth(0).Gap(4).Children(func() {
								ui.Text(c, t.Song.Title).FontSize(14).Bold().TextColor(th.TextMain).SingleLine().Ellipsis("…").Tooltip(t.Song.Title)
								ui.Text(c, t.Song.Artist+" / "+formatPlatform(t.Song.Platform)).FontSize(12).TextColor(th.TextSub).SingleLine().Ellipsis("…")
							})

							ui.Spacer(c)

							// Task Controls
							ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
								if t.Status == download.StatusDownloading {
									btn := components.Button(c.Key("pause"), "暂停")
									if btn.Clicked() {
										dlMgr.PauseTask(t.ID)
									}
								} else if t.Status == download.StatusPaused || t.Status == download.StatusFailed || t.Status == download.StatusCanceled {
									btn := components.PrimaryButton(c.Key("resume"), "继续")
									if btn.Clicked() {
										dlMgr.ResumeTask(t.ID)
									}
								}

								delBtn := components.IconButton(c.Key("delete"), th, "删除下载任务", icons.Close, th.Danger)
								if delBtn.Clicked() {
									go dlMgr.DeleteTask(t.ID, true)
								}
							})
						})

						// Row 2: Progress bar
						progPct := float32(t.Progress)
						if progPct < 0 {
							progPct = 0
						}
						if progPct > 1 {
							progPct = 1
						}

						barBg := th.HoverBg
						fillCol := th.Primary
						if t.Status == download.StatusPaused {
							fillCol = th.TextMuted
						} else if t.Status == download.StatusFailed {
							fillCol = th.Danger
						}

						ui.Box(c).FillWidth().Height(6).Radius(3).Background(barBg).Draw(func(p *ui.Painter, r ui.Rect) {
							p.Fill(ui.Rect{X: r.X, Y: r.Y, W: r.W * progPct, H: r.H}, fillCol, 3)
						})

						// Row 3: Status Details (size, speed, error)
						ui.Row(c).FillWidth().AlignItems(ui.Center).Children(func() {
							infoText := ""
							switch t.Status {
							case download.StatusQueued:
								infoText = "排队等待中..."
							case download.StatusDownloading:
								infoText = fmt.Sprintf("%s / %s · %s/s (%.1f%%)",
									formatBytes(t.DownloadedBytes),
									formatBytes(t.TotalBytes),
									formatBytes(t.Speed),
									t.Progress*100,
								)
							case download.StatusPaused:
								infoText = fmt.Sprintf("已暂停 (%s / %s)",
									formatBytes(t.DownloadedBytes),
									formatBytes(t.TotalBytes),
								)
							case download.StatusFailed:
								infoText = fmt.Sprintf("下载失败: %s", t.ErrorMsg)
							default:
								infoText = t.Status.String()
							}

							statCol := th.TextSub
							if t.Status == download.StatusFailed {
								statCol = th.Danger
							}
							ui.Text(c, infoText).Grow(1).MinWidth(0).FontSize(12).TextColor(statCol).SingleLine().Ellipsis("…").Tooltip(infoText)

							ui.Spacer(c)

							ui.Text(c, strings.ToUpper(t.Quality)).FontSize(11).TextColor(th.TextMuted)
						})
					})
				})
		}).FillWidth().Grow(1).MinHeight(0).Gap(8)
	})
}

func renderDownloadedTab(c *ui.Context, th *theme.Theme, state *DownloadViewState, dlMgr *download.Manager, eng *player.Engine, tasks []download.Task, getImage func(string) *ui.Bitmap) {
	var current *plugin.MusicItem
	playable := 0
	for _, task := range tasks {
		if task.FileAvailable {
			playable++
		}
	}
	if eng != nil {
		current = eng.CurrentSong()
	}
	ui.Column(c).Fill().MinHeight(0).Gap(12).Children(func() {
		ui.Row(c).FillWidth().Wrap().AlignItems(ui.Center).Gap(10).Children(func() {
			playAll := components.PrimaryButton(c.Key("dl_play_all"), "播放全部").Disabled(eng == nil || playable == 0)
			if playAll.Clicked() && eng != nil {
				songs := make([]plugin.MusicItem, 0, len(tasks))
				for _, task := range tasks {
					if !task.FileAvailable {
						continue
					}
					item := task.Song.Clone()
					item.Platform, item.URL = "local", task.FilePath
					songs = append(songs, item)
				}
				eng.SetQueue(songs, 0)
			}
			folder := ""
			if len(tasks) > 0 {
				folder = getFileDir(tasks[0].FilePath)
			}
			if components.Button(c.Key("dl_open_folder"), "打开下载目录").Disabled(folder == "").Clicked() {
				go openFolderInSystem(folder)
			}
			ui.Spacer(c)
			ui.Text(c, fmt.Sprintf("已下载 %d 首", len(tasks))).FontSize(12).TextColor(th.TextMuted)
		})
		if len(tasks) == 0 {
			components.EmptyState(c, th, icons.Folder, "暂无已下载歌曲")
			return
		}
		state.CompletedList.Key = func(i int) any { return tasks[i].ID }
		ui.List(c, &state.CompletedList, len(tasks), func(i int) {
			task := tasks[i]
			var play func()
			if eng != nil && task.FileAvailable {
				play = func() {
					item := task.Song.Clone()
					item.Platform, item.URL = "local", task.FilePath
					eng.PlaySong(item)
				}
			}
			options := songRowOptions{Index: i, Current: current != nil && current.Platform == "local" && current.URL == task.FilePath, TrailingText: formatBytes(task.TotalBytes), Play: play}
			if !task.FileAvailable {
				options.TrailingText = "文件缺失"
			}
			options.Actions = func(c *ui.Context) {
				if task.FileAvailable {
					if components.IconButton(c.Key("play"), th, "播放", icons.Play, th.Primary).Disabled(eng == nil).Clicked() && play != nil {
						play()
					}
				} else if components.IconButton(c.Key("retry"), th, "重新下载", icons.Download, th.Primary).Clicked() {
					dlMgr.ResumeTask(task.ID)
					state.ActiveTab = 0
				}
				if components.IconButton(c.Key("delete"), th, "删除下载文件", icons.Trash, th.Danger).Clicked() {
					go dlMgr.DeleteTask(task.ID, true)
				}
			}
			renderSongRow(c.Key("download_"+task.ID), th, task.Song, options, getImage)
		}).FillWidth().Grow(1).MinHeight(0)
	})
}

func formatPlatform(plat string) string {
	switch strings.ToLower(plat) {
	case "netease", "163":
		return "网易云"
	case "qq", "tencent":
		return "QQ音乐"
	case "kugou":
		return "酷狗"
	case "kuwo":
		return "酷我"
	case "bilibili", "bili":
		return "B站"
	case "migu":
		return "咪咕"
	case "local":
		return "本地"
	default:
		return plat
	}
}

func formatBytes(b int64) string {
	if b <= 0 {
		return "0 B"
	}
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := int64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(b)/float64(div), "KMGTPE"[exp])
}

func getFileDir(path string) string {
	for i := len(path) - 1; i >= 0; i-- {
		if path[i] == '/' || path[i] == '\\' {
			return path[:i]
		}
	}
	return path
}

func openFolderInSystem(dir string) {
	if dir == "" {
		return
	}
	_ = os.MkdirAll(dir, 0755)
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		cmd = exec.Command("explorer", dir)
	case "darwin":
		cmd = exec.Command("open", dir)
	default:
		cmd = exec.Command("xdg-open", dir)
	}
	_ = cmd.Start()
}
