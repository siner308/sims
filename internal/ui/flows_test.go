package ui

import (
	"context"
	"github.com/gdamore/tcell/v2"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/siner308/sims/internal/capture"
	"github.com/siner308/sims/internal/device"
	"github.com/siner308/sims/internal/proxy"
	"github.com/siner308/sims/internal/sims"
)

// proxyProvider is a fake that accepts being pointed at a proxy, so the flows view can be opened
// without a real device.
type proxyProvider struct {
	*fakeProvider
	steps []device.ProxyStep
}

func (p *proxyProvider) SetProxy(context.Context, device.Device, device.ProxyTarget) ([]device.ProxyStep, error) {
	return p.steps, nil
}
func (p *proxyProvider) ClearProxy(context.Context, device.Device) error { return nil }
func (p *proxyProvider) ProxyState(context.Context, device.Device) (device.ProxyState, error) {
	return device.ProxyState{}, nil
}

func emulator() device.Device {
	return device.Device{
		ID: "avd1", Name: "Pixel_7", Serial: "emulator-5554",
		Platform: device.PlatformAndroid, Kind: device.KindVirtual,
		Transport: device.TransportAVD, State: device.StateBooted,
	}
}

func startFlowsView(t *testing.T) (*App, *flowsView, *capture.Session, func()) {
	t.Helper()
	prov := &proxyProvider{fakeProvider: &fakeProvider{platform: device.PlatformAndroid, devices: []device.Device{emulator()}}}
	a := New("test", sims.New(prov))
	_, stop := runHeadless(t, a)

	session, err := a.m.StartCapture(t.Context(), emulator(), capture.Options{CertDir: t.TempDir()})
	if err != nil {
		stop()
		t.Fatal(err)
	}
	var v *flowsView
	a.tv.QueueUpdate(func() {
		v = newFlowsView(a, emulator(), session)
		a.push(v)
	})
	waitFor(t, a, 5*time.Second, func() bool { return v != nil })
	return a, v, session, func() {
		a.tv.QueueUpdate(func() { v.close() })
		a.m.StopAllCaptures()
		stop()
	}
}

// send puts a finished flow into the store the way the proxy would.
func send(s *capture.Session, method, url string, status int, origin proxy.Origin, process string) {
	// the store is written by the proxy; a test reaches it through the same path a real flow takes
	srv := &proxy.Server{CA: s.CA, Store: s.Store}
	srv.RecordForTest(proxy.Flow{
		Method: method, URL: url, Host: hostFromURL(url), Path: pathFromURL(url),
		Status: status, Kind: proxy.KindHTTP, Done: true, Origin: origin, Process: process,
		Start: time.Now(), ReqHeader: http.Header{}, RespHeader: http.Header{},
	})
}

func hostFromURL(u string) string {
	rest := strings.TrimPrefix(strings.TrimPrefix(u, "https://"), "http://")
	if i := strings.Index(rest, "/"); i >= 0 {
		return rest[:i]
	}
	return rest
}

func pathFromURL(u string) string {
	rest := strings.TrimPrefix(strings.TrimPrefix(u, "https://"), "http://")
	if i := strings.Index(rest, "/"); i >= 0 {
		return rest[i:]
	}
	return "/"
}

// The point of the device filter is that a simulator capture sweeps up this Mac's own apps; hiding
// them has to actually remove those rows.
func TestFlowsDeviceOnlyFiltersHostTraffic(t *testing.T) {
	a, v, s, stop := startFlowsView(t)
	defer stop()

	send(s, "GET", "https://api.example.com/v1/me", 200, proxy.OriginDevice, "Pixel_7")
	send(s, "GET", "https://slack.com/api/rtm", 200, proxy.OriginHost, "Slack")

	a.tv.QueueUpdate(func() { v.deviceOnly = false; v.reload() })
	waitFor(t, a, 5*time.Second, func() bool { return len(v.visible()) == 2 })

	a.tv.QueueUpdate(func() { v.deviceOnly = true; v.render() })
	waitFor(t, a, 5*time.Second, func() bool { return len(v.visible()) == 1 })

	var host string
	a.tv.QueueUpdate(func() { host = v.visible()[0].Host })
	if host != "api.example.com" {
		t.Errorf("device-only kept %q", host)
	}
}

