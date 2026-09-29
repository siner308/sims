package desktop

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"
	"time"
)

// macOS installs an iPhone or iPad app (Xcode's "My Mac (Designed for iPad)" destination) as a root-owned wrapper:
// <Name>.app/Wrapper/<Name>.app is the iOS bundle as built, WrappedBundle links to it, and the wrapper has no Contents.

// installerApp claims .ipa files (com.apple.IPAInstaller); it installs without a prompt and exits when done, so `open -W` is the wait.
const installerApp = "iOS App Installer"

const installWait = 15 * time.Second

type iosBundle struct {
	id, name string
	profile  []byte // embedded.mobileprovision, nil for an App Store build
}

func installApp(ctx context.Context, p string) error {
	ipa, done, err := ipaFor(ctx, p)
	if err != nil {
		return err
	}
	defer done()
	b, err := readIPA(ipa)
	if err != nil {
		return fmt.Errorf("%s: %w", filepath.Base(p), err)
	}
	if err := provisionedForThisMac(ctx, filepath.Base(p), b.profile); err != nil {
		return err
	}
	// the installer does not replace an app; given one already in Applications it adds
	// "<Name> 2.app" beside it, and both answer to the bundle identifier from then on
	replaced := ""
	if old := findWrapper(b.id); old != "" {
		if err := trash(ctx, old); err != nil {
			return fmt.Errorf("could not move the installed copy of %s aside: %w", b.id, err)
		}
		replaced = "; the copy that was installed is in the Trash"
	}
	out, err := exec.CommandContext(ctx, "open", "-W", "-a", installerApp, ipa).CombinedOutput()
	if err != nil {
		return fmt.Errorf("macOS could not run its %s: %w: %s (iPhone and iPad apps run only on an Apple silicon Mac)%s", installerApp, err, strings.TrimSpace(string(out)), replaced)
	}
	deadline := time.Now().Add(installWait)
	for findWrapper(b.id) == "" {
		if time.Now().After(deadline) || ctx.Err() != nil {
			return fmt.Errorf("%s ran but %s did not appear in Applications; a build the installer rejects shows an alert on this Mac%s", installerApp, b.id, replaced)
		}
		time.Sleep(300 * time.Millisecond)
	}
	return nil
}

// The installer takes only an .ipa, so a bundle is zipped under Payload, the layout Xcode exports.
func ipaFor(ctx context.Context, p string) (ipa string, done func(), err error) {
	done = func() {}
	switch strings.ToLower(filepath.Ext(p)) {
	case ".ipa":
		return p, done, nil
	case ".app":
	default:
		return "", done, fmt.Errorf("%s is not an .ipa or an .app; an iPhone or iPad app is what this Mac installs", filepath.Base(p))
	}
	if _, err := os.Stat(p); err != nil {
		return "", done, err
	}
	bundle := p
	if inner, err := os.Readlink(filepath.Join(p, "WrappedBundle")); err == nil {
		bundle = filepath.Join(p, inner)
	}
	if _, err := os.Stat(filepath.Join(bundle, "Contents", "Info.plist")); err == nil {
		return "", done, fmt.Errorf("%s is a Mac app, which runs from wherever it is; sims installs iPhone and iPad apps here", filepath.Base(p))
	}
	if _, err := os.Stat(filepath.Join(bundle, "Info.plist")); err != nil {
		return "", done, fmt.Errorf("%s has no Info.plist where an iPhone or iPad app keeps one", filepath.Base(p))
	}
	dir, err := os.MkdirTemp("", "sims-ipa-*")
	if err != nil {
		return "", done, err
	}
	done = func() { os.RemoveAll(dir) }
	abs, err := filepath.Abs(bundle)
	if err != nil {
		return "", done, err
	}
	// a link under Payload costs no copy: zip follows it and stores the bundle's own files
	if err := os.Mkdir(filepath.Join(dir, "Payload"), 0o755); err != nil {
		return "", done, err
	}
	if err := os.Symlink(abs, filepath.Join(dir, "Payload", filepath.Base(abs))); err != nil {
		return "", done, err
	}
	ipa = filepath.Join(dir, strings.TrimSuffix(filepath.Base(abs), ".app")+".ipa")
	cmd := exec.CommandContext(ctx, "zip", "-qr", "-X", ipa, "Payload")
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		return "", done, fmt.Errorf("zip %s: %w: %s", filepath.Base(p), err, strings.TrimSpace(string(out)))
	}
	return ipa, done, nil
}

