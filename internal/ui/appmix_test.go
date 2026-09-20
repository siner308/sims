package ui

import (
	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
	"strings"
	"testing"
	"time"

	"github.com/siner308/sims/internal/capture"
	"github.com/siner308/sims/internal/device"
	"github.com/siner308/sims/internal/proxy"
	"github.com/siner308/sims/internal/sims"
)

// From the apps list, l opens that app's log and t then mixes in the device's traffic. The log stays
// narrowed to the app while the traffic is the whole device, which is the combination that answers
// "my app logged this, and something on this device sent that at the same moment".
func TestAppLogMixesWithWholeDeviceTraffic(t *testing.T) {
	phone := device.Device{
		ID: "R3C", Name: "SM S928N", Serial: "R3C",
		Platform: device.PlatformAndroid, Kind: device.KindPhysical,
		Transport: device.TransportUSB, State: device.StateConnected,
	}
	prov := &proxyProvider{fakeProvider: &fakeProvider{platform: device.PlatformAndroid, devices: []device.Device{phone}}}
	a := New("test", sims.New(prov))
	_, stop := runHeadless(t, a)
	defer func() {
		a.m.StopAllCaptures()
		stop()
	}()

	app := device.App{BundleID: "com.example.app", Name: "Example", Process: "com.example.app"}
	var lv *logsView
	a.tv.QueueUpdate(func() {
		lv = newLogsView(a, phone, &app)
		a.push(lv)
	})
	waitFor(t, a, 5*time.Second, func() bool { return lv != nil })

	// the log is narrowed to the app
	if lv.only == nil || lv.only.BundleID != "com.example.app" {
		t.Fatalf("the log view is not scoped to the app: %+v", lv.only)
	}
	if !strings.Contains(lv.title(), "Example") {
		t.Errorf("title does not name the app: %q", lv.title())
	}

	session, err := a.m.StartCapture(t.Context(), phone, capture.Options{CertDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	a.tv.QueueUpdate(func() {
		lv.session = session
		lv.Refresh()
	})
	waitFor(t, a, 5*time.Second, func() bool { return lv.mixing() })

	// traffic from another process on the same device still shows: the capture is device-wide
	send(session, "GET", "https://ads.example.net/beacon", 200, proxy.OriginDevice, "com.other.app")
	waitFor(t, a, 5*time.Second, func() bool {
		lv.timeline.setFlows(lv.session.Flows())
		return len(lv.exchanges()) > 0
	})

	var host string
	a.tv.QueueUpdate(func() { host = lv.exchanges()[0].flow.Host })
	if host != "ads.example.net" {
		t.Errorf("another process's request did not reach the app-scoped view: %q", host)
	}
	// and the log is still the app's alone
	if lv.only == nil {
		t.Error("mixing in traffic widened the log past the app")
	}
	if !strings.Contains(lv.title(), "Example") {
		t.Errorf("title lost the app after mixing: %q", lv.title())
	}
}

// t on the apps list opens the device's traffic and remembers which app was chosen, so l adds that
// app's log to the same stream rather than the whole device's.
func TestAppsViewTOpensTheTrafficKeepingTheApp(t *testing.T) {
	emu := emulator()
	prov := &proxyProvider{fakeProvider: &fakeProvider{platform: device.PlatformAndroid, devices: []device.Device{emu}}}
	a := New("test", sims.New(prov))
	_, stop := runHeadless(t, a)
	defer func() {
		a.m.StopAllCaptures()
		stop()
	}()

	var av *appsView
	a.tv.QueueUpdate(func() {
		av = newAppsView(a, emu)
		a.push(av)
	})
	waitFor(t, a, 5*time.Second, func() bool { return av != nil && len(av.apps) > 0 })
	// the fake's apps are all preinstalled, which the view hides by default
	a.tv.QueueUpdate(func() { av.showSystem = true; av.render() })
	waitFor(t, a, 5*time.Second, func() bool {
		_, ok := av.selected()
		return ok
	})

	a.tv.QueueUpdate(func() { av.onKey(tcell.NewEventKey(tcell.KeyRune, 't', tcell.ModNone)) })
	// starting a capture changes settings on the device, so it asks first
	waitFor(t, a, 10*time.Second, func() bool { return a.body.HasPage("confirm") })
	a.tv.QueueUpdate(func() {
		_, prim := a.body.GetFrontPage()
		if prim == nil {
			return
		}
		if h := prim.InputHandler(); h != nil {
			h(tcell.NewEventKey(tcell.KeyRune, 'y', tcell.ModNone), func(tview.Primitive) {})
		}
	})

	var lv *logsView
	waitFor(t, a, 10*time.Second, func() bool {
		lv, _ = a.top().(*logsView)
		return lv != nil && lv.mixing()
	})
	if lv.only == nil {
		t.Error("t forgot the selected app, so l would open the whole device's log")
	}
	if lv.Name() != "traffic" {
		t.Errorf("view = %q; t opens the traffic layer alone", lv.Name())
	}
}
