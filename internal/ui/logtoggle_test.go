package ui

import (
	"context"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/siner308/sims/internal/capture"
	"github.com/siner308/sims/internal/device"
	"github.com/siner308/sims/internal/sims"
)

// Turning the log on clears the text to make room for the stream. The exchanges already captured
// have to be back on screen when it returns, whichever way that happens: the redraw on the spot, or
// the traffic watcher's next tick a quarter second later.
func TestTurningTheLogOnKeepsTheTrafficOnScreen(t *testing.T) {
	prov := &proxyLogProvider{
		proxyProvider: proxyProvider{fakeProvider: &fakeProvider{
			platform: device.PlatformAndroid, devices: []device.Device{emulator()},
		}},
		// a log that keeps talking, the way a real device does
		script: "while :; do echo \"09-21 01:00:00.000 I/App: tick\"; sleep 0.05; done",
	}
	a := New("test", sims.New(prov))
	_, stop := runHeadless(t, a)
	defer func() { a.m.StopAllCaptures(); stop() }()

	session, err := a.m.StartCapture(t.Context(), emulator(), capture.Options{CertDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	var v *logsView
	a.tv.QueueUpdate(func() {
		v = newTrafficView(a, emulator(), nil, session)
		a.push(v)
	})
	waitFor(t, a, 5*time.Second, func() bool { return v != nil })

	send(session, "GET", "https://a.example.com/one", 200, "", "")
	waitFor(t, a, 5*time.Second, func() bool {
		v.timeline.setFlows(session.Flows())
		v.redraw()
		return strings.Contains(v.text.GetText(true), "a.example.com")
	})

	a.tv.QueueUpdate(func() { v.toggleLog() })
	waitFor(t, a, 5*time.Second, func() bool { return v.showingLog() })

	var shown string
	waitFor(t, a, 5*time.Second, func() bool {
		shown = v.text.GetText(true)
		return strings.Contains(shown, "a.example.com")
	})
	if !strings.Contains(shown, "a.example.com") {
		t.Errorf("the traffic vanished when the log was turned on: %q", shown)
	}

	// and turning it back off leaves the traffic where it was
	a.tv.QueueUpdate(func() { v.toggleLog() })
	waitFor(t, a, 5*time.Second, func() bool { return !v.showingLog() })
	waitFor(t, a, 5*time.Second, func() bool {
		shown = v.text.GetText(true)
		return strings.Contains(shown, "a.example.com")
	})
}

type proxyLogProvider struct {
	proxyProvider
	script string
}

func (p *proxyLogProvider) LogCmd(ctx context.Context, _ device.Device, _ *device.App) (*exec.Cmd, error) {
	return exec.CommandContext(ctx, "sh", "-c", p.script), nil
}
