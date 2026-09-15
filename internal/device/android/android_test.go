package android_test

import (
	"testing"

	"github.com/siner308/sims/internal/device"
	"github.com/siner308/sims/internal/device/android"
)

func provider(t *testing.T) *android.Provider {
	t.Helper()
	p := android.New()
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
		if d.Platform != device.PlatformAndroid {
			t.Errorf("platform = %q", d.Platform)
		}
		if d.ID == "" || d.Name == "" {
			t.Errorf("empty id/name: %+v", d)
		}
		if d.State == device.StateBooted && d.Serial == "" {
			t.Errorf("booted without serial: %+v", d)
		}
		t.Logf("%+v", d)
	}
}

func TestProvider_Images(t *testing.T) {
	p := provider(t)
	images, err := p.Images(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(images) == 0 {
		t.Fatal("no system images listed")
	}
	installed := 0
	for _, img := range images {
		if img.Installed {
			installed++
		}
		if img.Version == "" || img.Name == "" {
			t.Errorf("unparsed image: %+v", img)
		}
	}
	t.Logf("%d images, %d installed", len(images), installed)
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
