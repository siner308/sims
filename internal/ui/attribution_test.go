package ui

import (
	"strings"
	"testing"
	"time"

	"github.com/siner308/sims/internal/capture"
	"github.com/siner308/sims/internal/device"
	"github.com/siner308/sims/internal/proxy"
)

// An app's log beside device-wide traffic must not read as if the app sent all of it. A device's
// connections do not say which app opened them, so the screen has to stop short of claiming it.
func TestAppScopedTitleDoesNotClaimTheTraffic(t *testing.T) {
	phone := device.Device{ID: "R3C", Name: "SM S928N", Platform: device.PlatformAndroid, Kind: device.KindPhysical}
	app := device.App{BundleID: "com.example.app", Name: "Example"}

	v := &logsView{dev: phone, only: &app, session: fakeSession(9090)}
	title := v.title()
	if !strings.Contains(title, "Example") {
		t.Errorf("the title lost the app whose log this is: %q", title)
	}
	if !strings.Contains(title, phone.Name) {
		t.Errorf("the title does not say whose traffic it is: %q", title)
	}
	// the old shape put the app last, which read as "this app's traffic"
	if strings.HasSuffix(strings.TrimSpace(title), "Example") {
		t.Errorf("the title still reads as the app's own traffic: %q", title)
	}
}

// A row whose sender sims does know says so, and one it does not stays silent rather than guessing.
func TestRequestRowNamesTheSenderOnlyWhenKnown(t *testing.T) {
	known := entry{kind: entryExchange, at: time.Now(), flow: proxy.Flow{
		Method: "GET", URL: "https://api.example.com/v1/me", Process: "MobileSafari",
	}}
	if got := plainRow(known.render("", detailLine)); !strings.Contains(got, "MobileSafari") {
		t.Errorf("a known sender was not shown: %q", got)
	}

	unknown := entry{kind: entryExchange, at: time.Now(), flow: proxy.Flow{
		Method: "GET", URL: "https://ads.example.net/beacon",
	}}
	got := plainRow(unknown.render("", detailLine))
	if strings.Contains(got, "Example") {
		t.Errorf("an unattributed request was labelled with an app: %q", got)
	}
	if !strings.Contains(got, "ads.example.net") {
		t.Errorf("row = %q", got)
	}
}

// fakeSession is enough of a session for the title, which only reads the port.
func fakeSession(port int) *capture.Session { return &capture.Session{Port: port} }

func plainRow(s string) string {
	var b strings.Builder
	depth := 0
	for _, r := range s {
		switch {
		case r == '[':
			depth++
		case r == ']' && depth > 0:
			depth--
		case depth == 0:
			b.WriteRune(r)
		}
	}
	return b.String()
}
