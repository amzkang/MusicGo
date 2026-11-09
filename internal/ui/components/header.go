package components

import (
	"time"

	"github.com/egoist/mygo/ui"
	"musicgo/internal/ui/icons"
	"musicgo/internal/ui/theme"
)

// HeaderState holds state for header search and controls
type HeaderState struct {
	Immersive          bool
	SearchQuery        string
	SearchHistory      []string
	ClearSearchHistory func()
	CanGoBack          bool
	SettingsActive     bool

	// Maximized tells whether the window fills the screen. The button that
	// maximizes it shows the glyph of the restored window while it does, so
	// that the user sees what the button will do.
	Maximized bool
}

// AccountChip is the logged-in account the header shows next to the settings
// button; nil hides the chip.
type AccountChip struct {
	Platform string
	Nickname string
	Avatar   string
	Logo     string
	VipLabel string
}

// windowButtonHover is the color the window buttons take under the pointer:
// the hover color, a little brighter, as it shows on the header, which is
// darker than the panels the other buttons live on.
func windowButtonHover(th *theme.Theme) ui.Color {
	return th.HoverBg.Mix(th.TextMain, 0.10)
}

// windowButtonPlate is the color of the round plate the window buttons sit
// on. It is a light grey in both themes, as the buttons of the original
// project are.
func windowButtonPlate(th *theme.Theme) ui.Color {
	return th.ActiveBg
}

// windowButtonGlyph is the color of the glyph of a window button. It is
// dark on the light plate of the light theme, and light on the plate of the
// dark theme.
func windowButtonGlyph(th *theme.Theme) ui.Color {
	return th.TextSub
}

// windowButtonState plates a window button: a quiet plate at rest, a
// brighter one under the pointer, and the deepest one while it is held.
func windowButtonState(btn ui.Element, th *theme.Theme) {
	switch {
	case btn.Pressed():
		btn.Background(th.ActiveBg)
	case btn.Hovered():
		btn.Background(windowButtonHover(th))
	default:
		btn.Background(windowButtonPlate(th))
	}
}

// windowButtonSize is the diameter of the round plate of a window button.
const windowButtonSize = 26

