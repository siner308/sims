package ui

import (
	"strings"
	"testing"
	"time"

	"github.com/siner308/sims/internal/proxy"
)

// The whole point of merging is that a request lands between the log lines that surround it. If the
// order is wrong the view is worse than two separate panes.
func TestTimelineInterleavesByTime(t *testing.T) {
	base := time.Date(2026, 9, 19, 12, 4, 1, 0, time.Local)
	tl := newTimeline(0)

	tl.addLog("09-19 12:04:01.220 I/MyApp  ( 1234): tapped sign in", base)
	tl.setFlows([]proxy.Flow{{
		ID: 1, Method: "POST", URL: "https://api.example.com/v1/login", Host: "api.example.com",
		Status: 200, Kind: proxy.KindHTTP, Done: true,
		Start: base.Add(244 * time.Millisecond), Duration: 146 * time.Millisecond,
	}})
	tl.addLog("09-19 12:04:01.402 I/MyApp  ( 1234): token stored", base)

	var got []string
	for _, e := range tl.all() {
		got = append(got, e.at.Format("15:04:05.000"))
	}
	want := []string{"12:04:01.220", "12:04:01.244", "12:04:01.390", "12:04:01.402"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("order = %v, want %v", got, want)
	}
	// the log line that follows the response must come after it, not before
	all := tl.all()
	if all[2].kind != entryResponse || all[3].kind != entryLog {
		t.Errorf("the response and the log after it are out of order: %v %v", all[2].kind, all[3].kind)
	}
}

// Both platforms stamp their log lines differently; a timestamp sims cannot read would pin the line
// to the wrong moment and scramble the order.
func TestLogTimestampsOfBothPlatformsAreRead(t *testing.T) {
	ref := time.Date(2026, 9, 19, 12, 0, 0, 0, time.Local)
	cases := []struct {
		name, line, want string
	}{
		{"android logcat -v time", "09-19 12:04:01.220 I/MyApp  ( 1234): hello", "12:04:01.220"},
		{"ios log stream --style compact", "2026-09-19 12:04:02.795 Df trustd[963:948aa5] hello", "12:04:02.795"},
	}
	for _, c := range cases {
		at, ok := parseLogTime(c.line, ref)
		if !ok {
			t.Errorf("%s: timestamp not recognised in %q", c.name, c.line)
			continue
		}
		if got := at.Format("15:04:05.000"); got != c.want {
			t.Errorf("%s: parsed %s, want %s", c.name, got, c.want)
		}
	}
}

// A line with no timestamp still has to appear, in the place it arrived.
func TestUnstampedLineKeepsItsPlace(t *testing.T) {
	ref := time.Date(2026, 9, 19, 12, 4, 5, 0, time.Local)
	tl := newTimeline(0)
	tl.addLog("09-19 12:04:01.000 I/MyApp: first", ref)
	tl.addLog("a line with no timestamp at all", ref)

	all := tl.all()
	if len(all) != 2 {
		t.Fatalf("entries = %d", len(all))
	}
	if !all[1].at.Equal(ref) {
		t.Errorf("an unstamped line was placed at %v, not at its arrival time %v", all[1].at, ref)
	}
}

// The store reports a flow while it is running and again when it finishes. It must not be added
// twice, and the response must appear once the exchange completes.
func TestFlowBecomesRequestThenResponse(t *testing.T) {
	base := time.Now()
	tl := newTimeline(0)
	running := proxy.Flow{ID: 7, Method: "GET", URL: "https://x/y", Kind: proxy.KindHTTP, Start: base}

	tl.setFlows([]proxy.Flow{running})
	tl.setFlows([]proxy.Flow{running}) // polled again, still running
	if n := len(tl.all()); n != 1 {
		t.Fatalf("a running flow produced %d entries, want 1", n)
	}

	done := running
	done.Done, done.Status, done.Duration = true, 200, 50*time.Millisecond
	tl.setFlows([]proxy.Flow{done})
	all := tl.all()
	if len(all) != 2 {
		t.Fatalf("a finished flow produced %d entries, want 2", len(all))
	}
	if all[0].kind != entryRequest || all[1].kind != entryResponse {
		t.Errorf("kinds = %v %v", all[0].kind, all[1].kind)
	}

	tl.setFlows([]proxy.Flow{done}) // polled again after finishing
	if n := len(tl.all()); n != 2 {
		t.Errorf("a finished flow was added again: %d entries", n)
	}
}

// A filter that kept a request but dropped its response would leave the reader with half an exchange.
func TestFilterKeepsBothHalvesOfAnExchange(t *testing.T) {
	f := proxy.Flow{Method: "POST", URL: "https://api.example.com/v1/login", Host: "api.example.com", Status: 200, Done: true}
	req := entry{kind: entryRequest, flow: f}
	resp := entry{kind: entryResponse, flow: f}
	if !req.matches("login") || !resp.matches("login") {
		t.Error("a filter matching the url dropped one half of the exchange")
	}
	if req.matches("nothing-here") {
		t.Error("a filter that matches nothing kept the row")
	}
}

func TestTimelineDropsOldestWhenFull(t *testing.T) {
	base := time.Now()
	tl := newTimeline(3)
	for i := range 5 {
		tl.addLog("line", base.Add(time.Duration(i)*time.Second))
	}
	if n := len(tl.all()); n != 3 {
		t.Errorf("entries = %d, want the cap of 3", n)
	}
}
