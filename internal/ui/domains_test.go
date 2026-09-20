package ui

import (
	"github.com/gdamore/tcell/v2"
	"strings"
	"testing"
	"time"

	"github.com/siner308/sims/internal/proxy"
)

func flowAt(host, path string, status int, at time.Time) proxy.Flow {
	return proxy.Flow{
		Method: "GET", Host: host, Path: path, URL: "https://" + host + path,
		Status: status, Kind: proxy.KindHTTP, Done: true,
		Start: at, Duration: 20 * time.Millisecond, RespSize: 100,
	}
}

// A phone's connections do not name the app, so the domain is what a reader groups by. The most
// recently active domain comes first, because that is the one they are watching.
func TestGroupsByDomainNewestFirst(t *testing.T) {
	base := time.Now()
	flows := []proxy.Flow{
		flowAt("api.example.com", "/v1/me", 200, base),
		flowAt("ads.example.net", "/beacon", 200, base.Add(time.Second)),
		flowAt("api.example.com", "/v1/feed", 200, base.Add(2*time.Second)),
	}
	groups := groupByDomain(flows)
	if len(groups) != 2 {
		t.Fatalf("groups = %d, want 2", len(groups))
	}
	if groups[0].Host != "api.example.com" {
		t.Errorf("first group = %q, want the most recently active", groups[0].Host)
	}
	if len(groups[0].Flows) != 2 {
		t.Errorf("api.example.com holds %d flows", len(groups[0].Flows))
	}
	// inside a domain the order is the order they happened
	if groups[0].Flows[0].Path != "/v1/me" {
		t.Errorf("first path in the group = %q", groups[0].Flows[0].Path)
	}
}

// A domain in trouble has to show it without being opened, or grouping hides what a flat list showed.
func TestGroupSummaryCountsFailuresAndTunnels(t *testing.T) {
	base := time.Now()
	flows := []proxy.Flow{
		flowAt("api.example.com", "/ok", 200, base),
		flowAt("api.example.com", "/gone", 404, base.Add(time.Second)),
		{Host: "api.example.com", Kind: proxy.KindTunnel, Done: true, Start: base.Add(2 * time.Second),
			Error: "TLS handshake failed"},
	}
	g := groupByDomain(flows)[0]
	// a 404 is the server answering, not a failure to reach it; the two are counted apart
	if g.Bad != 1 {
		t.Errorf("4xx/5xx = %d, want the 404", g.Bad)
	}
	if g.Errors != 1 {
		t.Errorf("errored = %d, want the failed tunnel", g.Errors)
	}
	if g.Unopened != 1 {
		t.Errorf("unopened = %d, want the tunnel", g.Unopened)
	}
	sum := g.summary()
	for _, want := range []string{"3", "4xx/5xx", "errored", "unopened"} {
		if !strings.Contains(sum, want) {
			t.Errorf("summary %q is missing %q", sum, want)
		}
	}
}

// Where sims can see senders the group says so; on a phone it stays empty rather than guessing.
func TestGroupSendersOnlyWhenKnown(t *testing.T) {
	base := time.Now()
	known := flowAt("api.example.com", "/a", 200, base)
	known.Process = "MobileSafari"
	other := flowAt("api.example.com", "/b", 200, base)
	other.Process = "parsecd"
	if got := groupSenders(domainGroup{Flows: []proxy.Flow{known, other}}); got != "MobileSafari +1" {
		t.Errorf("senders = %q", got)
	}
	anon := flowAt("api.example.com", "/c", 200, base)
	if got := groupSenders(domainGroup{Flows: []proxy.Flow{anon}}); got != "" {
		t.Errorf("an unattributed domain claimed a sender: %q", got)
	}
}

