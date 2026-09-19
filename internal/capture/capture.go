// Package capture runs one traffic capture: it owns a proxy, points a device at it, and puts
// everything back when it stops. It is what makes "watch this device's traffic" a single action
// whatever the device is underneath.
package capture

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/siner308/sims/internal/device"
	"github.com/siner308/sims/internal/proxy"
)

// Session is a running capture. New starts one; Stop undoes everything it did.
type Session struct {
	Device device.Device
	Port   int
	Store  *proxy.Store
	CA     *proxy.CA
	// Steps is what setting the device up took, including anything left for the user to do by hand.
	Steps []device.ProxyStep

	srv     *proxy.Server
	host    *hostProxy
	prov    device.Proxier
	scope   Scope
	certDir string
	// configured records that SetProxy was attempted, so Stop clears a setting that a failed start
	// may have left behind.
	configured bool
	stopped    sync.Once

	mu       sync.Mutex
	serveErr error
}

func (s *Session) setServeErr(err error) {
	s.mu.Lock()
	s.serveErr = err
	s.mu.Unlock()
}

// Err is why the proxy stopped listening, or nil while it is healthy. A capture whose proxy has died
// still has the device pointing at it, so a front end shows this rather than an empty flow list.
func (s *Session) Err() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.serveErr
}

// Scope is which traffic a capture opens. A capture that borrows this machine's proxy settings sees
// everything the machine sends; the scope decides what it touches.
type Scope string

const (
	// ScopeDevice opens only the device's traffic and relays everything else untouched. It is the
	// default, because pointing this Mac at a proxy must not change how its other apps behave.
	ScopeDevice Scope = "device"
	// ScopeAll opens everything that arrives, this machine's own apps included.
	ScopeAll Scope = "all"
)

// Options are the knobs a front end passes through; the zero value is the ordinary case.
type Options struct {
	// Scope is what the capture opens; empty means ScopeDevice.
	Scope Scope
	// Port to listen on; 0 takes any free one.
	Port int
	// MaxFlows and MaxBody bound what is kept in memory.
	MaxFlows int
	MaxBody  int
	// CertDir holds the root certificate between runs; empty uses the user cache directory.
	CertDir string
	// SSID names the wifi network a phone's profile attaches its proxy to.
	SSID string
}

// Start listens, points d at the proxy, and returns a running session. Whatever it managed to change
// is undone if a later step fails, so a failed start leaves the machine and the device as they were.
func Start(ctx context.Context, prov device.Provider, d device.Device, o Options) (*Session, error) {
	p, ok := prov.(device.Proxier)
	if !ok {
		return nil, fmt.Errorf("%s cannot be pointed at a proxy from here: %w", d.Platform, errors.ErrUnsupported)
	}
	dir := o.CertDir
	if dir == "" {
		var err error
		if dir, err = DefaultCertDir(); err != nil {
			return nil, err
		}
	}
	ca, err := proxy.LoadOrCreateCA(dir)
	if err != nil {
		return nil, err
	}
	if o.Scope == "" {
		o.Scope = ScopeDevice
	}
	srv := &proxy.Server{CA: ca, Store: proxy.NewStore(o.MaxFlows), MaxBody: o.MaxBody}
	s := &Session{Device: d, Store: srv.Store, CA: ca, srv: srv, prov: p, host: &hostProxy{}, scope: o.Scope, certDir: dir}
	srv.Attribute = s.attribute

	port, err := srv.Listen(fmt.Sprintf("%s:%d", listenHost(d), o.Port))
	if err != nil {
		return nil, err
	}
	s.Port = port
	go func() {
		// Serve returns nil on Close; anything else means the proxy stopped listening while the
		// device still points at it, which the caller has to be able to see.
		if err := srv.Serve(); err != nil {
			s.setServeErr(err)
		}
	}()

	target, err := s.target(ctx, d, ca, o)
	if err != nil {
		s.Stop()
		return nil, err
	}
	// from here the device may already carry the setting even if SetProxy reports an error, so Stop
	// has to clear it; s.prov is what tells Stop to try.
	s.configured = true
	// written before anything changes: a capture killed past this point is undone by the next run
	s.writeJournal()
	steps, err := p.SetProxy(ctx, d, target)
	if err != nil {
		s.Stop()
		return nil, err
	}
	s.Steps = steps

	// A simulator has no network settings of its own: it follows this Mac's, so the capture points
	// them here and every other app on the machine follows until Stop.
	if d.Kind == device.KindPhysical {
		s.Steps = append(s.Steps, device.ProxyStep{
			Title:  "on the network",
			Detail: fmt.Sprintf("the proxy is reachable at %s:%d for as long as this runs, by anything on the same network", target.Host, port),
		})
	}
	if needsHostProxy(d) {
		if !hostProxySupported() {
			s.Stop()
			return nil, errors.New("a simulator follows this machine's proxy settings, which sims can only change on macOS")
		}
		if err := s.host.set(ctx, "127.0.0.1", port); err != nil {
			s.Stop()
			return nil, err
		}
		// now that the machine's own settings are changed, record what they were
		s.writeJournal()
		s.Steps = append(s.Steps, device.ProxyStep{
			Title:  "this Mac",
			Detail: "its web proxy now points at 127.0.0.1:" + fmt.Sprint(port) + " and goes back when the capture stops",
		})
	}
	return s, nil
}

