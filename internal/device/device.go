package device

import (
	"context"
	"os/exec"
	"slices"
	"time"
)

type Platform string

const (
	PlatformAndroid Platform = "android"
	PlatformIOS     Platform = "ios"
)

type State string

const (
	StateBooted       State = "Booted"
	StateBooting      State = "Booting"
	StateShuttingDown State = "Shutting Down"
	StateShutdown     State = "Shutdown"
	StateUnknown      State = "Unknown"

	StateConnected    State = "Connected"
	StateOffline      State = "Offline"
	StateUnauthorized State = "Unauthorized"
	StateUnpaired     State = "Unpaired"
)

type Kind string

const (
	KindVirtual  Kind = "virtual"
	KindPhysical Kind = "physical"
)

// Transport is how the host reaches the device: avd/sim for virtual, usb/wifi for physical.
type Transport string

const (
	TransportAVD  Transport = "avd"
	TransportSim  Transport = "sim"
	TransportUSB  Transport = "usb"
	TransportWiFi Transport = "wifi"
)

// ID is what the provider needs back to act on the device: AVD name on Android, UDID on iOS.
type Device struct {
	ID        string
	Name      string
	Model     string
	Platform  Platform
	Kind      Kind
	Transport Transport
	Runtime   string
	State     State
	Serial    string // adb serial while an Android device is reachable; empty otherwise
	// LastActiveAt is the last boot (virtual) or last connection (physical); zero when the platform does not record it.
	LastActiveAt time.Time
}

// StateRank orders states for display: running first, then reachable-but-idle, then stopped.
func (d Device) StateRank() int {
	switch d.State {
	case StateBooted, StateConnected:
		return 0
	case StateBooting, StateShuttingDown:
		return 1
	case StateOffline:
		return 2
	case StateUnauthorized, StateUnpaired:
		return 3
	case StateShutdown:
		return 4
	}
	return 5
}

func (d Device) Running() bool {
	return d.State == StateBooted || d.State == StateConnected
}

type App struct {
	BundleID string
	Name     string
	Version  string
	System   bool   // shipped with the image (system partition / Apple built-in)
	Source   string // who installed it: "preinstalled", "store", "adb", "simctl", or "" when unknown
	Process  string // executable name as the OS logger reports it; empty when unknown
}

// ProcessName is what log filters match on; the display name is the fallback when the executable is unknown.
func (a App) ProcessName() string {
	if a.Process != "" {
		return a.Process
	}
	return a.Name
}

type Image struct {
	ID        string
	Name      string
	Version   string
	Installed bool
	Platform  string // OS the image runs ("iOS", "tvOS", "watchOS"); empty when the platform has only one
}

type Provider interface {
	Platform() Platform
	Available() error
	List(ctx context.Context) ([]Device, error)
	Boot(ctx context.Context, d Device) error
	Shutdown(ctx context.Context, d Device) error
	Erase(ctx context.Context, d Device) error
	Delete(ctx context.Context, d Device) error
	Apps(ctx context.Context, d Device) ([]App, error)
	InstallApp(ctx context.Context, d Device, path string) error
	UninstallApp(ctx context.Context, d Device, bundleID string) error
	LaunchApp(ctx context.Context, d Device, bundleID string) error
	// LogCmd returns a long-running process that streams the device log to stdout; a non-nil app narrows it to that app.
	LogCmd(ctx context.Context, d Device, app *App) (*exec.Cmd, error)
	Images(ctx context.Context) ([]Image, error)
	InstallImage(ctx context.Context, img Image) error
	Create(ctx context.Context, name string, img Image, deviceType string, hw *Hardware) error
	DeviceTypes(ctx context.Context) ([]DeviceType, error)
}

// DeviceType is a hardware profile a virtual device can be created from.
type DeviceType struct {
	ID     string
	Name   string
	Screen string // "1080x2400" plus density or scale when the platform exposes it; empty when unknown
	// MinRuntime and MaxRuntime bound the OS versions the type can run ("12.3.1", "15.255.255"); empty means any
	MinRuntime string
	MaxRuntime string
	Family     string // product line ("iPhone", "iPad", "Apple TV", "Apple Watch"); empty when unknown
}

// familiesByPlatform lists which product lines boot which OS; simctl exposes both but does not tie them.
var familiesByPlatform = map[string][]string{
	"iOS":      {"iPhone", "iPad"},
	"tvOS":     {"Apple TV"},
	"watchOS":  {"Apple Watch"},
	"xrOS":     {"Apple Vision"},
	"visionOS": {"Apple Vision"},
}

// Supports reports whether a type can run the image: its OS version must fall inside the type's
// runtime bounds and its product line must boot that OS. Unknown bounds, platform or family count as compatible.
func (t DeviceType) Supports(img Image) bool {
	if families, known := familiesByPlatform[img.Platform]; known && t.Family != "" && !slices.Contains(families, t.Family) {
		return false
	}
	if img.Version == "" {
		return true
	}
	if t.MinRuntime != "" && compareVersions(img.Version, t.MinRuntime) < 0 {
		return false
	}
	if t.MaxRuntime != "" && compareVersions(img.Version, t.MaxRuntime) > 0 {
		return false
	}
	return true
}

func compareVersions(a, b string) int {
	pa, pb := versionParts(a), versionParts(b)
	for i := 0; i < len(pa) || i < len(pb); i++ {
		var x, y int
		if i < len(pa) {
			x = pa[i]
		}
		if i < len(pb) {
			y = pb[i]
		}
		if x != y {
			if x < y {
				return -1
			}
			return 1
		}
	}
	return 0
}

func versionParts(v string) []int {
	var out []int
	n, has := 0, false
	for _, r := range v {
		switch {
		case r >= '0' && r <= '9':
			n, has = n*10+int(r-'0'), true
		case r == '.':
			if has {
				out = append(out, n)
			}
			n, has = 0, false
		default:
			if has {
				out = append(out, n)
			}
			return out
		}
	}
	if has {
		out = append(out, n)
	}
	return out
}

// Hardware is the part of a virtual device's configuration that a user tunes: zero values mean "leave as is".
type Hardware struct {
	RAMMB  int
	Cores  int
	DiskGB int
}

// HardwareEditor is implemented by providers whose virtual devices keep editable hardware settings.
type HardwareEditor interface {
	Hardware(ctx context.Context, d Device) (Hardware, error)
	SetHardware(ctx context.Context, d Device, hw Hardware) error
}

// KeySender is implemented by providers that can inject navigation keys into a running device.
type KeySender interface {
	SendKey(ctx context.Context, d Device, key Key) error
}

type Key string

const (
	KeyHome     Key = "home"
	KeyBack     Key = "back"
	KeyOverview Key = "overview"
)

// Describer is implemented by providers that can report toolchain facts for the header.
type Describer interface {
	Info(ctx context.Context) [][2]string
}

// Connector is implemented by providers whose paired physical devices need a session opened before use.
type Connector interface {
	Connect(ctx context.Context, d Device) error
}

// Pairer is implemented by providers that can pair a physical device with this host.
type Pairer interface {
	PairDevice(ctx context.Context, d Device) error
}

// Wireless is implemented by providers whose physical devices can be reached over the network.
type Wireless interface {
	Pair(ctx context.Context, addr, code string) error
	Connect(ctx context.Context, addr string) error
	Disconnect(ctx context.Context, d Device) error
	// EnableWireless switches a USB-attached device to network debugging and returns the address it connected to.
	EnableWireless(ctx context.Context, d Device) (string, error)
}
