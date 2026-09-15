package ui

import (
	"context"
	"fmt"
	"html"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"

	"github.com/siner308/sims/internal/device"
)

// Lives in the test package because the views it renders are unexported; skipped unless SIMS_SCREENSHOTS=1.
func TestGenerateScreenshots(t *testing.T) {
	if os.Getenv("SIMS_SCREENSHOTS") == "" {
		t.Skip("set SIMS_SCREENSHOTS=1 to regenerate docs/img")
	}
	outDir := filepath.Join("..", "..", "docs", "img")
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 15, 15, 30, 0, 0, time.Local)

	android := &shotProvider{fakeProvider: fakeProvider{platform: device.PlatformAndroid, devices: []device.Device{
		{ID: "Pixel_7_API_35", Name: "Pixel_7_API_35", Platform: device.PlatformAndroid, Kind: device.KindVirtual, Transport: device.TransportAVD, Runtime: "API 35", State: device.StateBooted, Serial: "emulator-5554", LastActiveAt: now.Add(-3 * time.Minute)},
		{ID: "Pixel_Tablet_API_36", Name: "Pixel_Tablet_API_36", Platform: device.PlatformAndroid, Kind: device.KindVirtual, Transport: device.TransportAVD, Runtime: "API 36", State: device.StateShutdown, LastActiveAt: now.Add(-2 * 24 * time.Hour)},
		{ID: "R3CT40ABCDE", Name: "SM S928N", Model: "e1q", Platform: device.PlatformAndroid, Kind: device.KindPhysical, Transport: device.TransportUSB, Runtime: "Android 15", State: device.StateConnected, Serial: "R3CT40ABCDE"},
		{ID: "192.168.0.23:5555", Name: "Pixel 7", Model: "panther", Platform: device.PlatformAndroid, Kind: device.KindPhysical, Transport: device.TransportWiFi, Runtime: "Android 14", State: device.StateConnected, Serial: "192.168.0.23:5555"},
	}}}
	ios := &shotProvider{fakeProvider: fakeProvider{platform: device.PlatformIOS, devices: []device.Device{
		{ID: "0728E045-9CAF-41BD-B724-54A8850D9327", Name: "iPhone 17 Pro", Platform: device.PlatformIOS, Kind: device.KindVirtual, Transport: device.TransportSim, Runtime: "iOS 26.5", State: device.StateBooted, LastActiveAt: now.Add(-40 * time.Second)},
		{ID: "81EA668E-D91C-420A-9FD1-7CED51248DD8", Name: "iPhone 16 Pro Max", Platform: device.PlatformIOS, Kind: device.KindVirtual, Transport: device.TransportSim, Runtime: "iOS 18.5", State: device.StateShutdown, LastActiveAt: now.Add(-6 * time.Hour)},
		{ID: "C1D2E3F4-0000-4000-8000-000000000001", Name: "iPad Pro 13-inch (M4)", Platform: device.PlatformIOS, Kind: device.KindVirtual, Transport: device.TransportSim, Runtime: "iOS 18.1", State: device.StateShutdown},
		{ID: "BBBBC218-A593-5E01-9D44-ACDCFDFBC7AF", Name: "my iPhone", Model: "iPhone 14 Pro", Platform: device.PlatformIOS, Kind: device.KindPhysical, Transport: device.TransportWiFi, Runtime: "iOS 27.0", State: device.StateConnected, LastActiveAt: now.Add(-20 * time.Minute)},
		{ID: "A0B516A2-89CC-5753-BB6A-0BEB0CB28622", Name: "QA iPhone", Model: "iPhone 15", Platform: device.PlatformIOS, Kind: device.KindPhysical, Transport: device.TransportUSB, Runtime: "iOS 17.5.1", State: device.StateUnpaired},
	}}}

	shoot := func(name string, width, height int, setup func(a *App)) {
		a := New("v0.1.0", android, ios)
		a.header.facts = [][2]string{
			{"Platforms", "android, ios"},
			{"Android SDK", "~/Library/Android/sdk"},
			{"Tools", "adb 36.0.0, emulator 36.1.9.0, Xcode 26.6"},
		}
		screen := tcell.NewSimulationScreen("UTF-8")
		if err := screen.Init(); err != nil {
			t.Fatal(err)
		}
		a.tv.SetScreen(screen)
		done := make(chan error, 1)
		go func() { done <- a.tv.Run() }()
		dv := a.stack[0].(*devicesView)
		waitFor(t, a, 5*time.Second, func() bool { return len(dv.devices) == 9 })
		// Run re-inits the simulation screen at 80x25, so the size is applied once the loop is up.
		onUI(a, func() { screen.SetSize(width, height); a.tv.Sync() })
		onUI(a, func() {
			dv.now = func() time.Time { return now }
			dv.render()
			if setup != nil {
				setup(a)
			}
			// fixed usage so regenerated SVGs only change when the UI does
			a.header.usage = usage{cpu: 23, mem: 71, disk: 64, free: 212 << 30, ok: true}
			a.drawHeader()
		})
		a.tv.QueueUpdateDraw(func() {})
		var svg string
		onUI(a, func() { svg = screenToSVG(screen) })
		a.tv.Stop()
		<-done
		path := filepath.Join(outDir, name+".svg")
		if err := os.WriteFile(path, []byte(svg), 0o644); err != nil {
			t.Fatal(err)
		}
		t.Logf("wrote %s", path)
	}

	shoot("devices", 176, 24, nil)
	shoot("apps", 150, 24, func(a *App) {
		dv := a.stack[0].(*devicesView)
		dv.table.Select(2, 0)
		d, _ := dv.selected()
		av := newAppsView(a, d)
		av.apps = []device.App{
			{BundleID: "com.example.sample", Name: "Sample App", Version: "1.2.3", Source: "adb"},
			{BundleID: "com.example.shop", Name: "Shop", Version: "3.4.1", Source: "store"},
			{BundleID: "com.android.chrome", Name: "com.android.chrome", System: true, Source: "preinstalled"},
			{BundleID: "com.google.android.gm", Name: "com.google.android.gm", System: true, Source: "preinstalled"},
		}
		a.stack = append(a.stack, av)
		a.body.AddAndSwitchToPage(av.Name(), av.Primitive(), true)
		a.tv.SetFocus(av.Primitive())
		av.render()
	})
	shoot("logs", 150, 24, func(a *App) {
		dv := a.stack[0].(*devicesView)
		dv.table.Select(2, 0)
		d, _ := dv.selected()
		lv := newLogsView(a, d)
		a.stack = append(a.stack, lv)
		a.body.AddAndSwitchToPage(lv.Name(), lv.Primitive(), true)
		a.tv.SetFocus(lv.Primitive())
		lv.filter = "Sample"
		lv.append([]string{
			"09-15 15:29:58.101 I/ActivityManager(  612): Start proc 8123:com.example.sample/u0a212 for pre-top-activity",
			"09-15 15:29:58.402 I/Unity   ( 8123): Sample: boot sequence started",
			"09-15 15:29:58.913 D/Sample  ( 8123): config loaded env=qa region=kr",
			"09-15 15:29:59.120 I/Sample  ( 8123): login: provider=google state=begin",
			"09-15 15:29:59.744 W/Sample  ( 8123): login: id token expires in 3599s, refreshing early",
			"09-15 15:30:00.008 I/Sample  ( 8123): login: ok user=12345",
			"09-15 15:30:00.311 I/Sample  ( 8123): fetching mailbox page=1",
			"09-15 15:30:00.590 I/Sample  ( 8123): mailbox: 3 items, 1 unread",
		})
		lv.redraw()
	})
	shoot("images", 150, 22, func(a *App) {
		iv := newImagesView(a)
		iv.rows = []imageRow{
			{platform: device.PlatformAndroid, image: device.Image{ID: "system-images;android-36;google_apis_playstore;arm64-v8a", Name: "google_apis_playstore arm64-v8a", Version: "36", Installed: true}},
			{platform: device.PlatformAndroid, image: device.Image{ID: "system-images;android-35;google_apis_playstore;arm64-v8a", Name: "google_apis_playstore arm64-v8a", Version: "35", Installed: true}},
			{platform: device.PlatformIOS, image: device.Image{ID: "com.apple.CoreSimulator.SimRuntime.iOS-26-5", Name: "iOS 26.5", Version: "26.5", Installed: true}},
			{platform: device.PlatformIOS, image: device.Image{ID: "com.apple.CoreSimulator.SimRuntime.iOS-18-5", Name: "iOS 18.5", Version: "18.5", Installed: true}},
			{platform: device.PlatformAndroid, image: device.Image{ID: "system-images;android-36;google_apis;arm64-v8a", Name: "google_apis arm64-v8a", Version: "36", Installed: false}},
			{platform: device.PlatformAndroid, image: device.Image{ID: "system-images;android-35;google_apis;arm64-v8a", Name: "google_apis arm64-v8a", Version: "35", Installed: false}},
			{platform: device.PlatformAndroid, image: device.Image{ID: "system-images;android-34;google_apis_playstore;arm64-v8a", Name: "google_apis_playstore arm64-v8a", Version: "34", Installed: false}},
		}
		a.stack = append(a.stack, iv)
		a.body.AddAndSwitchToPage(iv.Name(), iv.Primitive(), true)
		a.tv.SetFocus(iv.Primitive())
		iv.render()
	})
	shoot("help", 150, 26, func(a *App) {
		a.push(newHelpView(a))
	})
}