// listenHost keeps the proxy off the network when nothing needs it there. A phone reaches this
// machine by its LAN address, so a capture for one has to be reachable; a simulator and an emulator
// both arrive over loopback, and binding those to the network would offer an open proxy, and a CA
// that signs for any host, to everyone on the wifi.
//
// A phone capture is therefore reachable by anything on the same network for as long as it runs.
// The phone cannot be told apart by address (a USB serial is a hardware id, an iPhone's is a UDID),
// so the capture says so in its steps rather than pretending otherwise.
func listenHost(d device.Device) string {
	if d.Kind == device.KindPhysical {
		return "0.0.0.0"
	}
	return "127.0.0.1"
}

// writeJournal records what this capture has changed, so a run that is killed before it can put
// things back leaves enough for the next one to finish the job.
func (s *Session) writeJournal() {
	j := journal{PID: os.Getpid(), Port: s.Port}
	j.Service, j.Before = s.host.recordHostProxy()
	if s.configured {
		j.Device = &journalDevice{
			ID: s.Device.ID, Name: s.Device.Name, Serial: s.Device.Serial,
			Platform: string(s.Device.Platform), Kind: string(s.Device.Kind),
		}
	}
	_ = writeJournal(s.certDir, j)
}

// needsHostProxy reports whether the device borrows this machine's network settings. An iOS
// simulator does, and so does the machine itself; an Android emulator has its own.
func needsHostProxy(d device.Device) bool {
	if d.IsHost() {
		return true
	}
	return d.Platform == device.PlatformIOS && d.Kind == device.KindVirtual
}

func (s *Session) target(ctx context.Context, d device.Device, ca *proxy.CA, o Options) (device.ProxyTarget, error) {
	t := device.ProxyTarget{
		Port:      s.Port,
		CACert:    ca.CertPEM(),
		CACertDER: ca.CertDER(),
		CertName:  "sims proxy CA",
		SSID:      o.SSID,
		Signer:    ca,
	}
	switch {
	case needsHostProxy(d):
		t.Host = "127.0.0.1"
	case d.Platform == device.PlatformAndroid && d.Kind == device.KindVirtual:
		t.Host = androidEmulatorHost
	default:
		ip, err := LANAddress()
		if err != nil {
			return t, err
		}
		t.Host = ip
	}
	return t, nil
}

// androidEmulatorHost is the host's loopback as seen from inside an Android emulator.
const androidEmulatorHost = "10.0.2.2"

// attribute decides, for one client connection, whether it is the device's traffic and whether sims
// should open it. A capture that had to point this Mac at the proxy sees every app on the machine,
// and under the default scope it relays all of them untouched.
func (s *Session) attribute(ctx context.Context, clientAddr string) proxy.Attribution {
	host, _, err := net.SplitHostPort(clientAddr)
	if err != nil {
		return s.decide(proxy.Attribution{})
	}
	if !isLoopback(host) {
		// only this machine's own processes can be looked up; anything else reached the proxy over
		// the network, which while a capture is running means the device
		return s.decide(proxy.Attribution{Label: s.Device.Name, Origin: proxy.OriginDevice})
	}
	p, lookupErr := proxy.LookupProcess(ctx, clientAddr)
	if lookupErr != nil {
		if s.Device.IsHost() {
			// watching this machine: a connection sims cannot name is still this machine's
			return s.decide(proxy.Attribution{Origin: proxy.OriginDevice})
		}
		// an unattributable connection is left alone rather than opened on a guess
		return s.decide(proxy.Attribution{})
	}
	if s.Device.IsHost() {
		// the app on this machine is the device here, so its name is what the table should show
		return s.decide(proxy.Attribution{Label: p.Name, Origin: proxy.OriginDevice})
	}
	if needsHostProxy(s.Device) && isSimulatorProcess(p) {
		// a simulator's apps run as host processes, so lsof names the one that opened the
		// connection; keeping that name is what lets a row say more than "the simulator"
		return s.decide(proxy.Attribution{Label: simulatorProcessName(p), Origin: proxy.OriginDevice})
	}
	return s.decide(proxy.Attribution{Label: p.Name, Origin: proxy.OriginHost})
}