// RenderHeader renders the modern native MyGo top bar
func RenderHeader(
	c *ui.Context,
	th *theme.Theme,
	state *HeaderState,
	account *AccountChip,
	accountMenuOpen *bool,
	accountMode func() bool,
	getImage func(string) *ui.Bitmap,
	onAccountMode func(useAccount bool),
	onAccountLogout func(),
	onSearch func(query string),
	onBack func(),
	onForward func(),
	onMinClick func(),
	onMaxClick func(),
	onCloseClick func(),
	onSettingsClick func(),
) {
	surface := HeaderSurface(th)
	if state.Immersive {
		surface = ui.Transparent
	}
	header := ui.Row(c).FillWidth().Height(72).Shrink(0).Background(surface).
		Border(0, th.Border).Padding(0, 24).AlignItems(ui.Center).DragWindow()
	buttonSize, glyphSize := float32(windowButtonSize), float32(13)
	if state.Immersive {
		w, _ := c.Size()
		header.Padding(0, max(float32(24), w*0.045))
	}
	header.Children(func() {
		if state.Immersive {
			collapse := ui.ButtonBase(c.Key("lyric_collapse")).Label("收起歌词").Size(48, 48).Radius(24).Center().Cursor(ui.CursorPointer)
			collapse.Children(func() { ui.Icon(c, icons.ChevronDown).Size(36, 36).TextColor(th.TextSub) })
			if collapse.Clicked() && onBack != nil {
				onBack()
			}
			return
		} else {
			// Navigation Back / Forward Buttons with clean SVG icons (Mark 1 Fixed)
			ui.Row(c).AlignItems(ui.Center).Gap(4).Children(func() {
				backBtn := ui.ButtonBase(c.Key("nav_back")).Disabled(!state.CanGoBack || onBack == nil).
					Size(32, 32).
					Radius(16).
					Background(th.PanelBg).
					Border(1, th.Border).
					Center().
					Label("后退").
					Cursor(ui.CursorPointer).
					Tooltip("后退").
					Children(func() {
						ui.Icon(c, icons.ChevronLeft).Size(14, 14).TextColor(th.TextMain)
					})
				HoverLights(backBtn, th)
				Scales(backBtn, 1, 0.94)
				if backBtn.Clicked() && onBack != nil {
					onBack()
				}

				if onForward != nil {
					fwdBtn := ui.ButtonBase(c.Key("nav_forward")).
						Size(32, 32).
						Radius(16).
						Background(th.PanelBg).
						Border(1, th.Border).
						Center().
						Label("前进").
						Cursor(ui.CursorPointer).
						Tooltip("前进").
						Children(func() {
							ui.Icon(c, icons.ChevronRight).Size(14, 14).TextColor(th.TextMain)
						})
					HoverLights(fwdBtn, th)
					Scales(fwdBtn, 1, 0.94)
					if fwdBtn.Clicked() && onForward != nil {
						onForward()
					}
				}
			})

		}
		ui.Box(c).Width(16).Shrink(0)
		if !state.Immersive {
			searchPlate := ui.Row(c).Label("全局搜索框").Width(280).MinWidth(130).Shrink(1).Height(38).Radius(19).
				Background(th.CardBg).Border(1, th.Border).Padding(0, 14).Gap(8).AlignItems(ui.Center)
			searchPlate.Children(func() {
				ui.Icon(c, icons.Search).Size(14, 14).Shrink(0).TextColor(th.TextMuted)
				renderSearchInput(c, th, state, searchPlate, onSearch)
				if state.SearchQuery != "" {
					clear := ui.ButtonBase(c.Key("clear_search")).Label("清除搜索").Size(18, 18).Center().Cursor(ui.CursorPointer)
					clear.Children(func() { ui.Icon(c, icons.Close).Size(12, 12).TextColor(th.TextMuted) })
					if clear.Clicked() {
						state.SearchQuery = ""
					}
				}
			})

		}
		ui.Spacer(c)
		if account != nil && !state.Immersive {
			chip := ui.ButtonBase(c.Key("header_account")).Label("账号："+account.Nickname).
				Height(30).Shrink(0).Radius(15).Padding(0, 6).Center().Cursor(ui.CursorPointer).Tooltip(account.Platform)
			menuOpen := accountMenuOpen != nil && *accountMenuOpen
			if menuOpen || chip.Hovered() {
				chip.Background(th.HoverBg)
			} else {
				chip.Background(th.PanelBg).Border(1, th.Border)
			}
			chip.Children(func() {
				ui.Row(c).AlignItems(ui.Center).Gap(7).Children(func() {
					avatar := ui.Box(c).Size(22, 22).Shrink(0).Radius(11).Background(th.ActiveBg).Center()
					avatar.Children(func() {
						if getImage != nil {
							if bm := getImage(account.Avatar); bm != nil {
								ui.Image(c, bm).Size(22, 22).Radius(11).Fit(ui.Cover)
								return
							}
							if bm := getImage(account.Logo); bm != nil {
								avatar.Radius(5.5)
								ui.Image(c, bm).Size(22, 22).Radius(5.5).Fit(ui.Contain)
								return
							}
						}
						ui.Icon(c, icons.Headphone).Size(12, 12).TextColor(th.TextSub)
					})
					label := "音源模式"
					if accountMode != nil && accountMode() {
						label = account.Platform
					}
					ui.Text(c, label).FontSize(12).TextColor(th.TextMain).SingleLine().Ellipsis("…").MaxWidth(96)
					ui.Icon(c, icons.ChevronDown).Size(10, 10).TextColor(th.TextMuted)
				})
			})
			if chip.Clicked() && accountMenuOpen != nil {
				*accountMenuOpen = !*accountMenuOpen
			}
			renderAccountMenu(c, th, chip, account, accountMenuOpen, accountMode, getImage, onAccountMode, onAccountLogout)
			ui.Box(c).Width(10).Shrink(0)
		}
		if account == nil && !state.Immersive {
			ui.Text(c, "音源模式").FontSize(12).TextColor(th.TextSub)
			ui.Box(c).Width(10).Shrink(0)
		}
		settings := ui.ButtonBase(c.Key("header_settings")).Label("偏好设置").
			Size(32, 32).Shrink(0).Radius(16).Center().Cursor(ui.CursorPointer).Tooltip("偏好设置")
		settingsColor := th.TextMain
		if state.SettingsActive {
			settingsColor = th.PrimaryText()
			settings.Background(th.ActiveBg)
		} else {
			HoverLights(settings, th)
		}
		Scales(settings, 1, 0.94)
		settings.Children(func() { ui.Icon(c, icons.Settings).Size(18, 18).TextColor(settingsColor) })
		if settings.Clicked() && onSettingsClick != nil {
			onSettingsClick()
		}
		ui.Box(c).Width(16).Shrink(0)

		// 3. Right: Window Controls
		ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
			// Window Minimize, Maximize, Close
			// Each one sits on a round light plate, as the buttons of
			// the original project do, and tells the pointer is on it:
			// the plate darkens and the glyph turns to the text color.
			// They show no hint when the pointer rests on them: the
			// glyph says what they do.
			minBtn := ui.ButtonBase(c.Key("hdr_min_btn")).
				Size(buttonSize, buttonSize).
				Radius(buttonSize / 2).
				Center().
				Label("最小化").
				Cursor(ui.CursorPointer).
				Transition(ui.ElementTransition{Colors: true, Duration: 150 * time.Millisecond, Ease: ui.EaseOut})
			windowButtonState(minBtn, th)
			Scales(minBtn, 1, 0.9)
			minBtn.Children(func() {
				minCol := windowButtonGlyph(th)
				if minBtn.Hovered() {
					minCol = th.TextMain
				}
				ui.Icon(c, icons.WindowMinimize).Size(glyphSize, glyphSize).TextColor(minCol)
			})
			if minBtn.Clicked() && onMinClick != nil {
				onMinClick()
			}

			// The button shows the glyph of what it will do: the
			// square to maximize the window, and the two squares to
			// restore it while the window is maximized.
			maxIcon := icons.WindowMaximize
			maxLabel := "最大化"
			if state.Maximized {
				maxIcon = icons.WindowRestore
				maxLabel = "还原"
			}

			maxBtn := ui.ButtonBase(c.Key("hdr_max_btn")).
				Size(buttonSize, buttonSize).
				Radius(buttonSize / 2).
				Center().
				Label(maxLabel).
				Cursor(ui.CursorPointer).
				Transition(ui.ElementTransition{Colors: true, Duration: 150 * time.Millisecond, Ease: ui.EaseOut})
			windowButtonState(maxBtn, th)
			Scales(maxBtn, 1, 0.9)
			maxBtn.Children(func() {
				maxCol := windowButtonGlyph(th)
				if maxBtn.Hovered() {
					maxCol = th.TextMain
				}
				ui.Icon(c, maxIcon).Size(glyphSize, glyphSize).TextColor(maxCol)
			})
			if maxBtn.Clicked() && onMaxClick != nil {
				onMaxClick()
			}

			closeBtn := ui.ButtonBase(c.Key("hdr_close_btn")).
				Size(buttonSize, buttonSize).
				Radius(buttonSize / 2).
				Center().
				Label("关闭").
				Cursor(ui.CursorPointer).
				Transition(ui.ElementTransition{Colors: true, Duration: 150 * time.Millisecond, Ease: ui.EaseOut})
			windowButtonState(closeBtn, th)
			Scales(closeBtn, 1, 0.9)
			closeBtn.Children(func() {
				closeCol := windowButtonGlyph(th)
				if closeBtn.Hovered() {
					closeCol = th.TextMain
				}
				ui.Icon(c, icons.WindowClose).Size(glyphSize, glyphSize).TextColor(closeCol)
			})
			if closeBtn.Clicked() && onCloseClick != nil {
				onCloseClick()
			}
		})
	})
}

