package ui

import (
	"strings"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"

	"github.com/siner308/sims/internal/device"
	"github.com/siner308/sims/internal/sims"
)

func hostDevice() device.Device {
	return device.Device{
		ID: "localhost", Name: "This Mac", Model: "Mac16,10",
		Platform: device.PlatformDesktop, Kind: device.KindHost,
		Transport: device.TransportLocal, Runtime: "macOS 26.6.2", State: device.StateConnected,
	}
}

func devicesViewWithHost(t *testing.T) (*App, *devicesView, func()) {
	t.Helper()
	prov := &proxyProvider{fakeProvider: &fakeProvider{platform: device.PlatformDesktop, devices: []device.Device{hostDevice()}}}
	a := New("test", sims.New(prov))
	_, stop := runHeadless(t, a)
	dv := a.stack[0].(*devicesView)
	waitFor(t, a, 5*time.Second, func() bool { return dv.table.GetRowCount() == 2 })
	return a, dv, func() { a.m.StopAllCaptures(); stop() }
}

// Enter on this machine used to fail with "cannot list the apps of the machine it runs on" and
// leave the user nowhere. A Mac does have apps, so it opens them like any other device.
func TestEnterOnTheHostOpensAnAppList(t *testing.T) {
	a, dv, stop := devicesViewWithHost(t)
	defer stop()

	a.tv.QueueUpdate(func() { dv.onKey(tcell.NewEventKey(tcell.KeyEnter, 0, tcell.ModNone)) })
	waitFor(t, a, 10*time.Second, func() bool {
		_, ok := a.top().(*appsView)
		return ok
	})

	var status string
	a.tv.QueueUpdate(func() { status = a.status.GetText(true) })
	if strings.Contains(status, "unsupported") || strings.Contains(status, "cannot list the apps") {
		t.Errorf("enter on this machine still errors: %q", status)
	}
}

// Boot, wipe and delete have no meaning here. Each says so in one line rather than surfacing the
// provider's unsupported-operation error.
func TestHostRefusesDeviceActionsInOneLine(t *testing.T) {
	a, dv, stop := devicesViewWithHost(t)
	defer stop()

	for _, k := range []struct {
		name string
		ev   *tcell.EventKey
	}{
		{"boot", tcell.NewEventKey(tcell.KeyRune, 'b', tcell.ModNone)},
		{"wipe", tcell.NewEventKey(tcell.KeyCtrlE, 0, tcell.ModNone)},
		{"delete", tcell.NewEventKey(tcell.KeyCtrlD, 0, tcell.ModNone)},
	} {
		a.tv.QueueUpdate(func() { a.clearStatus(); dv.onKey(k.ev) })
		var status string
		var asked bool
		waitFor(t, a, 5*time.Second, func() bool {
			status = a.status.GetText(true)
			asked = a.body.HasPage("confirm")
			return strings.TrimSpace(status) != "" || asked
		})
		if !strings.Contains(status, "machine sims is running on") {
			t.Errorf("%s: status = %q", k.name, status)
		}
		if asked {
			t.Errorf("%s asked for confirmation of something it cannot do", k.name)
		}
	}
}

// The hints are what a user reads before pressing anything; offering boot and install here would
// send them straight into a refusal.
func TestHostHintsOfferOnlyWhatWorks(t *testing.T) {
	_, dv, stop := devicesViewWithHost(t)
	defer stop()

	var keys []string
	for _, h := range dv.Hints() {
		if !h.isBreak() {
			keys = append(keys, h.key+"="+h.label)
		}
	}
	joined := strings.Join(keys, " ")
	for _, gone := range []string{"boot", "wipe", "delete device", "new device", "pair"} {
		if strings.Contains(joined, gone) {
			t.Errorf("hints still offer %q for this machine: %s", gone, joined)
		}
	}
	for _, want := range []string{"traffic", "log"} {
		if !strings.Contains(joined, want) {
			t.Errorf("hints do not offer %q: %s", want, joined)
		}
	}
}

// The confirmation is the last thing shown before a capture changes anything, so it has to describe
// the device in hand. This machine fell through to the Android wording and talked about emulators.
func TestHostCaptureNoteDescribesThisMachine(t *testing.T) {
	note := captureNote(hostDevice())
	for _, wrong := range []string{"emulator", "API 24", "simulator", "phone"} {
		if strings.Contains(note, wrong) {
			t.Errorf("the note for this machine mentions %q:\n%s", wrong, note)
		}
	}
	for _, want := range []string{"this machine", "process"} {
		if !strings.Contains(note, want) {
			t.Errorf("the note does not say %q:\n%s", want, note)
		}
	}
}
