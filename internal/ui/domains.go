package ui

import (
	"cmp"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/siner308/sims/internal/proxy"
)

// domainGroup is one host's traffic in the grouped view. On a phone a connection does not say which
// app opened it, so the host it went to is the next best handle a reader has: a domain is usually
// one service, and one service is usually one part of the app.
type domainGroup struct {
	Host  string
	Flows []proxy.Flow
	// Last is the most recent exchange, which is what the list sorts on: the domain something just
	// talked to is the one a reader is looking for.
	Last time.Time
	// Errors counts exchanges that never completed: a refused connection, a handshake that failed,
	// a response cut short. A 4xx or 5xx is the server answering, which is a different thing and is
	// counted separately.
	Errors int
	// Bad counts answered exchanges the server refused: 4xx and 5xx.
	Bad int
	// Unopened counts the ones sims could not read, which is a property of the domain rather than
	// of any one request: an app that pins its certificate pins it for every call.
	Unopened int
}

// Bytes is what came back from this host.
func (g domainGroup) Bytes() int64 {
	var n int64
	for _, f := range g.Flows {
		n += f.RespSize
	}
	return n
}

// groupByDomain buckets flows by host, newest domain first. The order inside a bucket is the order
// they arrived, so a domain reads as a conversation from the top.
func groupByDomain(flows []proxy.Flow) []domainGroup {
	index := map[string]int{}
	var out []domainGroup
	for _, f := range flows {
		host := hostOf(f)
		i, seen := index[host]
		if !seen {
			index[host] = len(out)
			out = append(out, domainGroup{Host: host})
			i = len(out) - 1
		}
		g := &out[i]
		g.Flows = append(g.Flows, f)
		if at := flowEnd(f); at.After(g.Last) {
			g.Last = at
		}
		if f.Kind == proxy.KindTunnel {
			g.Unopened++
		}
		switch {
		case f.Error != "":
			g.Errors++
		case f.Status >= 400:
			g.Bad++
		}
	}
	slices.SortStableFunc(out, func(a, b domainGroup) int {
		// most recent first, then the busier domain, then by name so the order is stable
		return cmp.Or(b.Last.Compare(a.Last), len(b.Flows)-len(a.Flows), strings.Compare(a.Host, b.Host))
	})
	return out
}

func flowEnd(f proxy.Flow) time.Time {
	if f.Done {
		return f.Start.Add(f.Duration)
	}
	return f.Start
}

// summary is the one-line description of a domain, for the row that stands in for its exchanges.
func (g domainGroup) summary() string {
	parts := []string{fmt.Sprintf("%d", len(g.Flows))}
	if n := g.Bytes(); n > 0 {
		parts = append(parts, proxy.SizeString(n))
	}
	if g.Bad > 0 {
		parts = append(parts, fmt.Sprintf("[yellow]%d 4xx/5xx[-]", g.Bad))
	}
	if g.Errors > 0 {
		parts = append(parts, fmt.Sprintf("[red]%d errored[-]", g.Errors))
	}
	if g.Unopened > 0 {
		parts = append(parts, fmt.Sprintf("[gray]%d unopened[-]", g.Unopened))
	}
	return strings.Join(parts, "  ")
}
