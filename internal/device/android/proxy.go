package android

import (
	"context"
	"crypto/md5"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/siner308/sims/internal/device"
)

// emulatorHostAlias is the host's loopback as seen from inside an Android emulator; 127.0.0.1 there
// is the emulated device itself.
const emulatorHostAlias = "10.0.2.2"

// ProxyHost rewrites a proxy address for this device: an emulator reaches the host through an alias,
// a phone over the network it shares with this machine.
func (p *Provider) ProxyHost(d device.Device, hostIP string) (string, error) {
	if d.Kind == device.KindVirtual {
		return emulatorHostAlias, nil
	}
	if hostIP == "" {
		return "", errors.New("no address of this machine on the phone's network; connect both to the same wifi")
	}
	return hostIP, nil
}

// SetProxy points the device at the proxy and installs the CA as a user certificate.
// Apps built against API 24 and later ignore user certificates unless their network security config
// opts in, so the returned steps say so rather than leaving a silent failure.
func (p *Provider) SetProxy(ctx context.Context, d device.Device, t device.ProxyTarget) ([]device.ProxyStep, error) {
	if d.Serial == "" {
		return nil, errors.New("device is not running")
	}
	// a cert-only call: the caller wants the certificate trusted and nothing pointed anywhere
	if !t.CertOnly() {
		if err := p.putProxy(ctx, d, t.Addr()); err != nil {
			return nil, err
		}
	}
	steps := []device.ProxyStep{{Title: "proxy", Detail: "traffic goes to " + t.Addr()}}
	if t.CertOnly() {
		steps = nil
	}
	if len(t.CACert) == 0 {
		return steps, nil
	}
	step, err := p.installCA(ctx, d, t)
	if err != nil {
		return steps, err
	}
	return append(steps, step...), nil
}

// installCA puts the certificate in the user trust store. A rooted or emulator image lets us write
// the system store, which every app trusts; otherwise it lands in the user store, which only apps
// that opt in will honour, and the caller is told.
func (p *Provider) installCA(ctx context.Context, d device.Device, t device.ProxyTarget) ([]device.ProxyStep, error) {
	name, err := CertFileName(t.CACert)
	if err != nil {
		return nil, err
	}
	local := filepath.Join(os.TempDir(), name)
	if err := os.WriteFile(local, t.CACert, 0o600); err != nil {
		return nil, err
	}
	defer os.Remove(local)

	staged := "/data/local/tmp/" + name
	if _, err := run(ctx, p.adb(), "-s", d.Serial, "push", local, staged); err != nil {
		return nil, err
	}

	if err := p.installSystemCA(ctx, d, staged, name); err == nil {
		// the staged copy has been installed, so it is no longer needed
		run(ctx, p.adb(), "-s", d.Serial, "shell", "rm", "-f", staged)
		return []device.ProxyStep{{
			Title:  "certificate",
			Detail: t.CertName + " is in the system trust store; every app on the device trusts it",
		}}, nil
	}
	run(ctx, p.adb(), "-s", d.Serial, "shell", "rm", "-f", staged)

	// The user has to install it themselves, and Android's certificate picker browses shared
	// storage: it cannot reach /data/local/tmp, and a file deleted on the way out of this function
	// would not be there to pick either. So the copy they are sent to find lives in Downloads and
	// stays.
	visible := "/sdcard/Download/" + t.CertName + ".crt"
	if _, err := run(ctx, p.adb(), "-s", d.Serial, "push", local, visible); err != nil {
		return nil, fmt.Errorf("could not put the certificate where you can install it: %w", err)
	}
	return []device.ProxyStep{{
		Title:  "certificate",
		Detail: "it is in Downloads as " + t.CertName + ".crt; install it as a CA certificate on the phone, or HTTPS shows as tunnel",
		Manual: true,
		Todo: []string{
			`open Settings and search for "CA certificate" (on a Pixel or the emulator it sits under Security & privacy > More security & privacy > Encryption & credentials > Install a certificate)`,
			"tap CA certificate, then Install anyway, and unlock the screen when asked",
			"pick " + t.CertName + ".crt from Downloads",
		},
	}, {
		Title:  "app opt-in",
		Detail: "an app built for API 24+ ignores a user certificate unless its network security config trusts `user`; a release build usually does not",
		Manual: true,
		Todo: []string{
			`add <certificates src="user" /> to the app's debug network_security_config, or capture a debug build that has it`,
		},
	}}, nil
}

