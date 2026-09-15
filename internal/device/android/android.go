package android

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/siner308/sims/internal/device"
)

type Provider struct {
	sdk    sdk
	sdkErr error
	labels *labelCache
}

func New() *Provider {
	s, err := locateSDK()
	return &Provider{sdk: s, sdkErr: err, labels: newLabelCache()}
}

func (p *Provider) Platform() device.Platform { return device.PlatformAndroid }

func (p *Provider) Available() error {
	if p.sdkErr != nil {
		return p.sdkErr
	}
	for _, bin := range []string{p.sdk.adb(), p.sdk.emulator()} {
		if _, err := os.Stat(bin); err != nil {
			return fmt.Errorf("missing %s", bin)
		}
	}
	return nil
}

func (p *Provider) Info(ctx context.Context) [][2]string {
	info := [][2]string{{"Android SDK", p.sdk.root}}
	if out, err := run(ctx, p.sdk.adb(), "--version"); err == nil {
		for _, l := range lines(out) {
			if v, ok := strings.CutPrefix(l, "Version "); ok {
				info = append(info, [2]string{"adb", strings.SplitN(v, "-", 2)[0]})
			}
		}
	}
	if out, err := run(ctx, p.sdk.emulator(), "-version"); err == nil {
		if l := lines(out); len(l) > 0 {
			if v, ok := strings.CutPrefix(l[0], "Android emulator version "); ok {
				info = append(info, [2]string{"emulator", strings.Fields(v)[0]})
			}
		}
	}
	return info
}

func (p *Provider) List(ctx context.Context) ([]device.Device, error) {
	out, err := run(ctx, p.sdk.emulator(), "-list-avds")
	if err != nil {
		return nil, err
	}
	adbOut, err := run(ctx, p.sdk.adb(), "devices", "-l")
	if err != nil {
		return nil, err
	}
	entries := parseADBDevices(adbOut)
	running := p.runningSerials(ctx, entries)

	var devices []device.Device
	for _, name := range lines(out) {
		if strings.HasPrefix(name, "INFO") || name == "" {
			continue
		}
		d := device.Device{
			ID:           name,
			Name:         name,
			Platform:     device.PlatformAndroid,
			Kind:         device.KindVirtual,
			Transport:    device.TransportAVD,
			Runtime:      runtimeOf(name),
			State:        device.StateShutdown,
			LastActiveAt: lastBootOf(name),
		}
		if serial, ok := running[name]; ok {
			d.State = device.StateBooted
			d.Serial = serial
		}
		devices = append(devices, d)
	}
	return append(devices, p.physicalDevices(ctx, entries)...), nil
}

func (p *Provider) runningSerials(ctx context.Context, entries []adbEntry) map[string]string {
	result := map[string]string{}
	for _, e := range entries {
		if !e.isEmulator() || e.state != "device" {
			continue
		}
		name, err := run(ctx, p.sdk.adb(), "-s", e.serial, "emu", "avd", "name")
		if err != nil {
			continue
		}
		if l := lines(name); len(l) > 0 {
			result[l[0]] = e.serial
		}
	}
	return result
}

// The emulator rewrites emu-launch-params.txt on every launch, which makes its mtime the last boot time.
func lastBootOf(avdName string) time.Time {
	fi, err := os.Stat(filepath.Join(avdHome(), avdName+".avd", "emu-launch-params.txt"))
	if err != nil {
		return time.Time{}
	}
	return fi.ModTime()
}

func runtimeOf(avdName string) string {
	ini, err := os.ReadFile(filepath.Join(avdHome(), avdName+".ini"))
	if err != nil {
		return ""
	}
	for _, line := range lines(string(ini)) {
		if v, ok := strings.CutPrefix(line, "target=android-"); ok {
			return "API " + v
		}
	}
	return ""
}

var errPhysical = errors.New("not available for a physical device")

// Existing AVDs made by avdmanager still carry hw.keyboard = no, so the defaults are applied on every boot.
func (p *Provider) Boot(ctx context.Context, d device.Device) error {
	if d.Kind == device.KindPhysical {
		return errPhysical
	}
	if err := setAVDConfig(d.ID); err != nil {
		return err
	}
	return start(p.sdk.emulator(), "-avd", d.ID)
}

func (p *Provider) Shutdown(ctx context.Context, d device.Device) error {
	if d.Kind == device.KindPhysical {
		return errPhysical
	}
	if d.Serial == "" {
		return errors.New("device is not running")
	}
	_, err := run(ctx, p.sdk.adb(), "-s", d.Serial, "emu", "kill")
	return err
}

// Erase relaunches with -wipe-data because the emulator only rebuilds userdata at boot.
func (p *Provider) Erase(ctx context.Context, d device.Device) error {
	if d.Kind == device.KindPhysical {
		return errPhysical
	}
	if d.Serial != "" {
		return errors.New("shut down the device before erasing")
	}
	return start(p.sdk.emulator(), "-avd", d.ID, "-wipe-data")
}

