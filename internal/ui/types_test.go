package ui

import (
	"net/http"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"

	"github.com/siner308/sims/internal/capture"
	"github.com/siner308/sims/internal/proxy"
)

func keyRune(r rune) *tcell.EventKey { return tcell.NewEventKey(tcell.KeyRune, r, tcell.ModNone) }

func sendTyped(s *capture.Session, url, contentType string) {
	srv := &proxy.Server{CA: s.CA, Store: s.Store}
	f := proxy.Flow{
		Method: "GET", URL: url, Host: hostFromURL(url), Path: pathFromURL(url),
		Status: 200, Kind: proxy.KindHTTP, Done: true, Origin: proxy.OriginDevice,
		Start: time.Now(), ReqHeader: http.Header{}, RespHeader: http.Header{},
	}
	f.RespHeader.Set("Content-Type", contentType)
	srv.RecordForTest(f)
}

func TestHidingATypeDropsThoseExchangesFromTheTable(t *testing.T) {
	a, v, s, stop := startFlowsView(t)
	defer stop()

	sendTyped(s, "https://api.example.com/v1/me", "application/json")
	sendTyped(s, "https://cdn.example.com/hero.png", "image/png")
	sendTyped(s, "https://cdn.example.com/app.js", "text/javascript")

	a.tv.QueueUpdate(func() { v.deviceOnly = false; v.reload() })
	waitFor(t, a, 5*time.Second, func() bool { return len(v.visible()) == 3 })

	a.tv.QueueUpdate(func() {
		a.hiddenTypes[proxy.ResourceImg] = true
		a.hiddenTypes[proxy.ResourceJS] = true
		v.render()
	})
	waitFor(t, a, 5*time.Second, func() bool { return len(v.visible()) == 1 })

	var kept string
	var note string
	a.tv.QueueUpdate(func() { kept = v.visible()[0].Path; note = a.typesNote() })
	if kept != "/v1/me" {
		t.Errorf("hiding img and js kept %q", kept)
	}
	if note == "" {
		t.Error("the title does not say that types are hidden")
	}

	a.tv.QueueUpdate(func() { a.hiddenTypes = map[proxy.Resource]bool{}; v.render() })
	waitFor(t, a, 5*time.Second, func() bool { return len(v.visible()) == 3 })
}

func TestTypesViewOnlyKeepsOneBucket(t *testing.T) {
	a, v, s, stop := startFlowsView(t)
	defer stop()
	sendTyped(s, "https://api.example.com/v1/me", "application/json")
	sendTyped(s, "https://cdn.example.com/hero.png", "image/png")

	types := newTypesView(a, func() []proxy.Flow { return s.Flows() })
	a.tv.QueueUpdate(func() {
		a.push(types)
		// row 5 is img in proxy.Resources order: xhr, doc, js, css, img
		types.table.Select(5, 0)
		types.onKey(keyRune('o'))
	})
	waitFor(t, a, 5*time.Second, func() bool {
		return a.hiddenTypes[proxy.ResourceXHR] && !a.hiddenTypes[proxy.ResourceImg]
	})
	a.tv.QueueUpdate(func() { v.deviceOnly = false; v.reload() })
	waitFor(t, a, 5*time.Second, func() bool { return len(v.visible()) == 1 && v.visible()[0].Path == "/hero.png" })

	a.tv.QueueUpdate(func() { types.onKey(keyRune('a')) })
	waitFor(t, a, 5*time.Second, func() bool { return len(a.hiddenTypes) == 0 })
}

// "[x]" is a colour tag to tview and vanished from the screen, which made a shown type look unmarked and a hidden one marked
func TestTypeMarksSurviveRendering(t *testing.T) {
	a, _, s, stop := startFlowsView(t)
	defer stop()
	types := newTypesView(a, func() []proxy.Flow { return s.Flows() })
	var shown, hidden string
	a.tv.QueueUpdate(func() {
		a.hiddenTypes[proxy.ResourceDoc] = true
		a.push(types)
		shown = types.table.GetCell(1, 0).Text
		hidden = types.table.GetCell(2, 0).Text
	})
	waitFor(t, a, 5*time.Second, func() bool { return shown != "" })
	if shown != tview.Escape("[x]") || hidden != tview.Escape("[ ]") {
		t.Errorf("marks render as %q (shown) and %q (hidden)", shown, hidden)
	}
}
