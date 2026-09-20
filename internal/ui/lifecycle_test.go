package ui

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/siner308/sims/internal/capture"
	"github.com/siner308/sims/internal/device"
	"github.com/siner308/sims/internal/sims"
)

// The log view renames itself when traffic is mixed in. The page registry is keyed on a name, so a
// view that renamed itself was removed under a key it was never added under and its widget stayed
// registered for the life of the process.
func TestRenamingTheLogViewStillRemovesItsPage(t *testing.T) {
	prov := &proxyProvider{fakeProvider: &fakeProvider{platform: device.PlatformAndroid, devices: []device.Device{emulator()}}}
	a := New("test", sims.New(prov))
	_, stop := runHeadless(t, a)
	defer func() { a.m.StopAllCaptures(); stop() }()

	var lv *logsView
	a.tv.QueueUpdate(func() {
		lv = newLogsView(a, emulator(), nil)
		a.push(lv)
	})
	waitFor(t, a, 5*time.Second, func() bool { return lv != nil })

	before := len(a.body.GetPageNames(false))

	// mixing in traffic changes Name() from "logs" to "logs+traffic"
	session, err := a.m.StartCapture(t.Context(), emulator(), capture.Options{CertDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	a.tv.QueueUpdate(func() { lv.session = session; lv.Refresh() })
	waitFor(t, a, 5*time.Second, func() bool { return lv.mixing() })

	a.tv.QueueUpdate(func() { a.pop() })
	waitFor(t, a, 5*time.Second, func() bool { return len(a.body.GetPageNames(false)) == before-1 })

	var names []string
	a.tv.QueueUpdate(func() { names = a.body.GetPageNames(false) })
	for _, n := range names {
		if n == "logs" || n == "logs+traffic" {
			t.Errorf("the popped log page is still registered: %v", names)
		}
	}
}

// Stopping a capture queues a Refresh for when the device work finishes, which can be seconds. A
// user who presses esc in the meantime used to get both goroutines started again on a view that
// was no longer on the stack, where they ran for the life of the process.
func TestRefreshAfterCloseDoesNotRestartTheStreams(t *testing.T) {
	prov := &proxyProvider{fakeProvider: &fakeProvider{platform: device.PlatformAndroid, devices: []device.Device{emulator()}}}
	a := New("test", sims.New(prov))
	_, stop := runHeadless(t, a)
	defer func() { a.m.StopAllCaptures(); stop() }()

	session, err := a.m.StartCapture(t.Context(), emulator(), capture.Options{CertDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	var lv *logsView
	a.tv.QueueUpdate(func() {
		lv = newLogsView(a, emulator(), nil)
		lv.session = session
		a.push(lv)
	})
	waitFor(t, a, 5*time.Second, func() bool { return lv != nil && lv.mixing() })

	a.tv.QueueUpdate(func() { a.pop() })
	waitFor(t, a, 5*time.Second, func() bool { return lv.watchOff == nil })

	// the late Refresh a stopCapture callback would queue
	a.tv.QueueUpdate(func() { lv.Refresh() })
	time.Sleep(300 * time.Millisecond)

	var revived bool
	a.tv.QueueUpdate(func() { revived = lv.watchOff != nil || lv.cancel != nil })
	if revived {
		t.Error("a Refresh after the view was popped started its goroutines again")
	}
}

// The same guard on the flows view, where the shape is identical.
func TestFlowsRefreshAfterCloseDoesNothing(t *testing.T) {
	a, v, _, stop := startFlowsView(t)
	defer stop()

	waitFor(t, a, 5*time.Second, func() bool { return v.stop != nil })
	a.tv.QueueUpdate(func() { v.close() })
	waitFor(t, a, 5*time.Second, func() bool { return v.stop == nil })

	a.tv.QueueUpdate(func() { v.Refresh() })
	time.Sleep(300 * time.Millisecond)

	var revived bool
	a.tv.QueueUpdate(func() { revived = v.stop != nil })
	if revived {
		t.Error("a Refresh after close restarted the flows follower")
	}
}

// An error must stay on screen. Any background job finishing clears the status line, and an error
// that vanishes within a moment leaves the user thinking the key did nothing.
func TestAnErrorSurvivesABackgroundJobFinishing(t *testing.T) {
	prov := &proxyProvider{fakeProvider: &fakeProvider{platform: device.PlatformAndroid, devices: []device.Device{emulator()}}}
	a := New("test", sims.New(prov))
	_, stop := runHeadless(t, a)
	defer func() { a.m.StopAllCaptures(); stop() }()

	a.tv.QueueUpdate(func() { a.flashErr(errUnderTest) })
	// a background job completing clears the status, the way a refresh does
	a.tv.QueueUpdate(func() { a.startSpinner("working"); a.stopSpinner() })

	var status string
	a.tv.QueueUpdate(func() { status = a.status.GetText(true) })
	waitFor(t, a, 3*time.Second, func() bool {
		status = a.status.GetText(true)
		return status != ""
	})
	if !strings.Contains(status, errUnderTest.Error()) {
		t.Errorf("the error was wiped from the status line: %q", status)
	}
}

var errUnderTest = errors.New("this device cannot do that")
