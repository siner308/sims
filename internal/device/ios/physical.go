package ios

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"
	"time"

	"github.com/siner308/sims/internal/device"
)

type coreDevice struct {
	Identifier       string `json:"identifier"`
	DeviceProperties struct {
		Name            string `json:"name"`
		OSVersionNumber string `json:"osVersionNumber"`
	} `json:"deviceProperties"`
	ConnectionProperties struct {
		PairingState       string    `json:"pairingState"`
		TransportType      string    `json:"transportType"`
		TunnelState        string    `json:"tunnelState"`
		LastConnectionDate time.Time `json:"lastConnectionDate"`
	} `json:"connectionProperties"`
	HardwareProperties struct {
		MarketingName string `json:"marketingName"`
		ProductType   string `json:"productType"`
		Platform      string `json:"platform"`
		UDID          string `json:"udid"`
	} `json:"hardwareProperties"`
}

func parseCoreDevices(raw []byte) ([]device.Device, error) {
	var payload struct {
		Result struct {
			Devices []coreDevice `json:"devices"`
		} `json:"result"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil, err
	}
	var devices []device.Device
	for _, cd := range payload.Result.Devices {
		if cd.HardwareProperties.Platform != "iOS" {
			continue
		}
		d := device.Device{
			ID:           cd.Identifier,
			Serial:       cd.HardwareProperties.UDID,
			Name:         cd.DeviceProperties.Name,
			Model:        cd.HardwareProperties.MarketingName,
			Platform:     device.PlatformIOS,
			Kind:         device.KindPhysical,
			Transport:    device.TransportUSB,
			State:        device.StateOffline,
			LastActiveAt: cd.ConnectionProperties.LastConnectionDate,
		}
		if d.Model == "" {
			d.Model = cd.HardwareProperties.ProductType
		}
		if v := cd.DeviceProperties.OSVersionNumber; v != "" {
			d.Runtime = "iOS " + v
		}
		if cd.ConnectionProperties.TransportType == "localNetwork" {
			d.Transport = device.TransportWiFi
		}
		switch {
		case cd.ConnectionProperties.PairingState != "paired":
			d.State = device.StateUnpaired
		case cd.ConnectionProperties.TunnelState == "connected":
			d.State = device.StateConnected
		}
		devices = append(devices, d)
	}
	sort.Slice(devices, func(i, j int) bool { return devices[i].Name < devices[j].Name })
	return devices, nil
}

// devicectl only writes machine-readable output to a file, so every call goes through a temp path.
func devicectlJSON(ctx context.Context, args ...string) ([]byte, error) {
	f, err := os.CreateTemp("", "sims-devicectl-*.json")
	if err != nil {
		return nil, err
	}
	path := f.Name()
	_ = f.Close()
	defer os.Remove(path)

	cmd := exec.CommandContext(ctx, "xcrun", append([]string{"devicectl"}, append(args, "--json-output", path, "--quiet")...)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("devicectl %v: %w: %s", args, err, lastLine(out))
	}
	return os.ReadFile(path)
}

func (p *Provider) physicalDevices(ctx context.Context) ([]device.Device, error) {
	if _, err := exec.LookPath("xcrun"); err != nil {
		return nil, nil
	}
	raw, err := devicectlJSON(ctx, "list", "devices")
	if err != nil {
		return nil, err
	}
	return parseCoreDevices(raw)
}

// The default listing excludes default apps (see --include-default-apps), so system = in the full listing but not the default one.
func (p *Provider) physicalApps(ctx context.Context, d device.Device) ([]device.App, error) {
	user, err := p.devicectlApps(ctx, d)
	if err != nil {
		return nil, err
	}
	all, err := p.devicectlApps(ctx, d, "--include-all-apps")
	if err != nil {
		return nil, err
	}
	isUser := map[string]bool{}
	for _, a := range user {
		isUser[a.BundleID] = true
	}
	for i := range all {
		all[i].System = !isUser[all[i].BundleID]
		if all[i].System {
			all[i].Source = "preinstalled"
		}
	}
	sort.Slice(all, func(i, j int) bool { return all[i].Name < all[j].Name })
	return all, nil
}

func (p *Provider) devicectlApps(ctx context.Context, d device.Device, flags ...string) ([]device.App, error) {
	args := append([]string{"device", "info", "apps", "--device", d.ID}, flags...)
	raw, err := devicectlJSON(ctx, args...)
	if err != nil {
		return nil, err
	}
	var payload struct {
		Result struct {
			Apps []struct {
				BundleID string `json:"bundleIdentifier"`
				Name     string `json:"name"`
				Version  string `json:"version"`
			} `json:"apps"`
		} `json:"result"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil, err
	}
	apps := make([]device.App, 0, len(payload.Result.Apps))
	for _, a := range payload.Result.Apps {
		apps = append(apps, device.App{BundleID: a.BundleID, Name: a.Name, Version: a.Version})
	}
	return apps, nil
}

// Pairing waits for the trust prompt on the phone, so the timeout is generous.
func (p *Provider) PairDevice(ctx context.Context, d device.Device) error {
	if d.Kind != device.KindPhysical {
		return errors.New("only physical devices can be paired")
	}
	return devicectl(ctx, "manage", "pair", "--device", d.ID, "--timeout", "120")
}

func devicectl(ctx context.Context, args ...string) error {
	cmd := exec.CommandContext(ctx, "xcrun", append([]string{"devicectl"}, args...)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("devicectl %v: %w: %s", args, err, lastLine(out))
	}
	return nil
}

// devicectl has no log streaming; idevicesyslog from libimobiledevice is the CLI that does.
func physicalLogCmd(ctx context.Context, d device.Device) (*exec.Cmd, error) {
	bin, err := exec.LookPath("idevicesyslog")
	if err != nil {
		return nil, errors.New("idevicesyslog not found: brew install libimobiledevice")
	}
	args := []string{"-u", d.Serial}
	if d.Transport == device.TransportWiFi {
		args = append(args, "-n")
	}
	return exec.CommandContext(ctx, bin, args...), nil
}

var errPhysical = errors.New("not available for a physical device")

func lastLine(b []byte) string {
	s := strings.TrimRight(string(b), "\r\n")
	if i := strings.LastIndexByte(s, '\n'); i >= 0 {
		return s[i+1:]
	}
	return s
}
