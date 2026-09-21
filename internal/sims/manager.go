// Package sims fronts the platform providers with one API keyed by device, so a front end never picks adb or simctl itself.
package sims

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/siner308/sims/internal/device"
)

// BootTimeout bounds WaitBooted by default; a cold emulator with a large image needs most of it.
const BootTimeout = 3 * time.Minute

type Manager struct {
	all       []device.Provider
	providers map[device.Platform]device.Provider
	missing   map[device.Platform]error
	captures  captures
	standbys  standbys
}

// New keeps every provider for doctor to probe but routes work only to those whose toolchain is present.
func New(providers ...device.Provider) *Manager {
	m := &Manager{
		all:       providers,
		providers: map[device.Platform]device.Provider{},
		missing:   map[device.Platform]error{},
	}
	for _, p := range providers {
		if err := p.Available(); err != nil {
			m.missing[p.Platform()] = err
			continue
		}
		m.providers[p.Platform()] = p
	}
	return m
}

func (m *Manager) Platforms() []device.Platform {
	out := make([]device.Platform, 0, len(m.providers))
	for p := range m.providers {
		out = append(out, p)
	}
	slices.Sort(out)
	return out
}

func (m *Manager) Missing() map[device.Platform]error { return m.missing }

// Providers returns every registered provider, the unusable ones included.
func (m *Manager) Providers() []device.Provider { return m.all }

func (m *Manager) Provider(platform device.Platform) (device.Provider, error) {
	if p, ok := m.providers[platform]; ok {
		return p, nil
	}
	if err, ok := m.missing[platform]; ok {
		return nil, fmt.Errorf("%s is not available: %w", platform, err)
	}
	return nil, fmt.Errorf("unknown platform %q", platform)
}

func (m *Manager) Info(ctx context.Context, platform device.Platform) [][2]string {
	p, err := m.Provider(platform)
	if err != nil {
		return nil
	}
	if d, ok := p.(device.Describer); ok {
		return d.Info(ctx)
	}
	return nil
}

// Devices returns both a list and an error when one platform fails, so a broken adb does not hide the simulators.
func (m *Manager) Devices(ctx context.Context) ([]device.Device, error) {
	platforms := m.Platforms()
	lists := make([][]device.Device, len(platforms))
	errs := make([]error, len(platforms))
	var wg sync.WaitGroup
	for i, platform := range platforms {
		wg.Go(func() {
			list, err := m.providers[platform].List(ctx)
			if err != nil {
				errs[i] = fmt.Errorf("%s: %w", platform, err)
				return
			}
			lists[i] = list
		})
	}
	wg.Wait()
	return slices.Concat(lists...), errors.Join(errs...)
}

// Resolve matches ref against ID, then adb serial, then name (case-insensitive); a tier with two hits is an error, not a fall-through.
func (m *Manager) Resolve(ctx context.Context, ref string, platform device.Platform) (device.Device, error) {
	if platform != "" {
		if _, err := m.Provider(platform); err != nil {
			return device.Device{}, err
		}
	}
	devices, listErr := m.Devices(ctx)
	if platform != "" {
		devices = slices.DeleteFunc(devices, func(d device.Device) bool { return d.Platform != platform })
	}
	tiers := []func(device.Device) bool{
		func(d device.Device) bool { return d.ID == ref },
		func(d device.Device) bool { return d.Serial != "" && d.Serial == ref },
		func(d device.Device) bool { return strings.EqualFold(d.Name, ref) },
	}
	for _, match := range tiers {
		var hits []device.Device
		for _, d := range devices {
			if match(d) {
				hits = append(hits, d)
			}
		}
		switch len(hits) {
		case 0:
		case 1:
			return hits[0], nil
		default:
			ids := make([]string, len(hits))
			for i, d := range hits {
				ids[i] = fmt.Sprintf("%s (%s, %s)", d.ID, d.Platform, d.State)
			}
			return device.Device{}, fmt.Errorf("%q matches %d devices, use the id: %s", ref, len(hits), strings.Join(ids, "; "))
		}
	}
	if listErr != nil {
		return device.Device{}, fmt.Errorf("no device matches %q (%w)", ref, listErr)
	}
	return device.Device{}, fmt.Errorf("no device matches %q", ref)
}

func (m *Manager) provider(d device.Device) (device.Provider, error) {
	return m.Provider(d.Platform)
}

// OpenSettings brings up the device's Settings app, for a step only its owner can finish there.
func (m *Manager) OpenSettings(ctx context.Context, d device.Device) error {
	p, err := m.provider(d)
	if err != nil {
		return err
	}
	o, ok := p.(device.SettingsOpener)
	if !ok {
		return fmt.Errorf("%s cannot open Settings from here", d.Platform)
	}
	return o.OpenSettings(ctx, d)
}

