package ios

import (
	"os/exec"
	"strings"
	"testing"

	"github.com/siner308/sims/internal/device"
)

const coreDevicesFixture = `{
  "result": {
    "devices": [
      {
        "identifier": "AAAAAAAA-0000-0000-0000-000000000001",
        "deviceProperties": {"name": "wired phone", "osVersionNumber": "18.6"},
        "connectionProperties": {"pairingState": "paired", "transportType": "wired", "tunnelState": "connected"},
        "hardwareProperties": {"marketingName": "iPhone 15", "productType": "iPhone15,4", "platform": "iOS", "udid": "00008110-000000000000001E"}
      },
      {
        "identifier": "AAAAAAAA-0000-0000-0000-000000000002",
        "deviceProperties": {"name": "wifi phone", "osVersionNumber": "27.0"},
        "connectionProperties": {"pairingState": "paired", "transportType": "localNetwork", "tunnelState": "disconnected"},
        "hardwareProperties": {"marketingName": "iPhone 14 Pro", "productType": "iPhone15,2", "platform": "iOS", "udid": "00008120-000000000000002E"}
      },
      {
        "identifier": "AAAAAAAA-0000-0000-0000-000000000003",
        "deviceProperties": {"name": "unpaired phone"},
        "connectionProperties": {"pairingState": "unpaired", "tunnelState": "unavailable"},
        "hardwareProperties": {"productType": "iPhone14,7", "platform": "iOS", "udid": "00008110-000000000000003E"}
      },
      {
        "identifier": "AAAAAAAA-0000-0000-0000-000000000004",
        "deviceProperties": {"name": "watch"},
        "connectionProperties": {"pairingState": "paired", "tunnelState": "unavailable"},
        "hardwareProperties": {"productType": "Watch6,12", "platform": "watchOS", "udid": "00008301-000000000000004E"}
      }
    ]
  }
}`

func TestParseCoreDevices(t *testing.T) {
	devices, err := parseCoreDevices([]byte(coreDevicesFixture))
	if err != nil {
		t.Fatal(err)
	}
	if len(devices) != 3 {
		t.Fatalf("got %d devices, want 3 (watchOS filtered out)", len(devices))
	}
	byName := map[string]device.Device{}
	for _, d := range devices {
		byName[d.Name] = d
		if d.Kind != device.KindPhysical || d.Platform != device.PlatformIOS {
			t.Errorf("%s: kind/platform = %s/%s", d.Name, d.Kind, d.Platform)
		}
	}

	wired := byName["wired phone"]
	if wired.Transport != device.TransportUSB || wired.State != device.StateConnected {
		t.Errorf("wired: transport=%s state=%s", wired.Transport, wired.State)
	}
	if wired.Runtime != "iOS 18.6" || wired.Model != "iPhone 15" || wired.Serial != "00008110-000000000000001E" {
		t.Errorf("wired: runtime=%q model=%q serial=%q", wired.Runtime, wired.Model, wired.Serial)
	}

	wifi := byName["wifi phone"]
	if wifi.Transport != device.TransportWiFi || wifi.State != device.StateOffline {
		t.Errorf("wifi: transport=%s state=%s", wifi.Transport, wifi.State)
	}

	unpaired := byName["unpaired phone"]
	if unpaired.State != device.StateUnpaired || unpaired.Model != "iPhone14,7" || unpaired.Runtime != "" {
		t.Errorf("unpaired: state=%s model=%q runtime=%q", unpaired.State, unpaired.Model, unpaired.Runtime)
	}
}

func TestPhysicalLogCmd_WithoutIdevicesyslog(t *testing.T) {
	if _, err := exec.LookPath("idevicesyslog"); err == nil {
		t.Skip("idevicesyslog is installed")
	}
	d := device.Device{Kind: device.KindPhysical, Serial: "00008110-000000000000001E", State: device.StateConnected}
	cmd, err := physicalLogCmd(t.Context(), d, nil)
	if err == nil || cmd != nil {
		t.Fatalf("want an error pointing at libimobiledevice, got cmd=%v err=%v", cmd, err)
	}
	if !strings.Contains(err.Error(), "libimobiledevice") {
		t.Errorf("error should tell how to install: %v", err)
	}
}

func TestMapState(t *testing.T) {
	cases := map[string]device.State{
		"Booted": device.StateBooted, "Booting": device.StateBooting, "Shutting Down": device.StateShuttingDown,
		"Shutdown": device.StateShutdown, "Creating": device.State("Creating"),
	}
	for in, want := range cases {
		if got := mapState(in); got != want {
			t.Errorf("mapState(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestDeviceTypeSupports(t *testing.T) {
	ios := func(v string) device.Image { return device.Image{Version: v, Platform: "iOS"} }
	old := device.DeviceType{MinRuntime: "9.0", MaxRuntime: "15.255.255", Family: "iPhone"}
	if old.Supports(ios("17.5")) || !old.Supports(ios("15.4")) || !old.Supports(ios("")) {
		t.Error("6s-class type must reject iOS 17.5, accept 15.4 and an unknown version")
	}
	cur := device.DeviceType{MinRuntime: "17.0", MaxRuntime: "26.255.255", Family: "iPhone"}
	if !cur.Supports(ios("17.5")) || !cur.Supports(ios("26.5")) || cur.Supports(ios("16.4")) {
		t.Error("current type bounds wrong")
	}
	tv := device.DeviceType{MinRuntime: "9.0", MaxRuntime: "65535.255.255", Family: "Apple TV"}
	if tv.Supports(ios("26.5")) || !tv.Supports(device.Image{Version: "26.5", Platform: "tvOS"}) {
		t.Error("an Apple TV type must only pair with tvOS runtimes")
	}
	if !(device.DeviceType{}).Supports(device.Image{Version: "99"}) {
		t.Error("no bounds means compatible")
	}
}
