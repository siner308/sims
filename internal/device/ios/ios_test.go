package ios_test

import (
	"context"
	"testing"
	"time"

	"github.com/siner308/sims/internal/device"
	"github.com/siner308/sims/internal/device/ios"
)

func provider(t *testing.T) *ios.Provider {
	t.Helper()
	p := ios.New()
	if err := p.Available(); err != nil {
		t.Skip(err)
	}
	return p
}

func TestProvider_List(t *testing.T) {
	p := provider(t)
	devices, err := p.List(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range devices {
		if d.Platform != device.PlatformIOS {
			t.Errorf("platform = %q", d.Platform)
		}
		if d.ID == "" || d.Name == "" || d.Runtime == "" {
			t.Errorf("incomplete device: %+v", d)
		}
		if d.State == device.StateUnknown {
			t.Errorf("unmapped state: %+v", d)
		}
	}
	t.Logf("%d devices", len(devices))
}

func TestProvider_Images(t *testing.T) {
	p := provider(t)
	images, err := p.Images(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(images) == 0 {
		t.Fatal("no runtimes")
	}
	for _, img := range images {
		if img.Version == "" || img.Name == "" {
			t.Errorf("unparsed runtime: %+v", img)
		}
	}
}

func TestProvider_DeviceTypes(t *testing.T) {
	p := provider(t)
	types, err := p.DeviceTypes(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(types) == 0 {
		t.Fatal("no device types")
	}
	withScreen := 0
	for _, dt := range types {
		if dt.ID == "" {
			t.Errorf("device type without id: %+v", dt)
		}
		if dt.Screen != "" {
			withScreen++
		}
	}
	t.Logf("%d device types, %d with a screen size", len(types), withScreen)
}

// Booting a simulator is simctl's job; putting a window on screen is a convenience on top of it.
// Xcode 26 ships Simulator.app and 27 ships DeviceHub.app, and a machine with neither still has a
// booted device, so a missing window app must not be reported as a failed boot.
func TestBootSucceedsWithoutASimulatorWindow(t *testing.T) {
	p := ios.New()
	if err := p.Available(); err != nil {
		t.Skip(err)
	}
	devices, err := p.List(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	var target device.Device
	for _, d := range devices {
		if d.Kind == device.KindVirtual && !d.Running() {
			target = d
			break
		}
	}
	if target.ID == "" {
		t.Skip("no shut-down simulator to boot")
	}

	if err := p.Boot(t.Context(), target); err != nil {
		t.Fatalf("boot reported a failure: %v", err)
	}
	t.Cleanup(func() { p.Shutdown(context.Background(), target) })

	// and the device really is booting, rather than the error having been swallowed
	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		list, err := p.List(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		for _, d := range list {
			if d.ID == target.ID && d.Running() {
				return
			}
		}
		time.Sleep(time.Second)
	}
	t.Errorf("%s never reached a running state", target.Name)
}