func TestFlowsFilterMatchesURL(t *testing.T) {
	a, v, s, stop := startFlowsView(t)
	defer stop()

	send(s, "GET", "https://api.example.com/v1/me", 200, proxy.OriginDevice, "")
	send(s, "POST", "https://cdn.other.com/upload", 201, proxy.OriginDevice, "")

	a.tv.QueueUpdate(func() { v.deviceOnly = false; v.filter = "upload"; v.reload() })
	waitFor(t, a, 5*time.Second, func() bool { return len(v.visible()) == 1 })

	var method string
	a.tv.QueueUpdate(func() { method = v.visible()[0].Method })
	if method != "POST" {
		t.Errorf("filter kept the %s flow", method)
	}
}

// A tunnel is traffic sims could not read; saying so is the difference between a bug report and an
// understood limit, so the row and the detail both have to explain it.
func TestTunnelRowExplainsItself(t *testing.T) {
	f := proxy.Flow{
		Kind: proxy.KindTunnel, Method: "CONNECT", Host: "pinned.example.com:443", Done: true,
		Error: "TLS handshake failed: remote error: tls: unknown certificate (the device does not trust the sims CA, or the app pins its certificate)",
	}
	if got := statusCell(f); !strings.Contains(got, "tunnel") {
		t.Errorf("status cell = %q", got)
	}
	var b strings.Builder
	writeOverview(&b, f)
	text := b.String()
	if !strings.Contains(text, "pins its") {
		t.Errorf("overview does not explain the tunnel:\n%s", text)
	}
}

// The count in the flash after saving must match what the HAR actually contains: a tunnel carries
// no request or response to write.
func TestHARCountExcludesTunnels(t *testing.T) {
	flows := []proxy.Flow{
		{Kind: proxy.KindHTTP, Done: true},
		{Kind: proxy.KindTunnel, Done: true},
		{Kind: proxy.KindHTTP, Done: false},
	}
	if got := countHAR(flows); got != 1 {
		t.Errorf("countHAR = %d, want 1", got)
	}
}

func TestCaptureNoteWarnsAboutTheMac(t *testing.T) {
	sim := device.Device{Platform: device.PlatformIOS, Kind: device.KindVirtual}
	if note := captureNote(sim); !strings.Contains(note, "Mac") {
		t.Errorf("a simulator capture changes this Mac's settings but the note does not say so:\n%s", note)
	}
	emu := device.Device{Platform: device.PlatformAndroid, Kind: device.KindVirtual}
	if note := captureNote(emu); strings.Contains(note, "this Mac's web proxy") {
		t.Errorf("an emulator does not touch the Mac's proxy, but the note says it does:\n%s", note)
	}
}

// A view taken off the stack by anything other than esc used to keep its goroutine, redrawing a page
// that is no longer shown for the life of the process.
func TestLeavingTheFlowsViewStopsItsGoroutine(t *testing.T) {
	a, v, _, stop := startFlowsView(t)
	defer stop()

	waitFor(t, a, 5*time.Second, func() bool { return v.stop != nil })

	// :img replaces the top view without an esc
	a.tv.QueueUpdate(func() { a.replaceTop(newImagesView(a)) })
	waitFor(t, a, 5*time.Second, func() bool { return v.stop == nil })
}

func TestPopClosesTheFlowsView(t *testing.T) {
	a, v, _, stop := startFlowsView(t)
	defer stop()

	waitFor(t, a, 5*time.Second, func() bool { return v.stop != nil })
	a.tv.QueueUpdate(func() { a.pop() })
	waitFor(t, a, 5*time.Second, func() bool { return v.stop == nil })
}

