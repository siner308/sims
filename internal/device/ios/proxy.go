package ios

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/xml"
	"errors"
	"fmt"
	"os"
	"strings"
	"text/template"

	"github.com/siner308/sims/internal/device"
)

// A simulator has no network settings of its own: it uses this machine's, so pointing it at a proxy
// means setting the macOS system proxy, and every other app on the Mac follows. A phone carries its
// own settings and takes a configuration profile instead, which Safari on the phone fetches from a URL sims serves.
// Nothing installs one from here: devicectl in Xcode 26.6 has no profile command (its device subcommands are copy, info, install app, process, reboot, sysdiagnose and uninstall app), and libimobiledevice ships none.

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

func (p *Provider) installProfile(ctx context.Context, d device.Device, t device.ProxyTarget) ([]device.ProxyStep, error) {
	if err := reachable(d); err != nil {
		return nil, err
	}
	if t.Installed {
		return []device.ProxyStep{{
			Title:  "profile",
			Detail: "already on the phone from an earlier capture; it sends the phone's traffic on " + t.SSID + " to " + t.Addr() + " and trusts " + t.CertName + ". Nothing to tap; if no traffic shows, the phone lost the profile: redo the setup with --setup",
		}}, nil
	}
	if len(t.CACert) == 0 {
		return nil, errors.New("a phone needs the certificate to install a proxy profile")
	}
	if t.Signer == nil {
		return nil, errors.New("a phone's profile has to be signed, and no signer was given")
	}
	if t.Publish == nil {
		return nil, errors.New("a phone fetches its profile from a URL, and nothing is serving one")
	}
	body, err := buildProfile(t, t.Signer)
	if err != nil {
		return nil, err
	}
	url, err := t.Publish(profileFileName, profileContentType, body)
	if err != nil {
		return nil, err
	}
	// a certificate-only profile carries no proxy payload, so it must not promise to send traffic
	// anywhere: a phone told to use a proxy at nothing has no working network
	carries := "it carries the certificate and points the phone's traffic on " + t.SSID + " at " + t.Addr()
	if t.CertOnly() {
		carries = "it carries the certificate and changes no network setting"
	}
	download := device.ProxyStep{Title: "download", Detail: "Safari on the phone is open on it; iOS asks before taking a profile", Manual: true, Todo: []string{
		"tap Allow when Safari asks to download a configuration profile, then Close",
	}}
	if err := p.runDevicectl(ctx, "device", "process", "launch", "--device", d.ID, "--payload-url", url, "com.apple.mobilesafari"); err != nil {
		download = device.ProxyStep{Title: "download", Detail: "sims could not open Safari on the phone (" + err.Error() + "); open the address there yourself, on the same wifi as this Mac", Manual: true, Todo: []string{
			"type " + url + " into Safari's address bar",
			"tap Allow when Safari asks to download a configuration profile, then Close",
		}}
	}
	return []device.ProxyStep{
		{Title: "profile", Detail: "ready at " + url + "; " + carries},
		download,
		{Title: "approve", Detail: "then install it; iOS only does that when you ask", Manual: true, Todo: []string{
			`open Settings (sims offers to do this once the profile is downloaded) and tap "Profile Downloaded" at the top, or General > VPN & Device Management > sims proxy`,
			"tap Install at the top right and enter the passcode",
			"tap Install on the warning about the root certificate, Install once more to confirm, then Done",
		}},
		{Title: "trust", Detail: "then switch the certificate on, once; iOS installs a root without trusting it, and HTTPS shows as tunnel until you do. The profile stays on the phone and later captures reuse it", Manual: true, Todo: []string{
			"open Settings > General > About > Certificate Trust Settings",
			`switch "` + t.CertName + `" on and tap Continue`,
		}},
	}, nil
}

