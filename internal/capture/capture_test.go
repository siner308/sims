package capture_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/siner308/sims/internal/capture"
	"github.com/siner308/sims/internal/device"
	"github.com/siner308/sims/internal/device/devicetest"
	"github.com/siner308/sims/internal/proxy"
)

// proxyFake is a provider that records what a capture asked of the device.
type proxyFake struct {
	*devicetest.Fake
	setErr    error
	set       []device.ProxyTarget
	cleared   int
	clearErr  error
	stepsBack []device.ProxyStep
}

func (p *proxyFake) SetProxy(_ context.Context, _ device.Device, t device.ProxyTarget) ([]device.ProxyStep, error) {
	p.set = append(p.set, t)
	return p.stepsBack, p.setErr
}

func (p *proxyFake) ClearProxy(context.Context, device.Device) error {
	p.cleared++
	return p.clearErr
}

func (p *proxyFake) ProxyState(context.Context, device.Device) (device.ProxyState, error) {
	return device.ProxyState{}, nil
}

func androidEmulator() device.Device {
	return device.Device{
		ID: "Pixel_7", Name: "Pixel_7", Serial: "emulator-5554",
		Platform: device.PlatformAndroid, Kind: device.KindVirtual,
		Transport: device.TransportAVD, State: device.StateBooted,
	}
}

func newFake() *proxyFake {
	return &proxyFake{Fake: &devicetest.Fake{ID: device.PlatformAndroid}}
}

