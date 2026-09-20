package ios

import (
	"bytes"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/siner308/sims/internal/device"
	"github.com/siner308/sims/internal/proxy"
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

// devicectl refuses an unsigned .mobileconfig, reading it as a provisioning profile and reporting
// "The provisioning profile CMS/PKCS#7 envelope is invalid" (OSStatus -25257). A signed profile is
// a CMS envelope, so it no longer starts with a plist header.
func TestSignedProfileIsACMSEnvelope(t *testing.T) {
	ca, err := proxy.LoadOrCreateCA(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	target := device.ProxyTarget{
		Host: "192.168.1.20", Port: 9090,
		CACert: ca.CertPEM(), CACertDER: ca.CertDER(),
		CertName: "sims proxy CA", SSID: "Home",
	}
	path, cleanup, err := writeSignedProfile(target, ca)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()

	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.HasPrefix(bytes.TrimSpace(body), []byte("<?xml")) {
		t.Fatal("the profile went out unsigned; devicectl reads that as a provisioning profile and refuses it")
	}
	// DER SEQUENCE
	if body[0] != 0x30 {
		t.Errorf("signed profile starts with %#x, want a DER sequence", body[0])
	}
	// the payload survives inside the envelope
	if !bytes.Contains(body, []byte("com.apple.security.root")) {
		t.Error("the signed envelope does not carry the profile")
	}

	// and openssl agrees it is a CMS structure carrying our plist
	out, err := exec.Command("openssl", "smime", "-verify", "-inform", "der", "-in", path, "-noverify").Output()
	if err != nil {
		t.Skipf("openssl not usable here: %v", err)
	}
	if !bytes.Contains(out, []byte("ProxyServerPort")) {
		t.Error("the verified content is not the profile")
	}
}

// A phone must not be handed an unsigned profile by accident.
func TestPhoneProfileRequiresASigner(t *testing.T) {
	p := New()
	d := device.Device{ID: "x", Name: "phone", Kind: device.KindPhysical, Platform: device.PlatformIOS, State: device.StateConnected}
	_, err := p.SetProxy(t.Context(), d, device.ProxyTarget{
		Host: "10.0.0.2", Port: 1, CACert: []byte("x"), CACertDER: []byte{1}, SSID: "Home",
	})
	if err == nil {
		t.Fatal("an unsigned profile was accepted for a phone")
	}
	if !strings.Contains(err.Error(), "signed") {
		t.Errorf("error = %q", err)
	}
}

// Every value interpolated into the plist has to be escaped, not just the ones a test happened to
// cover. The host comes from a network interface or a flag, and an unescaped ampersand there makes
// a profile the phone rejects with nothing pointing back at the cause.
func TestProfileEscapesEveryInterpolatedValue(t *testing.T) {
	path, cleanup, err := writeProfile(device.ProxyTarget{
		Host: `10.0.0.1" & <bad>`, Port: 9090,
		CACertDER: []byte{1, 2, 3}, CertName: "sims proxy CA", SSID: "Home",
	})
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()

	if out, err := exec.Command("plutil", "-lint", path).CombinedOutput(); err != nil {
		body, _ := os.ReadFile(path)
		t.Fatalf("a host with & and <> produced an invalid plist: %v %s\n%s", err, out, body)
	}
}
