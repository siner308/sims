package ios

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/siner308/sims/internal/device"
)

type Provider struct{}

func New() *Provider { return &Provider{} }

func (p *Provider) Platform() device.Platform { return device.PlatformIOS }

func (p *Provider) Available() error {
	if runtime.GOOS != "darwin" {
		return errors.New("ios simulators need macOS")
	}
	if _, err := exec.LookPath("xcrun"); err != nil {
		return errors.New("xcrun not found: install Xcode command line tools")
	}
	return nil
}

type simDevice struct {
	UDID                 string    `json:"udid"`
	Name                 string    `json:"name"`
	State                string    `json:"state"`
	IsAvailable          bool      `json:"isAvailable"`
	DeviceTypeIdentifier string    `json:"deviceTypeIdentifier"`
	LastBootedAt         time.Time `json:"lastBootedAt"`
}

type simRuntime struct {
	Identifier  string `json:"identifier"`
	Name        string `json:"name"`
	Version     string `json:"version"`
	Platform    string `json:"platform"`
	IsAvailable bool   `json:"isAvailable"`
}

type simDeviceType struct {
	Identifier string `json:"identifier"`
	Name       string `json:"name"`
	BundlePath string `json:"bundlePath"`
	MinRuntime string `json:"minRuntimeVersionString"`
	MaxRuntime string `json:"maxRuntimeVersionString"`
	Family     string `json:"productFamily"`
}

func (p *Provider) Info(ctx context.Context) [][2]string {
	out, err := exec.CommandContext(ctx, "xcodebuild", "-version").Output()
	if err != nil {
		return nil
	}
	first, _, _ := strings.Cut(string(out), "\n")
	return [][2]string{{"Xcode", strings.TrimPrefix(first, "Xcode ")}}
}

func (p *Provider) List(ctx context.Context) ([]device.Device, error) {
	runtimes, err := p.runtimes(ctx)
	if err != nil {
		return nil, err
	}
	runtimeName := map[string]string{}
	for _, r := range runtimes {
		runtimeName[r.Identifier] = r.Name
	}

	var payload struct {
		Devices map[string][]simDevice `json:"devices"`
	}
	if err := simctlJSON(ctx, &payload, "list", "devices"); err != nil {
		return nil, err
	}
	var devices []device.Device
	for runtimeID, list := range payload.Devices {
		for _, d := range list {
			if !d.IsAvailable {
				continue
			}
			devices = append(devices, device.Device{
				ID:           d.UDID,
				Name:         d.Name,
				Platform:     device.PlatformIOS,
				Kind:         device.KindVirtual,
				Transport:    device.TransportSim,
				Runtime:      runtimeName[runtimeID],
				State:        mapState(d.State),
				LastActiveAt: d.LastBootedAt,
			})
		}
	}
	sort.Slice(devices, func(i, j int) bool {
		if devices[i].Runtime != devices[j].Runtime {
			return devices[i].Runtime > devices[j].Runtime
		}
		return devices[i].Name < devices[j].Name
	})
	physical, simLastSeen, err := p.physicalDevices(ctx)
	if err != nil {
		return devices, err
	}
	for i := range devices {
		if t := simLastSeen[devices[i].ID]; t.After(devices[i].LastActiveAt) {
			devices[i].LastActiveAt = t
		}
	}
	return append(devices, physical...), nil
}

func mapState(s string) device.State {
	switch s {
	case "Booted":
		return device.StateBooted
	case "Booting":
		return device.StateBooting
	case "Shutting Down":
		return device.StateShuttingDown
	case "Shutdown":
		return device.StateShutdown
	}
	// simctl also reports "Creating" while a device is being made; anything unmapped keeps its raw text
	return device.State(s)
}

func (p *Provider) Boot(ctx context.Context, d device.Device) error {
	if d.Kind == device.KindPhysical {
		return errPhysical
	}
	if _, err := simctl(ctx, "boot", d.ID); err != nil {
		return err
	}
	// simctl boot runs headless; Simulator.app is what puts a window on screen.
	return exec.CommandContext(ctx, "open", "-a", "Simulator").Run()
}

func (p *Provider) Shutdown(ctx context.Context, d device.Device) error {
	if d.Kind == device.KindPhysical {
		return errPhysical
	}
	_, err := simctl(ctx, "shutdown", d.ID)
	return err
}

func (p *Provider) Screenshot(ctx context.Context, d device.Device) ([]byte, error) {
	if d.Kind == device.KindPhysical {
		return nil, errors.New("devicectl cannot capture a physical device's screen")
	}
	if d.State != device.StateBooted {
		return nil, errors.New("device is not running")
	}
	// simctl documents "-" as stdout but writes a file of that name instead (Xcode 26.6), so the
	// image comes back through a temporary file; the .png extension is how simctl picks the format.
	f, err := os.CreateTemp("", "sims-*.png")
	if err != nil {
		return nil, err
	}
	path := f.Name()
	f.Close()
	defer os.Remove(path)
	if _, err := simctl(ctx, "io", d.ID, "screenshot", path); err != nil {
		return nil, err
	}
	return os.ReadFile(path)
}