// Only Payload/<Name>.app's own top level is read: a framework or an extension a level deeper carries a profile of its own.
func readIPA(ipa string) (iosBundle, error) {
	zr, err := zip.OpenReader(ipa)
	if err != nil {
		return iosBundle{}, err
	}
	defer zr.Close()
	var b iosBundle
	var plist []byte
	for _, f := range zr.File {
		dir, file := path.Split(strings.TrimPrefix(f.Name, "Payload/"))
		dir = strings.TrimSuffix(dir, "/")
		if !strings.HasPrefix(f.Name, "Payload/") || !strings.HasSuffix(dir, ".app") || strings.Contains(dir, "/") {
			continue
		}
		switch file {
		case "Info.plist":
			body, err := zipFile(f)
			if err != nil {
				return iosBundle{}, err
			}
			if plist, err = plistXML(body); err != nil {
				return iosBundle{}, fmt.Errorf("Info.plist: %w", err)
			}
		case "embedded.mobileprovision":
			if b.profile, err = zipFile(f); err != nil {
				return iosBundle{}, err
			}
		}
	}
	if plist == nil {
		return iosBundle{}, errors.New("no Payload/<Name>.app/Info.plist inside; this is not an iPhone or iPad app")
	}
	b.id = plistString(plist, "CFBundleIdentifier")
	if b.id == "" {
		return iosBundle{}, errors.New("Info.plist names no CFBundleIdentifier")
	}
	b.name = plistString(plist, "CFBundleDisplayName")
	if b.name == "" {
		b.name = plistString(plist, "CFBundleName")
	}
	return b, nil
}

func zipFile(f *zip.File) ([]byte, error) {
	rc, err := f.Open()
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	return io.ReadAll(rc)
}

type provisioningProfile struct {
	Name       string
	Expires    time.Time
	AllDevices bool
	Devices    []string
}

// The profile is CMS-signed with its plist in clear text, so the XML is cut out unverified: whether it is genuine is the installer's question.
func parseProfile(raw []byte) (provisioningProfile, error) {
	start := bytes.Index(raw, []byte("<?xml"))
	end := bytes.LastIndex(raw, []byte("</plist>"))
	if start < 0 || end < start {
		return provisioningProfile{}, errors.New("no plist inside the provisioning profile")
	}
	dict, err := plistDict(raw[start : end+len("</plist>")])
	if err != nil {
		return provisioningProfile{}, err
	}
	p := provisioningProfile{Name: dict.strings["Name"], AllDevices: dict.bools["ProvisionsAllDevices"], Devices: dict.arrays["ProvisionedDevices"]}
	if d := dict.strings["ExpirationDate"]; d != "" {
		p.Expires, _ = time.Parse(time.RFC3339, d)
	}
	return p, nil
}

// The installer accepts a build whose profile has expired or does not list this Mac; an expired one then failed to launch with "Launchd job spawn failed", which names no cause.
// A build with no profile is an App Store one, which the installer checks itself.
func provisionedForThisMac(ctx context.Context, file string, profile []byte) error {
	if profile == nil {
		return nil
	}
	p, err := parseProfile(profile)
	if err != nil {
		return fmt.Errorf("%s: %w", file, err)
	}
	if !p.Expires.IsZero() && p.Expires.Before(time.Now()) {
		return fmt.Errorf("%s was signed with the provisioning profile %q, which expired on %s; a rebuild with a current profile will run", file, p.Name, p.Expires.Format("2006-01-02"))
	}
	if p.AllDevices {
		return nil
	}
	udid := provisioningUDID(ctx)
	if udid == "" {
		// a check that cannot see is no reason to refuse an install that would have worked
		return nil
	}
	for _, d := range p.Devices {
		if strings.EqualFold(d, udid) {
			return nil
		}
	}
	return fmt.Errorf("%s is provisioned for %d devices and this Mac is not one of them; add its provisioning UDID %s to the profile %q and rebuild, or use an App Store or TestFlight build", file, len(p.Devices), udid, p.Name)
}

