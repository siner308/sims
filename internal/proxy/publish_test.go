package proxy_test

import (
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/siner308/sims/internal/proxy"
)

// A direct request is normally refused with "this is a proxy"; a published path is the one exception, because a phone fetches its profile before any proxy is set on it.
func TestAPublishedFileAnswersADirectRequest(t *testing.T) {
	var srv *proxy.Server
	var fetched <-chan struct{}
	srv, _ = start(t, func(s *proxy.Server) {
		srv = s
		fetched = s.Publish("/sims-proxy.mobileconfig", "application/x-apple-aspen-config", []byte("<plist/>"))
	})
	base := fmt.Sprintf("http://%s", srv.Addr())

	direct := &http.Client{Timeout: 5 * time.Second}
	res, err := direct.Get(base + "/sims-proxy.mobileconfig")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != http.StatusOK || string(body) != "<plist/>" {
		t.Fatalf("published file came back as %d %q", res.StatusCode, body)
	}
	if ct := res.Header.Get("Content-Type"); ct != "application/x-apple-aspen-config" {
		t.Errorf("Content-Type = %q; Safari only offers to install a profile under the aspen type", ct)
	}
	select {
	case <-fetched:
	case <-time.After(time.Second):
		t.Error("the fetch was not reported, so a command waiting for the phone to take the profile would hang")
	}

	res, err = direct.Get(base + "/somewhere-else")
	if err != nil {
		t.Fatal(err)
	}
	body, _ = io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != http.StatusBadRequest || !strings.Contains(string(body), "proxy") {
		t.Errorf("an unpublished path came back as %d %q; it should still be refused", res.StatusCode, body)
	}
}