// renderAccountMenu draws the popover the account chip opens: the profile,
// the library mode switch and the logout action.
func renderAccountMenu(
	c *ui.Context,
	th *theme.Theme,
	anchor ui.Element,
	account *AccountChip,
	open *bool,
	mode func() bool,
	getImage func(string) *ui.Bitmap,
	onMode func(useAccount bool),
	onLogout func(),
) {
	if open == nil {
		return
	}
	ui.PopoverBase(c, anchor, open, func(panel ui.Element) {
		width, _ := c.Size()
		panel.Label("账号菜单").
			Margin(8, 0, 0, 0).
			Width(min(252, width-24)).
			Background(th.CardBg).Border(1, th.Border).Radius(16).
			Shadow(0, 12, 32, 0, ui.RGBA(0, 0, 0, 0.18))

		ui.Column(c).FillWidth().Padding(14).Gap(8).Children(func() {
			ui.Row(c).FillWidth().Gap(12).AlignItems(ui.Center).Children(func() {
				avatar := ui.Box(c).Size(44, 44).Shrink(0).Radius(22).Background(th.ActiveBg).Center()
				avatar.Children(func() {
					if getImage != nil {
						if bm := getImage(account.Avatar); bm != nil {
							ui.Image(c, bm).Size(44, 44).Radius(22).Fit(ui.Cover)
							return
						}
						if bm := getImage(account.Logo); bm != nil {
							avatar.Radius(11)
							ui.Image(c, bm).Size(44, 44).Radius(11).Fit(ui.Contain)
							return
						}
					}
					ui.Icon(c, icons.Headphone).Size(20, 20).TextColor(th.TextSub)
				})
				ui.Column(c).Grow(1).MinWidth(0).Gap(3).Children(func() {
					ui.Text(c, account.Nickname).FontSize(14).Bold().TextColor(th.TextMain).SingleLine().Ellipsis("…")
					ui.Text(c, account.Platform).FontSize(11).TextColor(th.TextMuted).SingleLine().Ellipsis("…")
					if account.VipLabel != "" {
						ui.Text(c, account.VipLabel).FontSize(11).TextColor(th.TextSub).SingleLine().Ellipsis("…")
					}
				})
			})

			ui.Box(c).FillWidth().Height(1).Shrink(0).Background(th.Border)

			useAccount := mode != nil && mode()
			renderAccountModeRow(c, th, "acct_mode_local", "音源模式", !useAccount, func() {
				*open = false
				if onMode != nil {
					onMode(false)
				}
			})
			renderAccountModeRow(c, th, "acct_mode_account", "平台登录："+account.Platform, useAccount, func() {
				*open = false
				if onMode != nil {
					onMode(true)
				}
			})

			ui.Box(c).FillWidth().Height(1).Shrink(0).Background(th.Border)

			logout := ui.ButtonBase(c.Key("acct_logout")).Label("退出登录").
				FillWidth().Height(34).Radius(8).Center().Cursor(ui.CursorPointer)
			if logout.Hovered() {
				logout.Background(th.HoverBg)
			}
			logout.Children(func() {
				ui.Text(c, "退出登录").FontSize(12).TextColor(th.Danger)
			})
			if logout.Clicked() {
				*open = false
				if onLogout != nil {
					onLogout()
				}
			}
		})
	})
}

// renderAccountModeRow is one selectable row of the library mode switch.
func renderAccountModeRow(c *ui.Context, th *theme.Theme, key, label string, active bool, onClick func()) {
	row := ui.ButtonBase(c.Key(key)).Label(label).
		FillWidth().Height(34).Radius(8).Padding(0, 10).Cursor(ui.CursorPointer)
	if active {
		row.Background(th.ActiveBg)
	} else if row.Hovered() {
		row.Background(th.HoverBg)
	}
	row.Children(func() {
		ui.Row(c).FillWidth().AlignItems(ui.Center).Children(func() {
			color := th.TextMain
			if active {
				color = th.PrimaryText()
			}
			ui.Text(c, label).FontSize(12).TextColor(color)
			ui.Spacer(c)
			if active {
				ui.Icon(c, icons.Check).Size(14, 14).TextColor(th.PrimaryText())
			}
		})
	})
	if row.Clicked() && !active {
		onClick()
	}
}
