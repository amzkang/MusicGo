package components

import (
	"github.com/egoist/mygo/ui"
	"musicgo/internal/ui/theme"
)

// DrawSpectrum renders the animated GPU spectrum bars, downsampled to fit cleanly
func DrawSpectrum(c *ui.Context, th *theme.Theme, bars []float32, width, height float32) {
	if len(bars) == 0 {
		return
	}

	numBars := 8
	step := len(bars) / numBars
	if step < 1 {
		step = 1
	}
	barW := float32(2.5)
	gap := float32(2)

	ui.Row(c).Width(width).Height(height).AlignItems(ui.End).Gap(gap).Children(func() {
		for i := 0; i < numBars; i++ {
			idx := i * step
			val := float32(0.08)
			if idx < len(bars) {
				val = bars[idx]
			}
			barH := val * height
			if barH < 2 {
				barH = 2
			}
			if barH > height {
				barH = height
			}

			// Render vertical bar
			col := th.Primary
			if i%2 == 1 {
				col = th.TextSub
			}
			ui.Box(c.Key(i)).
				Width(barW).
				Height(barH).
				Background(col).
				Radius(1)
		}
	})
}
