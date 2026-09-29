package ui

import (
	"strings"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"

	"github.com/siner308/sims/internal/device"
	"github.com/siner308/sims/internal/sims"
)

// An app that crashes takes its log stream down with it, and esc on that view has to close it the way it closes a live one.
func TestEscapeClosesTheLogViewAfterItsStreamDied(t *testing.T) {
	p := &logProvider{fakeProvider: fakeProvider{platform: device.PlatformAndroid}, script: "echo one; echo two; exit 1"}
	a := New("test", sims.New(p))
	screen, stop := runHeadless(t, a)
	defer stop()

	dev := device.Device{ID: "dev", Name: "dev", Platform: device.PlatformAndroid, State: device.StateBooted}
	var lv *logsView
	onUI(a, func() {
		a.push(newAppsView(a, dev))
		lv = newLogsView(a, dev, &device.App{BundleID: "com.example.app", Name: "App"})
		a.push(lv)
	})
	waitFor(t, a, 5*time.Second, func() bool {
		return lv != nil && strings.Contains(a.status.GetText(true), "log stream ended")
	})
	depth := onUIGet(a, func() int { return len(a.stack) })

	screen.InjectKey(tcell.KeyEscape, 0, tcell.ModNone)

	waitFor(t, a, 5*time.Second, func() bool { return len(a.stack) == depth-1 })
	if !onUIGet(a, func() bool { return lv.closed }) {
		t.Error("the log view was popped but not closed")
	}
}
