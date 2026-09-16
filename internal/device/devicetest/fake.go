// Package devicetest provides in-memory providers for tests of the layers above the device package.
package devicetest

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"slices"
	"strings"
	"sync"

	"github.com/siner308/sims/internal/device"
)

type Fake struct {
	ID           device.Platform
	AvailableErr error
	Devices      []device.Device
	ListErr      error
	AppList      []device.App
	ImageList    []device.Image
	Types        []device.DeviceType
	HW           device.Hardware
	LogCommand   *exec.Cmd

	mu    sync.Mutex
	Calls []string
}

func (f *Fake) record(format string, args ...any) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Calls = append(f.Calls, fmt.Sprintf(format, args...))
}

func (f *Fake) Called(prefix string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.ContainsFunc(f.Calls, func(c string) bool { return strings.HasPrefix(c, prefix) })
}

func (f *Fake) SetState(id string, state device.State, serial string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := range f.Devices {
		if f.Devices[i].ID == id {
			f.Devices[i].State = state
			f.Devices[i].Serial = serial
		}
	}
}

func (f *Fake) Platform() device.Platform { return f.ID }
func (f *Fake) Available() error          { return f.AvailableErr }

func (f *Fake) List(context.Context) ([]device.Device, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.Devices), f.ListErr
}

func (f *Fake) Boot(_ context.Context, d device.Device) error {
	f.record("Boot %s", d.ID)
	f.SetState(d.ID, device.StateBooted, "emulator-5554")
	return nil
}

func (f *Fake) Shutdown(_ context.Context, d device.Device) error {
	f.record("Shutdown %s", d.ID)
	return nil
}

func (f *Fake) Erase(_ context.Context, d device.Device) error {
	f.record("Erase %s", d.ID)
	return nil
}

func (f *Fake) Delete(_ context.Context, d device.Device) error {
	f.record("Delete %s", d.ID)
	return nil
}

func (f *Fake) Apps(_ context.Context, d device.Device) ([]device.App, error) {
	f.record("Apps %s", d.ID)
	return slices.Clone(f.AppList), nil
}

func (f *Fake) InstallApp(_ context.Context, d device.Device, path string) error {
	f.record("InstallApp %s %s", d.ID, path)
	return nil
}

func (f *Fake) UninstallApp(_ context.Context, d device.Device, bundleID string) error {
	f.record("UninstallApp %s %s", d.ID, bundleID)
	return nil
}

func (f *Fake) LaunchApp(_ context.Context, d device.Device, bundleID string) error {
	f.record("LaunchApp %s %s", d.ID, bundleID)
	return nil
}

func (f *Fake) LogCmd(_ context.Context, d device.Device, app *device.App) (*exec.Cmd, error) {
	if app != nil {
		f.record("LogCmd %s %s", d.ID, app.BundleID)
	} else {
		f.record("LogCmd %s", d.ID)
	}
	if f.LogCommand == nil {
		return nil, errors.New("device is not running")
	}
	return f.LogCommand, nil
}

func (f *Fake) Images(context.Context) ([]device.Image, error) {
	return slices.Clone(f.ImageList), nil
}

func (f *Fake) InstallImage(_ context.Context, img device.Image) error {
	f.record("InstallImage %s", img.ID)
	return nil
}

// Create adds the device to the list because the layer above looks its creation up by name.
func (f *Fake) Create(_ context.Context, name string, img device.Image, deviceType string, hw *device.Hardware) error {
	f.record("Create %s %s %s %v", name, img.ID, deviceType, hw)
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Devices = append(f.Devices, device.Device{
		ID: name + "-id", Name: name, Platform: f.ID, Kind: device.KindVirtual, State: device.StateShutdown,
	})
	return nil
}

func (f *Fake) DeviceTypes(context.Context) ([]device.DeviceType, error) {
	return slices.Clone(f.Types), nil
}

func (f *Fake) Checks(_ context.Context, emit func(device.Check)) {
	emit(device.Check{Name: string(f.ID) + "-tool", Found: "/usr/bin/tool", OK: f.AvailableErr == nil, Hint: "missing"})
}

// Android mirrors the optional interfaces of the real Android provider; keep the two in step.
type Android struct{ *Fake }

func NewAndroid(devices ...device.Device) *Android {
	return &Android{&Fake{ID: device.PlatformAndroid, Devices: devices}}
}

func (a *Android) Pair(_ context.Context, addr, code string) error {
	a.record("Pair %s %s", addr, code)
	return nil
}

func (a *Android) Connect(_ context.Context, addr string) error {
	a.record("Connect %s", addr)
	return nil
}

func (a *Android) Disconnect(_ context.Context, d device.Device) error {
	a.record("Disconnect %s", d.ID)
	return nil
}

func (a *Android) EnableWireless(_ context.Context, d device.Device) (string, error) {
	a.record("EnableWireless %s", d.ID)
	return "192.168.0.5:5555", nil
}

func (a *Android) Hardware(_ context.Context, d device.Device) (device.Hardware, error) {
	a.record("Hardware %s", d.ID)
	return a.HW, nil
}

func (a *Android) SetHardware(_ context.Context, d device.Device, hw device.Hardware) error {
	a.record("SetHardware %s %+v", d.ID, hw)
	a.HW = hw
	return nil
}

func (a *Android) SendKey(_ context.Context, d device.Device, key device.Key) error {
	a.record("SendKey %s %s", d.ID, key)
	return nil
}

// IOS mirrors the optional interfaces of the real iOS provider; keep the two in step.
type IOS struct{ *Fake }

func NewIOS(devices ...device.Device) *IOS {
	return &IOS{&Fake{ID: device.PlatformIOS, Devices: devices}}
}

func (i *IOS) PairDevice(_ context.Context, d device.Device) error {
	i.record("PairDevice %s", d.ID)
	return nil
}

func (i *IOS) Connect(_ context.Context, d device.Device) error {
	i.record("Connect %s", d.ID)
	i.SetState(d.ID, device.StateConnected, d.Serial)
	return nil
}

var (
	_ device.Provider       = (*Fake)(nil)
	_ device.Checker        = (*Fake)(nil)
	_ device.Wireless       = (*Android)(nil)
	_ device.HardwareEditor = (*Android)(nil)
	_ device.KeySender      = (*Android)(nil)
	_ device.Connector      = (*IOS)(nil)
	_ device.Pairer         = (*IOS)(nil)
)
