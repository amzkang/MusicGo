package theme

import (
	"math"
	"strings"

	"github.com/egoist/mygo/ui"
	"musicgo/internal/store"
)

// Theme holds colors for rendering the native GPU UI
type Theme struct {
	Name            string
	Background      ui.Color
	HeaderBg        ui.Color
	HeaderTextColor ui.Color
	SidebarBg       ui.Color
	PanelBg         ui.Color
	HoverBg         ui.Color
	ActiveBg        ui.Color
	Border          ui.Color
	Primary         ui.Color
	TextMain        ui.Color
	TextSub         ui.Color
	TextMuted       ui.Color
	PlayBarBg       ui.Color
	CardBg          ui.Color
	// Danger paints failures. It follows the theme, so that error text
	// keeps its contrast on both surfaces.
	Danger ui.Color
}

func FromConfig(cfg store.AppConfig) *Theme {
	if cfg.UsesCustomTheme() {
		return FromCustom(cfg.CustomTheme)
	}
	if cfg.Theme == "dark" {
		return DarkTheme()
	}
	return LightTheme()
}

// DarkTheme layers deep neutral surfaces under a blue accent, so that the
// eye reads the interface in planes: canvas, sidebar, card, control.
func DarkTheme() *Theme {
	return &Theme{
		Name:       "Dark",
		Background: ui.Hex("#111111"), HeaderBg: ui.Hex("#111111"),
		HeaderTextColor: ui.Hex("#f2f2f2"), SidebarBg: ui.Hex("#161616"),
		PanelBg: ui.Hex("#111111"), HoverBg: ui.Hex("#232323"),
		ActiveBg: ui.Hex("#2d2d2d"), Border: ui.Hex("#2a2a2a"),
		Primary: ui.Hex("#5b8dff"), TextMain: ui.Hex("#f2f2f2"),
		TextSub: ui.Hex("#a6a6a6"), TextMuted: ui.Hex("#9a9a9a"),
		PlayBarBg: ui.Hex("#181818"), CardBg: ui.Hex("#191919"),
		Danger: ui.Hex("#f87171"),
	}
}

// LightTheme layers a quiet grey canvas under white cards and a blue
// accent, with the same plane ordering as the dark theme.
func LightTheme() *Theme {
	return &Theme{
		Name:       "Light",
		Background: ui.Hex("#f6f6f6"), HeaderBg: ui.Hex("#f6f6f6"),
		HeaderTextColor: ui.Hex("#1a1a1a"), SidebarBg: ui.Hex("#f1f1f1"),
		PanelBg: ui.Hex("#f6f6f6"), HoverBg: ui.Hex("#eeeeee"),
		ActiveBg: ui.Hex("#e6e6e6"), Border: ui.Hex("#e2e2e2"),
		Primary: ui.Hex("#2563eb"), TextMain: ui.Hex("#1a1a1a"),
		TextSub: ui.Hex("#5f5f5f"), TextMuted: ui.Hex("#6b6b6b"),
		PlayBarBg: ui.Hex("#ffffff"), CardBg: ui.Hex("#ffffff"),
		Danger: ui.Hex("#dc2626"),
	}
}

// OnPrimary chooses readable text on both built-in and custom accents.
func (t *Theme) OnPrimary() ui.Color {
	if luminance(t.Primary) > 0.179 {
		return ui.Hex("#000000")
	}
	return ui.Hex("#ffffff")
}

func luminance(color ui.Color) float64 {
	linear := func(v uint8) float64 {
		x := float64(v) / 255
		if x <= 0.04045 {
			return x / 12.92
		}
		return math.Pow((x+0.055)/1.055, 2.4)
	}
	return 0.2126*linear(color.R) + 0.7152*linear(color.G) + 0.0722*linear(color.B)
}

// IsDark follows the actual background, including custom theme names.
func (t *Theme) IsDark() bool {
	return t != nil && luminance(t.Background) < 0.179
}

// SelectedBg keeps small accent text readable on selected neutral surfaces.
func (t *Theme) SelectedBg() ui.Color {
	return t.ActiveBg.Mix(t.CardBg, .5)
}