func (m *Manager) Boot(ctx context.Context, d device.Device) error {
	p, err := m.provider(d)
	if err != nil {
		return err
	}
	return p.Boot(ctx, d)
}

// WaitBooted returns the fresh record rather than d because the adb serial only exists once the emulator is up.
func (m *Manager) WaitBooted(ctx context.Context, d device.Device, timeout time.Duration) (device.Device, error) {
	p, err := m.provider(d)
	if err != nil {
		return device.Device{}, err
	}
	if timeout <= 0 {
		timeout = BootTimeout
	}
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		list, err := p.List(ctx)
		if err != nil {
			return device.Device{}, err
		}
		if i := slices.IndexFunc(list, func(cur device.Device) bool { return cur.ID == d.ID && cur.Running() }); i >= 0 {
			return list[i], nil
		}
		select {
		case <-ctx.Done():
			return device.Device{}, ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
	return device.Device{}, fmt.Errorf("%s did not finish booting within %s", d.Name, timeout)
}

func (m *Manager) Shutdown(ctx context.Context, d device.Device) error {
	p, err := m.provider(d)
	if err != nil {
		return err
	}
	return p.Shutdown(ctx, d)
}

func (m *Manager) Erase(ctx context.Context, d device.Device) error {
	p, err := m.provider(d)
	if err != nil {
		return err
	}
	return p.Erase(ctx, d)
}

func (m *Manager) Delete(ctx context.Context, d device.Device) error {
	p, err := m.provider(d)
	if err != nil {
		return err
	}
	return p.Delete(ctx, d)
}

func (m *Manager) Apps(ctx context.Context, d device.Device) ([]device.App, error) {
	p, err := m.provider(d)
	if err != nil {
		return nil, err
	}
	return p.Apps(ctx, d)
}

func (m *Manager) InstallApp(ctx context.Context, d device.Device, path string) error {
	p, err := m.provider(d)
	if err != nil {
		return err
	}
	return p.InstallApp(ctx, d, path)
}

func (m *Manager) UninstallApp(ctx context.Context, d device.Device, bundleID string) error {
	p, err := m.provider(d)
	if err != nil {
		return err
	}
	return p.UninstallApp(ctx, d, bundleID)
}

func (m *Manager) LaunchApp(ctx context.Context, d device.Device, bundleID string) error {
	p, err := m.provider(d)
	if err != nil {
		return err
	}
	return p.LaunchApp(ctx, d, bundleID)
}

// LogCmd returns a process the caller must start; a non-nil app narrows the stream to it.
func (m *Manager) LogCmd(ctx context.Context, d device.Device, app *device.App) (*exec.Cmd, error) {
	p, err := m.provider(d)
	if err != nil {
		return nil, err
	}
	return p.LogCmd(ctx, d, app)
}

func (m *Manager) FindApp(ctx context.Context, d device.Device, ref string) (device.App, error) {
	apps, err := m.Apps(ctx, d)
	if err != nil {
		return device.App{}, err
	}
	if i := slices.IndexFunc(apps, func(a device.App) bool { return a.BundleID == ref }); i >= 0 {
		return apps[i], nil
	}
	if i := slices.IndexFunc(apps, func(a device.App) bool { return strings.EqualFold(a.Name, ref) }); i >= 0 {
		return apps[i], nil
	}
	return device.App{}, fmt.Errorf("no app %q on %s", ref, d.Name)
}

func (m *Manager) SendKey(ctx context.Context, d device.Device, key device.Key) error {
	p, err := m.provider(d)
	if err != nil {
		return err
	}
	ks, ok := p.(device.KeySender)
	if !ok {
		return fmt.Errorf("%s cannot send %s from here; use the simulator window: %w", d.Platform, key, errors.ErrUnsupported)
	}
	return ks.SendKey(ctx, d, key)
}

func (m *Manager) Screenshot(ctx context.Context, d device.Device) ([]byte, error) {
	p, err := m.provider(d)
	if err != nil {
		return nil, err
	}
	sc, ok := p.(device.Screenshotter)
	if !ok {
		return nil, fmt.Errorf("%s cannot capture a screen from here: %w", d.Platform, errors.ErrUnsupported)
	}
	return sc.Screenshot(ctx, d)
}

func (m *Manager) Reboot(ctx context.Context, d device.Device) error {
	p, err := m.provider(d)
	if err != nil {
		return err
	}
	rb, ok := p.(device.Rebooter)
	if !ok {
		return fmt.Errorf("%s cannot reboot a device from here: %w", d.Platform, errors.ErrUnsupported)
	}
	return rb.Reboot(ctx, d)
}

func (m *Manager) CanEditHardware(platform device.Platform) bool {
	_, ok := m.providers[platform].(device.HardwareEditor)
	return ok
}

func (m *Manager) hardwareEditor(d device.Device) (device.HardwareEditor, error) {
	p, err := m.provider(d)
	if err != nil {
		return nil, err
	}
	editor, ok := p.(device.HardwareEditor)
	if !ok || d.Kind != device.KindVirtual {
		return nil, fmt.Errorf("%s has no editable hardware: %w", d.Name, errors.ErrUnsupported)
	}
	return editor, nil
}

func (m *Manager) Hardware(ctx context.Context, d device.Device) (device.Hardware, error) {
	editor, err := m.hardwareEditor(d)
	if err != nil {
		return device.Hardware{}, err
	}
	return editor.Hardware(ctx, d)
}

func (m *Manager) SetHardware(ctx context.Context, d device.Device, hw device.Hardware) error {
	editor, err := m.hardwareEditor(d)
	if err != nil {
		return err
	}
	return editor.SetHardware(ctx, d, hw)
}

// Connect is one action with two meanings: it opens the CoreDevice tunnel to a paired iPhone, and switches a USB Android phone to adb over tcp.
func (m *Manager) Connect(ctx context.Context, d device.Device) (string, error) {
	p, err := m.provider(d)
	if err != nil {
		return "", err
	}
	switch c := p.(type) {
	case device.Connector:
		if err := c.Connect(ctx, d); err != nil {
			return "", err
		}
		return "connected", nil
	case device.Wireless:
		return c.EnableWireless(ctx, d)
	}
	return "", fmt.Errorf("%s has no connect action: %w", d.Platform, errors.ErrUnsupported)
}

// Disconnect drops a wifi adb connection; a wifi iPhone is forgotten with Delete (unpair) instead.
func (m *Manager) Disconnect(ctx context.Context, d device.Device) error {
	p, err := m.provider(d)
	if err != nil {
		return err
	}
	w, ok := p.(device.Wireless)
	if !ok {
		return fmt.Errorf("%s has no wireless connection to drop: %w", d.Platform, errors.ErrUnsupported)
	}
	return w.Disconnect(ctx, d)
}

// Pair is for a device already in the list (iOS); Android pairs by address with PairAddress.
func (m *Manager) Pair(ctx context.Context, d device.Device) error {
	p, err := m.provider(d)
	if err != nil {
		return err
	}
	pairer, ok := p.(device.Pairer)
	if !ok {
		return fmt.Errorf("%s pairs by address (HOST:PORT and the code the phone shows): %w", d.Platform, errors.ErrUnsupported)
	}
	return pairer.PairDevice(ctx, d)
}

// wireless accepts an empty platform while a single provider reaches devices by address, so `pair HOST:PORT CODE` needs no --platform today.
func (m *Manager) wireless(platform device.Platform) (device.Wireless, error) {
	var found []device.Wireless
	for _, p := range m.Platforms() {
		if platform != "" && p != platform {
			continue
		}
		if w, ok := m.providers[p].(device.Wireless); ok {
			found = append(found, w)
		}
	}
	switch len(found) {
	case 1:
		return found[0], nil
	case 0:
		if platform != "" {
			return nil, fmt.Errorf("%s cannot connect by address: %w", platform, errors.ErrUnsupported)
		}
		return nil, fmt.Errorf("no available platform connects by address: %w", errors.ErrUnsupported)
	}
	return nil, errors.New("more than one platform connects by address; pass --platform")
}

func (m *Manager) PairAddress(ctx context.Context, platform device.Platform, addr, code string) error {
	w, err := m.wireless(platform)
	if err != nil {
		return err
	}
	return w.Pair(ctx, addr, code)
}

func (m *Manager) ConnectAddress(ctx context.Context, platform device.Platform, addr string) error {
	w, err := m.wireless(platform)
	if err != nil {
		return err
	}
	return w.Connect(ctx, addr)
}

// PlatformImage adds the installing platform, which device.Image lacks: its OS field is the runtime the image boots ("iOS", "tvOS").
type PlatformImage struct {
	device.Image
	Platform device.Platform `json:"platform"`
}

func (m *Manager) Images(ctx context.Context) ([]PlatformImage, error) {
	var out []PlatformImage
	var errs []error
	for _, platform := range m.Platforms() {
		imgs, err := m.providers[platform].Images(ctx)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", platform, err))
			continue
		}
		for _, img := range imgs {
			out = append(out, PlatformImage{Image: img, Platform: platform})
		}
	}
	slices.SortStableFunc(out, func(a, b PlatformImage) int {
		if a.Installed != b.Installed {
			if a.Installed {
				return -1
			}
			return 1
		}
		if c := strings.Compare(string(a.Platform), string(b.Platform)); c != 0 {
			return c
		}
		if c := strings.Compare(b.Version, a.Version); c != 0 {
			return c
		}
		return strings.Compare(a.Name, b.Name)
	})
	return out, errors.Join(errs...)
}

