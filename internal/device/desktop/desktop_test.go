package desktop_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/siner308/sims/internal/device"
	"github.com/siner308/sims/internal/device/desktop"
)

func provider(t *testing.T) *desktop.Provider {
	t.Helper()
	p := desktop.New()
	if err := p.Available(); err != nil {
		t.Skip(err)
	}
	return p
}

// The machine sims runs on shows up as a device so it can be selected like any other.
func TestListsThisMachine(t *testing.T) {
	p := provider(t)
	devices, err := p.List(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(devices) != 1 {
		t.Fatalf("listed %d machines, want 1", len(devices))
	}
	d := devices[0]
	if d.ID != desktop.ID {
		t.Errorf("id = %q", d.ID)
	}
	if !d.IsHost() {
		t.Error("this machine is not marked as the host")
	}
	if !d.Reachable() {
		t.Error("this machine reports itself unreachable")
	}
	if d.Name == "" || d.Runtime == "" {
		t.Errorf("thin record: %+v", d)
	}
}

// Everything a desktop cannot do has to say why, and say it as unsupported rather than as a failure
// the user could retry.
func TestUnsupportedActionsExplainThemselves(t *testing.T) {
	p := provider(t)
	d := device.Device{ID: desktop.ID, Kind: device.KindHost, Platform: device.PlatformDesktop}

	checks := map[string]error{
		"boot":      p.Boot(t.Context(), d),
		"shutdown":  p.Shutdown(t.Context(), d),
		"erase":     p.Erase(t.Context(), d),
		"delete":    p.Delete(t.Context(), d),
		"install":   p.InstallApp(t.Context(), d, "x"),
		"uninstall": p.UninstallApp(t.Context(), d, "x"),
		"launch":    p.LaunchApp(t.Context(), d, "x"),
	}
	for name, err := range checks {
		if err == nil {
			t.Errorf("%s was accepted on this machine", name)
			continue
		}
		if !errors.Is(err, errors.ErrUnsupported) {
			t.Errorf("%s: %v is not reported as unsupported", name, err)
		}
		if !strings.Contains(err.Error(), "machine sims") {
			t.Errorf("%s: %q does not say why", name, err)
		}
	}
	if _, err := p.Apps(t.Context(), d); err == nil {
		t.Error("apps were listed for this machine")
	}
}

// The log is one of the two things a desktop can actually do.
func TestHostLogCommandIsBuilt(t *testing.T) {
	p := provider(t)
	cmd, err := p.LogCmd(t.Context(), device.Device{ID: desktop.ID, Kind: device.KindHost}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if cmd == nil || cmd.Path == "" {
		t.Fatal("no log command")
	}
	t.Logf("log command: %s %v", cmd.Path, cmd.Args[1:])
}

// A capture of this machine needs its certificate trusted, and that cannot happen silently: macOS
// puts up an authorisation panel. The step has to tell the user rather than the TUI hanging on it.
func TestCaptureAsksForTrustInsteadOfHanging(t *testing.T) {
	p := provider(t)
	d := device.Device{ID: desktop.ID, Kind: device.KindHost, Platform: device.PlatformDesktop}
	steps, err := p.SetProxy(t.Context(), d, device.ProxyTarget{
		Host: "127.0.0.1", Port: 9090,
		CACert:   []byte("-----BEGIN CERTIFICATE-----\nnot a real one\n-----END CERTIFICATE-----\n"),
		CertName: "sims proxy CA",
	})
	if err != nil {
		t.Fatal(err)
	}
	var sawManualCert bool
	for _, s := range steps {
		if s.Title == "certificate" && s.Manual {
			sawManualCert = true
			if !strings.Contains(s.Detail, "proxy ca") {
				t.Errorf("the certificate step does not say how to trust it: %q", s.Detail)
			}
		}
	}
	if !sawManualCert {
		t.Errorf("an untrusted certificate produced no manual step: %+v", steps)
	}
}
