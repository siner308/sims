package ui

import (
	"testing"
	"time"

	"github.com/siner308/sims/internal/device/android"
	"github.com/siner308/sims/internal/device/ios"
)

func TestApp_RealProviders(t *testing.T) {
	a := New("test", android.New(), ios.New())
	if len(a.providers) == 0 {
		t.Skip("no toolchain available")
	}
	_, stop := runHeadless(t, a)
	defer stop()

	dv := a.stack[0].(*devicesView)
	waitFor(t, a, 30*time.Second, func() bool { return len(dv.devices) > 0 })
	for _, d := range dv.devices {
		t.Logf("%-8s %-4s %-25s %-15s %s", d.Platform, d.Transport, d.Name, d.Runtime, d.State)
	}
}
