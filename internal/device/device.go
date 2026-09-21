package device

import (
	"context"
	"fmt"
	"os/exec"
	"slices"
	"time"
)

type Platform string

const (
	PlatformAndroid Platform = "android"
	PlatformIOS     Platform = "ios"
	// PlatformDesktop is the machine sims itself runs on. It is a device like any other for the
	// things a desktop can do (its traffic, its log) and says so for the rest.
	PlatformDesktop Platform = "desktop"
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
	// KindHost is this machine: not something sims boots or installs onto, but something whose
	// traffic and log it can show.
	KindHost Kind = "host"
)

// Transport is how the host reaches the device: avd/sim for virtual, usb/wifi for physical.
type Transport string

const (
	TransportAVD  Transport = "avd"
	TransportSim  Transport = "sim"
	TransportUSB  Transport = "usb"
	TransportWiFi Transport = "wifi"
	// TransportLocal is the machine sims runs on: nothing is reached, it is already here.
	TransportLocal Transport = "local"
)

// ID is what the provider needs back to act on the device: AVD name on Android, UDID on iOS.
type Device struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Model     string    `json:"model,omitempty"`
	Platform  Platform  `json:"platform"`
	Kind      Kind      `json:"kind"`
	Transport Transport `json:"transport"`
	Runtime   string    `json:"runtime,omitempty"`
	State     State     `json:"state"`
	Serial    string    `json:"serial,omitempty"` // adb serial while an Android device is reachable; empty otherwise
	// LastActiveAt is the last boot (virtual) or last connection (physical); zero when the platform does not record it.
	LastActiveAt time.Time `json:"lastActiveAt,omitzero"`
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

// IsHost reports whether this record is the machine sims is running on.
func (d Device) IsHost() bool { return d.Kind == KindHost }

// Reachable reports whether a command can be sent to the device as it stands. CoreDevice drops the
// wifi tunnel to an idle iPhone, which shows as Offline, and reopens it for whatever command comes
// next, so a paired phone is reachable there while a stopped virtual device is not.
func (d Device) Reachable() bool {
	if d.Kind == KindPhysical && d.Platform == PlatformIOS {
		return d.State != StateUnpaired
	}
	return d.Running()
}

// NeverBooted marks a simulator that has never run: a stock Xcode lists dozens, so listings hide them by default.
func (d Device) NeverBooted() bool {
	return d.Platform == PlatformIOS && d.Kind == KindVirtual && d.LastActiveAt.IsZero() && !d.Running()
}

type App struct {
	BundleID string `json:"bundleId"`
	Name     string `json:"name"`
	Version  string `json:"version,omitempty"`
	System   bool   `json:"system"`            // shipped with the image (system partition / Apple built-in)
	Source   string `json:"source,omitempty"`  // who installed it: "preinstalled", "store", "adb", "simctl", or "" when unknown
	Process  string `json:"process,omitempty"` // executable name as the OS logger reports it; empty when unknown
	Running  bool   `json:"running"`
}

// ProcessName is what log filters match on; the display name is the fallback when the executable is unknown.
func (a App) ProcessName() string {
	if a.Process != "" {
		return a.Process
	}
	return a.Name
}

type Image struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Version   string `json:"version,omitempty"`
	Installed bool   `json:"installed"`
	OS        string `json:"os,omitempty"` // OS the image runs ("iOS", "tvOS", "watchOS"); empty when the platform has only one
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
	ID     string `json:"id"`
	Name   string `json:"name"`
	Screen string `json:"screen,omitempty"` // "1080x2400" plus density or scale when the platform exposes it; empty when unknown
	// MinRuntime and MaxRuntime bound the OS versions the type can run ("12.3.1", "15.255.255"); empty means any
	MinRuntime string `json:"minRuntime,omitempty"`
	MaxRuntime string `json:"maxRuntime,omitempty"`
	Family     string `json:"family,omitempty"` // product line ("iPhone", "iPad", "Apple TV", "Apple Watch"); empty when unknown
}

