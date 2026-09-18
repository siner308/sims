package android

import (
	"context"
	"crypto/md5"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

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
	if _, err := run(ctx, p.adb(), "-s", d.Serial, "shell", "settings", "put", "global", "http_proxy", t.Addr()); err != nil {
		return nil, err
	}
	steps := []device.ProxyStep{{Title: "proxy", Detail: "traffic goes to " + t.Addr()}}
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

	remote := "/data/local/tmp/" + name
	if _, err := run(ctx, p.adb(), "-s", d.Serial, "push", local, remote); err != nil {
		return nil, err
	}
	defer run(ctx, p.adb(), "-s", d.Serial, "shell", "rm", "-f", remote)

	if err := p.installSystemCA(ctx, d, remote, name); err == nil {
		return []device.ProxyStep{{
			Title:  "certificate",
			Detail: t.CertName + " is in the system trust store; every app on the device trusts it",
		}}, nil
	}
	return []device.ProxyStep{{
		Title:  "certificate",
		Detail: "open Settings > Security > Encryption & credentials > Install a certificate > CA certificate and pick " + remote,
		Manual: true,
	}, {
		Title:  "app opt-in",
		Detail: "an app targeting API 24+ reads a user certificate only where its network security config trusts `user`; a release build usually does not",
		Manual: true,
	}}, nil
}

// installSystemCA works on an emulator started with a writable system image, and on a rooted phone.
// Android looks the certificate up by the hashed subject name, so the file must carry that name.
func (p *Provider) installSystemCA(ctx context.Context, d device.Device, remote, name string) error {
	if _, err := run(ctx, p.adb(), "-s", d.Serial, "root"); err != nil {
		return err
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
	_, err := run(ctx, p.adb(), "-s", d.Serial, "shell", "settings", "put", "global", "http_proxy", ":0")
	return err
}

func (p *Provider) ProxyState(ctx context.Context, d device.Device) (device.ProxyState, error) {
	if d.Serial == "" {
		return device.ProxyState{}, nil
	}
	out, err := run(ctx, p.adb(), "-s", d.Serial, "shell", "settings", "get", "global", "http_proxy")
	if err != nil {
		return device.ProxyState{}, err
	}
	addr := strings.TrimSpace(out)
	if addr == "" || addr == "null" || addr == ":0" {
		return device.ProxyState{}, nil
	}
	return device.ProxyState{Addr: addr}, nil
}
