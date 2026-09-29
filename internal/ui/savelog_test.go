package ui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"

	"github.com/siner308/sims/internal/device"
	"github.com/siner308/sims/internal/sims"
)

// The file is for the developer who has to read the whole log, so it carries every line the view holds, not the ones the filter left on screen.
func TestSavingTheLogWritesEveryLineHeld(t *testing.T) {
	p := &logProvider{fakeProvider: fakeProvider{platform: device.PlatformAndroid}, script: "echo 'E/AndroidRuntime: crash'; echo 'I/Other: fine'; sleep 10"}
	a := New("test", sims.New(p))
	screen, stop := runHeadless(t, a)
	defer stop()

	dev := device.Device{ID: "dev", Name: "Pixel 7", Platform: device.PlatformAndroid, State: device.StateBooted}
	var lv *logsView
	onUI(a, func() {
		lv = newLogsView(a, dev, &device.App{BundleID: "com.example.app", Name: "My App"})
		a.push(lv)
	})
	waitFor(t, a, 5*time.Second, func() bool { return lv != nil && len(lv.lines) == 2 })
	onUI(a, func() { lv.filter = "crash"; lv.redraw() })

	target := filepath.Join(t.TempDir(), "out.log")
	screen.InjectKey(tcell.KeyRune, 's', tcell.ModNone)
	var in *tview.InputField
	waitFor(t, a, 5*time.Second, func() bool {
		in, _ = a.tv.GetFocus().(*tview.InputField)
		return in != nil
	})
	suggested := onUIGet(a, func() string { return in.GetText() })
	if !strings.HasPrefix(filepath.Base(suggested), "Pixel-7-My-App-") || !strings.HasSuffix(suggested, ".log") {
		t.Errorf("suggested path = %q", suggested)
	}
	onUI(a, func() { in.SetText(target) })
	screen.InjectKey(tcell.KeyEnter, 0, tcell.ModNone)

	waitFor(t, a, 5*time.Second, func() bool { return strings.Contains(a.status.GetText(true), "wrote 2 log lines") })
	body, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "E/AndroidRuntime: crash\nI/Other: fine\n" {
		t.Errorf("file holds %q", body)
	}
	if _, isInput := onUIGet(a, func() tview.Primitive { return a.tv.GetFocus() }).(*tview.InputField); isInput {
		t.Error("the prompt kept the focus after saving")
	}
}
