package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/siner308/sims/internal/device"
	"github.com/siner308/sims/internal/device/devicetest"
	"github.com/siner308/sims/internal/sims"
	"github.com/siner308/sims/skills"
)

var (
	pixel = device.Device{ID: "Pixel_7", Name: "Pixel_7", Platform: device.PlatformAndroid, Kind: device.KindVirtual, Transport: device.TransportAVD, Runtime: "API 36", State: device.StateShutdown, LastActiveAt: time.Now().Add(-time.Hour)}
	phone = device.Device{ID: "Pixel_7_phys", Name: "Pixel 7", Model: "Pixel 7", Platform: device.PlatformAndroid, Kind: device.KindPhysical, Transport: device.TransportUSB, State: device.StateConnected, Serial: "R3CT30ABCDE"}
	sim   = device.Device{ID: "UDID-1", Name: "iPhone 17", Platform: device.PlatformIOS, Kind: device.KindVirtual, Transport: device.TransportSim, Runtime: "iOS 26.5", State: device.StateBooted, LastActiveAt: time.Now()}
	fresh = device.Device{ID: "UDID-2", Name: "iPhone 16", Platform: device.PlatformIOS, Kind: device.KindVirtual, Transport: device.TransportSim, State: device.StateShutdown}
)

type fixture struct {
	android *devicetest.Android
	ios     *devicetest.IOS
	out     bytes.Buffer
	err     bytes.Buffer
	tui     int
}

func newFixture() *fixture {
	f := &fixture{android: devicetest.NewAndroid(pixel, phone), ios: devicetest.NewIOS(sim, fresh)}
	f.android.AppList = []device.App{
		{BundleID: "com.example.app", Name: "Example", Version: "1.2", Source: "adb"},
		{BundleID: "com.android.settings", Name: "Settings", System: true, Source: "preinstalled"},
	}
	f.android.ImageList = []device.Image{
		{ID: "system-images;android-36;google_apis;arm64-v8a", Name: "google_apis arm64-v8a", Version: "36", Installed: true},
		{ID: "system-images;android-35;google_apis;arm64-v8a", Name: "google_apis arm64-v8a", Version: "35"},
	}
	f.android.Types = []device.DeviceType{{ID: "pixel_7", Name: "Pixel 7", Screen: "1080x2400"}, {ID: "tv_1080p", Name: "Television"}}
	f.android.HW = device.Hardware{RAMMB: 2048, Cores: 4, DiskGB: 6}
	f.ios.ImageList = []device.Image{{ID: "com.apple.CoreSimulator.SimRuntime.iOS-26-5", Name: "iOS 26.5", Version: "26.5", Installed: true, OS: "iOS"}}
	f.ios.Types = []device.DeviceType{
		{ID: "iPhone-17-Pro", Name: "iPhone 17 Pro", Screen: "1206x2622 @3x", MinRuntime: "26.0", MaxRuntime: "65535.255.255", Family: "iPhone"},
		{ID: "Apple-TV", Name: "Apple TV", Family: "Apple TV"},
	}
	return f
}

func (f *fixture) run(t *testing.T, args ...string) int {
	t.Helper()
	f.out.Reset()
	f.err.Reset()
	return Execute(t.Context(), Options{
		Version: "v9.9.9",
		Manager: sims.New(f.android, f.ios),
		RunTUI:  func(context.Context) error { f.tui++; return nil },
		Update: func(_ context.Context, checkOnly bool, w io.Writer) error {
			io.WriteString(w, "update checkOnly=")
			if checkOnly {
				io.WriteString(w, "true\n")
			} else {
				io.WriteString(w, "false\n")
			}
			return nil
		},
		Out:  &f.out,
		Err:  &f.err,
		Args: append([]string{}, args...),
	})
}

func (f *fixture) ok(t *testing.T, args ...string) string {
	t.Helper()
	if code := f.run(t, args...); code != 0 {
		t.Fatalf("sims %s exited %d: %s", strings.Join(args, " "), code, f.err.String())
	}
	return f.out.String()
}

func (f *fixture) fails(t *testing.T, want string, args ...string) string {
	t.Helper()
	if code := f.run(t, args...); code == 0 {
		t.Fatalf("sims %s should fail; stdout %q", strings.Join(args, " "), f.out.String())
	}
	if !strings.Contains(f.err.String(), want) {
		t.Fatalf("sims %s stderr = %q, want %q", strings.Join(args, " "), f.err.String(), want)
	}
	return f.err.String()
}

