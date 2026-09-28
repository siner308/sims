package ui

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/siner308/sims/internal/device"
	"github.com/siner308/sims/internal/sims"
)

func job(a *App, caption string, work func() error, then func()) {
	a.async(caption, work, then)
}

func newStatusApp(t *testing.T) *App {
	t.Helper()
	a := New("t", sims.New(&fakeProvider{platform: device.PlatformAndroid}))
	_, stop := runHeadless(t, a)
	t.Cleanup(stop)
	waitFor(t, a, 5*time.Second, func() bool { return len(a.spinJobs) == 0 })
	return a
}

func shortHolds(t *testing.T) {
	t.Helper()
	flash, err := flashHold, errorHold
	flashHold, errorHold = 300*time.Millisecond, 300*time.Millisecond
	t.Cleanup(func() { flashHold, errorHold = flash, err })
}

func statusText(a *App) string {
	var text string
	onUI(a, func() { text = a.status.GetText(true) })
	return text
}

func blocker(t *testing.T) (func() error, func()) {
	t.Helper()
	release := make(chan struct{})
	var once bool
	done := func() {
		if !once {
			once = true
			close(release)
		}
	}
	t.Cleanup(done)
	return func() error { <-release; return nil }, done
}

func TestStatus_CaptionLeavesWithItsJob(t *testing.T) {
	a := newStatusApp(t)
	install, finishInstall := blocker(t)
	refresh, _ := blocker(t)
	onUI(a, func() {
		job(a, " installing app.apk...", install, nil)
		job(a, " loading apps...", refresh, nil)
	})
	waitFor(t, a, 3*time.Second, func() bool { return strings.Contains(a.status.GetText(true), "loading apps") })
	finishInstall()
	waitFor(t, a, 3*time.Second, func() bool { return len(a.spinJobs) == 1 })
	time.Sleep(2 * spinnerInterval)
	text := statusText(a)
	if strings.Contains(text, "installing") {
		t.Errorf("the finished install still captions the runner: %q", text)
	}
	if !strings.Contains(text, "loading apps") {
		t.Errorf("the job still running lost its caption: %q", text)
	}
}

func TestStatus_OlderCaptionStaysWhileANewerUncaptionedJobRuns(t *testing.T) {
	a := newStatusApp(t)
	install, _ := blocker(t)
	launch, _ := blocker(t)
	onUI(a, func() {
		job(a, " installing app.apk...", install, nil)
		job(a, "", launch, nil)
	})
	time.Sleep(2 * spinnerInterval)
	if text := statusText(a); !strings.Contains(text, "installing app.apk") {
		t.Errorf("the runner lost the caption of the job still running: %q", text)
	}
}

func TestStatus_JobWithoutCaptionDoesNotBorrowTheLastMessage(t *testing.T) {
	a := newStatusApp(t)
	launch, _ := blocker(t)
	onUI(a, func() {
		a.flash("installed app.apk")
		job(a, "", launch, nil)
	})
	time.Sleep(2 * spinnerInterval)
	text := statusText(a)
	if !strings.Contains(text, ">_ >_") {
		t.Errorf("the runner should show for the new job: %q", text)
	}
	if strings.Contains(text, "installed app.apk") {
		t.Errorf("the new job took the last flash as its caption: %q", text)
	}
}

func TestStatus_FlashOutlivesTheNextFrame(t *testing.T) {
	a := newStatusApp(t)
	refresh, _ := blocker(t)
	onUI(a, func() {
		job(a, " loading apps...", refresh, nil)
		a.flash("installed app.apk")
	})
	time.Sleep(3 * spinnerInterval)
	if text := statusText(a); !strings.Contains(text, "installed app.apk") {
		t.Errorf("the runner drew over the flash: %q", text)
	}
}