// Ports that are the scheme's default are noise in a domain heading.
func TestHostOfDropsDefaultPorts(t *testing.T) {
	cases := map[string]string{
		"api.example.com:443":  "api.example.com",
		"api.example.com:80":   "api.example.com",
		"api.example.com:8443": "api.example.com:8443",
	}
	for in, want := range cases {
		if got := hostOf(proxy.Flow{Host: in}); got != want {
			t.Errorf("hostOf(%q) = %q, want %q", in, got, want)
		}
	}
}

// g switches the table to the grouped shape and back, and folding a domain hides its exchanges
// without losing them.
func TestGroupingFoldsAndUnfolds(t *testing.T) {
	a, v, s, stop := startFlowsView(t)
	defer stop()

	send(s, "GET", "https://api.example.com/v1/me", 200, proxy.OriginDevice, "")
	send(s, "GET", "https://api.example.com/v1/feed", 200, proxy.OriginDevice, "")
	send(s, "GET", "https://ads.example.net/beacon", 200, proxy.OriginDevice, "")
	waitFor(t, a, 5*time.Second, func() bool { return len(v.visible()) == 3 })

	// flat: a header plus three exchanges
	var flat int
	a.tv.QueueUpdate(func() { flat = v.table.GetRowCount() })
	if flat != 4 {
		t.Fatalf("flat table rows = %d, want 4", flat)
	}

	// grouped: two domain rows plus their three exchanges
	a.tv.QueueUpdate(func() { v.byDomain = true; v.render() })
	var grouped int
	waitFor(t, a, 5*time.Second, func() bool {
		grouped = v.table.GetRowCount()
		return grouped == 6
	})
	if grouped != 6 {
		t.Errorf("grouped rows = %d, want 2 domains and 3 exchanges under a header", grouped)
	}

	// folding everything leaves only the domains
	a.tv.QueueUpdate(func() { v.toggleAllGroups() })
	waitFor(t, a, 5*time.Second, func() bool { return v.table.GetRowCount() == 3 })

	// and unfolding brings the exchanges back
	a.tv.QueueUpdate(func() { v.toggleAllGroups() })
	waitFor(t, a, 5*time.Second, func() bool { return v.table.GetRowCount() == 6 })
}

// The cursor on a domain heading is not an exchange; enter must fold it rather than open a flow.
func TestEnterOnADomainFoldsIt(t *testing.T) {
	a, v, s, stop := startFlowsView(t)
	defer stop()

	send(s, "GET", "https://api.example.com/v1/me", 200, proxy.OriginDevice, "")
	waitFor(t, a, 5*time.Second, func() bool { return len(v.visible()) == 1 })
	a.tv.QueueUpdate(func() { v.byDomain = true; v.render(); v.table.Select(1, 0) })

	var isGroup bool
	waitFor(t, a, 5*time.Second, func() bool {
		_, isGroup = v.currentRow().(domainGroup)
		return isGroup
	})

	a.tv.QueueUpdate(func() { v.onKey(tcell.NewEventKey(tcell.KeyEnter, 0, tcell.ModNone)) })
	waitFor(t, a, 5*time.Second, func() bool { return v.collapsed["api.example.com"] })
	// the flow view must not have opened
	if _, opened := a.top().(*flowView); opened {
		t.Error("enter on a domain heading opened a flow")
	}
}

// An IPv6 address is full of colons, so trimming the port by scanning for the last one turns
// "fe80::443" into "fe80:". That value is the domain grouping key and the fold-state key, so
// distinct hosts collapse into one bucket.
func TestHostOfHandlesIPv6(t *testing.T) {
	cases := map[string]string{
		"[fe80::1]:443":       "fe80::1",
		"[2001:db8::80]:443":  "2001:db8::80",
		"[fe80::1]:8443":      "[fe80::1]:8443",
		"fe80::443":           "fe80::443",
		"2001:db8::80":        "2001:db8::80",
		"api.example.com:443": "api.example.com",
		"api.example.com":     "api.example.com",
	}
	for in, want := range cases {
		if got := hostOf(proxy.Flow{Host: in}); got != want {
			t.Errorf("hostOf(%q) = %q, want %q", in, got, want)
		}
	}
}
