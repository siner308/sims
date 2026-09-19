package ui

import (
	"strings"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"

	"github.com/siner308/sims/internal/device"
	"github.com/siner308/sims/internal/sims"
)

// Enter on this machine opens its app list, which is what a user expects from every other row in
// the devices table.
func TestEnterOnTheHostOpensItsApps(t *testing.T) {
	a, dv, stop := devicesViewWithHost(t)
	defer stop()

	a.tv.QueueUpdate(func() { dv.onKey(tcell.NewEventKey(tcell.KeyEnter, 0, tcell.ModNone)) })

	var av *appsView
	waitFor(t, a, 10*time.Second, func() bool {
		av, _ = a.top().(*appsView)
		return av != nil
	})
	if !av.dev.IsHost() {
		t.Errorf("the app list opened for %q", av.dev.Name)
	}
	var status string
	a.tv.QueueUpdate(func() { status = a.status.GetText(true) })
	if strings.Contains(status, "cannot list the apps") {
		t.Errorf("enter still errors: %q", status)
	}
}

// Installing onto this machine is not something sims does, so it is neither offered nor attempted.
func TestHostAppsOfferNoInstall(t *testing.T) {
	prov := &proxyProvider{fakeProvider: &fakeProvider{platform: device.PlatformDesktop, devices: []device.Device{hostDevice()}}}
	a := New("test", sims.New(prov))
	_, stop := runHeadless(t, a)
	defer func() { a.m.StopAllCaptures(); stop() }()

	var av *appsView
	a.tv.QueueUpdate(func() {
		av = newAppsView(a, hostDevice())
		a.push(av)
	})
	waitFor(t, a, 5*time.Second, func() bool { return av != nil })

	var keys []string
	for _, h := range av.Hints() {
		if !h.isBreak() {
			keys = append(keys, h.key+"="+h.label)
		}
	}
	joined := strings.Join(keys, " ")
	for _, gone := range []string{"install", "uninstall"} {
		if strings.Contains(joined, gone) {
			t.Errorf("the host app list offers %q: %s", gone, joined)
		}
	}
	if !strings.Contains(joined, "traffic") {
		t.Errorf("the host app list does not offer its traffic: %s", joined)
	}

	// and pressing the key anyway says so rather than opening a file dialog
	a.tv.QueueUpdate(func() { a.setStatus(""); av.onKey(tcell.NewEventKey(tcell.KeyRune, 'i', tcell.ModNone)) })
	var status string
	waitFor(t, a, 5*time.Second, func() bool {
		status = a.status.GetText(true)
		return strings.TrimSpace(status) != ""
	})
	if !strings.Contains(status, "does not install") {
		t.Errorf("status = %q", status)
	}
}
