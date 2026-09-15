package android

import (
	"testing"

	"github.com/siner308/sims/internal/device"
)

const adbDevicesFixture = `List of devices attached
emulator-5554          device product:sdk_gphone64_arm64 model:sdk_gphone64_arm64 device:emu64a transport_id:3
R3CT40ABCDE            device usb:1-1 product:e1qksx model:SM_S928N device:e1q transport_id:5
192.168.0.23:5555      device product:panther model:Pixel_7 device:panther transport_id:7
adb-R3CT40ABCDE-XyZabc._adb-tls-connect._tcp offline transport_id:8
0123456789ABCDEF       unauthorized transport_id:9
`

func TestParseADBDevices(t *testing.T) {
	entries := parseADBDevices(adbDevicesFixture)
	if len(entries) != 5 {
		t.Fatalf("got %d entries, want 5", len(entries))
	}

	want := []struct {
		serial    string
		emulator  bool
		transport device.Transport
		state     device.State
		model     string
	}{
		{"emulator-5554", true, device.TransportUSB, device.StateConnected, "sdk_gphone64_arm64"},
		{"R3CT40ABCDE", false, device.TransportUSB, device.StateConnected, "SM_S928N"},
		{"192.168.0.23:5555", false, device.TransportWiFi, device.StateConnected, "Pixel_7"},
		{"adb-R3CT40ABCDE-XyZabc._adb-tls-connect._tcp", false, device.TransportWiFi, device.StateOffline, ""},
		{"0123456789ABCDEF", false, device.TransportUSB, device.StateUnauthorized, ""},
	}
	for i, w := range want {
		e := entries[i]
		if e.serial != w.serial {
			t.Errorf("[%d] serial = %q, want %q", i, e.serial, w.serial)
		}
		if e.isEmulator() != w.emulator {
			t.Errorf("[%d] isEmulator = %v", i, e.isEmulator())
		}
		if e.transport() != w.transport {
			t.Errorf("[%d] transport = %q, want %q", i, e.transport(), w.transport)
		}
		if e.deviceState() != w.state {
			t.Errorf("[%d] state = %q, want %q", i, e.deviceState(), w.state)
		}
		if e.props["model"] != w.model {
			t.Errorf("[%d] model = %q, want %q", i, e.props["model"], w.model)
		}
	}
}

func TestSourceOf(t *testing.T) {
	cases := []struct {
		user      bool
		installer string
		want      string
	}{
		{false, "null", "preinstalled"},
		{false, "com.android.vending", "preinstalled"},
		{true, "null", "adb"},
		{true, "", "adb"},
		{true, "com.android.vending", "store"},
		{true, "com.sec.android.app.samsungapps", "com.sec.android.app.samsungapps"},
	}
	for _, c := range cases {
		if got := sourceOf(c.user, c.installer); got != c.want {
			t.Errorf("sourceOf(%v, %q) = %q, want %q", c.user, c.installer, got, c.want)
		}
	}
}