// l on the traffic table moves to the merged stream without disturbing the capture: the same
// exchanges, now between the log lines they happened around.
func TestMixWithLogKeepsTheCaptureRunning(t *testing.T) {
	a, v, s, stop := startFlowsView(t)
	defer stop()

	send(s, "GET", "https://api.example.com/v1/me", 200, proxy.OriginDevice, "")
	waitFor(t, a, 5*time.Second, func() bool { return len(v.visible()) == 1 })

	a.tv.QueueUpdate(func() { v.mixWithLog() })

	var lv *logsView
	waitFor(t, a, 5*time.Second, func() bool {
		lv, _ = a.top().(*logsView)
		return lv != nil
	})
	var mixing bool
	var entries int
	waitFor(t, a, 5*time.Second, func() bool {
		mixing = lv.mixing()
		if lv.timeline != nil {
			entries = len(lv.timeline.all())
		}
		return mixing && entries > 0
	})
	if _, running := a.m.Capture(emulator()); !running {
		t.Error("moving to the merged view stopped the capture")
	}
	if entries == 0 {
		t.Error("the exchange already captured did not carry over to the merged stream")
	}
}

// t on the merged stream takes the traffic back out and leaves the log running.
func TestTogglingTrafficOffLeavesTheLog(t *testing.T) {
	a, v, s, stop := startFlowsView(t)
	defer stop()
	send(s, "GET", "https://x/y", 200, proxy.OriginDevice, "")
	waitFor(t, a, 5*time.Second, func() bool { return len(v.visible()) == 1 })

	a.tv.QueueUpdate(func() { v.mixWithLog() })
	var lv *logsView
	waitFor(t, a, 5*time.Second, func() bool {
		lv, _ = a.top().(*logsView)
		return lv != nil && lv.mixing()
	})

	a.tv.QueueUpdate(func() { lv.toggleTraffic() })
	waitFor(t, a, 5*time.Second, func() bool { return !lv.mixing() })
	if lv.Name() != "logs" {
		t.Errorf("view name after hiding traffic = %q", lv.Name())
	}
	// the capture itself is untouched: hiding is not stopping
	if _, running := a.m.Capture(emulator()); !running {
		t.Error("hiding the traffic stopped the capture")
	}
}

// mergedView builds a logs view already mixing traffic, with the log stream stubbed out.
func mergedView(t *testing.T) (*App, *logsView, *capture.Session, func()) {
	t.Helper()
	a, v, s, stop := startFlowsView(t)
	a.tv.QueueUpdate(func() { v.mixWithLog() })
	var lv *logsView
	waitFor(t, a, 5*time.Second, func() bool {
		lv, _ = a.top().(*logsView)
		return lv != nil && lv.mixing()
	})
	return a, lv, s, stop
}

// Stepping has to land on a specific exchange. The first version guessed from the scroll position,
// which picked the wrong row whenever the reader had scrolled.
func TestSteppingSelectsExchangesInOrder(t *testing.T) {
	a, lv, s, stop := mergedView(t)
	defer stop()

	send(s, "GET", "https://a.example.com/first", 200, proxy.OriginDevice, "")
	send(s, "POST", "https://b.example.com/second", 201, proxy.OriginDevice, "")
	// waitFor runs its condition on the UI goroutine already; queueing from inside it would deadlock
	waitFor(t, a, 5*time.Second, func() bool {
		lv.timeline.setFlows(s.Flows())
		return len(lv.exchanges()) == 4 // two exchanges, a request and a response each
	})

	// with nothing selected, n starts at the newest exchange
	a.tv.QueueUpdate(func() { lv.step(true) })
	var got proxy.Flow
	waitFor(t, a, 5*time.Second, func() bool {
		f, ok := lv.selectedFlow()
		got = f
		return ok
	})
	if !strings.Contains(got.URL, "second") {
		t.Errorf("n with no cursor selected %q, want the newest", got.URL)
	}

	// shift+n walks back through the stream
	a.tv.QueueUpdate(func() { lv.step(false); lv.step(false) })
	waitFor(t, a, 5*time.Second, func() bool {
		f, ok := lv.selectedFlow()
		got = f
		return ok && strings.Contains(f.URL, "first")
	})
	if !strings.Contains(got.URL, "first") {
		t.Errorf("stepping back reached %q", got.URL)
	}
}

