package ui

import (
	"testing"
	"time"

	"github.com/siner308/sims/internal/device"
	"github.com/siner308/sims/internal/proxy"
)

// The merged view was written for phones. The machine itself has to work the same way, or the claim
// that t and l compose on a desktop is untrue.
//
// The device is marked virtual rather than host on purpose: a real host capture changes this
// machine's own proxy settings, and a test that does that fights every other test and leaves the
// developer's machine altered. What is under test here is the view, not the plumbing.
func TestMergedViewWorksForTheHostDevice(t *testing.T) {
	host := device.Device{
		ID: "localhost", Name: "This Mac",
		Platform: device.PlatformDesktop, Kind: device.KindVirtual,
		Transport: device.TransportLocal, State: device.StateConnected,
	}
	prov := &proxyProvider{fakeProvider: &fakeProvider{platform: device.PlatformDesktop, devices: []device.Device{host}}}
	a, v, s, stop := startFlowsViewFor(t, prov, host)
	defer stop()

	send(s, "GET", "https://api.example.com/from-my-mac", 200, proxy.OriginDevice, "curl")
	waitFor(t, a, 5*time.Second, func() bool { return len(v.visible()) == 1 })

	a.tv.QueueUpdate(func() { v.mixWithLog() })
	var lv *logsView
	waitFor(t, a, 5*time.Second, func() bool {
		lv, _ = a.top().(*logsView)
		return lv != nil && lv.mixing()
	})
	waitFor(t, a, 5*time.Second, func() bool {
		lv.timeline.setFlows(lv.session.Flows())
		return len(lv.exchanges()) > 0
	})
	if lv.Name() != "logs+traffic" {
		t.Errorf("view = %q", lv.Name())
	}
}
