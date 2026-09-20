package ui

import (
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/siner308/sims/internal/proxy"
)

// A body is written under the extension of what it actually is, because that is what decides which
// application opens it: a png saved as .txt opens in a text editor and shows its bytes.
func TestABodyIsNamedForWhatItIs(t *testing.T) {
	for _, tc := range []struct {
		name, contentType, path, want string
	}{
		{"png from the content type", "image/png", "/a/b", ".png"},
		{"jpeg with parameters", "image/jpeg; charset=binary", "/a/b", ".jpg"},
		{"mp4", "video/mp4", "/clip", ".mp4"},
		{"json", "application/json", "/v1/me", ".json"},
		{"falls back to the request path", "", "/assets/logo.svg", ".svg"},
		{"nothing to go on", "", "/download", ".bin"},
		{"a path that is not an extension", "", "/a.very-long-suffix", ".bin"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := http.Header{}
			if tc.contentType != "" {
				h.Set("Content-Type", tc.contentType)
			}
			got := bodyExtension(proxy.Flow{RespHeader: h, Path: tc.path})
			if got != tc.want {
				t.Errorf("extension = %q, want %q", got, tc.want)
			}
		})
	}
}

// The file has to hold the bytes as they arrived, or the application opening it gets something that
// is not the response.
func TestTheBodyFileHoldsTheBytes(t *testing.T) {
	png := []byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a, 0x00, 0x01}
	f := proxy.Flow{
		Method: "GET", Host: "cdn.example.com", Path: "/logo.png",
		RespHeader: http.Header{"Content-Type": []string{"image/png"}},
	}
	path, err := writeBodyFile(f, png)
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(path)

	if !strings.HasSuffix(path, ".png") {
		t.Errorf("the file is not named as a png: %s", path)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(png) {
		t.Errorf("the bytes were changed on the way out: %v", got)
	}
}

// A body the store dropped to make room has to say so. Reporting it as having no body would have a
// reader looking for a bug in the capture instead of in their own memory ceiling.
func TestADroppedBodySaysWhyItIsGone(t *testing.T) {
	a, _, _, stop := streamFor(t, true)
	defer stop()

	dropped := proxy.Flow{
		Method: "GET", Host: "cdn.example.com", Path: "/big.mp4", Done: true, Status: 200,
		RespSize: 40 << 20, RespTruncated: true,
		RespHeader: http.Header{"Content-Type": []string{"video/mp4"}},
	}
	a.tv.QueueUpdate(func() { a.openBody(dropped) })
	var status string
	waitFor(t, a, 5*time.Second, func() bool {
		status = a.status.GetText(true)
		return strings.TrimSpace(status) != ""
	})
	if !strings.Contains(status, "dropped") {
		t.Errorf("status = %q, want it to say the body was dropped", status)
	}

	// and one that genuinely had no body says that instead
	a.tv.QueueUpdate(func() { a.setStatus(""); a.openBody(proxy.Flow{Method: "HEAD", Host: "x", Done: true}) })
	waitFor(t, a, 5*time.Second, func() bool {
		status = a.status.GetText(true)
		return strings.Contains(status, "no response body")
	})
}

// f holds the view still. Without it a reader trying to look at something has the stream scroll out
// from under them every time a request lands.
func TestFHoldsTheViewStill(t *testing.T) {
	a, v, _, stop := streamFor(t, true)
	defer stop()

	onUIResult(a, func() bool { return v.noFollow }, 5*time.Second)
	if v.noFollow {
		t.Error("the stream started held rather than following")
	}

	press := func(r rune) {
		a.tv.QueueUpdate(func() { v.onKey(tcell.NewEventKey(tcell.KeyRune, r, tcell.ModNone)) })
	}
	press('f')
	waitFor(t, a, 5*time.Second, func() bool { return v.noFollow })

	var title string
	onUIResult(a, func() bool { title = v.text.GetTitle(); return true }, 5*time.Second)
	if !strings.Contains(title, "held") {
		t.Errorf("the title does not say the view is held: %q", title)
	}

	press('f')
	waitFor(t, a, 5*time.Second, func() bool { return !v.noFollow })
}

// A client that closes a connection early got what it asked for, so the row keeps its status. It
// used to read as err next to a response the app had finished with.
func TestAnAbandonedResponseIsNotAnError(t *testing.T) {
	f := proxy.Flow{
		Method: "GET", URL: "https://cdn.example.com/clip.mp4", Host: "cdn.example.com",
		Kind: proxy.KindHTTP, Done: true, Status: 200, RespSize: 1 << 20,
		Duration: 400 * time.Millisecond, Abandoned: true,
	}
	row := plainRow(entry{kind: entryExchange, flow: f, at: time.Now()}.render("", detailLine))
	if strings.Contains(row, "err") {
		t.Errorf("an abandoned response is still shown as an error: %q", row)
	}
	if !strings.Contains(row, "200") {
		t.Errorf("the status the client did get is missing: %q", row)
	}
	if !strings.Contains(row, "client closed early") {
		t.Errorf("nothing says the body stops short: %q", row)
	}
	// the size carries a marker, because it is what arrived rather than the whole response
	if !strings.Contains(row, "+") {
		t.Errorf("the partial size is not marked as partial: %q", row)
	}
}

// An exchange arriving on an untouched stream is selected straight away, so o and enter act on
// something. Before this the cursor stayed empty until the reader pressed a key, and a first visit
// to a quiet capture had nothing to select at all.
func TestTheNewestExchangeIsSelectedWhileFollowing(t *testing.T) {
	a, v, s, stop := streamFor(t, true)
	defer stop()

	send(s, "GET", "https://a.example.com/first", 200, "", "")
	waitFor(t, a, 5*time.Second, func() bool {
		v.timeline.setFlows(s.Flows())
		v.redraw()
		return v.cursor != ""
	})
	if f, ok := v.selectedFlow(); !ok || !strings.Contains(f.URL, "first") {
		t.Errorf("the arriving exchange was not selected: %v %q", ok, f.URL)
	}

	// a later one takes the selection, the way a following view follows
	send(s, "GET", "https://b.example.com/second", 200, "", "")
	waitFor(t, a, 5*time.Second, func() bool {
		v.timeline.setFlows(s.Flows())
		v.redraw()
		f, ok := v.selectedFlow()
		return ok && strings.Contains(f.URL, "second")
	})

	// once the reader steps, the cursor is theirs and new arrivals leave it alone
	a.tv.QueueUpdate(func() { v.step(false) })
	waitFor(t, a, 5*time.Second, func() bool {
		f, ok := v.selectedFlow()
		return ok && strings.Contains(f.URL, "first")
	})
	send(s, "GET", "https://c.example.com/third", 200, "", "")
	waitFor(t, a, 5*time.Second, func() bool {
		v.timeline.setFlows(s.Flows())
		v.redraw()
		return len(v.exchanges()) == 3
	})
	if f, _ := v.selectedFlow(); !strings.Contains(f.URL, "first") {
		t.Errorf("a new arrival moved the cursor the reader had placed: %q", f.URL)
	}
}
