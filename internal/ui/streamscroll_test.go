package ui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/siner308/sims/internal/capture"
	"github.com/siner308/sims/internal/device"
	"github.com/siner308/sims/internal/proxy"
	"github.com/siner308/sims/internal/sims"
)

// onUIGet runs fn on the UI goroutine and returns its result over a channel, the synchronisation the race detector needs to read a view's state from a test safely.
func onUIGet[T any](a *App, fn func() T) T {
	ch := make(chan T, 1)
	a.tv.QueueUpdate(func() { ch <- fn() })
	return <-ch
}

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
		send(s, "POST", fmt.Sprintf("https://intake.example.com/api/v2/replay?dd-request-id=%08d&pad=%s", i, strings.Repeat("x", 200)), 202, proxy.OriginDevice, "")
	}
	waitFor(t, a, 5*time.Second, func() bool {
		lv.timeline.setFlows(s.Flows())
		return len(lv.exchanges()) == 8
	})

	got := onUIGet(a, func() [4]bool {
		afterStart := lv.following
		lv.step(true)  // onto the newest
		lv.step(false) // one older
		afterUp := lv.following
		for i := 0; i < 10; i++ {
			lv.step(false)
		}
		afterBackToTop := lv.following
		for i := 0; i < 20; i++ {
			lv.step(true)
		}
		afterDownToNewest := lv.following
		return [4]bool{afterStart, afterUp, afterBackToTop, afterDownToNewest}
	})

	if !got[0] {
		t.Error("a fresh stream should follow the newest exchange")
	}
	if got[1] {
		t.Error("stepping up off the newest exchange must stop following, or new arrivals yank the view down")
	}
	if got[2] {
		t.Error("sitting on the oldest exchange must not be treated as following the end")
	}
	if !got[3] {
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
	// the 240-char pad makes each exchange wrap to several rows, which is what triggered the blank
	for i := 0; i < 25; i++ {
		send(s, "POST", fmt.Sprintf("https://intake.example.com/api/v2/replay?dd-request-id=%08d&pad=%s", i, strings.Repeat("x", 240)), 202, proxy.OriginDevice, "")
	}
	waitFor(t, a, 5*time.Second, func() bool {
		lv.logOff = true
		lv.timeline.setFlows(s.Flows())
		return len(lv.exchanges()) == 25
	})

	onUIGet(a, func() bool { lv.moveTo(0); return true })
	a.tv.QueueUpdateDraw(func() {})

	type snap struct {
		url         string
		blank, rows int
	}
	got := onUIGet(a, func() snap {
		f, _ := lv.selectedFlow()
		cells, w, h := screen.GetContents()
		out := snap{url: f.URL}
		for row := 9; row <= h-3; row++ {
			var b strings.Builder
			for x := 0; x < w; x++ {
				if c := cells[row*w+x]; len(c.Runes) > 0 {
					b.WriteRune(c.Runes[0])
				}
			}
			out.rows++
			if strings.TrimSpace(strings.Trim(strings.TrimRight(b.String(), " "), "║ ")) == "" {
				out.blank++
			}
		}
		return out
	})
	if !strings.Contains(got.url, "id=00000000&") {
		t.Fatalf("moving to the top selected %s, not the first exchange", got.url)
	}
	if got.blank > 0 {
		t.Errorf("%d of %d body rows are blank with the cursor on the first of 25 wrapping exchanges; the lower rows blanked", got.blank, got.rows)
	}
}
