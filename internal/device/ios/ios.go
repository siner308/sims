package ios

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
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
	IsAvailable bool   `json:"isAvailable"`
}

type simDeviceType struct {
	Identifier string `json:"identifier"`
	Name       string `json:"name"`
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
	physical, err := p.physicalDevices(ctx)
	if err != nil {
		return devices, err
	}
	return append(devices, physical...), nil
}

func mapState(s string) device.State {
	switch s {
	case "Booted":
		return device.StateBooted
	case "Booting":
		return device.StateBooting
	case "Shutdown":
		return device.StateShutdown
	}
	return device.StateUnknown
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

func (p *Provider) Delete(ctx context.Context, d device.Device) error {
	if d.Kind == device.KindPhysical {
		return errPhysical
	}
	_, err := simctl(ctx, "delete", d.ID)
	return err
}

func (p *Provider) Apps(ctx context.Context, d device.Device) ([]device.App, error) {
	if !d.Running() {
		return nil, errors.New("device is not running")
	}
	if d.Kind == device.KindPhysical {
		return p.physicalApps(ctx, d)
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
		BundleID string `json:"CFBundleIdentifier"`
		Name     string `json:"CFBundleDisplayName"`
		AltName  string `json:"CFBundleName"`
		Version  string `json:"CFBundleShortVersionString"`
		Type     string `json:"ApplicationType"`
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
		apps = append(apps, device.App{BundleID: a.BundleID, Name: name, Version: a.Version, System: a.Type != "User", Source: source})
	}
	sort.Slice(apps, func(i, j int) bool { return apps[i].Name < apps[j].Name })
	return apps, nil
}

func (p *Provider) InstallApp(ctx context.Context, d device.Device, path string) error {
	if d.Kind == device.KindPhysical {
		return devicectl(ctx, "device", "install", "app", "--device", d.ID, path)
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

func (p *Provider) LogCmd(ctx context.Context, d device.Device) (*exec.Cmd, error) {
	if !d.Running() {
		return nil, errors.New("device is not running")
	}
	if d.Kind == device.KindPhysical {
		return physicalLogCmd(ctx, d)
	}
	return exec.CommandContext(ctx, "xcrun", "simctl", "spawn", d.ID, "log", "stream", "--style", "compact"), nil
}

func (p *Provider) Images(ctx context.Context) ([]device.Image, error) {
	runtimes, err := p.runtimes(ctx)
	if err != nil {
		return nil, err
	}
	var images []device.Image
	for _, r := range runtimes {
		images = append(images, device.Image{ID: r.Identifier, Name: r.Name, Version: r.Version, Installed: r.IsAvailable})
	}
	return images, nil
}

func (p *Provider) InstallImage(ctx context.Context, img device.Image) error {
	return errors.New("download runtimes with: xcodebuild -downloadPlatform iOS")
}

func (p *Provider) Create(ctx context.Context, name string, img device.Image, deviceType string) error {
	if deviceType == "" {
		return errors.New("device type is required")
	}
	_, err := simctl(ctx, "create", name, deviceType, img.ID)
	return err
}

func (p *Provider) DeviceTypes(ctx context.Context) ([]string, error) {
	var payload struct {
		DeviceTypes []simDeviceType `json:"devicetypes"`
	}
	if err := simctlJSON(ctx, &payload, "list", "devicetypes"); err != nil {
		return nil, err
	}
	var types []string
	for _, t := range payload.DeviceTypes {
		types = append(types, t.Identifier)
	}
	return types, nil
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
		msg := strings.TrimSpace(stderr.String())
		if i := strings.LastIndexByte(msg, '\n'); i >= 0 {
			msg = msg[i+1:]
		}
		return "", fmt.Errorf("simctl %s: %w: %s", strings.Join(args, " "), err, msg)
	}
	return string(out), nil
}
