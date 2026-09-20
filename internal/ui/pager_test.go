package ui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/siner308/sims/internal/proxy"
)

// Reading an exchange has to actually run the tool and give the TUI back afterwards.
func TestPagerRunsAndTheTUIComesBack(t *testing.T) {
	a, v, s, stop := streamFor(t, true)
	defer stop()

	// a "pager" that records the file it was handed
	marker := filepath.Join(t.TempDir(), "opened")
	script := filepath.Join(t.TempDir(), "pager")
	body := "#!/bin/sh\ncp \"$1\" " + marker + "\n"
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PAGER", script)

	send(s, "POST", "https://api.example.com/v1/login", 200, proxy.OriginDevice, "")
	waitFor(t, a, 5*time.Second, func() bool {
		v.timeline.setFlows(s.Flows())
		return len(v.exchanges()) > 0
	})

	a.tv.QueueUpdate(func() { v.readSelected(false) })

	deadline := time.Now().Add(10 * time.Second)
	var opened []byte
	for time.Now().Before(deadline) {
		if b, err := os.ReadFile(marker); err == nil {
			opened = b
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if opened == nil {
		t.Fatal("the pager never ran")
	}
	if !strings.Contains(string(opened), "api.example.com") {
		t.Errorf("the pager was handed the wrong text:\n%s", opened)
	}

	// and the TUI is responsive afterwards
	responded := make(chan struct{})
	a.tv.QueueUpdate(func() { close(responded) })
	select {
	case <-responded:
	case <-time.After(5 * time.Second):
		t.Fatal("the TUI did not come back after the pager exited")
	}
}