func (p *Provider) Delete(ctx context.Context, d device.Device) error {
	if d.Kind == device.KindPhysical {
		if d.Transport == device.TransportWiFi {
			_, err := run(ctx, p.sdk.adb(), "disconnect", d.Serial)
			return err
		}
		return errors.New("a USB device leaves the list when it is unplugged")
	}
	if d.Serial != "" {
		return errors.New("shut down the device before deleting")
	}
	_, err := run(ctx, p.sdk.avdmanager(), "delete", "avd", "-n", d.ID)
	return err
}

func (p *Provider) Apps(ctx context.Context, d device.Device) ([]device.App, error) {
	if d.Serial == "" {
		return nil, errors.New("device is not running")
	}
	all, err := p.packages(ctx, d.Serial, "-f", "-i")
	if err != nil {
		return nil, err
	}
	user, err := p.packages(ctx, d.Serial, "-3")
	if err != nil {
		return nil, err
	}
	apps := make([]device.App, 0, len(all))
	for pkg, e := range all {
		_, isUser := user[pkg]
		apps = append(apps, device.App{BundleID: pkg, Name: pkg, System: !isUser, Source: sourceOf(isUser, e.installer)})
	}
	p.enrichLabels(ctx, d.Serial, apps, all)
	return apps, nil
}

// Preinstalled apps stay as package names: pulling a few hundred system APKs would take minutes,
// while the handful the user installed is what they need to recognise.
func (p *Provider) enrichLabels(ctx context.Context, serial string, apps []device.App, all map[string]packageEntry) {
	var wg sync.WaitGroup
	sem := make(chan struct{}, 4)
	for i := range apps {
		if apps[i].System || all[apps[i].BundleID].apkPath == "" {
			continue
		}
		wg.Go(func() {
			sem <- struct{}{}
			defer func() { <-sem }()
			info, err := p.apkInfoFor(ctx, serial, all[apps[i].BundleID].apkPath)
			if err != nil {
				return
			}
			apps[i].Name = info.Label
			apps[i].Version = info.Version
		})
	}
	wg.Wait()
}

// pm reports installer=null for adb installs and for image apps alike, so the -3 membership decides between them.
func sourceOf(user bool, installer string) string {
	switch {
	case !user:
		return "preinstalled"
	case installer == "com.android.vending":
		return "store"
	case installer == "null", installer == "":
		return "adb"
	}
	return installer
}

type packageEntry struct {
	apkPath   string // set when -f was passed
	installer string // set when -i was passed
}

func (p *Provider) packages(ctx context.Context, serial string, flags ...string) (map[string]packageEntry, error) {
	args := append([]string{"-s", serial, "shell", "pm", "list", "packages"}, flags...)
	out, err := run(ctx, p.sdk.adb(), args...)
	if err != nil {
		return nil, err
	}
	pkgs := map[string]packageEntry{}
	for _, line := range lines(out) {
		rest, ok := strings.CutPrefix(line, "package:")
		if !ok {
			continue
		}
		apkPath, pkg, installer := splitPackageLine(rest)
		pkgs[pkg] = packageEntry{apkPath: apkPath, installer: installer}
	}
	return pkgs, nil
}

func (p *Provider) InstallApp(ctx context.Context, d device.Device, path string) error {
	_, err := run(ctx, p.sdk.adb(), "-s", d.Serial, "install", "-r", path)
	return err
}

func (p *Provider) UninstallApp(ctx context.Context, d device.Device, bundleID string) error {
	_, err := run(ctx, p.sdk.adb(), "-s", d.Serial, "uninstall", bundleID)
	return err
}

// monkey fails with exit 251 on emulators without physical keys, so the launcher activity is resolved and started directly.
func (p *Provider) LaunchApp(ctx context.Context, d device.Device, bundleID string) error {
	out, err := run(ctx, p.sdk.adb(), "-s", d.Serial, "shell", "cmd", "package", "resolve-activity", "--brief", "-c", "android.intent.category.LAUNCHER", bundleID)
	if err != nil {
		return err
	}
	l := lines(strings.TrimSpace(out))
	if len(l) == 0 || !strings.Contains(l[len(l)-1], "/") {
		return fmt.Errorf("%s has no launcher activity", bundleID)
	}
	component := l[len(l)-1]
	out, err = run(ctx, p.sdk.adb(), "-s", d.Serial, "shell", "am", "start", "-n", component)
	if err != nil {
		return err
	}
	for _, line := range lines(out) {
		if strings.HasPrefix(line, "Error") {
			return fmt.Errorf("am start %s: %s", component, line)
		}
	}
	return nil
}