func (p *Provider) ClearProxy(ctx context.Context, d device.Device) error {
	if d.Kind != device.KindPhysical {
		// the simulator's proxy is this Mac's, which the caller restores; the certificate stays, so a
		// later capture needs no second approval
		return nil
	}
	// the profile stays on purpose: it names a fixed port, the next capture reuses it, and no tool on this Mac could remove it anyway
	return nil
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

const (
	profileIdentifier  = "dev.sims.proxy"
	profileFileName    = "sims-proxy.mobileconfig"
	profileContentType = "application/x-apple-aspen-config"
)

// A .mobileconfig is a plist of payloads. This one carries the root certificate and a global HTTP
// proxy, which is what a phone needs to send its traffic here and let sims open the TLS.
const profileTemplate = `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>PayloadDisplayName</key><string>sims proxy</string>
  <key>PayloadDescription</key><string>{{if .CertOnly}}Trusts the certificate sims signs with.{{else}}Sends this device's web traffic to sims on {{.Addr}} and trusts its certificate.{{end}}</string>
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
    {{if not .CertOnly}}<dict>
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
    </dict>{{end}}
  </array>
</dict>
</plist>
`

type profileData struct {
	// CertOnly leaves the proxy payload out: the profile then only adds a trusted root.
	CertOnly    bool
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

// signer turns a profile into the CMS envelope iOS expects. The capture passes the CA; a nil signer
// writes the profile unsigned, which only the tests do.
type signer interface {
	SignCMS(data []byte) ([]byte, error)
}

func writeProfile(t device.ProxyTarget) (string, func(), error) {
	return writeSignedProfile(t, nil)
}

func writeSignedProfile(t device.ProxyTarget, sign signer) (string, func(), error) {
	out, err := buildProfile(t, sign)
	if err != nil {
		return "", nil, err
	}
	// a path derived from the CA alone is the same for every capture, so two running at once write
	// and delete each other's file
	f, err := os.CreateTemp("", "sims-proxy-*.mobileconfig")
	if err != nil {
		return "", nil, err
	}
	path := f.Name()
	if _, err := f.Write(out); err != nil {
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

func buildProfile(t device.ProxyTarget, sign signer) ([]byte, error) {
	// the wifi name scopes the proxy payload; a certificate-only profile has no proxy payload and
	// so needs no network to attach it to
	if t.SSID == "" && !t.CertOnly() {
		return nil, errors.New("a phone takes its proxy from the wifi network it is on, and this Mac is not on one; " +
			"pass the phone's wifi name with --ssid, join that network on this Mac, or use sims proxy ca <phone> --install and set the proxy on the phone by hand")
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
		CertOnly:   t.CertOnly(),
		Identifier: profileIdentifier,
		CertName:   xmlEscape(t.CertName),
		CertBase64: base64.StdEncoding.EncodeToString(t.CACertDER),
		Host:       xmlEscape(t.Host),
		Port:       t.Port,
		Addr:       xmlEscape(t.Addr()),
		// a wifi name is whatever its owner typed; & or < would otherwise make the plist unparseable
		SSID: xmlEscape(t.SSID),
	}
	var err error
	if data.ProfileUUID, err = uuid(); err != nil {
		return nil, err
	}
	if data.CertUUID, err = uuid(); err != nil {
		return nil, err
	}
	if data.WiFiUUID, err = uuid(); err != nil {
		return nil, err
	}
	tmpl, err := template.New("profile").Parse(profileTemplate)
	if err != nil {
		return nil, err
	}
	var body bytes.Buffer
	if err := tmpl.Execute(&body, data); err != nil {
		return nil, err
	}
	out := body.Bytes()
	if sign != nil {
		out, err = sign.SignCMS(out)
		if err != nil {
			return nil, fmt.Errorf("could not sign the profile: %w", err)
		}
	}
	return out, nil
}

func xmlEscape(s string) string {
	var b strings.Builder
	if err := xml.EscapeText(&b, []byte(s)); err != nil {
		return s
	}
	return b.String()
}

func shortHash(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:4])
}