// Reboot on a simulator is shutdown then boot: simctl has no reboot, and erase would take the data with it.
func (p *Provider) Reboot(ctx context.Context, d device.Device) error {
	if d.Kind == device.KindPhysical {
		return devicectl(ctx, "device", "reboot", "--device", d.ID, "--quiet")
	}
	if _, err := simctl(ctx, "shutdown", d.ID); err != nil {
		return err
	}
	return p.Boot(ctx, d)
}

func (p *Provider) Erase(ctx context.Context, d device.Device) error {
	if d.Kind == device.KindPhysical {
		return errPhysical
	}
	if d.State == device.StateBooted {
		return errors.New("shut down the device before erasing")
	}
	_, err := simctl(ctx, "erase", d.ID)
	return err
}

// For a physical device "delete" forgets the CoreDevice pairing, which is what keeps an unplugged
// phone in the list; pairing it again (p) brings it back.
func (p *Provider) Delete(ctx context.Context, d device.Device) error {
	if d.Kind == device.KindPhysical {
		return devicectl(ctx, "manage", "unpair", "--device", d.ID, "--quiet")
	}
	_, err := simctl(ctx, "delete", d.ID)
	return err
}

func (p *Provider) Apps(ctx context.Context, d device.Device) ([]device.App, error) {
	if d.Kind == device.KindPhysical {
		if err := reachable(d); err != nil {
			return nil, err
		}
		return p.physicalApps(ctx, d)
	}
	if !d.Running() {
		return nil, errors.New("device is not running")
	}
	raw, err := simctl(ctx, "listapps", d.ID)
	if err != nil {
		return nil, err
	}
	// listapps emits an OpenStep plist, which encoding/json cannot read; plutil converts it.
	conv := exec.CommandContext(ctx, "plutil", "-convert", "json", "-o", "-", "-")
	conv.Stdin = strings.NewReader(raw)
	out, err := conv.Output()
	if err != nil {
		return nil, fmt.Errorf("plutil: %w", err)
	}
	var payload map[string]struct {
		BundleID   string `json:"CFBundleIdentifier"`
		Name       string `json:"CFBundleDisplayName"`
		AltName    string `json:"CFBundleName"`
		Executable string `json:"CFBundleExecutable"`
		Version    string `json:"CFBundleShortVersionString"`
		Type       string `json:"ApplicationType"`
	}
	if err := json.Unmarshal(out, &payload); err != nil {
		return nil, err
	}
	var apps []device.App
	for _, a := range payload {
		name := a.Name
		if name == "" {
			name = a.AltName
		}
		source := "simctl"
		if a.Type != "User" {
			source = "preinstalled"
		}
		apps = append(apps, device.App{BundleID: a.BundleID, Name: name, Version: a.Version, System: a.Type != "User", Source: source, Process: a.Executable})
	}
	if running, err := p.runningBundles(ctx, d); err == nil {
		for i := range apps {
			apps[i].Running = running[apps[i].BundleID]
		}
	}
	sort.Slice(apps, func(i, j int) bool {
		if apps[i].Running != apps[j].Running {
			return apps[i].Running
		}
		return apps[i].Name < apps[j].Name
	})
	return apps, nil
}

// launchctl keeps a job per app as "UIKitApplication:<bundle id>[...]", and a job that has never
// run or has exited keeps its label with "-" where the pid goes.
func (p *Provider) runningBundles(ctx context.Context, d device.Device) (map[string]bool, error) {
	out, err := simctl(ctx, "spawn", d.ID, "launchctl", "list")
	if err != nil {
		return nil, err
	}
	running := map[string]bool{}
	for _, l := range strings.Split(out, "\n") {
		fields := strings.Fields(l)
		if len(fields) < 3 || fields[0] == "-" {
			continue
		}
		label, ok := strings.CutPrefix(fields[2], "UIKitApplication:")
		if !ok {
			continue
		}
		if i := strings.IndexByte(label, '['); i >= 0 {
			label = label[:i]
		}
		running[label] = true
	}
	return running, nil
}

func (p *Provider) InstallApp(ctx context.Context, d device.Device, path string) error {
	if d.Kind == device.KindPhysical {
		return devicectl(ctx, "device", "install", "app", "--device", d.ID, path)
	}
	// simctl takes a device build and reports success, leaving an app that fails to launch with a
	// SpringBoard error naming no cause, so the mismatch is caught here instead.
	if DeviceOnlyBuild(path) {
		return deviceBuildError(path)
	}
	_, err := simctl(ctx, "install", d.ID, path)
	return err
}

