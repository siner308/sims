package ios

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/siner308/sims/internal/device"
)

type stubSigner struct{}

func (stubSigner) SignCMS(data []byte) ([]byte, error) {
	return append([]byte("signed:"), data...), nil
}

func phone() device.Device {
	return device.Device{ID: "x", Name: "phone", Kind: device.KindPhysical, Platform: device.PlatformIOS, State: device.StateConnected}
}

func publishingTarget(record func(name, contentType string, body []byte)) device.ProxyTarget {
	return device.ProxyTarget{
		Host: "192.168.1.20", Port: 9090, CACert: []byte("x"), CACertDER: []byte{1, 2, 3},
		CertName: "sims proxy CA", SSID: "Home", Signer: stubSigner{},
		Publish: func(name, contentType string, body []byte) (string, error) {
			record(name, contentType, body)
			return "http://192.168.1.20:9090/" + name, nil
		},
	}
}

func taps(steps []device.ProxyStep) string {
	var all []string
	for _, s := range steps {
		all = append(all, s.Detail)
		all = append(all, s.Todo...)
	}
	return strings.Join(all, "\n")
}

func TestPhoneProfileIsPublishedAndOpenedInSafari(t *testing.T) {
	var gotName, gotType string
	var gotBody []byte
	var launched []string
	p := New()
	p.runDevicectl = func(ctx context.Context, args ...string) error {
		launched = append(launched, strings.Join(args, " "))
		return nil
	}
	steps, err := p.SetProxy(t.Context(), phone(), publishingTarget(func(name, contentType string, body []byte) {
		gotName, gotType, gotBody = name, contentType, body
	}))
	if err != nil {
		t.Fatal(err)
	}
	if gotName != "sims-proxy.mobileconfig" || gotType != "application/x-apple-aspen-config" {
		t.Errorf("published as %q %q", gotName, gotType)
	}
	if !strings.HasPrefix(string(gotBody), "signed:") || !strings.Contains(string(gotBody), "com.apple.wifi.managed") {
		t.Errorf("the published body is not the signed capture profile: %.40q", gotBody)
	}
	want := "device process launch --device x --payload-url http://192.168.1.20:9090/sims-proxy.mobileconfig com.apple.mobilesafari"
	if len(launched) != 1 || launched[0] != want {
		t.Errorf("devicectl calls = %q, want Safari opened on the profile", launched)
	}
	if got := taps(steps); strings.Contains(got, "type http") || !strings.Contains(got, "Allow") {
		t.Errorf("with Safari opened the user should only be asked to tap Allow:\n%s", got)
	}
}

func TestPhoneFallsBackToTypingTheURLWhenSafariCannotBeOpened(t *testing.T) {
	p := New()
	p.runDevicectl = func(ctx context.Context, args ...string) error { return errors.New("device is locked") }
	steps, err := p.SetProxy(t.Context(), phone(), publishingTarget(func(string, string, []byte) {}))
	if err != nil {
		t.Fatal(err)
	}
	got := taps(steps)
	if !strings.Contains(got, "type http://192.168.1.20:9090/sims-proxy.mobileconfig") || !strings.Contains(got, "device is locked") {
		t.Errorf("the user is not told to open the URL themselves, or not told why:\n%s", got)
	}
}

func TestPhoneProfileNeedsSomethingToServeIt(t *testing.T) {
	target := publishingTarget(func(string, string, []byte) {})
	target.Publish = nil
	if _, err := New().SetProxy(t.Context(), phone(), target); err == nil || !strings.Contains(err.Error(), "URL") {
		t.Errorf("a phone with nothing serving its profile was accepted: %v", err)
	}
}

// The profile is reused by the next capture, so clearing a phone changes nothing and says nothing.
func TestClearingAPhoneLeavesItsProfile(t *testing.T) {
	if err := New().ClearProxy(t.Context(), phone()); err != nil {
		t.Errorf("ClearProxy on a phone = %v", err)
	}
}

func TestAnInstalledProfileIsReportedNotResent(t *testing.T) {
	p := New()
	p.runDevicectl = func(ctx context.Context, args ...string) error {
		t.Errorf("devicectl was run for a phone that already has its profile: %v", args)
		return nil
	}
	target := publishingTarget(func(name, _ string, _ []byte) { t.Errorf("profile %s was published again", name) })
	target.Installed = true
	steps, err := p.SetProxy(t.Context(), phone(), target)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range steps {
		if s.Manual {
			t.Errorf("a phone with its profile was given homework: %+v", s)
		}
	}
	if got := taps(steps); !strings.Contains(got, "192.168.1.20:9090") || !strings.Contains(got, "Home") {
		t.Errorf("the step does not say where the phone's traffic goes:\n%s", got)
	}
}

func TestOpenSettingsLaunchesPreferencesOnAPhone(t *testing.T) {
	var launched string
	p := New()
	p.runDevicectl = func(ctx context.Context, args ...string) error {
		launched = strings.Join(args, " ")
		return nil
	}
	if err := p.OpenSettings(t.Context(), phone()); err != nil {
		t.Fatal(err)
	}
	if launched != "device process launch --device x com.apple.Preferences" {
		t.Errorf("devicectl call = %q", launched)
	}
}