// installSystemCA works on an emulator started with a writable system image, and on a rooted phone.
// Android looks the certificate up by the hashed subject name, so the file must carry that name.
func (p *Provider) installSystemCA(ctx context.Context, d device.Device, remote, name string) error {
	if _, err := run(ctx, p.adb(), "-s", d.Serial, "root"); err != nil {
		return err
	}
	// adb root restarts adbd, which drops the connection; remounting before it is back fails with
	// "device not found" and sends the user to the manual path on a device where the system store
	// would have worked
	waitCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if _, err := run(waitCtx, p.adb(), "-s", d.Serial, "wait-for-device"); err != nil {
		return fmt.Errorf("%s did not come back after adb root: %w", d.Name, err)
	}
	if _, err := run(ctx, p.adb(), "-s", d.Serial, "remount"); err != nil {
		return err
	}
	if _, err := run(ctx, p.adb(), "-s", d.Serial, "shell", "cp", remote, "/system/etc/security/cacerts/"+name); err != nil {
		return err
	}
	_, err := run(ctx, p.adb(), "-s", d.Serial, "shell", "chmod", "644", "/system/etc/security/cacerts/"+name)
	return err
}

// CertFileName is the file name Android looks a root up by: the OpenSSL subject hash, then ".0".
// A certificate stored under any other name is ignored without an error.
func CertFileName(certPEM []byte) (string, error) {
	block, _ := pem.Decode(certPEM)
	if block == nil {
		return "", errors.New("the certificate is not PEM")
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return "", err
	}
	// -subject_hash_old: MD5 over the DER subject, first four bytes read little-endian
	sum := md5.Sum(cert.RawSubject)
	h := uint32(sum[0]) | uint32(sum[1])<<8 | uint32(sum[2])<<16 | uint32(sum[3])<<24
	return fmt.Sprintf("%08x.0", h), nil
}

func (p *Provider) ClearProxy(ctx context.Context, d device.Device) error {
	if d.Serial == "" {
		return errors.New("device is not running")
	}
	// ":0" is how Android spells "no proxy"; an empty value leaves the old one in place
	return p.putProxy(ctx, d, ":0")
}

// putProxy writes the setting and reads it back. `settings put` exits 0 and prints its failure on
// stdout on a device where the shell cannot write global settings, so trusting the exit status
// would report a configured device that will never send a single request to the proxy.
func (p *Provider) putProxy(ctx context.Context, d device.Device, value string) error {
	out, err := run(ctx, p.adb(), "-s", d.Serial, "shell", "settings", "put", "global", "http_proxy", value)
	if err != nil {
		return err
	}
	if msg := strings.TrimSpace(out); msg != "" {
		return fmt.Errorf("could not set the proxy on %s: %s", d.Name, firstLine(msg))
	}
	got, err := p.readProxy(ctx, d)
	if err != nil {
		return err
	}
	if got != value && !(value == ":0" && got == "") {
		return fmt.Errorf("the proxy on %s reads back as %q after setting it to %q", d.Name, got, value)
	}
	return nil
}

// readProxy is the setting as the device reports it, normalised so "no proxy" is the empty string
// however the device spells it.
func (p *Provider) readProxy(ctx context.Context, d device.Device) (string, error) {
	out, err := run(ctx, p.adb(), "-s", d.Serial, "shell", "settings", "get", "global", "http_proxy")
	if err != nil {
		return "", err
	}
	got := strings.TrimSpace(out)
	switch got {
	case "", "null", ":0":
		return "", nil
	}
	// anything that is not host:port is the shell reporting a problem, not an address
	if _, _, err := net.SplitHostPort(got); err != nil {
		return "", fmt.Errorf("%s reported %q instead of a proxy address", d.Name, firstLine(got))
	}
	return got, nil
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

func (p *Provider) ProxyState(ctx context.Context, d device.Device) (device.ProxyState, error) {
	if d.Serial == "" {
		return device.ProxyState{}, nil
	}
	addr, err := p.readProxy(ctx, d)
	if err != nil {
		return device.ProxyState{}, err
	}
	return device.ProxyState{Addr: addr}, nil
}