func decode[T any](t *testing.T, s string) T {
	t.Helper()
	var v T
	if err := json.Unmarshal([]byte(s), &v); err != nil {
		t.Fatalf("bad json %q: %v", s, err)
	}
	return v
}

func TestBareInvocationOpensTheTUI(t *testing.T) {
	f := newFixture()
	f.ok(t)
	if f.tui != 1 {
		t.Fatalf("RunTUI called %d times", f.tui)
	}
	f.ok(t, "device", "list")
	if f.tui != 1 {
		t.Fatal("a subcommand must not open the TUI")
	}
}

func TestVersion(t *testing.T) {
	f := newFixture()
	if out := f.ok(t, "--version"); out != "sims v9.9.9\n" {
		t.Fatalf("--version printed %q", out)
	}
	if out := f.ok(t, "-v"); out != "sims v9.9.9\n" {
		t.Fatalf("-v printed %q", out)
	}
}

func TestDeviceList(t *testing.T) {
	f := newFixture()
	out := f.ok(t, "device", "list")
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 4 || !strings.HasPrefix(lines[0], "PLATFORM") {
		t.Fatalf("table:\n%s", out)
	}
	if !strings.HasPrefix(lines[1], "ios") || !strings.Contains(lines[1], "iPhone 17") {
		t.Fatalf("the booted device should lead:\n%s", out)
	}
	if strings.Contains(out, "iPhone 16") || !strings.Contains(f.err.String(), "1 never-booted simulators hidden") {
		t.Fatalf("never-booted sims should be hidden with a note; out %q err %q", out, f.err.String())
	}

	all := decode[[]device.Device](t, f.ok(t, "device", "list", "--all", "--json"))
	if len(all) != 4 || all[0].ID != "UDID-1" {
		t.Fatalf("--all --json = %+v", all)
	}
	android := decode[[]device.Device](t, f.ok(t, "device", "ls", "-p", "android", "--json"))
	if len(android) != 2 {
		t.Fatalf("-p android = %+v", android)
	}
}

func TestDeviceList_PartialFailureStillPrints(t *testing.T) {
	f := newFixture()
	f.ios.ListErr = errors.New("simctl timed out")
	out := f.ok(t, "device", "list")
	if !strings.Contains(out, "Pixel_7") || !strings.Contains(f.err.String(), "ios: simctl timed out") {
		t.Fatalf("out %q err %q", out, f.err.String())
	}
}

func TestDeviceGetAndResolution(t *testing.T) {
	f := newFixture()
	d := decode[device.Device](t, f.ok(t, "device", "get", "R3CT30ABCDE", "--json"))
	if d.ID != "Pixel_7_phys" {
		t.Fatalf("by serial = %+v", d)
	}
	if out := f.ok(t, "device", "get", "pixel 7"); !strings.Contains(out, "Pixel_7_phys") {
		t.Fatalf("by name:\n%s", out)
	}
	f.fails(t, `no device matches "nope"`, "device", "get", "nope")
	if strings.Contains(f.err.String(), "--help") {
		t.Fatalf("a runtime error should not print a usage hint: %q", f.err.String())
	}
}

func TestUsageErrorsPointAtHelp(t *testing.T) {
	f := newFixture()
	f.fails(t, "see 'sims device boot --help'", "device", "boot")
	f.fails(t, `--platform must be android, ios or desktop, not "windows"`, "device", "list", "-p", "windows")
	f.fails(t, "unknown command", "devices", "defenestrate")
}

func TestDeviceBootWait(t *testing.T) {
	f := newFixture()
	if out := f.ok(t, "device", "boot", "Pixel_7"); out != "booting Pixel_7\n" || !f.android.Called("Boot Pixel_7") {
		t.Fatalf("out %q calls %v", out, f.android.Calls)
	}
	f = newFixture()
	d := decode[device.Device](t, f.ok(t, "device", "boot", "Pixel_7", "--wait", "--json"))
	if d.State != device.StateBooted || d.Serial != "emulator-5554" {
		t.Fatalf("--wait --json should print the booted record, got %+v", d)
	}
	f.fails(t, "did not finish booting", "device", "wait", "iPhone 16", "--timeout", "20ms")
}

func TestDeviceActions(t *testing.T) {
	f := newFixture()
	f.ok(t, "device", "shutdown", "UDID-1")
	f.ok(t, "device", "erase", "Pixel_7")
	f.ok(t, "device", "delete", "Pixel_7")
	for _, want := range []string{"Shutdown UDID-1", "Erase Pixel_7", "Delete Pixel_7"} {
		if !f.ios.Called(want) && !f.android.Called(want) {
			t.Errorf("missing call %q; android %v ios %v", want, f.android.Calls, f.ios.Calls)
		}
	}
}

