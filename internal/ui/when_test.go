package ui

import (
	"strings"
	"testing"
	"time"

	"github.com/siner308/sims/internal/proxy"
)

// How long a request took says nothing about what it happened next to. The table now carries when
// it went out as well.
func TestTableShowsWhenARequestHappened(t *testing.T) {
	at := time.Date(2026, 9, 20, 12, 4, 1, 244_000_000, time.Local)
	f := proxy.Flow{Method: "GET", Start: at, Duration: 146 * time.Millisecond, Done: true, Status: 200}

	if got := clockCell(f); got != "12:04:01.244" {
		t.Errorf("when = %q", got)
	}
	if got := timeCell(f); got != "146ms" {
		t.Errorf("took = %q", got)
	}
	// a flow that has not started shows nothing rather than the zero time
	if got := clockCell(proxy.Flow{}); got != "-" {
		t.Errorf("an unstarted flow shows %q", got)
	}
}

// The detail says when the exchange began and when it came back, so a reader can line it up with a
// log or another capture without doing the arithmetic.
func TestFlowDetailShowsBothEnds(t *testing.T) {
	at := time.Date(2026, 9, 20, 12, 4, 1, 244_000_000, time.Local)
	f := proxy.Flow{
		Method: "GET", URL: "https://api.example.com/v1/me", Status: 200, Kind: proxy.KindHTTP,
		Done: true, Start: at, Duration: 146 * time.Millisecond,
	}
	text := plainRow(exchangeText(f))
	for _, want := range []string{"2026-09-20 12:04:01.244", "2026-09-20 12:04:01.390", "146ms"} {
		if !strings.Contains(text, want) {
			t.Errorf("the detail is missing %q:\n%s", want, text)
		}
	}
	if !strings.Contains(text, "started") || !strings.Contains(text, "finished") {
		t.Errorf("the detail does not label both ends:\n%s", text)
	}
}
