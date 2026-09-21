package ui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"

	"github.com/siner308/sims/internal/capture"
	"github.com/siner308/sims/internal/device"
	"github.com/siner308/sims/internal/proxy"
	"github.com/siner308/sims/internal/sims"
)

func mixedStream(t *testing.T) (*App, *logsView, *capture.Session, func()) {
	t.Helper()
	prov := &proxyProvider{fakeProvider: &fakeProvider{platform: device.PlatformAndroid, devices: []device.Device{emulator()}}}
	a := New("test", sims.New(prov))
	_, stop := runHeadless(t, a)
	s, err := a.m.StartCapture(t.Context(), emulator(), capture.Options{CertDir: t.TempDir()})
	if err != nil {
		stop()
		t.Fatal(err)
	}
	var lv *logsView
	a.tv.QueueUpdate(func() {
		v := newFlowsView(a, emulator(), s)
		a.push(v)
		v.mixWithLog()
	})
	waitFor(t, a, 5*time.Second, func() bool {
		lv, _ = a.top().(*logsView)
		return lv != nil && lv.mixing()
	})
	return a, lv, s, func() { a.m.StopAllCaptures(); stop() }
}

func TestSteppingUpStopsFollowingUntilBackAtTheNewest(t *testing.T) {
	a, lv, s, stop := mixedStream(t)
	defer stop()

	for i := 0; i < 8; i++ {
		send(s, "POST", fmt.Sprintf("https://intake.example.com/api/v2/replay?dd-request-id=%08d-8ed2-4218-b007-232c02abb9a4&pad=%s", i, strings.Repeat("x", 200)), 202, proxy.OriginDevice, "")
	}
	waitFor(t, a, 5*time.Second, func() bool {
		lv.timeline.setFlows(s.Flows())
		return len(lv.exchanges()) == 8
	})

	var afterStart, afterUp, afterBackToTop, afterDownToNewest bool
	a.tv.QueueUpdate(func() {
		afterStart = lv.following
		lv.step(true)  // onto the newest
		lv.step(false) // one older
		afterUp = lv.following
		for i := 0; i < 10; i++ {
			lv.step(false)
		}
		afterBackToTop = lv.following
		for i := 0; i < 20; i++ {
			lv.step(true)
		}
		afterDownToNewest = lv.following
	})
	waitFor(t, a, 5*time.Second, func() bool { return true })

	if !afterStart {
		t.Error("a fresh stream should follow the newest exchange")
	}
	if afterUp {
		t.Error("stepping up off the newest exchange must stop following, or new arrivals yank the view down")
	}
	if afterBackToTop {
		t.Error("sitting on the oldest exchange must not be treated as following the end")
	}
	if !afterDownToNewest {
		t.Error("stepping back down to the newest exchange should resume following")
	}
}

func TestSteppingToTheTopFillsTheScreen(t *testing.T) {
	prov := &proxyProvider{fakeProvider: &fakeProvider{platform: device.PlatformAndroid, devices: []device.Device{emulator()}}}
	a := New("test", sims.New(prov))
	screen, stop := runHeadless(t, a)
	defer func() { a.m.StopAllCaptures(); stop() }()
	onUI(a, func() { screen.SetSize(200, 45); a.tv.Sync() })

	s, err := a.m.StartCapture(t.Context(), emulator(), capture.Options{CertDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	var lv *logsView
	onUI(a, func() {
		v := newFlowsView(a, emulator(), s)
		a.push(v)
		v.mixWithLog()
	})
	waitFor(t, a, 5*time.Second, func() bool {
		if l, ok := a.top().(*logsView); ok {
			lv = l
		}
		return lv != nil && lv.mixing()
	})
	// long URLs so each exchange wraps to several rows, which is what triggered the blank
	for i := 0; i < 25; i++ {
		send(s, "POST", fmt.Sprintf("https://intake.example.com/api/v2/replay?dd-request-id=%08d&pad=%s", i, strings.Repeat("x", 240)), 202, proxy.OriginDevice, "")
	}
	onUI(a, func() { lv.logOff = true; lv.timeline.setFlows(s.Flows()) })
	waitFor(t, a, 5*time.Second, func() bool { return len(lv.exchanges()) == 25 })

	// End then a run of Up walks the cursor to the first exchange, the case that used to blank the lower half
	screen.InjectKey(tcell.KeyDown, 0, tcell.ModNone)
	time.Sleep(50 * time.Millisecond)
	for i := 0; i < 40; i++ {
		screen.InjectKey(tcell.KeyUp, 0, tcell.ModNone)
		time.Sleep(12 * time.Millisecond)
	}
	time.Sleep(200 * time.Millisecond)

	f, _ := lv.selectedFlow()
	if !strings.Contains(f.URL, "id=00000000&") {
		t.Fatalf("stepping up did not reach the first exchange; cursor on %s", f.URL)
	}
	cells, w, h := screen.GetContents()
	top, bottom, blank := 9, h-3, 0
	for row := top; row <= bottom; row++ {
		var b strings.Builder
		for x := 0; x < w; x++ {
			if c := cells[row*w+x]; len(c.Runes) > 0 {
				b.WriteRune(c.Runes[0])
			}
		}
		if strings.TrimSpace(strings.Trim(strings.TrimRight(b.String(), " "), "║ ")) == "" {
			blank++
		}
	}
	if blank > 0 {
		t.Errorf("%d body rows are blank at the top of a 25-exchange stream; the lower rows blanked", blank)
	}
}