func TestDeviceKey(t *testing.T) {
	f := newFixture()
	f.ok(t, "device", "key", "Pixel_7", "home")
	if !f.android.Called("SendKey Pixel_7 home") {
		t.Fatalf("calls %v", f.android.Calls)
	}
	f.fails(t, "key must be home, back or overview", "device", "key", "Pixel_7", "menu")
	f.fails(t, "ios cannot send home", "device", "key", "UDID-1", "home")
}

func TestDeviceCreate(t *testing.T) {
	f := newFixture()
	d := decode[device.Device](t, f.ok(t, "device", "create", "Test_36", "--image", "system-images;android-36;google_apis;arm64-v8a", "--ram", "4096", "--json"))
	if d.Name != "Test_36" || d.Platform != device.PlatformAndroid {
		t.Fatalf("created %+v", d)
	}
	if !f.android.Called("Create Test_36 system-images;android-36;google_apis;arm64-v8a pixel_7 &{4096 0 0}") {
		t.Fatalf("the default type should be pixel_7 and the hardware passed through; calls %v", f.android.Calls)
	}

	out := f.ok(t, "device", "create", "Sim", "--image", "iOS 26.5", "--type", "iphone 17 pro", "--boot", "--timeout", "2s")
	if !strings.Contains(out, "created and booted Sim") || !f.ios.Called("Create Sim com.apple.CoreSimulator.SimRuntime.iOS-26-5 iPhone-17-Pro <nil>") {
		t.Fatalf("out %q calls %v", out, f.ios.Calls)
	}

	f.fails(t, "is not installed", "device", "create", "Old", "--image", "system-images;android-35;google_apis;arm64-v8a")
	f.fails(t, "take no --ram", "device", "create", "Sim2", "--image", "iOS 26.5", "--ram", "1")
	f.fails(t, `no device type matches "Apple TV" among the types that run iOS 26.5`, "device", "create", "TV", "--image", "iOS 26.5", "--type", "Apple TV")
	f.fails(t, `required flag(s) "image" not set`, "device", "create", "X")
}

func TestDeviceHardware(t *testing.T) {
	f := newFixture()
	hw := decode[device.Hardware](t, f.ok(t, "device", "hardware", "Pixel_7", "--json"))
	if hw.RAMMB != 2048 {
		t.Fatalf("read %+v", hw)
	}
	out := f.ok(t, "device", "hardware", "Pixel_7", "--ram", "4096")
	if !strings.Contains(out, "4096") || !f.android.Called("SetHardware Pixel_7 {RAMMB:4096 Cores:0 DiskGB:0}") {
		t.Fatalf("out %q calls %v", out, f.android.Calls)
	}
	f.fails(t, "no editable hardware", "device", "hardware", "UDID-1")
}

func TestDeviceConnectPairDisconnect(t *testing.T) {
	f := newFixture()
	if out := f.ok(t, "device", "connect", "Pixel 7"); !strings.Contains(out, "192.168.0.5:5555") {
		t.Fatalf("android connect should report the wifi address: %q", out)
	}
	f.ok(t, "device", "connect", "10.0.0.9:5555")
	f.ok(t, "device", "pair", "10.0.0.9:37099", "123456")
	f.ok(t, "device", "disconnect", "Pixel 7")
	for _, want := range []string{"EnableWireless Pixel_7_phys", "Connect 10.0.0.9:5555", "Pair 10.0.0.9:37099 123456", "Disconnect Pixel_7_phys"} {
		if !f.android.Called(want) {
			t.Errorf("missing %q in %v", want, f.android.Calls)
		}
	}
	f.ok(t, "device", "pair", "iPhone 17")
	if !f.ios.Called("PairDevice UDID-1") {
		t.Fatalf("ios calls %v", f.ios.Calls)
	}
	f.fails(t, "pairs by address", "device", "pair", "Pixel 7")
	f.fails(t, "no wireless connection to drop", "device", "disconnect", "iPhone 17")
}

