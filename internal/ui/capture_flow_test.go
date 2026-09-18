package ui

import (
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
	"github.com/siner308/sims/internal/device"
	"github.com/siner308/sims/internal/sims"
)

// Pressing t on a device has to reach a running capture and a flows view, and leaving the TUI has
// to take the capture down with it.
func TestPressingTStartsAndStopsACapture(t *testing.T) {
	prov := &proxyProvider{fakeProvider: &fakeProvider{platform: device.PlatformAndroid, devices: []device.Device{emulator()}}}
	m := sims.New(prov)
	a := New("test", m)
	screen, stop := runHeadless(t, a)
	_ = screen

	dv := a.stack[0].(*devicesView)
	waitFor(t, a, 5*time.Second, func() bool { return dv.table.GetRowCount() == 2 })

	// t on the devices view asks for confirmation, then y accepts
	a.tv.QueueUpdate(func() { dv.onKey(tcell.NewEventKey(tcell.KeyRune, 't', tcell.ModNone)) })
	waitFor(t, a, 5*time.Second, func() bool { return a.body.HasPage("confirm") })
	a.tv.QueueUpdate(func() {
		if _, prim := a.body.GetFrontPage(); prim != nil {
			prim.InputHandler()(tcell.NewEventKey(tcell.KeyRune, 'y', tcell.ModNone), func(tview.Primitive) {})
		}
	})

	waitFor(t, a, 10*time.Second, func() bool {
		_, running := m.Capture(emulator())
		return running
	})
	waitFor(t, a, 10*time.Second, func() bool {
		_, ok := a.top().(*flowsView)
		return ok
	})

	stop()
	if _, running := m.Capture(emulator()); running {
		t.Error("the capture outlived the TUI")
	}
}
