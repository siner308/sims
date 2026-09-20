package ui

import (
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/siner308/sims/internal/proxy"
)

func sampleFlow() proxy.Flow {
	return proxy.Flow{
		ID: 1, Method: "POST", URL: "https://api.example.com/v1/login",
		Host: "api.example.com", Path: "/v1/login", Status: 200, Kind: proxy.KindHTTP, Done: true,
		Start: time.Date(2026, 9, 20, 12, 4, 1, 244_000_000, time.Local), Duration: 146 * time.Millisecond,
		ReqHeader:  http.Header{"Content-Type": []string{"application/json"}, "Authorization": []string{"Bearer abc"}},
		RespHeader: http.Header{"Content-Type": []string{"application/json"}},
		ReqBody:    []byte(`{"email":"kim@example.com"}`),
		RespBody:   []byte(`{"token":"eyJ0eXAi","expiresIn":3600}`),
		ReqSize:    27, RespSize: 37,
	}
}

// What goes to the pager has to be the whole exchange as plain text: a tool that searches and folds
// is the point, and markup or a truncated body would defeat it.
func TestExchangeTextCarriesEverything(t *testing.T) {
	got := exchangeText(sampleFlow())
	for _, want := range []string{
		"POST https://api.example.com/v1/login",
		"200 OK",
		"2026-09-20 12:04:01.244",
		"146ms",
		"Authorization: Bearer abc",
		"--- REQUEST ---",
		"--- RESPONSE ---",
		`"email": "kim@example.com"`,
		`"token": "eyJ0eXAi"`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("the text is missing %q:\n%s", want, got)
		}
	}
	// tview markup would show up literally in a pager
	if strings.Contains(got, "[aqua]") || strings.Contains(got, "[-]") {
		t.Errorf("terminal markup leaked into the text:\n%s", got)
	}
}

// An exchange sims could not read says so rather than showing an empty response.
func TestExchangeTextExplainsATunnel(t *testing.T) {
	got := exchangeText(proxy.Flow{
		Method: "CONNECT", URL: "https://pinned.example.com:443", Host: "pinned.example.com:443",
		Kind: proxy.KindTunnel, Done: true, Start: time.Now(),
		Error: "TLS handshake failed: unknown certificate",
	})
	if !strings.Contains(got, "pins its certificate") {
		t.Errorf("a tunnel does not explain itself:\n%s", got)
	}
}

// The viewer is whatever the user already configured.
func TestViewerFollowsTheEnvironment(t *testing.T) {
	t.Setenv("PAGER", "bat --style=plain")
	name, args := viewerFor(false)
	if name != "bat" || len(args) != 1 || args[0] != "--style=plain" {
		t.Errorf("pager = %q %v", name, args)
	}

	t.Setenv("VISUAL", "")
	t.Setenv("EDITOR", "vim")
	if name, _ := viewerFor(true); name != "vim" {
		t.Errorf("editor = %q", name)
	}

	// VISUAL wins over EDITOR, which is the convention
	t.Setenv("VISUAL", "code -w")
	if name, _ := viewerFor(true); name != "code" {
		t.Errorf("editor = %q, want VISUAL to win", name)
	}
}

// The file handed to the tool has to exist and hold the text.
func TestScratchFileHoldsTheExchange(t *testing.T) {
	path, err := writeScratch("POST-api.example.com", exchangeText(sampleFlow()))
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(path)

	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "api.example.com") {
		t.Errorf("the file does not hold the exchange:\n%s", body)
	}
	if !strings.HasSuffix(path, ".txt") {
		t.Errorf("the file is not named for a text tool: %s", path)
	}
}
