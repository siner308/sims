package ios

import (
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/siner308/sims/internal/device"
)

// The profile is a plist Apple's own tooling has to accept; plutil is the check that catches a
// malformed one here rather than on the phone.
func TestProfileIsValidPlist(t *testing.T) {
	target := device.ProxyTarget{
		Host:      "192.168.1.20",
		Port:      9090,
		CACertDER: []byte{0x30, 0x82, 0x01, 0x02, 0x03},
		CertName:  "sims proxy CA",
		SSID:      "Home Wifi",
	}
	path, cleanup, err := writeProfile(target)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()

	out, err := exec.Command("plutil", "-lint", path).CombinedOutput()
	if err != nil {
		body, _ := os.ReadFile(path)
		t.Fatalf("plutil -lint: %v %s\n%s", err, out, body)
	}

	printed, err := exec.Command("plutil", "-p", path).Output()
	if err != nil {
		t.Fatal(err)
	}
	text := string(printed)
	for _, want := range []string{"192.168.1.20", "9090", "Home Wifi", "com.apple.security.root", "dev.sims.proxy"} {
		if !strings.Contains(text, want) {
			t.Errorf("profile is missing %q:\n%s", want, text)
		}
	}
}

func TestProfileNeedsWifiName(t *testing.T) {
	_, _, err := writeProfile(device.ProxyTarget{Host: "10.0.0.2", Port: 1, CACertDER: []byte{1}})
	if err == nil {
		t.Fatal("a profile without a wifi name should be refused")
	}
	if !strings.Contains(err.Error(), "network") {
		t.Errorf("error = %q", err)
	}
}

// A wifi name is whatever its owner typed. Unescaped, an ampersand or a bracket makes the profile
// unparseable and the install fails on the phone with nothing to explain it.
func TestProfileEscapesTheWifiName(t *testing.T) {
	path, cleanup, err := writeProfile(device.ProxyTarget{
		Host: "192.168.1.20", Port: 9090, CACertDER: []byte{1, 2, 3},
		CertName: "sims proxy CA", SSID: `Joe & Ann's <Home>`,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()

	if out, err := exec.Command("plutil", "-lint", path).CombinedOutput(); err != nil {
		body, _ := os.ReadFile(path)
		t.Fatalf("a wifi name with & and <> produced an invalid plist: %v %s\n%s", err, out, body)
	}
	printed, err := exec.Command("plutil", "-p", path).Output()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(printed), "Joe & Ann's <Home>") {
		t.Errorf("the wifi name did not survive the round trip:\n%s", printed)
	}
}
