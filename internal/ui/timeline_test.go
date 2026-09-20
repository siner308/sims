package ui

import (
	"net/http"
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
	// the exchange sits at the time its request went out, between the two log lines
	want := []string{"12:04:01.220", "12:04:01.244", "12:04:01.402"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("order = %v, want %v", got, want)
	}
	all := tl.all()
	if all[0].kind != entryLog || all[1].kind != entryExchange || all[2].kind != entryLog {
		t.Errorf("the exchange did not land between the log lines: %v %v %v", all[0].kind, all[1].kind, all[2].kind)
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
	if len(all) != 1 {
		t.Fatalf("a finished flow produced %d entries, want the one line it started as", len(all))
	}
	// finishing fills the line in where it already was rather than adding a second one
	if all[0].kind != entryExchange || !all[0].flow.Done || all[0].flow.Status != 200 {
		t.Errorf("the line did not gain its response: kind=%v done=%v status=%d",
			all[0].kind, all[0].flow.Done, all[0].flow.Status)
	}
	if !all[0].at.Equal(running.Start) {
		t.Errorf("finishing moved the line off its request time: %v", all[0].at)
	}

	tl.setFlows([]proxy.Flow{done}) // polled again after finishing
	if n := len(tl.all()); n != 1 {
		t.Errorf("a finished flow was added again: %d entries", n)
	}
}

// A filter has to match an exchange on either half: the url is in the request, the status in the
// response, and a reader typing either means the same line.
func TestFilterMatchesAnExchangeOnEitherHalf(t *testing.T) {
	f := proxy.Flow{Method: "POST", URL: "https://api.example.com/v1/login", Host: "api.example.com", Status: 200, Done: true}
	ex := entry{kind: entryExchange, flow: f}
	if !ex.matches("login") {
		t.Error("a filter matching the url dropped the exchange")
	}
	if ex.matches("nothing-here") {
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

// Android's log lines carry no year, so it comes from the stream's clock. A line from the end of
// December read in January belongs to the year before, not a year in the future.
func TestAndroidLineKeepsItsYearAcrossNewYear(t *testing.T) {
	ref := time.Date(2027, 1, 2, 10, 0, 0, 0, time.Local)
	at, ok := parseLogTime("12-31 23:59:01.000 I/MyApp  ( 1234): last of the year", ref)
	if !ok {
		t.Fatal("timestamp not recognised")
	}
	if at.Year() != 2027-1 {
		t.Errorf("year = %d, want %d", at.Year(), 2027-1)
	}
	if at.After(ref) {
		t.Errorf("a line already logged was placed in the future: %v is after %v", at, ref)
	}
}

// Opening an exchange has to show the headers in place, then the body, then fold back. Reading a
// request without its headers is the thing this feature exists to fix.
func TestOpeningAnExchangeShowsHeadersThenBody(t *testing.T) {
	f := proxy.Flow{
		ID: 1, Method: "POST", URL: "https://api.example.com/v1/login",
		Host: "api.example.com", Status: 200, Kind: proxy.KindHTTP, Done: true,
		Start: time.Now(), Duration: 20 * time.Millisecond,
		ReqHeader:  http.Header{"Content-Type": []string{"application/json"}, "Authorization": []string{"Bearer abc123"}},
		RespHeader: http.Header{"Content-Type": []string{"application/json"}},
		ReqBody:    []byte(`{"user":"kim"}`),
		RespBody:   []byte(`{"token":"xyz"}`),
		ReqSize:    14,
		RespSize:   15,
	}
	ex := entry{kind: entryExchange, flow: f, at: f.Start}

	line := ex.render("", detailLine)
	if strings.Contains(line, "Authorization") {
		t.Error("the one-line form is showing headers")
	}
	// the summary carries both halves: the call and what came back
	for _, want := range []string{"POST", "api.example.com", "200", "20ms"} {
		if !plainContains(line, want) {
			t.Errorf("the one-line summary is missing %q: %q", want, plainRow(line))
		}
	}

	headers := ex.render("", detailHeaders)
	for _, want := range []string{"REQUEST", "RESPONSE", "Authorization", "Bearer abc123", "Content-Type"} {
		if !strings.Contains(headers, want) {
			t.Errorf("opened headers are missing %q:\n%s", want, headers)
		}
	}
	if strings.Contains(headers, `"user"`) {
		t.Error("the headers level is already showing the body")
	}

	body := ex.render("", detailBody)
	// one line opens to both bodies, so the reader sees what was sent and what came back together
	if !strings.Contains(body, `"user": "kim"`) {
		t.Errorf("the request body is missing or not pretty-printed:\n%s", body)
	}
	if !strings.Contains(body, `"token": "xyz"`) {
		t.Errorf("the response body is missing:\n%s", body)
	}
}

func plainContains(rendered, want string) bool {
	return strings.Contains(plainRow(rendered), want)
}

// detail cycles and comes back round, so one key can both open and close.
func TestDetailCycles(t *testing.T) {
	got := []detail{detailLine}
	for range 3 {
		got = append(got, got[len(got)-1].next())
	}
	want := []detail{detailLine, detailHeaders, detailBody, detailLine}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("cycle = %v, want %v", got, want)
		}
	}
}

// A tunnel has no headers or body to show; opening it must not print an empty block as if it did.
func TestTunnelHasNothingToOpen(t *testing.T) {
	f := proxy.Flow{Kind: proxy.KindTunnel, Method: "CONNECT", Host: "pinned.example.com:443", Done: true, Error: "pinned"}
	ex := entry{kind: entryExchange, flow: f, at: time.Now()}
	if strings.Contains(ex.render("", detailBody), "RESPONSE") {
		t.Error("a tunnel printed a response block it never read")
	}
}

// A url can be longer than the terminal is wide, and whatever follows it wraps out of sight with
// it. The size and the timing go in front of the url for that reason, and the columns before it are
// held to a fixed width so every url starts in the same place.
func TestSizeAndTimingComeBeforeTheURL(t *testing.T) {
	long := "https://cdn.example.com/a/very/long/path/that/keeps/going/and/going/asset-9f2a1c.png"
	f := proxy.Flow{
		Method: "GET", URL: long, Host: "cdn.example.com", Kind: proxy.KindHTTP,
		Done: true, Status: 200, RespSize: 284912, Duration: 1204 * time.Millisecond,
	}
	row := plainRow(entry{kind: entryExchange, flow: f, at: time.Now()}.render("", detailLine))

	urlAt := strings.Index(row, long)
	if urlAt < 0 {
		t.Fatalf("the url is missing from the row: %q", row)
	}
	for _, before := range []string{"278.2 kB", "1204ms", "200"} {
		at := strings.Index(row, before)
		if at < 0 {
			t.Errorf("the row does not show %q: %q", before, row)
			continue
		}
		if at > urlAt {
			t.Errorf("%q sits after the url, where a wrap hides it: %q", before, row)
		}
	}

	// every url starts in the same column, whatever the size, timing and method around it
	short := proxy.Flow{
		Method: "CONNECT", URL: "https://api.example.com/v1/me", Host: "api.example.com",
		Kind: proxy.KindHTTP, Done: true, Status: 404, RespSize: 42, Duration: 8 * time.Millisecond,
	}
	other := plainRow(entry{kind: entryExchange, flow: short, at: time.Now()}.render("", detailLine))
	if a, b := strings.Index(row, "https://"), strings.Index(other, "https://"); a != b {
		t.Errorf("the urls start in different columns, %d and %d:\n%s\n%s", a, b, row, other)
	}
}

// A call still out, and one that failed, have no size or timing. The column still has to hold its
// place or the urls below it step out of line.
func TestTheSizeColumnHoldsItsPlaceWithNothingToShow(t *testing.T) {
	done := proxy.Flow{Method: "GET", URL: "https://api.example.com/a", Host: "api.example.com",
		Kind: proxy.KindHTTP, Done: true, Status: 200, RespSize: 10, Duration: time.Millisecond}
	running := proxy.Flow{Method: "GET", URL: "https://api.example.com/b", Host: "api.example.com",
		Kind: proxy.KindHTTP}
	failed := proxy.Flow{Method: "GET", URL: "https://api.example.com/c", Host: "api.example.com",
		Kind: proxy.KindHTTP, Done: true, Error: "connection refused"}

	var at []int
	for _, f := range []proxy.Flow{done, running, failed} {
		row := plainRow(entry{kind: entryExchange, flow: f, at: time.Now()}.render("", detailLine))
		i := strings.Index(row, "https://")
		if i < 0 {
			t.Fatalf("no url in %q", row)
		}
		at = append(at, i)
	}
	if at[0] != at[1] || at[1] != at[2] {
		t.Errorf("the urls do not line up: %v", at)
	}
}
