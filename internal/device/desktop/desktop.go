// Package desktop makes the machine sims runs on a device in its own right. A laptop is as real a
// device as a phone, and the two things sims can do for one, watch its traffic and follow its log,
// are the two it already does for everything else. Everything a desktop cannot do says so plainly
// rather than being hidden.
package desktop

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"

	"github.com/siner308/sims/internal/device"
)

// ID is stable across runs, so a capture or a script can name this machine.
const ID = "localhost"

type Provider struct{}

func New() *Provider { return &Provider{} }

func (p *Provider) Platform() device.Platform { return device.PlatformDesktop }

func (p *Provider) Available() error {
	if !supported() {
		return fmt.Errorf("sims cannot watch %s's own traffic yet", runtime.GOOS)
	}
	return nil
}

func (p *Provider) List(ctx context.Context) ([]device.Device, error) {
	if !supported() {
		return nil, nil
	}
	return []device.Device{{
		ID:        ID,
		Name:      hostName(),
		Model:     hardwareModel(ctx),
		Platform:  device.PlatformDesktop,
		Kind:      device.KindHost,
		Transport: device.TransportLocal,
		Runtime:   osVersion(ctx),
		State:     device.StateConnected,
	}}, nil
}

// homeDirOf is os.UserHomeDir, named so the platform files can share it.
func homeDirOf() (string, error) { return os.UserHomeDir() }

func hostName() string {
	if name := localName(); name != "" {
		return name
	}
	if h, err := os.Hostname(); err == nil {
		return h
	}
	return "this machine"
}

// errNotADevice is what every action a desktop has no meaning for returns.
var errNotADevice = errors.New("this is the machine sims is running on, not a device it can drive")

func notSupported(verb string) error {
	return fmt.Errorf("sims cannot %s the machine it runs on: %w", verb, errors.Join(errNotADevice, errors.ErrUnsupported))
}

func (p *Provider) Boot(context.Context, device.Device) error     { return notSupported("boot") }
func (p *Provider) Shutdown(context.Context, device.Device) error { return notSupported("shut down") }
func (p *Provider) Erase(context.Context, device.Device) error    { return notSupported("erase") }
func (p *Provider) Delete(context.Context, device.Device) error   { return notSupported("delete") }

func (p *Provider) InstallApp(context.Context, device.Device, string) error {
	return notSupported("install an app on")
}

func (p *Provider) UninstallApp(context.Context, device.Device, string) error {
	return notSupported("uninstall an app from")
}

// LaunchApp opens an app on this machine, which is the one app action a desktop can honestly do.
func (p *Provider) LaunchApp(ctx context.Context, _ device.Device, bundleID string) error {
	return launchApp(ctx, bundleID)
}

func (p *Provider) Images(context.Context) ([]device.Image, error) { return nil, nil }

func (p *Provider) InstallImage(context.Context, device.Image) error {
	return notSupported("install an image on")
}

func (p *Provider) Create(context.Context, string, device.Image, string, *device.Hardware) error {
	return notSupported("create")
}

func (p *Provider) DeviceTypes(context.Context) ([]device.DeviceType, error) { return nil, nil }

// LogCmd follows this machine's own system log.
func (p *Provider) LogCmd(ctx context.Context, _ device.Device, app *device.App) (*exec.Cmd, error) {
	return hostLogCmd(ctx, app)
}

func (p *Provider) Info(ctx context.Context) [][2]string {
	if !supported() {
		return nil
	}
	return [][2]string{{"This machine", hostName() + " (" + osVersion(ctx) + ")"}}
}