func TestAppCommands(t *testing.T) {
	f := newFixture()
	out := f.ok(t, "app", "list", "Pixel 7")
	if !strings.Contains(out, "com.example.app") || strings.Contains(out, "com.android.settings") {
		t.Fatalf("preinstalled apps should be hidden by default:\n%s", out)
	}
	apps := decode[[]device.App](t, f.ok(t, "app", "list", "Pixel 7", "--all", "--json"))
	if len(apps) != 2 {
		t.Fatalf("--all = %+v", apps)
	}
	f.ok(t, "app", "install", "Pixel 7", "/tmp/a.apk")
	f.ok(t, "app", "launch", "Pixel 7", "com.example.app")
	f.ok(t, "app", "uninstall", "Pixel 7", "com.example.app")
	for _, want := range []string{"InstallApp Pixel_7_phys /tmp/a.apk", "LaunchApp Pixel_7_phys com.example.app", "UninstallApp Pixel_7_phys com.example.app"} {
		if !f.android.Called(want) {
			t.Errorf("missing %q in %v", want, f.android.Calls)
		}
	}
}

func TestLogs(t *testing.T) {
	f := newFixture()
	f.fails(t, "device is not running", "device", "logs", "Pixel 7")
	f.fails(t, `no app "nope"`, "app", "logs", "Pixel 7", "nope")
	f.android.LogCommand = echoCmd(t, "line one")
	if out := f.ok(t, "app", "logs", "Pixel 7", "example"); out != "line one\n" || !f.android.Called("LogCmd Pixel_7_phys com.example.app") {
		t.Fatalf("out %q calls %v", out, f.android.Calls)
	}
}

func TestImageAndDeviceTypeCommands(t *testing.T) {
	f := newFixture()
	out := f.ok(t, "image", "list")
	if !strings.Contains(out, "android-36") || strings.Contains(out, "android-35") {
		t.Fatalf("installed only by default:\n%s", out)
	}
	images := decode[[]sims.PlatformImage](t, f.ok(t, "image", "list", "--all", "--json"))
	if len(images) != 3 || images[0].Platform != device.PlatformAndroid || images[2].Installed {
		t.Fatalf("--all --json = %+v", images)
	}
	f.ok(t, "image", "install", "system-images;android-35;google_apis;arm64-v8a")
	if !f.android.Called("InstallImage system-images;android-35") {
		t.Fatalf("calls %v", f.android.Calls)
	}
	if out := f.ok(t, "image", "install", "iOS 26.5"); !strings.Contains(out, "already installed") {
		t.Fatalf("out %q", out)
	}

	out = f.ok(t, "device-type", "list")
	if !strings.Contains(out, "pixel_7") || !strings.Contains(out, "Apple-TV") {
		t.Fatalf("every platform by default:\n%s", out)
	}
	out = f.ok(t, "device-type", "list", "--image", "iOS 26.5")
	if !strings.Contains(out, "iPhone-17-Pro") || strings.Contains(out, "Apple-TV") || strings.Contains(out, "pixel_7") {
		t.Fatalf("--image should keep only compatible types of that platform:\n%s", out)
	}
	if !strings.Contains(out, "26.0+") {
		t.Fatalf("runtime range column:\n%s", out)
	}
}

func TestDoctorAndUpdate(t *testing.T) {
	f := newFixture()
	out := f.ok(t, "doctor")
	if !strings.HasPrefix(out, "sims v9.9.9\n") || !strings.Contains(out, "android-tool") {
		t.Fatalf("doctor:\n%s", out)
	}
	f.ios.AvailableErr = errors.New("no xcrun")
	f.android.AvailableErr = errors.New("no sdk")
	if code := f.run(t, "doctor"); code != 1 || !strings.Contains(f.out.String(), "no usable platform") || f.err.Len() != 0 {
		t.Fatalf("doctor with nothing usable: code %d out %q err %q", code, f.out.String(), f.err.String())
	}

	f = newFixture()
	if out := f.ok(t, "update", "--check"); out != "update checkOnly=true\n" {
		t.Fatalf("update --check printed %q", out)
	}
	if out := f.ok(t, "update"); out != "update checkOnly=false\n" {
		t.Fatalf("update printed %q", out)
	}
}

func echoCmd(t *testing.T, line string) *exec.Cmd {
	t.Helper()
	if _, err := exec.LookPath("echo"); err != nil {
		t.Skip("no echo on this machine")
	}
	return exec.Command("echo", line)
}

func TestAppList_EmptyOnAPhoneSaysWhy(t *testing.T) {
	f := newFixture()
	f.ios.Devices = append(f.ios.Devices, device.Device{
		ID: "PHONE-1", Name: "my iPhone", Platform: device.PlatformIOS,
		Kind: device.KindPhysical, Transport: device.TransportWiFi, State: device.StateConnected,
	})
	f.ios.AppList = nil
	if out := f.ok(t, "app", "list", "PHONE-1"); !strings.Contains(out, "BUNDLE ID") {
		t.Fatalf("stdout should still hold the header: %q", out)
	}
	if !strings.Contains(f.err.String(), "devicectl listed no apps on my iPhone") {
		t.Fatalf("stderr %q", f.err.String())
	}

	f = newFixture()
	if f.ok(t, "app", "list", "Pixel_7"); strings.Contains(f.err.String(), "devicectl") {
		t.Fatalf("an android device should not get the devicectl note: %q", f.err.String())
	}
}