// decide applies the scope: under ScopeDevice anything that is not the device passes through
// untouched, so the rest of the machine behaves as if there were no proxy. When the device IS this
// machine, its own apps are the thing being watched, so everything is opened.
func (s *Session) decide(a proxy.Attribution) proxy.Attribution {
	if s.scope == ScopeAll || s.Device.IsHost() {
		return a
	}
	a.Ignore = a.Origin != proxy.OriginDevice
	return a
}

// simulatorProcessName is the executable that opened the connection, without the runtime path it
// lives under. Apple's own networking processes (WebKit.Networking, nsurlsessiond) carry traffic on
// behalf of other apps, so the name is where a request came out, not always which app wanted it.
func simulatorProcessName(p proxy.Process) string {
	if p.Name != "" {
		return p.Name
	}
	return p.Path
}

// A simulator's requests come from processes inside CoreSimulator, which run on the host and so are
// visible to lsof; their executables live under the simulator runtime.
func isSimulatorProcess(p proxy.Process) bool {
	path := strings.ToLower(p.Path)
	return strings.Contains(path, "coresimulator") ||
		strings.Contains(path, "/library/developer/") ||
		strings.Contains(path, ".simruntime")
}

func isLoopback(host string) bool {
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// Flows returns what has been captured so far.
func (s *Session) Flows() []proxy.Flow { return s.Store.Flows() }

// Addr is the address a device was told to use.
func (s *Session) Addr() string { return fmt.Sprintf(":%d", s.Port) }

// Stop puts the device and this machine back and closes the proxy. It is safe to call twice, and it
// runs every step even when one fails, so one error does not strand the rest.
func (s *Session) Stop() error {
	errs := []error{s.Err()}
	s.stopped.Do(func() {
		if s.prov != nil && s.configured {
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			if err := s.prov.ClearProxy(ctx, s.Device); err != nil {
				errs = append(errs, fmt.Errorf("could not clear the proxy on %s: %w", s.Device.Name, err))
			}
		}
		if err := s.host.restore(); err != nil {
			errs = append(errs, fmt.Errorf("could not restore this machine's proxy settings: %w", err))
		}
		if s.srv != nil {
			if err := s.srv.Close(); err != nil {
				errs = append(errs, err)
			}
		}
		// everything is back, so there is nothing left for a later run to undo
		removeJournal(s.certDir)
	})
	return errors.Join(errs...)
}

// DefaultCertDir is where the root certificate, its key and the in-flight note live between runs.
// SIMS_PROXY_DIR moves all three, which a test uses to stay off the real one. The error is passed on
// rather than falling back to a temp directory: a CA key belongs in the user's own cache, not
// somewhere every account on the machine can reach.
func DefaultCertDir() (string, error) {
	if dir := os.Getenv("SIMS_PROXY_DIR"); dir != "" {
		return dir, nil
	}
	dir, err := os.UserCacheDir()
	if err != nil {
		return "", fmt.Errorf("no user cache directory for the proxy certificate: %w", err)
	}
	return filepath.Join(dir, "sims", "proxy"), nil
}

// LANAddress is this machine's address on the network a phone would reach it by.
func LANAddress() (string, error) {
	ifaces, err := net.Interfaces()
	if err != nil {
		return "", err
	}
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, addr := range addrs {
			ipnet, ok := addr.(*net.IPNet)
			if !ok {
				continue
			}
			ip := ipnet.IP.To4()
			if ip == nil || !ip.IsPrivate() {
				continue
			}
			return ip.String(), nil
		}
	}
	return "", errors.New("this machine has no private IPv4 address; connect it to the same network as the phone")
}
