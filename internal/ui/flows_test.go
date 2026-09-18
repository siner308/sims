package ui

import (
	"context"
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
