package android_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/siner308/sims/internal/device"
	"github.com/siner308/sims/internal/device/android"
	"github.com/siner308/sims/internal/proxy"
)

// stubADB writes a fake adb that records its arguments and answers the reads sims makes.
func stubADB(t *testing.T, script string) (path string, calls func() []string) {
	t.Helper()
	dir := t.TempDir()
	log := filepath.Join(dir, "calls")
	bin := filepath.Join(dir, "adb")
	body := "#!/bin/sh\necho \"$@\" >> " + log + "\n" + script
	if err := os.WriteFile(bin, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	return bin, func() []string {
		b, err := os.ReadFile(log)
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
}

func testDevice() device.Device {
	return device.Device{ID: "avd", Name: "Pixel_7", Serial: "emulator-5554", Kind: device.KindVirtual, Platform: device.PlatformAndroid}
}

func testTarget(t *testing.T) device.ProxyTarget {
	t.Helper()
	ca, err := proxy.LoadOrCreateCA(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return device.ProxyTarget{Host: "10.0.2.2", Port: 9090, CACert: ca.CertPEM(), CertName: "sims proxy CA"}
}

// `settings put` exits 0 and prints the failure on stdout on a device that will not let the shell
// write global settings. Reporting success there gives the user a capture that records nothing and
// no reason why.
func TestSetProxyFailsWhenTheDeviceRefusesTheSetting(t *testing.T) {
	// the put prints an exception and exits 0, exactly as a real locked-down device does
	bin, _ := stubADB(t, `case "$*" in
  *"settings put"*) echo "Exception occurred while executing 'put': java.lang.SecurityException"; exit 0 ;;
  *"settings get"*) echo "null"; exit 0 ;;
esac
exit 0`)
	p := android.NewWithADB(bin)

	_, err := p.SetProxy(t.Context(), testDevice(), testTarget(t))
	if err == nil {
		t.Fatal("a refused setting was reported as a configured device")
	}
	if !strings.Contains(err.Error(), "could not set the proxy") {
		t.Errorf("error = %q", err)
	}
}

// A device that accepts the write but does not keep it is the same failure with no error text to
// notice, so the value is read back.
func TestSetProxyFailsWhenTheSettingDoesNotStick(t *testing.T) {
	bin, _ := stubADB(t, `case "$*" in
  *"settings get"*) echo "null"; exit 0 ;;
esac
exit 0`)
	p := android.NewWithADB(bin)

	_, err := p.SetProxy(t.Context(), testDevice(), testTarget(t))
	if err == nil {
		t.Fatal("a setting that did not stick was reported as success")
	}
	if !strings.Contains(err.Error(), "reads back as") {
		t.Errorf("error = %q", err)
	}
}

// The certificate the user is told to install has to still be there, and somewhere Android's
// picker can reach: it browses shared storage, not /data/local/tmp.
func TestManualCertificateIsLeftWhereTheUserCanFindIt(t *testing.T) {
	// root fails, as it does on every production phone, so the manual path is taken
	bin, calls := stubADB(t, `case "$*" in
  *"settings get"*) echo "10.0.2.2:9090"; exit 0 ;;
  *" root"*) echo "adbd cannot run as root in production builds"; exit 1 ;;
esac
exit 0`)
	p := android.NewWithADB(bin)

	steps, err := p.SetProxy(t.Context(), testDevice(), testTarget(t))
	if err != nil {
		t.Fatal(err)
	}
	var instruction string
	for _, s := range steps {
		if s.Title == "certificate" && s.Manual {
			instruction = s.Detail
		}
	}
	if instruction == "" {
		t.Fatalf("no manual certificate step: %+v", steps)
	}
	if strings.Contains(instruction, "/data/local/tmp") {
		t.Errorf("the user is sent to a path the certificate picker cannot browse: %q", instruction)
	}

	// the file named in the instruction must not be one the code deleted
	var pushedVisible, deletedVisible bool
	for _, c := range calls() {
		if strings.Contains(c, "push") && strings.Contains(c, "/sdcard/Download/") {
			pushedVisible = true
		}
		if strings.Contains(c, "rm -f") && strings.Contains(c, "/sdcard/Download/") {
			deletedVisible = true
		}
	}
	if !pushedVisible {
		t.Errorf("the certificate was never put anywhere the user can reach: %v", calls())
	}
	if deletedVisible {
		t.Error("the certificate the instruction names was deleted again")
	}
}
