package ui

import (
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"

	"github.com/siner308/sims/internal/device"
	"github.com/siner308/sims/internal/sims"
)

// pressEnterOnConfirm answers the open confirmation with enter alone, the way a user carrying on
// from a key they already pressed would.
func pressEnterOnConfirm(t *testing.T, a *App) {
	t.Helper()
	a.tv.QueueUpdate(func() {
		_, prim := a.body.GetFrontPage()
		if prim == nil {
			return
		}
		if h := prim.InputHandler(); h != nil {
			h(tcell.NewEventKey(tcell.KeyEnter, 0, tcell.ModNone), func(tview.Primitive) {})
		}
	})
}

// An ordinary confirmation follows a key the user already pressed, so enter carries on rather than
// making them reach for the arrow keys first.
func TestOrdinaryConfirmDefaultsToYes(t *testing.T) {
	prov := &proxyProvider{fakeProvider: &fakeProvider{platform: device.PlatformAndroid, devices: []device.Device{emulator()}}}
	a := New("test", sims.New(prov))
	_, stop := runHeadless(t, a)
	defer func() { a.m.StopAllCaptures(); stop() }()

	answered := make(chan struct{}, 1)
	a.tv.QueueUpdate(func() { a.confirm("carry on?", func() { answered <- struct{}{} }) })
	waitFor(t, a, 5*time.Second, func() bool { return a.body.HasPage("confirm") })

	pressEnterOnConfirm(t, a)
	select {
	case <-answered:
	case <-time.After(5 * time.Second):
		t.Error("enter on an ordinary confirmation did not go ahead")
	}
}

// Wiping, deleting and uninstalling cannot be undone, so enter alone must not do them: the cursor
// starts on No and saying yes has to be deliberate.
func TestDangerousConfirmDefaultsToNo(t *testing.T) {
	prov := &proxyProvider{fakeProvider: &fakeProvider{platform: device.PlatformAndroid, devices: []device.Device{emulator()}}}
	a := New("test", sims.New(prov))
	_, stop := runHeadless(t, a)
	defer func() { a.m.StopAllCaptures(); stop() }()

	destroyed := make(chan struct{}, 1)
	a.tv.QueueUpdate(func() { a.confirmDangerous("delete everything?", func() { destroyed <- struct{}{} }) })
	waitFor(t, a, 5*time.Second, func() bool { return a.body.HasPage("confirm") })

	pressEnterOnConfirm(t, a)
	select {
	case <-destroyed:
		t.Error("enter alone destroyed something")
	case <-time.After(time.Second):
	}

	// y still answers yes, which is how a user says it deliberately
	a.tv.QueueUpdate(func() { a.confirmDangerous("delete everything?", func() { destroyed <- struct{}{} }) })
	waitFor(t, a, 5*time.Second, func() bool { return a.body.HasPage("confirm") })
	a.tv.QueueUpdate(func() {
		if _, prim := a.body.GetFrontPage(); prim != nil {
			if h := prim.InputHandler(); h != nil {
				h(tcell.NewEventKey(tcell.KeyRune, 'y', tcell.ModNone), func(tview.Primitive) {})
			}
		}
	})
	select {
	case <-destroyed:
	case <-time.After(5 * time.Second):
		t.Error("y did not answer yes on a dangerous confirmation")
	}
}