func TestStatus_FlashReturnsWhenTheLastJobEnds(t *testing.T) {
	a := newStatusApp(t)
	refresh, finish := blocker(t)
	onUI(a, func() {
		a.flash("installed app.apk")
		job(a, " loading apps...", refresh, nil)
	})
	waitFor(t, a, 3*time.Second, func() bool { return strings.Contains(a.status.GetText(true), "loading apps") })
	finish()
	waitFor(t, a, 3*time.Second, func() bool { return len(a.spinJobs) == 0 })
	if text := statusText(a); !strings.Contains(text, "installed app.apk") {
		t.Errorf("the flash still held should come back once the runner goes: %q", text)
	}
}

func TestStatus_FlashClearsWhenItExpires(t *testing.T) {
	shortHolds(t)
	a := newStatusApp(t)
	onUI(a, func() { a.flash("installed app.apk") })
	if text := statusText(a); !strings.Contains(text, "installed app.apk") {
		t.Fatalf("the flash did not show: %q", text)
	}
	waitFor(t, a, 3*time.Second, func() bool { return strings.TrimSpace(a.status.GetText(true)) == "" })
}

func TestStatus_ErrorStaysAfterItsHold(t *testing.T) {
	shortHolds(t)
	a := newStatusApp(t)
	onUI(a, func() { a.flashErr(errors.New("adb: device offline")) })
	time.Sleep(2 * errorHold)
	if text := statusText(a); !strings.Contains(text, "device offline") {
		t.Errorf("an error nobody replaced should stay: %q", text)
	}
}

func TestStatus_ErrorGivesWayToTheNextJob(t *testing.T) {
	shortHolds(t)
	a := newStatusApp(t)
	onUI(a, func() { a.flashErr(errors.New("adb: device offline")) })
	time.Sleep(2 * errorHold)
	boot, finish := blocker(t)
	onUI(a, func() { job(a, " booting Pixel_7...", boot, nil) })
	if text := statusText(a); strings.Contains(text, "device offline") || !strings.Contains(text, "booting Pixel_7") {
		t.Errorf("the next job should replace an error past its hold: %q", text)
	}
	finish()
	waitFor(t, a, 3*time.Second, func() bool { return len(a.spinJobs) == 0 })
	if text := statusText(a); strings.TrimSpace(text) != "" {
		t.Errorf("nothing should be left once the job ends: %q", text)
	}
}

func TestStatus_MessageShowsUntilCleared(t *testing.T) {
	a := newStatusApp(t)
	onUI(a, func() { a.setStatus(" waiting for the file dialog...") })
	if text := statusText(a); !strings.Contains(text, "waiting for the file dialog") {
		t.Fatalf("the message did not show: %q", text)
	}
	onUI(a, func() { a.setStatus("") })
	if text := statusText(a); strings.TrimSpace(text) != "" {
		t.Errorf("clearing left %q", text)
	}
}

func TestStatus_MessageWrittenDuringAJobIsNotDrawnOver(t *testing.T) {
	a := newStatusApp(t)
	refresh, _ := blocker(t)
	onUI(a, func() {
		job(a, " loading apps...", refresh, nil)
		a.setStatus(" waiting for the file dialog...")
	})
	time.Sleep(3 * spinnerInterval)
	if text := statusText(a); !strings.Contains(text, "waiting for the file dialog") {
		t.Errorf("the runner drew over a message written after it started: %q", text)
	}
}

func TestStatus_ErrorKeepsItsColourWhenItReturns(t *testing.T) {
	a := newStatusApp(t)
	work, finish := blocker(t)
	onUI(a, func() {
		a.flashErr(errors.New("adb: device offline"))
		job(a, " loading apps...", work, nil)
	})
	finish()
	waitFor(t, a, 3*time.Second, func() bool { return len(a.spinJobs) == 0 })
	var markup string
	onUI(a, func() { markup = a.status.GetText(false) })
	if !strings.Contains(markup, "[red]") || !strings.Contains(markup, "device offline") {
		t.Errorf("the error came back without its colour: %q", markup)
	}
}
