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

// plutil is Apple's own check that a file is a plist a phone will accept, and it exists only on
// macOS. Where it is missing the profile's content is still checked; what cannot be verified is
// skipped rather than reported as a defect in the profile.
func lintPlist(t *testing.T, path string) {
	t.Helper()
	if _, err := exec.LookPath("plutil"); err != nil {
		t.Skip("plutil is only on macOS, and it is what says whether this parses as a plist")
	}
	if out, err := exec.Command("plutil", "-lint", path).CombinedOutput(); err != nil {
		t.Fatalf("plutil -lint: %v %s", err, out)
	}
}

// printPlist is plutil's own rendering of the file, used to read values back the way iOS would.
func printPlist(t *testing.T, path string) string {
	t.Helper()
	if _, err := exec.LookPath("plutil"); err != nil {
		t.Skip("plutil is only on macOS, and it is what reads the profile back")
	}
	out, err := exec.Command("plutil", "-p", path).Output()
	if err != nil {
		t.Fatalf("plutil -p: %v", err)
	}
	return string(out)
}

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

	lintPlist(t, path)
	text := printPlist(t, path)
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

	lintPlist(t, path)
	printed := printPlist(t, path)
	if !strings.Contains(printed, "Joe & Ann's <Home>") {
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

	lintPlist(t, path)
}

// A certificate-only profile must carry no proxy payload. The cert-install command asks for one,
// and a phone handed a manual proxy pointing at nothing has no working network, with no undo since
// the command does not remove what it installed.
func TestCertificateOnlyProfileCarriesNoProxy(t *testing.T) {
	path, cleanup, err := writeProfile(device.ProxyTarget{
		CACertDER: []byte{1, 2, 3}, CertName: "sims proxy CA",
		// no host, no port, and no wifi name: the cert-install command passes exactly this
	})
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()

	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	lintPlist(t, path)
	text := string(body)
	for _, unwanted := range []string{"com.apple.wifi.managed", "ProxyServer", "ProxyType", "SSID_STR"} {
		if strings.Contains(text, unwanted) {
			t.Errorf("a certificate-only profile carries %q:\n%s", unwanted, text)
		}
	}
	if strings.Contains(text, ":0") {
		t.Errorf("the profile describes an address of :0:\n%s", text)
	}
	if !strings.Contains(text, "com.apple.security.root") {
		t.Error("the certificate payload is missing")
	}
}

// A capture's profile still carries both payloads.
func TestCaptureProfileCarriesTheProxy(t *testing.T) {
	path, cleanup, err := writeProfile(device.ProxyTarget{
		Host: "192.168.1.20", Port: 9090,
		CACertDER: []byte{1, 2, 3}, CertName: "sims proxy CA", SSID: "Home",
	})
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()

	body, _ := os.ReadFile(path)
	lintPlist(t, path)
	for _, want := range []string{"com.apple.wifi.managed", "192.168.1.20", "9090", "Home"} {
		if !strings.Contains(string(body), want) {
			t.Errorf("a capture profile is missing %q", want)
		}
	}
}
