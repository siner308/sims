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
	// adbOverride replaces the SDK's adb; only tests set it.
	adbOverride string
}

// adb is the binary every device command runs through.
func (p *Provider) adb() string {
	if p.adbOverride != "" {
		return p.adbOverride
	}
	return p.sdk.adb()
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
		if r, ok := running[name]; ok {
			d.State, d.Serial = device.StateBooted, r.serial
			if !r.bootCompleted {
				d.State = device.StateBooting
			}
		}
		devices = append(devices, d)
	}
	return append(devices, p.physicalDevices(ctx, entries)...), nil
}

type runningEmulator struct {
	serial        string
	bootCompleted bool
}

// adbd answers well before the framework has finished starting, so an emulator that adb already
// lists is only Booted once sys.boot_completed is set; until then it is still Booting, and a caller
// waiting for it does not get a device that refuses the install it is about to attempt.
func (p *Provider) runningSerials(ctx context.Context, entries []adbEntry) map[string]runningEmulator {
	result := map[string]runningEmulator{}
	for _, e := range entries {
		if !e.isEmulator() || e.state != "device" {
			continue
		}
		name, err := run(ctx, p.sdk.adb(), "-s", e.serial, "emu", "avd", "name")
		if err != nil {
			continue
		}
		if l := lines(name); len(l) > 0 {
			result[l[0]] = runningEmulator{serial: e.serial, bootCompleted: p.bootCompleted(ctx, e.serial)}
		}
	}
	return result
}

func (p *Provider) bootCompleted(ctx context.Context, serial string) bool {
	out, err := run(ctx, p.sdk.adb(), "-s", serial, "shell", "getprop", "sys.boot_completed")
	return err == nil && strings.TrimSpace(out) == "1"
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
	_, err := output(p.sdk.avdmanagerCmd(ctx, "delete", "avd", "-n", d.ID))
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
	if running, err := p.runningPackages(ctx, d.Serial); err == nil {
		for i := range apps {
			apps[i].Running = running[apps[i].BundleID]
		}
	}
	p.enrichLabels(ctx, d.Serial, apps, all)
	return apps, nil
}

// An Android app's process is named after its package unless the manifest renames it, so a package
// with no process of its own reads as not running even when it has a service alive under another name.
func (p *Provider) runningPackages(ctx context.Context, serial string) (map[string]bool, error) {
	out, err := run(ctx, p.sdk.adb(), "-s", serial, "shell", "ps", "-A", "-o", "NAME")
	if err != nil {
		return nil, err
	}
	running := map[string]bool{}
	for _, l := range lines(out) {
		name := strings.TrimSpace(l)
		// A renamed process is "<package>:<suffix>"; the package in front is what identifies the app.
		if pkg, _, found := strings.Cut(name, ":"); found {
			name = pkg
		}
		if name != "" && name != "NAME" {
			running[name] = true
		}
	}
	return running, nil
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
	// the ring buffer holds ~100k lines on a busy emulator; the view keeps 5000, so do not ship the rest
	args := []string{"-s", d.Serial, "logcat", "-v", "time", "-T", "2000"}
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
	out, err := run(ctx, p.sdk.sdkmanager(), p.sdk.sdkRootFlag(), "--list")
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
	cmd := exec.CommandContext(ctx, p.sdk.sdkmanager(), p.sdk.sdkRootFlag(), img.ID)
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
	cmd := p.sdk.avdmanagerCmd(ctx, args...)
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
	out, err := output(p.sdk.avdmanagerCmd(ctx, "list", "device", "-c"))
	if err != nil {
		return nil, err
	}
	var types []device.DeviceType
	for _, id := range lines(string(out)) {
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

func (p *Provider) Screenshot(ctx context.Context, d device.Device) ([]byte, error) {
	if d.Serial == "" {
		return nil, errors.New("device is not running")
	}
	// exec-out keeps the PNG bytes intact; plain `adb shell` would translate the line endings and corrupt it.
	return runRaw(ctx, p.sdk.adb(), "-s", d.Serial, "exec-out", "screencap", "-p")
}

func (p *Provider) Reboot(ctx context.Context, d device.Device) error {
	if d.Serial == "" {
		return errors.New("device is not running")
	}
	if _, err := run(ctx, p.sdk.adb(), "-s", d.Serial, "reboot"); err != nil {
		return err
	}
	p.waitDown(ctx, d.Serial)
	return nil
}

// waitDown returns once adb stops reporting the device, so a caller that then waits for it to come
// back does not pass straight through on the old connection that has not dropped yet.
// The reboot has already been accepted at this point, so a device that never drops is not an error.
func (p *Provider) waitDown(ctx context.Context, serial string) {
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		out, err := run(ctx, p.sdk.adb(), "-s", serial, "get-state")
		if err != nil || strings.TrimSpace(out) != "device" {
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(250 * time.Millisecond):
		}
	}
}

func run(ctx context.Context, bin string, args ...string) (string, error) {
	out, err := runRaw(ctx, bin, args...)
	return string(out), err
}

func runRaw(ctx context.Context, bin string, args ...string) ([]byte, error) {
	return output(exec.CommandContext(ctx, bin, args...))
}

func output(cmd *exec.Cmd) ([]byte, error) {
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("%s %s: %w: %s", filepath.Base(cmd.Path), strings.Join(cmd.Args[1:], " "), err, tail(stderr.Bytes()))
	}
	return out, nil
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
