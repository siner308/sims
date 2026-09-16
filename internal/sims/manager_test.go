package sims

import (
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/siner308/sims/internal/device"
	"github.com/siner308/sims/internal/device/devicetest"
)

var (
	pixel = device.Device{ID: "Pixel_7", Name: "Pixel_7", Platform: device.PlatformAndroid, Kind: device.KindVirtual, Transport: device.TransportAVD, State: device.StateShutdown}
	phone = device.Device{ID: "Pixel_7_phys", Name: "Pixel 7", Platform: device.PlatformAndroid, Kind: device.KindPhysical, Transport: device.TransportUSB, State: device.StateConnected, Serial: "R3CT30ABCDE"}
	sim   = device.Device{ID: "UDID-1", Name: "iPhone 17", Platform: device.PlatformIOS, Kind: device.KindVirtual, Transport: device.TransportSim, State: device.StateShutdown, LastActiveAt: time.Now()}
	fresh = device.Device{ID: "UDID-2", Name: "iPhone 17", Platform: device.PlatformIOS, Kind: device.KindVirtual, Transport: device.TransportSim, State: device.StateShutdown}
	ipad  = device.Device{ID: "UDID-3", Name: "iPad", Platform: device.PlatformIOS, Kind: device.KindPhysical, Transport: device.TransportWiFi, State: device.StateOffline}
)

func TestNew_SortsProvidersByAvailability(t *testing.T) {
	android := devicetest.NewAndroid(pixel)
	ios := devicetest.NewIOS(sim)
	ios.AvailableErr = errors.New("needs macOS")
	m := New(android, ios)

	if got := m.Platforms(); len(got) != 1 || got[0] != device.PlatformAndroid {
		t.Fatalf("Platforms() = %v, want [android]", got)
	}
	if err := m.Missing()[device.PlatformIOS]; err == nil || err.Error() != "needs macOS" {
		t.Fatalf("Missing()[ios] = %v", err)
	}
	if len(m.Providers()) != 2 {
		t.Fatalf("Providers() should keep the unusable one for doctor, got %d", len(m.Providers()))
	}
	if _, err := m.Provider(device.PlatformIOS); err == nil || !strings.Contains(err.Error(), "needs macOS") {
		t.Fatalf("Provider(ios) = %v, want the availability error", err)
	}
	if _, err := m.Provider("windows"); err == nil {
		t.Fatal("Provider(windows) should fail")
	}
}

func TestDevices_MergesAndReportsPartialFailure(t *testing.T) {
	android := devicetest.NewAndroid(pixel, phone)
	ios := devicetest.NewIOS(sim)
	ios.ListErr = errors.New("simctl timed out")
	m := New(android, ios)

	devices, err := m.Devices(t.Context())
	if len(devices) != 2 {
		t.Fatalf("got %d devices, want the two android ones despite the ios failure", len(devices))
	}
	if err == nil || !strings.Contains(err.Error(), "ios: simctl timed out") {
		t.Fatalf("err = %v, want the ios failure named by platform", err)
	}
}

func TestResolve(t *testing.T) {
	m := New(devicetest.NewAndroid(pixel, phone), devicetest.NewIOS(sim, fresh, ipad))
	ctx := t.Context()

	cases := []struct {
		ref, platform, wantID, wantErr string
	}{
		{ref: "Pixel_7", wantID: "Pixel_7"},              // exact id beats the name "Pixel 7"
		{ref: "R3CT30ABCDE", wantID: "Pixel_7_phys"},     // adb serial
		{ref: "pixel 7", wantID: "Pixel_7_phys"},         // case-insensitive name
		{ref: "iphone 17", wantErr: "matches 2 devices"}, // two sims share the name
		{ref: "iPad", platform: "ios", wantID: "UDID-3"}, // platform filter
		{ref: "iPad", platform: "android", wantErr: "no device matches"},
		{ref: "nope", wantErr: `no device matches "nope"`},
		{ref: "iPad", platform: "windows", wantErr: "unknown platform"},
	}
	for _, c := range cases {
		d, err := m.Resolve(ctx, c.ref, device.Platform(c.platform))
		switch {
		case c.wantErr != "":
			if err == nil || !strings.Contains(err.Error(), c.wantErr) {
				t.Errorf("Resolve(%q, %q) err = %v, want %q", c.ref, c.platform, err, c.wantErr)
			}
		case err != nil:
			t.Errorf("Resolve(%q, %q) failed: %v", c.ref, c.platform, err)
		case d.ID != c.wantID:
			t.Errorf("Resolve(%q, %q) = %s, want %s", c.ref, c.platform, d.ID, c.wantID)
		}
	}
}