// logcat has no package filter, only --pid, so the app must be running; a restart changes the pid and needs a fresh stream.
func (p *Provider) LogCmd(ctx context.Context, d device.Device, app *device.App) (*exec.Cmd, error) {
	if d.Serial == "" {
		return nil, errors.New("device is not running")
	}
	args := []string{"-s", d.Serial, "logcat", "-v", "time"}
	if app != nil {
		out, err := run(ctx, p.sdk.adb(), "-s", d.Serial, "shell", "pidof", "-s", app.BundleID)
		pid := strings.TrimSpace(out)
		if err != nil || pid == "" {
			return nil, fmt.Errorf("%s is not running; launch it first (enter) and open logs again", app.BundleID)
		}
		args = append(args, "--pid="+pid)
	}
	return exec.CommandContext(ctx, p.sdk.adb(), args...), nil
}

func (p *Provider) Images(ctx context.Context) ([]device.Image, error) {
	out, err := run(ctx, p.sdk.sdkmanager(), "--list")
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	var images []device.Image
	installed := false
	for _, line := range lines(out) {
		switch {
		case strings.HasPrefix(line, "Installed packages:"):
			installed = true
			continue
		case strings.HasPrefix(line, "Available Packages:"), strings.HasPrefix(line, "Available Updates:"):
			installed = false
			continue
		}
		id := strings.TrimSpace(strings.SplitN(line, "|", 2)[0])
		if !strings.HasPrefix(id, "system-images;") || seen[id] {
			continue
		}
		seen[id] = true
		parts := strings.Split(id, ";")
		if len(parts) != 4 {
			continue
		}
		images = append(images, device.Image{
			ID:        id,
			Name:      parts[2] + " " + parts[3],
			Version:   strings.TrimPrefix(parts[1], "android-"),
			Installed: installed,
		})
	}
	return images, nil
}

// sdkmanager prompts for license acceptance on stdin; "y" per prompt is the only way through non-interactively.
func (p *Provider) InstallImage(ctx context.Context, img device.Image) error {
	cmd := exec.CommandContext(ctx, p.sdk.sdkmanager(), img.ID)
	cmd.Stdin = strings.NewReader(strings.Repeat("y\n", 20))
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%w: %s", err, tail(out))
	}
	return nil
}

func (p *Provider) Create(ctx context.Context, name string, img device.Image, deviceType string, hw *device.Hardware) error {
	args := []string{"create", "avd", "-n", name, "-k", img.ID}
	if deviceType != "" {
		args = append(args, "-d", deviceType)
	}
	cmd := exec.CommandContext(ctx, p.sdk.avdmanager(), args...)
	cmd.Stdin = strings.NewReader("no\n")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%w: %s", err, tail(out))
	}
	if err := setAVDConfig(name); err != nil {
		return err
	}
	if hw != nil {
		return p.SetHardware(ctx, device.Device{ID: name, Kind: device.KindVirtual}, *hw)
	}
	return nil
}

// Screen size comes from the SDK's skin for the profile when one is installed; avdmanager itself only lists ids.
func (p *Provider) DeviceTypes(ctx context.Context) ([]device.DeviceType, error) {
	out, err := run(ctx, p.sdk.avdmanager(), "list", "device", "-c")
	if err != nil {
		return nil, err
	}
	var types []device.DeviceType
	for _, id := range lines(out) {
		if id == "" {
			continue
		}
		t := device.DeviceType{ID: id, Name: id}
		if layout, err := os.ReadFile(filepath.Join(p.sdk.root, "skins", id, "layout")); err == nil {
			t.Screen = screenFromSkin(string(layout))
		}
		types = append(types, t)
	}
	return types, nil
}

func run(ctx context.Context, bin string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, bin, args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("%s %s: %w: %s", filepath.Base(bin), strings.Join(args, " "), err, tail(stderr.Bytes()))
	}
	return string(out), nil
}

// start detaches the process so the emulator outlives the TUI and never blocks it.
func start(bin string, args ...string) error {
	cmd := exec.Command(bin, args...)
	cmd.Stdout, cmd.Stderr = nil, nil
	if err := cmd.Start(); err != nil {
		return err
	}
	go func() { _ = cmd.Wait() }()
	return nil
}

func lines(s string) []string {
	var out []string
	sc := bufio.NewScanner(strings.NewReader(s))
	for sc.Scan() {
		out = append(out, strings.TrimRight(sc.Text(), "\r"))
	}
	return out
}

func tail(b []byte) string {
	s := strings.TrimSpace(string(b))
	if i := strings.LastIndexByte(s, '\n'); i >= 0 {
		return s[i+1:]
	}
	return s
}

var keycodes = map[device.Key]string{
	device.KeyHome:     "KEYCODE_HOME",
	device.KeyBack:     "KEYCODE_BACK",
	device.KeyOverview: "KEYCODE_APP_SWITCH",
}

func (p *Provider) SendKey(ctx context.Context, d device.Device, key device.Key) error {
	if d.Serial == "" {
		return errors.New("device is not running")
	}
	code, ok := keycodes[key]
	if !ok {
		return fmt.Errorf("unknown key %q", key)
	}
	_, err := run(ctx, p.sdk.adb(), "-s", d.Serial, "shell", "input", "keyevent", code)
	return err
}
