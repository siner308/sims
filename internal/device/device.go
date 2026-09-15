package device

import (
	"context"
	"os/exec"
	"time"
)

type Platform string

const (
	PlatformAndroid Platform = "android"
	PlatformIOS     Platform = "ios"
)

type State string

const (
	StateBooted   State = "Booted"
	StateBooting  State = "Booting"
	StateShutdown State = "Shutdown"
	StateUnknown  State = "Unknown"

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
	case StateBooting:
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
}

type Image struct {
	ID        string
	Name      string
	Version   string
	Installed bool
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
	// LogCmd returns a long-running process that streams the device log to stdout.
	LogCmd(ctx context.Context, d Device) (*exec.Cmd, error)
	Images(ctx context.Context) ([]Image, error)
	InstallImage(ctx context.Context, img Image) error
	Create(ctx context.Context, name string, img Image, deviceType string) error
	DeviceTypes(ctx context.Context) ([]string, error)
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