// o opens the selected exchange in place and leaves the others alone.
func TestOpenAffectsOnlyTheSelectedExchange(t *testing.T) {
	a, lv, s, stop := mergedView(t)
	defer stop()

	send(s, "GET", "https://a.example.com/one", 200, proxy.OriginDevice, "")
	send(s, "GET", "https://b.example.com/two", 200, proxy.OriginDevice, "")
	waitFor(t, a, 5*time.Second, func() bool {
		lv.timeline.setFlows(s.Flows())
		return len(lv.exchanges()) == 4
	})

	a.tv.QueueUpdate(func() { lv.step(true); lv.openMore() })
	waitFor(t, a, 5*time.Second, func() bool { return len(lv.opened) == 1 })

	var opened int
	a.tv.QueueUpdate(func() { opened = len(lv.opened) })
	if opened != 1 {
		t.Errorf("opened %d exchanges, want just the selected one", opened)
	}

	// o again goes to the body, a third time folds it back
	a.tv.QueueUpdate(func() { lv.openMore() })
	waitFor(t, a, 5*time.Second, func() bool { return lv.opened[lv.cursor] == detailBody })
	a.tv.QueueUpdate(func() { lv.openMore() })
	waitFor(t, a, 5*time.Second, func() bool { return len(lv.opened) == 0 })
}

// shift+O opens everything, and again closes everything: reading a whole conversation at once.
func TestOpenAllTogglesEverything(t *testing.T) {
	a, lv, s, stop := mergedView(t)
	defer stop()

	send(s, "GET", "https://a.example.com/one", 200, proxy.OriginDevice, "")
	send(s, "GET", "https://b.example.com/two", 200, proxy.OriginDevice, "")
	waitFor(t, a, 5*time.Second, func() bool {
		lv.timeline.setFlows(s.Flows())
		return len(lv.exchanges()) == 4
	})

	a.tv.QueueUpdate(func() { lv.openAll() })
	waitFor(t, a, 5*time.Second, func() bool { return len(lv.opened) == 4 })

	a.tv.QueueUpdate(func() { lv.openAll() })
	waitFor(t, a, 5*time.Second, func() bool { return len(lv.opened) == 0 })
}

// enter with nothing selected has to do something useful rather than nothing: the newest exchange is
// what the reader is watching arrive.
func TestEnterWithNoCursorOpensTheNewestExchange(t *testing.T) {
	a, lv, s, stop := mergedView(t)
	defer stop()

	send(s, "GET", "https://a.example.com/old", 200, proxy.OriginDevice, "")
	send(s, "GET", "https://b.example.com/newest", 200, proxy.OriginDevice, "")
	waitFor(t, a, 5*time.Second, func() bool {
		lv.timeline.setFlows(s.Flows())
		return len(lv.exchanges()) == 4
	})

	a.tv.QueueUpdate(func() { lv.onKey(tcell.NewEventKey(tcell.KeyEnter, 0, tcell.ModNone)) })

	var fv *flowView
	waitFor(t, a, 5*time.Second, func() bool {
		fv, _ = a.top().(*flowView)
		return fv != nil
	})
	f, ok := s.Store.Get(fv.id)
	if !ok {
		t.Fatalf("flow %d is not in the store", fv.id)
	}
	if !strings.Contains(f.URL, "newest") {
		t.Errorf("enter opened %q, want the newest exchange", f.URL)
	}
}

// With nothing selected, n should land on the newest request, not on its response: the request is
// what a reader is looking for, and the response is on the next line.
func TestFirstStepLandsOnTheRequest(t *testing.T) {
	a, lv, s, stop := mergedView(t)
	defer stop()

	send(s, "POST", "https://api.example.com/v1/login", 200, proxy.OriginDevice, "")
	waitFor(t, a, 5*time.Second, func() bool {
		lv.timeline.setFlows(s.Flows())
		return len(lv.exchanges()) == 2
	})

	a.tv.QueueUpdate(func() { lv.step(true) })
	var cursor string
	waitFor(t, a, 5*time.Second, func() bool {
		cursor = lv.cursor
		return cursor != ""
	})
	if !strings.HasPrefix(cursor, "req-") {
		t.Errorf("the first step selected %q, want the request", cursor)
	}
}