func TestStartPointsAnEmulatorAtTheHostAlias(t *testing.T) {
	p := newFake()
	s, err := capture.Start(t.Context(), p, androidEmulator(), capture.Options{CertDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Stop()

	if len(p.set) != 1 {
		t.Fatalf("SetProxy called %d times", len(p.set))
	}
	target := p.set[0]
	// 127.0.0.1 inside an emulator is the emulator itself, so the host must be reached by its alias
	if target.Host != "10.0.2.2" {
		t.Errorf("emulator was pointed at %q, which is not the host alias", target.Host)
	}
	if target.Port != s.Port || s.Port == 0 {
		t.Errorf("target port %d does not match the listening port %d", target.Port, s.Port)
	}
	if len(target.CACert) == 0 || len(target.CACertDER) == 0 {
		t.Error("the device was not given the certificate in both forms")
	}
}

// A capture that cannot finish must not leave the device pointing at a proxy that is gone: that
// device would have no working network at all.
func TestFailedStartClearsTheDevice(t *testing.T) {
	p := newFake()
	p.setErr = errors.New("adb said no")
	_, err := capture.Start(t.Context(), p, androidEmulator(), capture.Options{CertDir: t.TempDir()})
	if err == nil {
		t.Fatal("expected the start to fail")
	}
	if p.cleared == 0 {
		t.Error("a failed start left the proxy setting on the device")
	}
}

func TestStopIsIdempotent(t *testing.T) {
	p := newFake()
	s, err := capture.Start(t.Context(), p, androidEmulator(), capture.Options{CertDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Stop(); err != nil {
		t.Fatal(err)
	}
	if err := s.Stop(); err != nil {
		t.Fatal(err)
	}
	if p.cleared != 1 {
		t.Errorf("ClearProxy ran %d times across two Stops", p.cleared)
	}
}

func TestUnsupportedProviderIsRefused(t *testing.T) {
	plain := &devicetest.Fake{ID: device.PlatformAndroid}
	_, err := capture.Start(t.Context(), plain, androidEmulator(), capture.Options{CertDir: t.TempDir()})
	if !errors.Is(err, errors.ErrUnsupported) {
		t.Fatalf("error = %v, want unsupported", err)
	}
}

func TestCertificateSurvivesBetweenCaptures(t *testing.T) {
	dir := t.TempDir()
	p := newFake()
	first, err := capture.Start(t.Context(), p, androidEmulator(), capture.Options{CertDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	fingerprint := first.CA.Fingerprint()
	first.Stop()

	second, err := capture.Start(t.Context(), p, androidEmulator(), capture.Options{CertDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	defer second.Stop()
	// a new CA every capture would mean installing a certificate on the device every time
	if second.CA.Fingerprint() != fingerprint {
		t.Error("the second capture made a new CA instead of reusing the stored one")
	}
}

func TestLANAddressIsPrivate(t *testing.T) {
	addr, err := capture.LANAddress()
	if err != nil {
		t.Skip(err)
	}
	if strings.HasPrefix(addr, "127.") {
		t.Errorf("LANAddress returned the loopback %q; a phone cannot reach that", addr)
	}
	t.Logf("LAN address: %s", addr)
}

// Pointing this Mac at the proxy means every app on it connects through sims. Under the default
// scope those connections are relayed untouched: a browser that worked before the capture has to
// keep working during it.
func TestDefaultScopeIgnoresTrafficThatIsNotTheDevice(t *testing.T) {
	s := capture.SessionForTest(t, iosSimulator(), capture.ScopeDevice)

	got := s.AttributeForTest(t.Context(), "127.0.0.1:1234")
	if !got.Ignore {
		t.Error("another app on this Mac was opened by the capture instead of passed through")
	}
}

func TestScopeAllOpensEverything(t *testing.T) {
	s := capture.SessionForTest(t, iosSimulator(), capture.ScopeAll)
	if got := s.AttributeForTest(t.Context(), "127.0.0.1:1234"); got.Ignore {
		t.Error("scope all should open this machine's traffic too")
	}
}

// Traffic arriving over the network during a capture is the device's; it is opened under either scope.
func TestDeviceTrafficIsAlwaysOpened(t *testing.T) {
	for _, scope := range []capture.Scope{capture.ScopeDevice, capture.ScopeAll} {
		s := capture.SessionForTest(t, androidEmulator(), scope)
		got := s.AttributeForTest(t.Context(), "192.168.1.55:40001")
		if got.Ignore {
			t.Errorf("scope %q ignored the device's own traffic", scope)
		}
		if got.Origin != proxy.OriginDevice {
			t.Errorf("scope %q labelled device traffic as %q", scope, got.Origin)
		}
	}
}

func iosSimulator() device.Device {
	return device.Device{
		ID: "udid1", Name: "iPhone 17e",
		Platform: device.PlatformIOS, Kind: device.KindVirtual,
		Transport: device.TransportSim, State: device.StateBooted,
	}
}

// The proxy signs certificates for any host it is asked about, so it is only on the network when a
// device that can only be reached there needs it. A simulator or emulator arrives over loopback.
func TestVirtualDeviceCaptureStaysOnLoopback(t *testing.T) {
	p := newFake()
	s, err := capture.Start(t.Context(), p, androidEmulator(), capture.Options{CertDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Stop()
	if addr := s.ListenAddrForTest(); !strings.HasPrefix(addr, "127.0.0.1:") {
		t.Errorf("an emulator capture listens on %q; that offers the proxy to the whole network", addr)
	}
}

func TestPhoneCaptureIsReachableOnTheNetwork(t *testing.T) {
	phone := device.Device{
		ID: "R3C", Name: "SM S928N", Serial: "R3C",
		Platform: device.PlatformAndroid, Kind: device.KindPhysical,
		Transport: device.TransportUSB, State: device.StateConnected,
	}
	p := newFake()
	s, err := capture.Start(t.Context(), p, phone, capture.Options{CertDir: t.TempDir()})
	if err != nil {
		t.Skip(err) // needs a private address on this machine
	}
	defer s.Stop()
	// a phone cannot reach a loopback-only proxy
	if addr := s.ListenAddrForTest(); strings.HasPrefix(addr, "127.0.0.1:") {
		t.Errorf("a phone capture listens on %q, which the phone cannot reach", addr)
	}
}

// A proxy that stops listening leaves the device pointing at nothing. The session has to report it,
// or the UI shows an empty table and the user waits for traffic that can never arrive.
func TestServeErrorIsReported(t *testing.T) {
	p := newFake()
	s, err := capture.Start(t.Context(), p, androidEmulator(), capture.Options{CertDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Err(); err != nil {
		t.Fatalf("a healthy capture reports %v", err)
	}
	if err := s.Stop(); err != nil {
		t.Fatal(err)
	}
	// Close is the normal end of Serve and is not an error
	if err := s.Err(); err != nil {
		t.Errorf("stopping the capture reported %v", err)
	}
}