type shotProvider struct {
	fakeProvider
}

func (s *shotProvider) Info(context.Context) [][2]string { return nil }

func screenToSVG(screen tcell.SimulationScreen) string {
	w, h := screen.Size()
	cells, _, _ := screen.GetContents()
	const cw, ch, pad = 8.4, 18.0, 16.0
	var b strings.Builder
	fmt.Fprintf(&b, `<svg xmlns="http://www.w3.org/2000/svg" width="%.0f" height="%.0f" viewBox="0 0 %.0f %.0f">`+"\n",
		float64(w)*cw+2*pad, float64(h)*ch+2*pad, float64(w)*cw+2*pad, float64(h)*ch+2*pad)
	fmt.Fprintf(&b, `<rect width="100%%" height="100%%" rx="10" fill="#11111b"/>`+"\n")
	fmt.Fprintf(&b, `<g font-family="SFMono-Regular, Menlo, Consolas, 'Liberation Mono', monospace" font-size="14" xml:space="preserve">`+"\n")
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			c := cells[y*w+x]
			_, bg, _ := c.Style.Decompose()
			if hex := colorHex(bg, ""); hex != "" {
				fmt.Fprintf(&b, `<rect x="%.1f" y="%.1f" width="%.1f" height="%.1f" fill="%s"/>`+"\n", pad+float64(x)*cw, pad+float64(y)*ch, cw+0.5, ch, hex)
			}
		}
		x := 0
		for x < w {
			c := cells[y*w+x]
			fg, _, attrs := c.Style.Decompose()
			run := []rune{}
			start := x
			for x < w {
				n := cells[y*w+x]
				nfg, _, nattrs := n.Style.Decompose()
				if nfg != fg || nattrs != attrs {
					break
				}
				if len(n.Runes) > 0 {
					run = append(run, n.Runes...)
				} else {
					run = append(run, ' ')
				}
				x++
			}
			weight := ""
			if attrs&tcell.AttrBold != 0 {
				weight = ` font-weight="bold"`
			}
			// one <text> per word, pinned to its cell span with textLength, so a viewer font with a
			// different advance cannot drift the columns and gaps between words stay untouched
			for i := 0; i < len(run); {
				if run[i] == ' ' {
					i++
					continue
				}
				j := i
				for j < len(run) && run[j] != ' ' {
					j++
				}
				word := string(run[i:j])
				fmt.Fprintf(&b, `<text x="%.1f" y="%.1f" textLength="%.1f" lengthAdjust="spacing" fill="%s"%s>%s</text>`+"\n",
					pad+float64(start+i)*cw, pad+float64(y)*ch+13.5, float64(j-i)*cw, colorHex(fg, "#cdd6f4"), weight, html.EscapeString(word))
				i = j
			}
		}
	}
	b.WriteString("</g>\n</svg>\n")
	return b.String()
}

func colorHex(c tcell.Color, def string) string {
	if c == tcell.ColorDefault || c == tcell.ColorReset {
		return def
	}
	if c.Hex() < 0 {
		return def
	}
	return fmt.Sprintf("#%06x", c.Hex())
}