func TestDeviceScreenshot(t *testing.T) {
	f := newFixture()
	dir := t.TempDir()
	path := filepath.Join(dir, "shot.png")
	if out := f.ok(t, "device", "screenshot", "Pixel_7", path); !strings.Contains(out, "wrote "+path) {
		t.Fatalf("screenshot printed %q", out)
	}
	got, err := os.ReadFile(path)
	if err != nil || !strings.HasSuffix(string(got), "Pixel_7") {
		t.Fatalf("saved file: %v %q", err, got)
	}
	if out := f.ok(t, "device", "screenshot", "UDID-1", "-"); !strings.HasSuffix(out, "UDID-1") {
		t.Fatalf("screenshot to stdout printed %q", out)
	}

	cwd, _ := os.Getwd()
	t.Chdir(dir)
	defer os.Chdir(cwd)
	out := f.ok(t, "device", "screenshot", "iPhone 17", "--json")
	var res struct {
		Path  string `json:"path"`
		Bytes int    `json:"bytes"`
	}
	if err := json.Unmarshal([]byte(out), &res); err != nil || res.Bytes == 0 {
		t.Fatalf("json screenshot: %v %s", err, out)
	}
	if strings.ContainsAny(res.Path, ` /\:`) || !strings.HasPrefix(res.Path, "iPhone_17-") {
		t.Fatalf("default name %q should be filename-safe", res.Path)
	}
	if _, err := os.Stat(filepath.Join(dir, res.Path)); err != nil {
		t.Fatalf("default-named file: %v", err)
	}

	f.android.ScreenshotErr = errors.New("device is not running")
	f.fails(t, "device is not running", "device", "screenshot", "Pixel_7", path)
}

func TestDeviceReboot(t *testing.T) {
	f := newFixture()
	if out := f.ok(t, "device", "reboot", "Pixel_7"); out != "rebooting Pixel_7\n" {
		t.Fatalf("reboot printed %q", out)
	}
	if !f.android.Called("reboot Pixel_7") {
		t.Fatalf("provider calls: %v", f.android.Calls)
	}
	f.android.SetState("Pixel_7", device.StateBooted, "emulator-5554")
	if out := f.ok(t, "device", "reboot", "Pixel_7", "--wait"); out != "rebooted Pixel_7\n" {
		t.Fatalf("reboot --wait printed %q", out)
	}
}

func TestSkillPrintsTheEmbeddedFile(t *testing.T) {
	f := newFixture()
	if out := f.ok(t, "skill"); out != skills.SimsCLI || !strings.HasPrefix(out, "---\nname: sims-cli\n") {
		t.Fatalf("skill printed %q", out[:min(len(out), 60)])
	}
}

func TestSkillInstall(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	f := newFixture()
	f.fails(t, "no agent found", "skill", "install")

	os.Mkdir(filepath.Join(home, ".claude"), 0o755)
	claude := filepath.Join(home, ".claude", "skills", "sims-cli", "SKILL.md")
	if out := f.ok(t, "skill", "install"); out != "Claude Code: written ("+claude+")\n" {
		t.Fatalf("first install printed %q", out)
	}
	if got, err := os.ReadFile(claude); err != nil || string(got) != skills.SimsCLI {
		t.Fatalf("installed copy: %v", err)
	}
	if out := f.ok(t, "skill", "install"); !strings.Contains(out, "up to date") {
		t.Fatalf("second install printed %q", out)
	}

	os.Mkdir(filepath.Join(home, ".agents"), 0o755)
	os.WriteFile(claude, []byte("stale"), 0o644)
	out := f.ok(t, "skill", "install", "--refresh", "--json")
	var results []map[string]string
	if err := json.Unmarshal([]byte(out), &results); err != nil || len(results) != 1 || results[0]["result"] != "written" {
		t.Fatalf("refresh: %v %s", err, out)
	}
	if _, err := os.Stat(filepath.Join(home, ".agents", "skills")); !os.IsNotExist(err) {
		t.Fatal("refresh created a copy for a new agent")
	}

	dir := t.TempDir()
	if out := f.ok(t, "skill", "install", "--dir", dir); !strings.HasPrefix(out, dir+": written (") {
		t.Fatalf("--dir printed %q", out)
	}
}
