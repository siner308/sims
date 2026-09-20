package android_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/siner308/sims/internal/device"

	"github.com/siner308/sims/internal/device/android"
	"github.com/siner308/sims/internal/proxy"
)

// Android finds a root certificate by the OpenSSL subject hash; a file under any other name is
// ignored without a word, so the name this package builds must match what openssl prints.
func TestCertNameMatchesOpenSSL(t *testing.T) {
	openssl, err := exec.LookPath("openssl")
	if err != nil {
		t.Skip("openssl not on PATH")
	}
	ca, err := proxy.LoadOrCreateCA(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "ca.pem")
	if err := os.WriteFile(path, ca.CertPEM(), 0o600); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command(openssl, "x509", "-subject_hash_old", "-in", path, "-noout").Output()
	if err != nil {
		t.Fatal(err)
	}
	want := strings.TrimSpace(string(out)) + ".0"
	got, err := android.CertFileName(ca.CertPEM())
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Errorf("cert file name = %q, openssl says %q", got, want)
	}
}

// The proxy setting is written and cleared with the exact strings Android expects. ":0" is how
// Android spells "no proxy"; an empty value leaves the previous one in place, which would strand a
// device on a port that has stopped listening.
func TestClearUsesAndroidsNoProxySpelling(t *testing.T) {
	adb := fakeADB(t)
	p := android.NewWithADB(adb.path)
	d := device.Device{ID: "avd", Serial: "emulator-5554", Kind: device.KindVirtual, Platform: device.PlatformAndroid}

	if err := p.ClearProxy(t.Context(), d); err != nil {
		t.Fatal(err)
	}
	got := adb.calls(t)
	want := "-s emulator-5554 shell settings put global http_proxy :0"
	if len(got) == 0 || got[0] != want {
		t.Errorf("adb calls = %q, want the first to be %q", got, want)
	}
	// the write is read back, since settings put reports its failures on stdout and exits 0
	if len(got) < 2 || !strings.Contains(got[1], "settings get global http_proxy") {
		t.Errorf("the cleared setting was not read back: %q", got)
	}
}

func TestSetProxyWritesHostAndPort(t *testing.T) {
	adb := fakeADB(t)
	p := android.NewWithADB(adb.path)
	d := device.Device{ID: "avd", Serial: "emulator-5554", Kind: device.KindVirtual, Platform: device.PlatformAndroid}

	if _, err := p.SetProxy(t.Context(), d, device.ProxyTarget{Host: "10.0.2.2", Port: 9090}); err != nil {
		t.Fatal(err)
	}
	got := adb.calls(t)
	want := "-s emulator-5554 shell settings put global http_proxy 10.0.2.2:9090"
	if len(got) == 0 || got[0] != want {
		t.Errorf("adb calls = %q, want the first to be %q", got, want)
	}
	if len(got) < 2 || !strings.Contains(got[1], "settings get global http_proxy") {
		t.Errorf("the setting was not read back: %q", got)
	}
}

// A device with no serial is not running; writing a proxy setting to it would silently target
// whatever adb picks instead.
func TestProxyNeedsARunningDevice(t *testing.T) {
	p := android.NewWithADB(fakeADB(t).path)
	d := device.Device{ID: "avd", Kind: device.KindVirtual, Platform: device.PlatformAndroid}
	if _, err := p.SetProxy(t.Context(), d, device.ProxyTarget{Host: "10.0.2.2", Port: 1}); err == nil {
		t.Error("SetProxy accepted a device that is not running")
	}
	if err := p.ClearProxy(t.Context(), d); err == nil {
		t.Error("ClearProxy accepted a device that is not running")
	}
}

type adbRecorder struct {
	path string
	log  string
}

// fakeADB writes a stub adb that appends its arguments to a file, so the exact command line sims
// builds can be asserted without a device.
func fakeADB(t *testing.T) adbRecorder {
	t.Helper()
	dir := t.TempDir()
	log := filepath.Join(dir, "calls")
	script := filepath.Join(dir, "adb")
	// the stub remembers what the last put wrote, so the read-back sims does finds it
	value := filepath.Join(dir, "value")
	body := "#!/bin/sh\n" +
		"echo \"$@\" >> " + log + "\n" +
		"if [ \"$4\" = settings ] && [ \"$5\" = put ]; then printf '%s' \"$8\" > " + value + "; fi\n" +
		"if [ \"$4\" = settings ] && [ \"$5\" = get ]; then cat " + value + " 2>/dev/null; echo; fi\n"
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	return adbRecorder{path: script, log: log}
}

func (r adbRecorder) calls(t *testing.T) []string {
	t.Helper()
	b, err := os.ReadFile(r.log)
	if err != nil {
		return nil
	}
	var out []string
	for _, l := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		if l != "" {
			out = append(out, l)
		}
	}
	return out
}
