package ios

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"text/template"

	"github.com/siner308/sims/internal/device"
)

// A simulator has no network settings of its own: it uses this machine's, so pointing it at a proxy
// means setting the macOS system proxy, and every other app on the Mac follows. A phone carries its
// own settings and takes a configuration profile instead.

// SetProxy installs the CA and, for a phone, the proxy setting that comes with it. The system proxy
// a simulator needs is not this provider's to set: the caller owns it, because it is machine-wide.
func (p *Provider) SetProxy(ctx context.Context, d device.Device, t device.ProxyTarget) ([]device.ProxyStep, error) {
	if d.Kind == device.KindPhysical {
		return p.installProfile(ctx, d, t)
	}
	if !d.Running() {
		return nil, errors.New("device is not running")
	}
	if len(t.CACert) > 0 {
		path, cleanup, err := tempCert(t.CACert)
		if err != nil {
			return nil, err
		}
		defer cleanup()
		if _, err := simctl(ctx, "keychain", d.ID, "add-root-cert", path); err != nil {
			return nil, err
		}
	}
	return []device.ProxyStep{
		{Title: "certificate", Detail: t.CertName + " is trusted by this simulator"},
		{Title: "proxy", Detail: "a simulator follows this Mac's network settings, so sims points them at " + t.Addr() + " while the capture runs"},
	}, nil
}

// installProfile hands the phone a configuration profile carrying the root certificate and a global
// HTTP proxy. iOS asks the person to approve it in Settings, and a root still has to be switched on
// under Certificate Trust Settings; neither can be done from here.
func (p *Provider) installProfile(ctx context.Context, d device.Device, t device.ProxyTarget) ([]device.ProxyStep, error) {
	if err := reachable(d); err != nil {
		return nil, err
	}
	if len(t.CACert) == 0 {
		return nil, errors.New("a phone needs the certificate to install a proxy profile")
	}
	path, cleanup, err := writeProfile(t)
	if err != nil {
		return nil, err
	}
	defer cleanup()
	if err := devicectl(ctx, "device", "profile", "install", "--device", d.ID, path); err != nil {
		return nil, err
	}
	return []device.ProxyStep{
		{Title: "profile", Detail: "sent to the phone; it carries the certificate and points traffic at " + t.Addr()},
		{Title: "approve it", Detail: "on the phone: Settings > General > VPN & Device Management > sims proxy > Install", Manual: true},
		{Title: "trust the certificate", Detail: "then Settings > General > About > Certificate Trust Settings, and switch " + t.CertName + " on", Manual: true},
	}, nil
}

func (p *Provider) ClearProxy(ctx context.Context, d device.Device) error {
	if d.Kind != device.KindPhysical {
		// the simulator's proxy is this Mac's, which the caller restores; the certificate stays, so a
		// later capture needs no second approval
		return nil
	}
	if err := reachable(d); err != nil {
		return err
	}
	return devicectl(ctx, "device", "profile", "remove", "--device", d.ID, profileIdentifier)
}

func (p *Provider) ProxyState(ctx context.Context, d device.Device) (device.ProxyState, error) {
	// Neither simctl nor devicectl reports a device's proxy setting back, so what sims knows is what
	// it set; the session that set it holds that, not the device.
	return device.ProxyState{}, nil
}

func tempCert(pemBytes []byte) (string, func(), error) {
	f, err := os.CreateTemp("", "sims-ca-*.pem")
	if err != nil {
		return "", nil, err
	}
	if _, err := f.Write(pemBytes); err != nil {
		f.Close()
		os.Remove(f.Name())
		return "", nil, err
	}
	if err := f.Close(); err != nil {
		os.Remove(f.Name())
		return "", nil, err
	}
	return f.Name(), func() { os.Remove(f.Name()) }, nil
}

const profileIdentifier = "dev.sims.proxy"

