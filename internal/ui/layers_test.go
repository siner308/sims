package ui

import (
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"

	"github.com/siner308/sims/internal/capture"
	"github.com/siner308/sims/internal/device"
	"github.com/siner308/sims/internal/proxy"
	"github.com/siner308/sims/internal/sims"
)

// streamFor opens the stream view on a device with the traffic layer already on.
func streamFor(t *testing.T, logOff bool) (*App, *logsView, *capture.Session, func()) {
	t.Helper()
	prov := &proxyProvider{fakeProvider: &fakeProvider{platform: device.PlatformAndroid, devices: []device.Device{emulator()}}}
	a := New("test", sims.New(prov))
	_, stop := runHeadless(t, a)

	session, err := a.m.StartCapture(t.Context(), emulator(), capture.Options{CertDir: t.TempDir()})
	if err != nil {
		stop()
		t.Fatal(err)
	}
	var v *logsView
	a.tv.QueueUpdate(func() {
		v = newLogsView(a, emulator(), nil)
		v.session = session
		v.logOff = logOff
		a.push(v)
	})
	waitFor(t, a, 5*time.Second, func() bool { return v != nil })
	return a, v, session, func() { a.m.StopAllCaptures(); stop() }
}

// t opens the traffic stream, and l then adds the log to the same stream rather than replacing it.
func TestTrafficStreamTakesTheLogAsALayer(t *testing.T) {
	a, v, s, stop := streamFor(t, true)
	defer stop()

	if v.showingLog() {
		t.Error("t opened with the log already on")
	}
	if !v.mixing() {
		t.Fatal("t did not turn the traffic on")
	}
	if v.Name() != "traffic" {
		t.Errorf("view = %q, want the traffic stream", v.Name())
	}

	send(s, "GET", "https://api.example.com/v1/me", 200, proxy.OriginDevice, "")
	waitFor(t, a, 5*time.Second, func() bool {
		v.timeline.setFlows(s.Flows())
		return len(v.exchanges()) > 0
	})

	// l adds the log layer; the traffic stays
	a.tv.QueueUpdate(func() { v.onKey(tcell.NewEventKey(tcell.KeyRune, 'l', tcell.ModNone)) })
	waitFor(t, a, 5*time.Second, func() bool { return v.showingLog() })
	if !v.mixing() {
		t.Error("turning the log on dropped the traffic")
	}
	if v.Name() != "logs+traffic" {
		t.Errorf("view = %q, want both layers", v.Name())
	}
	if len(v.exchanges()) == 0 {
		t.Error("the exchanges already captured were lost when the log came on")
	}
}

// l opens the log stream, and t then adds the traffic to the same stream.
func TestLogStreamTakesTheTrafficAsALayer(t *testing.T) {
	prov := &proxyProvider{fakeProvider: &fakeProvider{platform: device.PlatformAndroid, devices: []device.Device{emulator()}}}
	a := New("test", sims.New(prov))
	_, stop := runHeadless(t, a)
	defer func() { a.m.StopAllCaptures(); stop() }()

	var v *logsView
	a.tv.QueueUpdate(func() {
		v = newLogsView(a, emulator(), nil)
		a.push(v)
	})
	waitFor(t, a, 5*time.Second, func() bool { return v != nil })

	if v.mixing() {
		t.Error("the log stream opened with traffic already on")
	}
	if v.Name() != "logs" {
		t.Errorf("view = %q", v.Name())
	}

	// t adds the traffic layer without leaving the log
	a.tv.QueueUpdate(func() { v.onKey(tcell.NewEventKey(tcell.KeyRune, 't', tcell.ModNone)) })
	waitFor(t, a, 5*time.Second, func() bool { return a.body.HasPage("confirm") })
	a.tv.QueueUpdate(func() {
		if _, prim := a.body.GetFrontPage(); prim != nil {
			if h := prim.InputHandler(); h != nil {
				h(tcell.NewEventKey(tcell.KeyRune, 'y', tcell.ModNone), func(tview.Primitive) {})
			}
		}
	})
	waitFor(t, a, 10*time.Second, func() bool { return v.mixing() })
	if !v.showingLog() {
		t.Error("adding the traffic turned the log off")
	}
	if v.Name() != "logs+traffic" {
		t.Errorf("view = %q, want both layers", v.Name())
	}
}

// Each layer toggles on its own, and the last one cannot be turned off: an empty stream shows
// nothing and the user would have no way to tell it from a quiet device.
func TestTheLastLayerStaysOn(t *testing.T) {
	a, v, _, stop := streamFor(t, false)
	defer stop()

	// with traffic on, the log can go
	a.tv.QueueUpdate(func() { v.toggleLog() })
	waitFor(t, a, 5*time.Second, func() bool { return !v.showingLog() })

	// and come back
	a.tv.QueueUpdate(func() { v.toggleLog() })
	waitFor(t, a, 5*time.Second, func() bool { return v.showingLog() })

	// with the traffic off, the log is all there is and stays
	a.tv.QueueUpdate(func() { v.toggleTraffic() })
	waitFor(t, a, 5*time.Second, func() bool { return !v.mixing() })
	a.tv.QueueUpdate(func() { a.clearStatus(); v.toggleLog() })

	var status string
	waitFor(t, a, 5*time.Second, func() bool {
		status = a.status.GetText(true)
		return status != ""
	})
	if v.showingLog() != true {
		t.Error("the only remaining layer was turned off, leaving an empty stream")
	}
}

// Turning the traffic off drops the timeline the exchanges lived on. Anything that draws afterwards
// used to dereference it and take the whole TUI down.
func TestTurningTrafficOffDoesNotCrashTheStream(t *testing.T) {
	a, v, s, stop := streamFor(t, false)
	defer stop()

	send(s, "GET", "https://api.example.com/v1/me", 200, proxy.OriginDevice, "")
	waitFor(t, a, 5*time.Second, func() bool {
		v.timeline.setFlows(s.Flows())
		return len(v.exchanges()) > 0
	})

	a.tv.QueueUpdate(func() { v.toggleTraffic() })
	waitFor(t, a, 5*time.Second, func() bool { return !v.mixing() })

	// every path that touches the timeline has to survive it being gone
	done := make(chan struct{})
	a.tv.QueueUpdate(func() {
		defer close(done)
		_ = v.visibleEntries()
		_ = v.exchanges()
		v.redraw()
		v.append([]string{"09-20 12:04:01.220 I/Example: still logging"})
		v.onKey(tcell.NewEventKey(tcell.KeyRune, 'c', tcell.ModNone))
	})
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the stream stopped responding after the traffic went off")
	}
	if v.Name() != "logs" {
		t.Errorf("view = %q, want the log alone", v.Name())
	}
}
