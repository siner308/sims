package doctor_test

import (
	"bytes"
	"context"
	"os/exec"
	"strings"
	"testing"

	"github.com/siner308/sims/internal/device"
	"github.com/siner308/sims/internal/doctor"
)

type stub struct {
	platform device.Platform
	checks   []device.Check
}

func (s stub) Platform() device.Platform                                 { return s.platform }
func (s stub) Checks(context.Context) []device.Check                     { return s.checks }
func (s stub) Available() error                                          { return nil }
func (s stub) List(context.Context) ([]device.Device, error)             { return nil, nil }
func (s stub) Boot(context.Context, device.Device) error                 { return nil }
func (s stub) Shutdown(context.Context, device.Device) error             { return nil }
func (s stub) Erase(context.Context, device.Device) error                { return nil }
func (s stub) Delete(context.Context, device.Device) error               { return nil }
func (s stub) Apps(context.Context, device.Device) ([]device.App, error) { return nil, nil }
func (s stub) InstallApp(context.Context, device.Device, string) error   { return nil }
func (s stub) UninstallApp(context.Context, device.Device, string) error { return nil }
func (s stub) LaunchApp(context.Context, device.Device, string) error    { return nil }
func (s stub) LogCmd(context.Context, device.Device, *device.App) (*exec.Cmd, error) {
	return nil, nil
}
func (s stub) Images(context.Context) ([]device.Image, error)   { return nil, nil }
func (s stub) InstallImage(context.Context, device.Image) error { return nil }
func (s stub) Create(context.Context, string, device.Image, string, *device.Hardware) error {
	return nil
}
func (s stub) DeviceTypes(context.Context) ([]device.DeviceType, error) { return nil, nil }

func TestRun_ReportsMissingAndOptional(t *testing.T) {
	var out bytes.Buffer
	ok := doctor.Run(t.Context(), &out, stub{platform: device.PlatformAndroid, checks: []device.Check{
		{Name: "adb", OK: true, Found: "/sdk/platform-tools/adb"},
		{Name: "avdmanager", OK: false, Hint: "install cmdline-tools"},
		{Name: "aapt2", OK: false, Optional: true, Hint: "sdkmanager build-tools"},
	}})
	text := out.String()
	for _, want := range []string{"android", "ok       adb", "MISSING  avdmanager     install cmdline-tools", "skip     aapt2          optional: sdkmanager build-tools"} {
		if !strings.Contains(text, want) {
			t.Errorf("missing %q in:\n%s", want, text)
		}
	}
	if ok {
		t.Error("a required tool is missing, so the platform must not count as usable")
	}
}

func TestRun_OptionalOnlyIsUsable(t *testing.T) {
	var out bytes.Buffer
	ok := doctor.Run(t.Context(), &out, stub{platform: device.PlatformIOS, checks: []device.Check{
		{Name: "xcrun", OK: true, Found: "/usr/bin/xcrun"},
		{Name: "idevicesyslog", OK: false, Optional: true, Hint: "brew install libimobiledevice"},
	}})
	if !ok || strings.Contains(out.String(), "no usable platform") {
		t.Errorf("optional misses must not make the platform unusable:\n%s", out.String())
	}
}

func TestRun_OnlyOptionalMissesIsNotAPlatform(t *testing.T) {
	var out bytes.Buffer
	ok := doctor.Run(t.Context(), &out, stub{platform: device.PlatformIOS, checks: []device.Check{
		{Name: "Xcode", OK: false, Optional: true, Hint: "iOS simulators and devices need macOS"},
	}})
	if ok || !strings.Contains(out.String(), "no usable platform") {
		t.Errorf("a platform with nothing present must not count as usable:\n%s", out.String())
	}
}
