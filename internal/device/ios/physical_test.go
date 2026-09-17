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
      },
      {
        "identifier": "513A49F9-427C-4EE9-AD64-C6B52B4AC716",
        "deviceProperties": {"name": "iPad Air 11-inch (M2)", "osVersionNumber": "18.2"},
        "connectionProperties": {"pairingState": "paired", "transportType": "sameMachine", "tunnelState": "disconnected", "lastConnectionDate": "2026-06-06T10:00:00.000Z"},
        "hardwareProperties": {"marketingName": "iPad Air 11-inch (M2)", "productType": "iPad14,8", "platform": "iOS", "udid": "513A49F9-427C-4EE9-AD64-C6B52B4AC716", "reality": "simulated"}
      }
    ]
  }
}`

func TestParseCoreDevices(t *testing.T) {
	devices, simLastSeen, err := parseCoreDevices([]byte(coreDevicesFixture))
	if err != nil {
		t.Fatal(err)
	}
	if len(devices) != 3 {
		t.Fatalf("got %d devices, want 3 (watchOS and the simulator filtered out)", len(devices))
	}
	if got := simLastSeen["513A49F9-427C-4EE9-AD64-C6B52B4AC716"]; got.IsZero() || got.Year() != 2026 || got.Month() != 6 {
		t.Errorf("simulator connection date not kept: %v", got)
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
	ios := func(v string) device.Image { return device.Image{Version: v, OS: "iOS"} }
	old := device.DeviceType{MinRuntime: "9.0", MaxRuntime: "15.255.255", Family: "iPhone"}
	if old.Supports(ios("17.5")) || !old.Supports(ios("15.4")) || !old.Supports(ios("")) {
		t.Error("6s-class type must reject iOS 17.5, accept 15.4 and an unknown version")
	}
	cur := device.DeviceType{MinRuntime: "17.0", MaxRuntime: "26.255.255", Family: "iPhone"}
	if !cur.Supports(ios("17.5")) || !cur.Supports(ios("26.5")) || cur.Supports(ios("16.4")) {
		t.Error("current type bounds wrong")
	}
	tv := device.DeviceType{MinRuntime: "9.0", MaxRuntime: "65535.255.255", Family: "Apple TV"}
	if tv.Supports(ios("26.5")) || !tv.Supports(device.Image{Version: "26.5", OS: "tvOS"}) {
		t.Error("an Apple TV type must only pair with tvOS runtimes")
	}
	if !(device.DeviceType{}).Supports(device.Image{Version: "99"}) {
		t.Error("no bounds means compatible")
	}
}

func TestReachable(t *testing.T) {
	offline := device.Device{ID: "UDID", Kind: device.KindPhysical, State: device.StateOffline}
	if err := reachable(offline); err != nil {
		t.Fatalf("an offline phone is a closed tunnel, not an unreachable device: %v", err)
	}
	if err := reachable(device.Device{ID: "UDID", State: device.StateConnected}); err != nil {
		t.Fatalf("connected: %v", err)
	}
	err := reachable(device.Device{ID: "UDID", State: device.StateUnpaired})
	if err == nil || !strings.Contains(err.Error(), "sims device pair UDID") {
		t.Fatalf("unpaired should name the pair command, got %v", err)
	}
}