// A .mobileconfig is a plist of payloads. This one carries the root certificate and a global HTTP
// proxy, which is what a phone needs to send its traffic here and let sims open the TLS.
const profileTemplate = `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>PayloadDisplayName</key><string>sims proxy</string>
  <key>PayloadDescription</key><string>Sends this device's web traffic to sims on {{.Addr}} and trusts its certificate.</string>
  <key>PayloadIdentifier</key><string>{{.Identifier}}</string>
  <key>PayloadType</key><string>Configuration</string>
  <key>PayloadUUID</key><string>{{.ProfileUUID}}</string>
  <key>PayloadVersion</key><integer>1</integer>
  <key>PayloadRemovalDisallowed</key><false/>
  <key>PayloadContent</key>
  <array>
    <dict>
      <key>PayloadType</key><string>com.apple.security.root</string>
      <key>PayloadIdentifier</key><string>{{.Identifier}}.cert</string>
      <key>PayloadUUID</key><string>{{.CertUUID}}</string>
      <key>PayloadVersion</key><integer>1</integer>
      <key>PayloadDisplayName</key><string>{{.CertName}}</string>
      <key>PayloadCertificateFileName</key><string>sims-proxy-ca.cer</string>
      <key>PayloadContent</key>
      <data>{{.CertBase64}}</data>
    </dict>
    <dict>
      <key>PayloadType</key><string>com.apple.wifi.managed</string>
      <key>PayloadIdentifier</key><string>{{.Identifier}}.wifi</string>
      <key>PayloadUUID</key><string>{{.WiFiUUID}}</string>
      <key>PayloadVersion</key><integer>1</integer>
      <key>PayloadDisplayName</key><string>sims proxy</string>
      <key>SSID_STR</key><string>{{.SSID}}</string>
      <key>AutoJoin</key><true/>
      <key>ProxyType</key><string>Manual</string>
      <key>ProxyServer</key><string>{{.Host}}</string>
      <key>ProxyServerPort</key><integer>{{.Port}}</integer>
    </dict>
  </array>
</dict>
</plist>
`

type profileData struct {
	Identifier  string
	ProfileUUID string
	CertUUID    string
	WiFiUUID    string
	CertName    string
	CertBase64  string
	Host        string
	Port        int
	Addr        string
	SSID        string
}

func writeProfile(t device.ProxyTarget) (string, func(), error) {
	if t.SSID == "" {
		return "", nil, errors.New("a phone takes its proxy from the wifi network it is on; sims needs that network's name")
	}
	uuid := func() (string, error) {
		var b [16]byte
		if _, err := rand.Read(b[:]); err != nil {
			return "", err
		}
		b[6] = (b[6] & 0x0f) | 0x40
		b[8] = (b[8] & 0x3f) | 0x80
		h := hex.EncodeToString(b[:])
		return strings.ToUpper(h[0:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:]), nil
	}
	data := profileData{
		Identifier: profileIdentifier,
		CertName:   t.CertName,
		CertBase64: base64.StdEncoding.EncodeToString(t.CACertDER),
		Host:       t.Host,
		Port:       t.Port,
		Addr:       t.Addr(),
		SSID:       t.SSID,
	}
	var err error
	if data.ProfileUUID, err = uuid(); err != nil {
		return "", nil, err
	}
	if data.CertUUID, err = uuid(); err != nil {
		return "", nil, err
	}
	if data.WiFiUUID, err = uuid(); err != nil {
		return "", nil, err
	}
	tmpl, err := template.New("profile").Parse(profileTemplate)
	if err != nil {
		return "", nil, err
	}
	path := filepath.Join(os.TempDir(), fmt.Sprintf("sims-proxy-%s.mobileconfig", shortHash(t.CACertDER)))
	f, err := os.Create(path)
	if err != nil {
		return "", nil, err
	}
	if err := tmpl.Execute(f, data); err != nil {
		f.Close()
		os.Remove(path)
		return "", nil, err
	}
	if err := f.Close(); err != nil {
		os.Remove(path)
		return "", nil, err
	}
	return path, func() { os.Remove(path) }, nil
}

func shortHash(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:4])
}