func (m *Manager) ResolveImage(ctx context.Context, ref string, platform device.Platform) (PlatformImage, error) {
	images, listErr := m.Images(ctx)
	if platform != "" {
		images = slices.DeleteFunc(images, func(img PlatformImage) bool { return img.Platform != platform })
	}
	if i := slices.IndexFunc(images, func(img PlatformImage) bool { return img.ID == ref }); i >= 0 {
		return images[i], nil
	}
	var hits []PlatformImage
	for _, img := range images {
		if strings.EqualFold(img.Name, ref) {
			hits = append(hits, img)
		}
	}
	switch len(hits) {
	case 1:
		return hits[0], nil
	case 0:
		if listErr != nil {
			return PlatformImage{}, fmt.Errorf("no image matches %q (%w)", ref, listErr)
		}
		return PlatformImage{}, fmt.Errorf("no image matches %q", ref)
	}
	ids := make([]string, len(hits))
	for i, img := range hits {
		ids[i] = img.ID
	}
	return PlatformImage{}, fmt.Errorf("%q matches %d images, use the id: %s", ref, len(hits), strings.Join(ids, "; "))
}

func (m *Manager) InstallImage(ctx context.Context, img PlatformImage) error {
	p, err := m.Provider(img.Platform)
	if err != nil {
		return err
	}
	return p.InstallImage(ctx, img.Image)
}