// A profile lists a Mac under its provisioning UDID, which is not the platform UUID ioreg reports; System Information is where Apple shows it.
func provisioningUDID(ctx context.Context) string {
	out, err := exec.CommandContext(ctx, "system_profiler", "SPHardwareDataType").Output()
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(out), "\n") {
		if k, v, ok := strings.Cut(strings.TrimSpace(line), ":"); ok && k == "Provisioning UDID" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

func uninstallApp(ctx context.Context, bundleID string) error {
	if w := findWrapper(bundleID); w != "" {
		return trash(ctx, w)
	}
	for _, a := range installedApps(ctx) {
		if a.BundleID == bundleID {
			return fmt.Errorf("%s is a Mac app; sims removes only the iPad apps it installs on this machine: %w", a.Name, errors.ErrUnsupported)
		}
	}
	return fmt.Errorf("no iPhone or iPad app %s is installed on this Mac", bundleID)
}

func findWrapper(bundleID string) string {
	for _, dir := range applicationDirs() {
		matches, _ := filepath.Glob(filepath.Join(dir, "*.app"))
		for _, app := range matches {
			if !isWrapper(app) {
				continue
			}
			if facts := bundleInfo(app); facts.id == bundleID {
				return app
			}
		}
	}
	return ""
}

func isWrapper(app string) bool {
	_, err := os.Readlink(filepath.Join(app, "WrappedBundle"))
	return err == nil
}

func applicationDirs() []string {
	dirs := []string{"/Applications"}
	if home := homeApplications(); home != "" {
		dirs = append(dirs, home)
	}
	return dirs
}

// The Finder may remove what the user cannot: the installer writes a wrapper as root.
func trash(ctx context.Context, p string) error {
	abs, err := filepath.Abs(p)
	if err != nil {
		return err
	}
	quoted := strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(abs)
	script := fmt.Sprintf(`tell application "Finder" to delete POSIX file "%s"`, quoted)
	if out, err := exec.CommandContext(ctx, "osascript", "-e", script).CombinedOutput(); err != nil {
		return fmt.Errorf("move %s to the Trash: %w: %s", filepath.Base(p), err, strings.TrimSpace(string(out)))
	}
	return nil
}

type plistValues struct {
	strings map[string]string
	bools   map[string]bool
	arrays  map[string][]string
}

func plistDict(body []byte) (plistValues, error) {
	v := plistValues{strings: map[string]string{}, bools: map[string]bool{}, arrays: map[string][]string{}}
	dec := xml.NewDecoder(bytes.NewReader(body))
	depth, key := 0, ""
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			return v, nil
		}
		if err != nil {
			return v, err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "plist":
			case "dict":
				depth++
				if depth > 1 {
					if err := dec.Skip(); err != nil {
						return v, err
					}
					depth--
				}
			case "key":
				key, _ = text(dec, t)
			case "string", "date", "integer", "real":
				s, _ := text(dec, t)
				v.strings[key] = s
			case "true":
				v.bools[key] = true
			case "false":
				v.bools[key] = false
			case "array":
				v.arrays[key], err = stringArray(dec)
				if err != nil {
					return v, err
				}
			default:
				if err := dec.Skip(); err != nil {
					return v, err
				}
			}
		case xml.EndElement:
			if t.Name.Local == "dict" {
				depth--
			}
		}
	}
}

func text(dec *xml.Decoder, start xml.StartElement) (string, error) {
	var s string
	if err := dec.DecodeElement(&s, &start); err != nil {
		return "", err
	}
	return s, nil
}

func stringArray(dec *xml.Decoder) ([]string, error) {
	var out []string
	for {
		tok, err := dec.Token()
		if err != nil {
			return out, err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			if t.Name.Local == "string" {
				s, err := text(dec, t)
				if err != nil {
					return out, err
				}
				out = append(out, s)
				continue
			}
			if err := dec.Skip(); err != nil {
				return out, err
			}
		case xml.EndElement:
			if t.Name.Local == "array" {
				return out, nil
			}
		}
	}
}
