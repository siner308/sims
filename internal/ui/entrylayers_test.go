//go:build darwin

package ui

import (
	"strings"
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

// A capture that cannot finish its own setup has to say what is left to do before the stream opens.
// Certificate trust is the usual one: without it HTTPS arrives as unopened tunnels, and a user who
// is not told waits for bodies that cannot come.
func TestASetupStepTheCaptureCannotDoIsShownBeforeTheStream(t *testing.T) {
	left := manualSteps([]device.ProxyStep{
		{Title: "proxy", Detail: "points here while the capture runs"},
		{Title: "certificate", Detail: "run sims proxy ca localhost --install", Manual: true},
	})
	if !strings.Contains(left, "--install") {
		t.Errorf("the step the user still has to do was dropped: %q", left)
	}
	if strings.Contains(left, "points here") {
		t.Errorf("a step sims did itself was read back as homework: %q", left)
	}

	prov := &proxyProvider{
		fakeProvider: &fakeProvider{platform: device.PlatformDesktop, devices: []device.Device{hostDevice()}},
		steps: []device.ProxyStep{
			{Title: "certificate", Detail: "run sims proxy ca localhost --install", Manual: true},
		},
	}
	a := New("test", sims.New(prov))
	_, stop := runHeadless(t, a)
	defer func() { a.m.StopAllCaptures(); stop() }()

	var dv *devicesView
	waitFor(t, a, 5*time.Second, func() bool {
		dv, _ = a.top().(*devicesView)
		return dv != nil
	})

	a.tv.QueueUpdate(func() { dv.watchTraffic() })
	waitFor(t, a, 5*time.Second, func() bool { return a.body.HasPage("confirm") })
	pressEnterOnConfirm(t, a)

	// the capture starts, and the leftover step holds the stream behind a second confirmation
	waitFor(t, a, 10*time.Second, func() bool { return a.body.HasPage("confirm") })
	var opened bool
	onUIResult(a, func() bool {
		_, opened = a.top().(*logsView)
		return true
	}, 5*time.Second)
	if opened {
		t.Error("the stream opened before the user was told what setup is still missing")
	}
}