func (m *Manager) DeviceTypes(ctx context.Context, platform device.Platform) ([]device.DeviceType, error) {
	p, err := m.Provider(platform)
	if err != nil {
		return nil, err
	}
	return p.DeviceTypes(ctx)
}

func ResolveDeviceType(types []device.DeviceType, ref string) (device.DeviceType, error) {
	if i := slices.IndexFunc(types, func(t device.DeviceType) bool { return t.ID == ref }); i >= 0 {
		return types[i], nil
	}
	if i := slices.IndexFunc(types, func(t device.DeviceType) bool { return strings.EqualFold(t.Name, ref) }); i >= 0 {
		return types[i], nil
	}
	return device.DeviceType{}, fmt.Errorf("no device type matches %q", ref)
}

// DefaultDeviceType prefers a current mid-range phone, then the first phone (simctl lists phones newest first), then whatever is first.
func DefaultDeviceType(types []device.DeviceType) (device.DeviceType, bool) {
	if len(types) == 0 {
		return device.DeviceType{}, false
	}
	if i := slices.IndexFunc(types, func(t device.DeviceType) bool {
		return strings.Contains(strings.ToLower(t.ID), "pixel_7") || strings.HasSuffix(t.ID, "iPhone-17-Pro")
	}); i >= 0 {
		return types[i], true
	}
	if i := slices.IndexFunc(types, func(t device.DeviceType) bool {
		return strings.Contains(strings.ToLower(t.Name), "iphone")
	}); i >= 0 {
		return types[i], true
	}
	return types[0], true
}

// Create looks the new device up by name because the tools report no id; among namesakes the never-booted one is the new one.
func (m *Manager) Create(ctx context.Context, img PlatformImage, name, deviceType string, hw *device.Hardware) (device.Device, error) {
	p, err := m.Provider(img.Platform)
	if err != nil {
		return device.Device{}, err
	}
	if err := p.Create(ctx, name, img.Image, deviceType, hw); err != nil {
		return device.Device{}, err
	}
	devices, err := p.List(ctx)
	if err != nil {
		return device.Device{}, err
	}
	var found *device.Device
	for i := range devices {
		d := &devices[i]
		if d.Name != name || d.Kind != device.KindVirtual {
			continue
		}
		if found == nil || (d.LastActiveAt.IsZero() && !found.LastActiveAt.IsZero()) {
			found = d
		}
	}
	if found == nil {
		return device.Device{}, fmt.Errorf("%s was created but does not show up in the device list yet", name)
	}
	return *found, nil
}
