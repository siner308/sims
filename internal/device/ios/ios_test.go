package ios_test

import (
	"testing"

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
}