// PrimaryState preserves text contrast; near black/white it moves inward
// so that the pointer still produces a visible change.
func (t *Theme) PrimaryState(amount float32) ui.Color {
	brightness := luminance(t.Primary)
	target := ui.Hex("#000000")
	if brightness < .02 || brightness > .179 && brightness < .8 {
		target = ui.Hex("#ffffff")
	}
	return t.Primary.Mix(target, amount)
}

// Stage is the fixed, inverted palette of the recommendation sleeves.
// It is local to that artwork stage and never replaces the window's theme.
func Stage() *Theme {
	t := DarkTheme()
	t.Background = ui.Hex("#242424")
	t.TextMain = ui.Hex("#fafafa")
	t.TextSub = ui.Hex("#bebebe")
	t.Primary = t.TextMain
	t.Border = ui.Hex("#707070")
	t.HoverBg = ui.Hex("#424242")
	t.ActiveBg = ui.Hex("#d8d8d8")
	return t
}

// ForLyrics keeps the user's accent while making controls readable over artwork.
// The library theme and MyGo's application palette are left intact.
func (t *Theme) ForLyrics() *Theme {
	result := *t
	white := ui.Hex("#ffffff")
	result.TextMain = white
	result.TextSub = white.Alpha(0.6)
	result.TextMuted = white.Alpha(0.4)
	result.Border = white.Alpha(0.18)
	result.CardBg = white.Alpha(0.08)
	result.HoverBg = white.Alpha(0.12)
	result.ActiveBg = white.Alpha(0.18)
	return &result
}

// ToMyGoTheme converts Theme to a native MyGo Theme
func (t *Theme) ToMyGoTheme() *ui.Theme {
	mt := ui.LightTheme()
	if t.IsDark() {
		mt = ui.DarkTheme()
	}
	mt.Background = t.Background
	mt.Surface = t.CardBg
	mt.SurfaceHover = t.HoverBg
	mt.SurfacePressed = t.ActiveBg
	mt.Border = t.Border
	mt.Text = t.TextMain
	mt.TextMuted = t.TextMuted
	mt.Accent = t.Primary
	mt.AccentText = t.OnPrimary()
	mt.AccentHover = t.PrimaryState(.12)
	mt.AccentPressed = t.PrimaryState(.20)
	mt.Focus = t.Primary.Alpha(0.45)
	mt.Selection = t.Primary.Alpha(0.18)
	mt.Danger = t.Danger
	mt.Radius = 10
	mt.Spacing = 4
	mt.Font = "PingFang SC, PingFangSC-Regular, -apple-system, BlinkMacSystemFont, Noto Sans CJK SC, sans-serif"
	return mt
}

// FromCustom creates a Theme from custom config
func FromCustom(cfg *store.CustomThemeCfg) *Theme {
	if cfg == nil {
		return LightTheme()
	}
	t := LightTheme()
	background := parseColor(cfg.ColorBg, parseColor(cfg.ColorPanel, t.Background))
	if luminance(background) < 0.179 {
		t = DarkTheme()
	}
	t.Name = strings.TrimSpace(cfg.Name)
	if t.Name == "" {
		t.Name = "Custom"
	}
	if cfg.ColorPrimary != "" {
		t.Primary = parseColor(cfg.ColorPrimary, t.Primary)
	}
	if cfg.ColorBg != "" {
		t.Background = parseColor(cfg.ColorBg, t.Background)
	}
	if cfg.ColorPanel != "" {
		t.PanelBg = parseColor(cfg.ColorPanel, t.PanelBg)
		t.CardBg = t.PanelBg
		t.HeaderBg = t.PanelBg
		t.SidebarBg = t.PanelBg
		t.PlayBarBg = t.PanelBg
	}
	if cfg.ColorTextMain != "" {
		t.TextMain = parseColor(cfg.ColorTextMain, t.TextMain)
	}
	if cfg.ColorTextSub != "" {
		t.TextSub = parseColor(cfg.ColorTextSub, t.TextSub)
	}
	if cfg.ColorBorder != "" {
		t.Border = parseColor(cfg.ColorBorder, t.Border)
	}
	return t
}

// parseColor reads a color of the configuration, falling back to the color
// the theme holds when the text is not a color.
func parseColor(str string, fallback ui.Color) (color ui.Color) {
	color = fallback
	defer func() {
		if r := recover(); r != nil {
			color = fallback
		}
	}()
	return ui.Hex(str)
}
