package ui

import (
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/siner308/sims/internal/device"
	"github.com/siner308/sims/internal/sims"
)

// t opens the traffic layer alone wherever it is pressed, and l is what adds the log to it. Both
// branches of the app list used to open the stream with the log already on.
func TestTPressedInTheAppListOpensTrafficAlone(t *testing.T) {
	for _, withApp := range []bool{false, true} {
		name := "with no app selected"
		if withApp {
			name = "with an app selected"
		}
		t.Run(name, func(t *testing.T) {
			prov := &proxyProvider{fakeProvider: &fakeProvider{platform: device.PlatformDesktop, devices: []device.Device{hostDevice()}}}
			a := New("test", sims.New(prov))
			_, stop := runHeadless(t, a)
			defer func() { a.m.StopAllCaptures(); stop() }()

			var av *appsView
			a.tv.QueueUpdate(func() {
				av = newAppsView(a, hostDevice())
				a.push(av)
			})
			waitFor(t, a, 5*time.Second, func() bool { return av != nil && len(av.apps) > 0 })

			// the fake provider offers only system apps, which the list hides until s
			a.tv.QueueUpdate(func() {
				av.showSystem = withApp
				av.render()
			})
			waitFor(t, a, 5*time.Second, func() bool {
				_, ok := av.selected()
				return ok == withApp
			})

			a.tv.QueueUpdate(func() { av.onKey(tcell.NewEventKey(tcell.KeyRune, 't', tcell.ModNone)) })
			waitFor(t, a, 5*time.Second, func() bool { return a.body.HasPage("confirm") })
			pressEnterOnConfirm(t, a)

			var lv *logsView
			waitFor(t, a, 10*time.Second, func() bool {
				lv, _ = a.top().(*logsView)
				return lv != nil
			})
			var mixing, showingLog bool
			onUIResult(a, func() bool {
				mixing, showingLog = lv.mixing(), lv.showingLog()
				return true
			}, 5*time.Second)
			if !mixing {
				t.Error("t did not open the traffic layer")
			}
			if showingLog {
				t.Error("t opened with the log layer on too; l is what adds it")
			}
		})
	}
}