// familiesByOS lists which product lines boot which OS; simctl exposes both but does not tie them.
var familiesByOS = map[string][]string{
	"iOS":      {"iPhone", "iPad"},
	"tvOS":     {"Apple TV"},
	"watchOS":  {"Apple Watch"},
	"xrOS":     {"Apple Vision"},
	"visionOS": {"Apple Vision"},
}

// Supports reports whether a type can run the image: its OS version must fall inside the type's
// runtime bounds and its product line must boot that OS. Unknown bounds, platform or family count as compatible.
func (t DeviceType) Supports(img Image) bool {
	if families, known := familiesByOS[img.OS]; known && t.Family != "" && !slices.Contains(families, t.Family) {
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
	RAMMB  int `json:"ramMb,omitempty"`
	Cores  int `json:"cores,omitempty"`
	DiskGB int `json:"diskGb,omitempty"`
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

// Screenshotter is implemented by providers that can capture a running device's screen as a PNG.
type Screenshotter interface {
	Screenshot(ctx context.Context, d Device) ([]byte, error)
}

// Rebooter is implemented by providers that can restart a device in place.
type Rebooter interface {
	Reboot(ctx context.Context, d Device) error
}

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

// ProxyTarget is where a device should send its traffic, and what it must trust to have TLS opened.
type ProxyTarget struct {
	// Host and Port are how the device reaches the proxy: an emulator uses its host loopback alias,
	// a phone the address of this machine on the network it shares.
	Host string
	Port int
	// CACert is the root certificate in PEM form, empty when only the proxy setting is wanted.
	CACert []byte
	// CACertDER is the same certificate in DER, which an iOS configuration profile carries.
	CACertDER []byte
	// CertName labels the certificate where the device shows it.
	CertName string
	// SSID is the wifi network a phone's proxy setting is attached to; iOS has no global proxy.
	SSID string
	// Signer wraps a phone's configuration profile in CMS, which iOS requires before it will read one.
	Signer ProfileSigner
	// Publish puts a file where the device's browser can fetch it and returns that URL; an iPhone takes its configuration profile this way. Nil when nothing is listening for it.
	Publish func(name, contentType string, body []byte) (string, error)
	// Installed says the device already carries this target's profile from an earlier capture, so a provider reports that instead of installing it again.
	Installed bool
}

// ProfileSigner signs a configuration profile. The proxy's CA implements it.
type ProfileSigner interface {
	SignCMS(data []byte) ([]byte, error)
}

func (t ProxyTarget) Addr() string { return fmt.Sprintf("%s:%d", t.Host, t.Port) }

// CertOnly reports whether the caller wants the certificate trusted and nothing pointed anywhere.
// A provider must not change a network setting for one of these: a device told to use a proxy that
// does not exist has no working network.
func (t ProxyTarget) CertOnly() bool { return t.Port == 0 }

// ProxyStep is one thing that has to happen on the device, and whether sims did it.
type ProxyStep struct {
	Title string `json:"title"`
	// Detail carries what the user has to do when Manual is set, or what sims did when it is not.
	Detail string `json:"detail,omitempty"`
	Manual bool   `json:"manual"`
	// Todo spells a manual step out, one tap or screen per entry, in the order they happen.
	Todo []string `json:"todo,omitempty"`
}

// SettingsOpener brings up the device's Settings app, where a step only the device's owner can do is finished.
type SettingsOpener interface {
	OpenSettings(ctx context.Context, d Device) error
}

// ProxyState is what a device currently reports about its proxy configuration.
type ProxyState struct {
	Addr    string `json:"addr,omitempty"`
	Trusted bool   `json:"trusted"`
	// Steps records how the device was set up, so a UI can show what is left to do by hand.
	Steps []ProxyStep `json:"steps,omitempty"`
}

func (s ProxyState) On() bool { return s.Addr != "" }

// Proxier is implemented by providers that can point a device at an HTTP proxy. SetProxy with a
// zero ProxyTarget clears the setting, which every implementation must support: a device left
// pointing at a proxy that is gone cannot reach the network.
type Proxier interface {
	SetProxy(ctx context.Context, d Device, t ProxyTarget) ([]ProxyStep, error)
	ClearProxy(ctx context.Context, d Device) error
	ProxyState(ctx context.Context, d Device) (ProxyState, error)
}