func (p *Provider) UninstallApp(ctx context.Context, d device.Device, bundleID string) error {
	if d.Kind == device.KindPhysical {
		return devicectl(ctx, "device", "uninstall", "app", "--device", d.ID, bundleID)
	}
	_, err := simctl(ctx, "uninstall", d.ID, bundleID)
	return err
}

func (p *Provider) LaunchApp(ctx context.Context, d device.Device, bundleID string) error {
	if d.Kind == device.KindPhysical {
		return devicectl(ctx, "device", "process", "launch", "--terminate-existing", "--device", d.ID, bundleID)
	}
	_, err := simctl(ctx, "launch", d.ID, bundleID)
	return err
}

func (p *Provider) LogCmd(ctx context.Context, d device.Device, app *device.App) (*exec.Cmd, error) {
	if d.Kind == device.KindPhysical {
		if err := reachable(d); err != nil {
			return nil, err
		}
		return physicalLogCmd(ctx, d, app)
	}
	if !d.Running() {
		return nil, errors.New("device is not running")
	}
	args := []string{"simctl", "spawn", d.ID, "log", "stream", "--style", "compact"}
	if app != nil {
		// the logger's process is the executable (CFBundleExecutable), which can differ from the display name
		args = append(args, "--predicate", fmt.Sprintf(`process == %q OR subsystem == %q`, app.ProcessName(), app.BundleID))
	}
	return exec.CommandContext(ctx, "xcrun", args...), nil
}

func (p *Provider) Images(ctx context.Context) ([]device.Image, error) {
	runtimes, err := p.runtimes(ctx)
	if err != nil {
		return nil, err
	}
	var images []device.Image
	for _, r := range runtimes {
		images = append(images, device.Image{ID: r.Identifier, Name: r.Name, Version: r.Version, Installed: r.IsAvailable, OS: r.Platform})
	}
	return images, nil
}

func (p *Provider) InstallImage(ctx context.Context, img device.Image) error {
	return errors.New("download runtimes with: xcodebuild -downloadPlatform iOS")
}

func (p *Provider) Create(ctx context.Context, name string, img device.Image, deviceType string, _ *device.Hardware) error {
	if deviceType == "" {
		return errors.New("device type is required")
	}
	_, err := simctl(ctx, "create", name, deviceType, img.ID)
	return err
}

// The screen size lives in each device type's profile.plist inside its bundle, not in the simctl JSON.
func (p *Provider) DeviceTypes(ctx context.Context) ([]device.DeviceType, error) {
	var payload struct {
		DeviceTypes []simDeviceType `json:"devicetypes"`
	}
	if err := simctlJSON(ctx, &payload, "list", "devicetypes"); err != nil {
		return nil, err
	}
	var types []device.DeviceType
	for _, t := range payload.DeviceTypes {
		types = append(types, device.DeviceType{ID: t.Identifier, Name: t.Name, Screen: screenFromProfile(ctx, t.BundlePath), MinRuntime: t.MinRuntime, MaxRuntime: t.MaxRuntime, Family: t.Family})
	}
	return types, nil
}

func screenFromProfile(ctx context.Context, bundlePath string) string {
	if bundlePath == "" {
		return ""
	}
	out, err := exec.CommandContext(ctx, "plutil", "-convert", "json", "-o", "-", bundlePath+"/Contents/Resources/profile.plist").Output()
	if err != nil {
		return ""
	}
	var profile struct {
		Width  int     `json:"mainScreenWidth"`
		Height int     `json:"mainScreenHeight"`
		Scale  float64 `json:"mainScreenScale"`
	}
	if json.Unmarshal(out, &profile) != nil || profile.Width == 0 {
		return ""
	}
	return fmt.Sprintf("%dx%d @%gx", profile.Width, profile.Height, profile.Scale)
}

func (p *Provider) runtimes(ctx context.Context) ([]simRuntime, error) {
	var payload struct {
		Runtimes []simRuntime `json:"runtimes"`
	}
	if err := simctlJSON(ctx, &payload, "list", "runtimes"); err != nil {
		return nil, err
	}
	return payload.Runtimes, nil
}

func simctlJSON(ctx context.Context, v any, args ...string) error {
	out, err := simctl(ctx, append(args, "--json")...)
	if err != nil {
		return err
	}
	return json.Unmarshal([]byte(out), v)
}

func simctl(ctx context.Context, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "xcrun", append([]string{"simctl"}, args...)...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		// simctl create puts the error code on stderr and the offending type/runtime pair on stdout
		var parts []string
		for _, s := range []string{stderr.String(), string(out)} {
			s = strings.TrimSpace(s)
			if i := strings.LastIndexByte(s, '\n'); i >= 0 {
				s = s[i+1:]
			}
			if s != "" {
				parts = append(parts, s)
			}
		}
		return "", fmt.Errorf("simctl %s: %w: %s", strings.Join(args, " "), err, strings.Join(parts, ": "))
	}
	return string(out), nil
}
