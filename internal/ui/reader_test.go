package ui

import (
	"strings"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"
)

// enter opens the exchange inside sims, and esc goes back to the view it was read from. Handing it
// straight to a pager took the terminal away and came back to a screen nobody had redrawn.
func TestEnterReadsInsideSimsAndEscComesBack(t *testing.T) {
	a, v, s, stop := streamFor(t, true)
	defer stop()

	send(s, "POST", "https://api.example.com/v1/login", 200, "", "")
	waitFor(t, a, 5*time.Second, func() bool {
		v.timeline.setFlows(s.Flows())
		return len(v.exchanges()) > 0
	})

	a.tv.QueueUpdate(func() { v.readSelected(false) })

	var rv *readerView
	waitFor(t, a, 5*time.Second, func() bool {
		rv, _ = a.top().(*readerView)
		return rv != nil
	})
	var shown string
	onUIResult(a, func() bool { shown = rv.text.GetText(true); return true }, 5*time.Second)
	if !strings.Contains(shown, "api.example.com") {
		t.Errorf("the reader does not show the exchange:\n%s", shown)
	}

	a.tv.QueueUpdate(func() { a.onKey(tcell.NewEventKey(tcell.KeyEscape, 0, tcell.ModNone)) })
	waitFor(t, a, 5*time.Second, func() bool {
		_, back := a.top().(*logsView)
		return back
	})
}

// Finding text has to move the view to the match, which is the whole reason for reading it here.
func TestReaderFindsAndSteps(t *testing.T) {
	a, _, _, stop := streamFor(t, true)
	defer stop()

	body := "alpha\nbeta\ntarget one\ngamma\ntarget two\ndelta\n"
	var rv *readerView
	a.tv.QueueUpdate(func() {
		rv = newReaderView(a, "sample", body)
		a.push(rv)
	})
	waitFor(t, a, 5*time.Second, func() bool { return rv != nil })

	onUIResult(a, func() bool { rv.find("target"); return true }, 5*time.Second)
	if len(rv.hits) != 2 {
		t.Fatalf("hits = %d, want 2", len(rv.hits))
	}
	if rv.hit != 0 {
		t.Errorf("the first match is not selected: %d", rv.hit)
	}
	onUIResult(a, func() bool { rv.step(1); return true }, 5*time.Second)
	if rv.hit != 1 {
		t.Errorf("n did not step to the second match: %d", rv.hit)
	}
	// and it wraps rather than stopping at the end
	onUIResult(a, func() bool { rv.step(1); return true }, 5*time.Second)
	if rv.hit != 0 {
		t.Errorf("n did not wrap round: %d", rv.hit)
	}

	// a term that is not there says so and leaves the text unmarked
	onUIResult(a, func() bool { rv.find("nowhere"); return true }, 5*time.Second)
	if rv.found != "" {
		t.Errorf("a failed search was left active: %q", rv.found)
	}
}
