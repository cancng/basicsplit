package main

import (
	"log"
	"math"

	"fyne.io/systray"
)

const iconSize = 32

var (
	iconOn  = appIcon(0x3b, 0x6e, 0xf5) // blue
	iconOff = appIcon(0x7a, 0x7a, 0x7a) // grey
)

func onReady(logPath string) {
	trayReady.Store(true)
	setTrayState()

	mActive := systray.AddMenuItemCheckbox("Aktif", "", enabled.Load())
	mAuto := systray.AddMenuItemCheckbox(autostartLabel, "", autostartEnabled())
	systray.AddSeparator()
	mRules := systray.AddMenuItem("Kuralları düzenle", "")
	mLog := systray.AddMenuItem("Logu göster", "")
	systray.AddSeparator()
	mQuit := systray.AddMenuItem("Çıkış", "")

	go func() {
		for {
			select {
			case <-mActive.ClickedCh:
				setEnabled(!mActive.Checked())
				setChecked(mActive, enabled.Load())
				setTrayState()
			case <-mAuto.ClickedCh:
				if err := setAutostart(!mAuto.Checked()); err != nil {
					log.Printf("otomatik baslatma: %v", err)
					showError("Otomatik başlatma ayarlanamadı:\n" + err.Error())
				}
				setChecked(mAuto, autostartEnabled())
			case <-mRules.ClickedCh:
				openInEditor(cfgPath)
			case <-mLog.ClickedCh:
				openInEditor(logPath)
			case <-mQuit.ClickedCh:
				quitting.Store(true)
				stopEngine()
				systray.Quit()
				return
			}
		}
	}()
}

func setChecked(m *systray.MenuItem, on bool) {
	if on {
		m.Check()
	} else {
		m.Uncheck()
	}
}

func setTrayState() {
	if !trayReady.Load() {
		return
	}
	if !tunnelUp.Load() {
		systray.SetIcon(iconOff)
		systray.SetTooltip("basicsplit - baslatiliyor")
	} else if enabled.Load() {
		systray.SetIcon(iconOn)
		systray.SetTooltip("basicsplit - aktif")
	} else {
		systray.SetIcon(iconOff)
		systray.SetTooltip("basicsplit - kapali")
	}
}

// segDist is the distance from point p to the line segment a-b.
func segDist(px, py, ax, ay, bx, by float64) float64 {
	dx, dy := bx-ax, by-ay
	t := ((px-ax)*dx + (py-ay)*dy) / (dx*dx + dy*dy)
	t = math.Max(0, math.Min(1, t))
	return math.Hypot(px-ax-t*dx, py-ay-t*dy)
}

// iconRGBA draws the tray icon, a white "one line splits into two" glyph on
// a rounded square of the given color, as iconSize x iconSize RGBA pixels.
func iconRGBA(r, g, b byte) []byte {
	const size = iconSize
	clamp := func(v float64) float64 { return math.Max(0, math.Min(1, v)) }
	mix := func(bg byte, t float64) byte { return byte(float64(bg)*(1-t) + 255*t) }

	pix := make([]byte, 0, size*size*4)
	const c, half, radius = float64(size-1) / 2, 15.5, 7.0
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			px, py := float64(x), float64(y)

			// rounded square background
			qx := math.Abs(px-c) - (half - radius)
			qy := math.Abs(py-c) - (half - radius)
			d := math.Hypot(math.Max(qx, 0), math.Max(qy, 0)) + math.Min(math.Max(qx, qy), 0) - radius
			alpha := clamp(0.5 - d)

			// stem, two branches, and a dot at each end
			line := math.Min(segDist(px, py, 6, 15.5, 14, 15.5),
				math.Min(segDist(px, py, 14, 15.5, 23, 8.5), segDist(px, py, 14, 15.5, 23, 22.5)))
			dots := math.Min(math.Hypot(px-6, py-15.5),
				math.Min(math.Hypot(px-23, py-8.5), math.Hypot(px-23, py-22.5)))
			t := math.Max(clamp(2-line), clamp(3.6-dots))

			pix = append(pix, mix(r, t), mix(g, t), mix(b, t), byte(alpha*255))
		}
	}
	return pix
}
