package ui

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/siner308/sims/internal/device"
	"github.com/siner308/sims/internal/sims"
)

// The TUI has to raise leftover work on the way in, or a user whose capture was killed sees a
// broken network and no explanation.
func TestTUIOffersToCleanLeftoverOnStart(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("SIMS_PROXY_DIR", dir)
	// the note a capture killed outright would have left, with a pid that cannot be running
	note := `{"pid":-1,"port":65123,"device":{"id":"avd1","name":"Pixel_7","platform":"android","kind":"virtual"}}`
	if err := os.MkdirAll(filepath.Join(dir, "in-flight"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "in-flight", "-1-65123.json"), []byte(note), 0o600); err != nil {
		t.Fatal(err)
	}

	prov := &proxyProvider{fakeProvider: &fakeProvider{platform: device.PlatformAndroid, devices: []device.Device{emulator()}}}
	a := New("test", sims.New(prov))
	_, stop := runHeadless(t, a)
	defer stop()

	waitFor(t, a, 5*time.Second, func() bool { return a.body.HasPage("confirm") })
	t.Log("the TUI asked about the leftover capture on start")
}
