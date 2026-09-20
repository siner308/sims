package ui

import (
	"context"
	"errors"
	"os/exec"
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

// A log the platform cannot open must be refused before the view appears. Opening a screen that
// then says it cannot do what it was opened for leaves the user nowhere.
func TestAnImpossibleAppLogIsRefusedBeforeOpening(t *testing.T) {
	prov := &noAppLogProvider{fakeProvider: &fakeProvider{platform: device.PlatformAndroid, devices: []device.Device{emulator()}}}
	a := New("test", sims.New(prov))
	_, stop := runHeadless(t, a)
	defer func() { a.m.StopAllCaptures(); stop() }()

	var av *appsView
	a.tv.QueueUpdate(func() {
		av = newAppsView(a, emulator())
		a.push(av)
	})
	waitFor(t, a, 5*time.Second, func() bool { return av != nil && len(av.apps) > 0 })
	a.tv.QueueUpdate(func() { av.showSystem = true; av.render() })
	waitFor(t, a, 5*time.Second, func() bool {
		_, ok := av.selected()
		return ok
	})

	a.tv.QueueUpdate(func() { a.setStatus(""); av.onKey(tcell.NewEventKey(tcell.KeyRune, 'l', tcell.ModNone)) })

	var status string
	waitFor(t, a, 5*time.Second, func() bool {
		status = a.status.GetText(true)
		return strings.TrimSpace(status) != ""
	})
	if !strings.Contains(status, "cannot") && !strings.Contains(status, "no log") {
		t.Errorf("status = %q", status)
	}
	if _, opened := a.top().(*logsView); opened {
		t.Error("the log view opened even though the platform cannot produce that log")
	}
}

// noAppLogProvider refuses a log scoped to one app, the way a platform without the capability does.
type noAppLogProvider struct {
	*fakeProvider
}

func (p *noAppLogProvider) LogCmd(_ context.Context, _ device.Device, app *device.App) (*exec.Cmd, error) {
	if app != nil {
		return nil, errors.New("sims cannot filter its log by app here")
	}
	return exec.Command("true"), nil
}
