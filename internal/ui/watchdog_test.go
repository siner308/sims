package ui

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/siner308/sims/internal/device"
	"github.com/siner308/sims/internal/sims"
)

// A UI goroutine that stops answering cannot report itself, so the watchdog writes every goroutine's stack to a file: once per stall, and never while the screen answers.
func TestStuckUIWritesItsStacksOnce(t *testing.T) {
	prov := &fakeProvider{platform: device.PlatformAndroid, devices: []device.Device{emulator()}}
	a := New("test", sims.New(prov))
	a.hangs.dir = t.TempDir()
	_, stop := runHeadless(t, a)
	defer stop()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go a.watchUI(ctx, 20*time.Millisecond, 150*time.Millisecond)

	time.Sleep(300 * time.Millisecond)
	if dumps := a.hangs.written(); len(dumps) != 0 {
		t.Fatalf("an answering UI was reported stuck: %v", dumps)
	}

	release := make(chan struct{})
	onUI(a, func() { go func() { a.tv.QueueUpdate(func() { <-release }) }() })
	var dumps []string
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline) && len(dumps) == 0; time.Sleep(20 * time.Millisecond) {
		dumps = a.hangs.written()
	}
	if len(dumps) != 1 {
		t.Fatalf("stuck UI produced %d dumps", len(dumps))
	}
	body, err := os.ReadFile(dumps[0])
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "goroutine ") || !strings.Contains(string(body), "TestStuckUIWritesItsStacksOnce") {
		t.Errorf("the dump does not hold every goroutine's stack: %.300s", body)
	}
	if filepath.Dir(dumps[0]) != a.hangs.dir || !strings.HasPrefix(filepath.Base(dumps[0]), "hang-") {
		t.Errorf("dump at %s", dumps[0])
	}

	time.Sleep(400 * time.Millisecond)
	if n := len(a.hangs.written()); n != 1 {
		t.Errorf("one stall produced %d dumps", n)
	}

	close(release)
	waitFor(t, a, 5*time.Second, func() bool { return true })
	time.Sleep(300 * time.Millisecond)
	if n := len(a.hangs.written()); n != 1 {
		t.Errorf("a UI answering again was dumped %d times in all", n)
	}
}