func TestResolve_NamesListFailureWhenNothingMatches(t *testing.T) {
	ios := devicetest.NewIOS(sim)
	ios.ListErr = errors.New("simctl broke")
	m := New(devicetest.NewAndroid(pixel), ios)

	if _, err := m.Resolve(t.Context(), "Pixel_7", ""); err != nil {
		t.Fatalf("a match on the healthy platform should still resolve: %v", err)
	}
	_, err := m.Resolve(t.Context(), "iPhone 17", "")
	if err == nil || !strings.Contains(err.Error(), "simctl broke") {
		t.Fatalf("err = %v, want the list failure so the user knows why nothing matched", err)
	}
}

func TestBootAndWaitBooted_ReturnsFreshRecord(t *testing.T) {
	android := devicetest.NewAndroid(pixel)
	m := New(android)
	ctx := t.Context()

	if err := m.Boot(ctx, pixel); err != nil {
		t.Fatal(err)
	}
	booted, err := m.WaitBooted(ctx, pixel, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if booted.State != device.StateBooted || booted.Serial != "emulator-5554" {
		t.Fatalf("WaitBooted returned %+v, want the booted record with its serial", booted)
	}
	if !android.Called("Boot Pixel_7") {
		t.Fatalf("calls = %v", android.Calls)
	}
}

func TestWaitBooted_TimesOut(t *testing.T) {
	ios := devicetest.NewIOS(fresh)
	m := New(ios)
	_, err := m.WaitBooted(t.Context(), fresh, 10*time.Millisecond)
	if err == nil || !strings.Contains(err.Error(), "did not finish booting") {
		t.Fatalf("err = %v", err)
	}
}

func TestConnect_DispatchesPerPlatform(t *testing.T) {
	android := devicetest.NewAndroid(phone)
	ios := devicetest.NewIOS(ipad)
	m := New(android, ios)
	ctx := t.Context()

	note, err := m.Connect(ctx, phone)
	if err != nil || note != "192.168.0.5:5555" {
		t.Fatalf("android Connect = %q, %v; want the wifi address from EnableWireless", note, err)
	}
	note, err = m.Connect(ctx, ipad)
	if err != nil || note != "connected" || !ios.Called("Connect UDID-3") {
		t.Fatalf("ios Connect = %q, %v, calls %v", note, err, ios.Calls)
	}

	plain := &devicetest.Fake{ID: "plain", Devices: []device.Device{{ID: "x", Platform: "plain"}}}
	if _, err := New(plain).Connect(ctx, device.Device{ID: "x", Platform: "plain"}); !errors.Is(err, errors.ErrUnsupported) {
		t.Fatalf("a provider without Connector or Wireless should be unsupported, got %v", err)
	}
}

func TestPairAndDisconnect_Unsupported(t *testing.T) {
	m := New(devicetest.NewAndroid(phone), devicetest.NewIOS(ipad))
	ctx := t.Context()

	if err := m.Pair(ctx, phone); !errors.Is(err, errors.ErrUnsupported) {
		t.Fatalf("android Pair = %v, want ErrUnsupported (it pairs by address)", err)
	}
	if err := m.Pair(ctx, ipad); err != nil {
		t.Fatalf("ios Pair = %v", err)
	}
	if err := m.Disconnect(ctx, ipad); !errors.Is(err, errors.ErrUnsupported) {
		t.Fatalf("ios Disconnect = %v, want ErrUnsupported", err)
	}
	if err := m.Disconnect(ctx, phone); err != nil {
		t.Fatalf("android Disconnect = %v", err)
	}
}

func TestAddressCommands_PickTheOnlyWirelessProvider(t *testing.T) {
	android := devicetest.NewAndroid()
	m := New(android, devicetest.NewIOS())
	ctx := t.Context()

	if err := m.PairAddress(ctx, "", "10.0.0.2:37099", "123456"); err != nil || !android.Called("Pair 10.0.0.2:37099 123456") {
		t.Fatalf("PairAddress: %v, calls %v", err, android.Calls)
	}
	if err := m.ConnectAddress(ctx, "", "10.0.0.2:5555"); err != nil || !android.Called("Connect 10.0.0.2:5555") {
		t.Fatalf("ConnectAddress: %v, calls %v", err, android.Calls)
	}
	if err := m.ConnectAddress(ctx, device.PlatformIOS, "10.0.0.2:5555"); !errors.Is(err, errors.ErrUnsupported) {
		t.Fatalf("ios ConnectAddress = %v, want ErrUnsupported", err)
	}

	two := New(devicetest.NewAndroid(), &devicetest.Android{Fake: &devicetest.Fake{ID: "android2"}})
	if err := two.ConnectAddress(ctx, "", "10.0.0.2:5555"); err == nil || !strings.Contains(err.Error(), "--platform") {
		t.Fatalf("two wireless providers without --platform should be rejected, got %v", err)
	}
}

func TestSendKeyAndHardware(t *testing.T) {
	android := devicetest.NewAndroid(pixel, phone)
	android.HW = device.Hardware{RAMMB: 2048, Cores: 4, DiskGB: 6}
	ios := devicetest.NewIOS(sim)
	m := New(android, ios)
	ctx := t.Context()

	if err := m.SendKey(ctx, pixel, device.KeyHome); err != nil || !android.Called("SendKey Pixel_7 home") {
		t.Fatalf("SendKey: %v, calls %v", err, android.Calls)
	}
	if err := m.SendKey(ctx, sim, device.KeyHome); !errors.Is(err, errors.ErrUnsupported) {
		t.Fatalf("ios SendKey = %v, want ErrUnsupported", err)
	}
	if !m.CanEditHardware(device.PlatformAndroid) || m.CanEditHardware(device.PlatformIOS) {
		t.Fatal("CanEditHardware should be android only")
	}
	hw, err := m.Hardware(ctx, pixel)
	if err != nil || hw.RAMMB != 2048 {
		t.Fatalf("Hardware = %+v, %v", hw, err)
	}
	if _, err := m.Hardware(ctx, phone); !errors.Is(err, errors.ErrUnsupported) {
		t.Fatalf("a physical phone has no editable hardware, got %v", err)
	}
	if err := m.SetHardware(ctx, pixel, device.Hardware{RAMMB: 4096}); err != nil || android.HW.RAMMB != 4096 {
		t.Fatalf("SetHardware: %v, hw %+v", err, android.HW)
	}
}

func TestImages_SortAndResolve(t *testing.T) {
	android := devicetest.NewAndroid()
	android.ImageList = []device.Image{
		{ID: "system-images;android-35;google_apis;arm64-v8a", Name: "google_apis arm64-v8a", Version: "35", Installed: false},
		{ID: "system-images;android-36;google_apis;arm64-v8a", Name: "google_apis arm64-v8a", Version: "36", Installed: true},
	}
	ios := devicetest.NewIOS()
	ios.ImageList = []device.Image{
		{ID: "com.apple.CoreSimulator.SimRuntime.iOS-18-5", Name: "iOS 18.5", Version: "18.5", Installed: true, OS: "iOS"},
		{ID: "com.apple.CoreSimulator.SimRuntime.iOS-26-5", Name: "iOS 26.5", Version: "26.5", Installed: true, OS: "iOS"},
	}
	m := New(android, ios)
	ctx := t.Context()

	images, err := m.Images(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var order []string
	for _, img := range images {
		order = append(order, img.Version)
	}
	if got := strings.Join(order, " "); got != "36 26.5 18.5 35" {
		t.Fatalf("order = %q, want installed first, then platform, then newest", got)
	}

	img, err := m.ResolveImage(ctx, "ios 26.5", "")
	if err != nil || img.Platform != device.PlatformIOS || img.Version != "26.5" {
		t.Fatalf("ResolveImage by name = %+v, %v", img, err)
	}
	if _, err := m.ResolveImage(ctx, "google_apis arm64-v8a", ""); err == nil || !strings.Contains(err.Error(), "matches 2 images") {
		t.Fatalf("two versions share the name; err = %v", err)
	}
	img, err = m.ResolveImage(ctx, "system-images;android-35;google_apis;arm64-v8a", device.PlatformAndroid)
	if err != nil || img.Installed {
		t.Fatalf("ResolveImage by id = %+v, %v", img, err)
	}
	if err := m.InstallImage(ctx, img); err != nil || !android.Called("InstallImage system-images;android-35") {
		t.Fatalf("InstallImage: %v, calls %v", err, android.Calls)
	}
}

func TestDefaultDeviceType(t *testing.T) {
	types := []device.DeviceType{{ID: "tv_1080p", Name: "Television"}, {ID: "pixel_7", Name: "Pixel 7"}, {ID: "pixel_9", Name: "Pixel 9"}}
	if got, ok := DefaultDeviceType(types); !ok || got.ID != "pixel_7" {
		t.Fatalf("got %+v", got)
	}
	ipadFirst := []device.DeviceType{{ID: "iPad-Pro", Name: "iPad Pro"}, {ID: "iPhone-16", Name: "iPhone 16"}, {ID: "iPhone-15", Name: "iPhone 15"}}
	if got, _ := DefaultDeviceType(ipadFirst); got.ID != "iPhone-16" {
		t.Fatalf("the first phone should win, got %+v", got)
	}
	if got, _ := DefaultDeviceType([]device.DeviceType{{ID: "watch"}}); got.ID != "watch" {
		t.Fatalf("anything beats nothing, got %+v", got)
	}
	if _, ok := DefaultDeviceType(nil); ok {
		t.Fatal("no types, no default")
	}
	if got, err := ResolveDeviceType(types, "pixel 9"); err != nil || got.ID != "pixel_9" {
		t.Fatalf("ResolveDeviceType by name = %+v, %v", got, err)
	}
	if _, err := ResolveDeviceType(types, "pixel 10"); err == nil {
		t.Fatal("unknown type should fail")
	}
}

func TestCreate_FindsTheNeverBootedNamesake(t *testing.T) {
	ios := devicetest.NewIOS(sim)
	m := New(ios)
	img := PlatformImage{Image: device.Image{ID: "rt", Name: "iOS 26.5"}, Platform: device.PlatformIOS}

	created, err := m.Create(t.Context(), img, "iPhone 17", "iPhone-17-Pro", nil)
	if err != nil {
		t.Fatal(err)
	}
	if created.ID != "iPhone 17-id" {
		t.Fatalf("Create returned %s; the booted namesake %s should lose to the fresh one", created.ID, sim.ID)
	}
	if !ios.Called("Create iPhone 17 rt iPhone-17-Pro") {
		t.Fatalf("calls = %v", ios.Calls)
	}
}

func TestFindApp(t *testing.T) {
	android := devicetest.NewAndroid(phone)
	android.AppList = []device.App{{BundleID: "com.example.app", Name: "Example"}, {BundleID: "com.android.settings", Name: "Settings", System: true}}
	m := New(android)
	ctx := t.Context()

	if a, err := m.FindApp(ctx, phone, "example"); err != nil || a.BundleID != "com.example.app" {
		t.Fatalf("by name: %+v, %v", a, err)
	}
	if a, err := m.FindApp(ctx, phone, "com.android.settings"); err != nil || a.Name != "Settings" {
		t.Fatalf("by id: %+v, %v", a, err)
	}
	if _, err := m.FindApp(ctx, phone, "nope"); err == nil {
		t.Fatal("unknown app should fail")
	}
}

func TestDefaultOrder(t *testing.T) {
	old := time.Now().Add(-time.Hour)
	devices := []device.Device{
		{ID: "a", Name: "a", State: device.StateShutdown, LastActiveAt: time.Now()},
		{ID: "b", Name: "b", State: device.StateBooted, LastActiveAt: old},
		{ID: "c", Name: "c", State: device.StateShutdown, LastActiveAt: old},
		{ID: "d", Name: "d", State: device.StateOffline},
	}
	want := "b d a c"
	slices.SortFunc(devices, DefaultOrder)
	var ids []string
	for _, d := range devices {
		ids = append(ids, d.ID)
	}
	if got := strings.Join(ids, " "); got != want {
		t.Fatalf("order = %q, want %q (running, then offline, then shutdown by recency)", got, want)
	}
}
