//go:build darwin

package desktop

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const profileXML = `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>AppIDName</key>
	<string>XC com devsisters test</string>
	<key>DeveloperCertificates</key>
	<array>
		<data>MIIF</data>
	</array>
	<key>Entitlements</key>
	<dict>
		<key>get-task-allow</key>
		<true/>
		<key>application-identifier</key>
		<string>R23YV6Y65Y.com.devsisters.test</string>
	</dict>
	<key>ExpirationDate</key>
	<date>2027-03-25T07:00:22Z</date>
	<key>Name</key>
	<string>com.devsisters.test AdHoc</string>
	<key>ProvisionedDevices</key>
	<array>
		<string>00008120-0014744E0EEB401E</string>
		<string>00006021-001C098C11EBC01E</string>
	</array>
	<key>TeamIdentifier</key>
	<array>
		<string>R23YV6Y65Y</string>
	</array>
	<key>Version</key>
	<integer>1</integer>
</dict>
</plist>`

// The profile is CMS-signed, so the plist sits between binary bytes; it has to be read from there.
func signedProfile(xml string) []byte {
	return append(append([]byte{0x30, 0x82, 0x1a, 0x2b, 0x06, 0x09, 0x2a, 0x86}, xml...), 0x00, 0x31, 0x82, 0x0b, 0x8f)
}

func TestProfileIsReadOutOfItsSignature(t *testing.T) {
	p, err := parseProfile(signedProfile(profileXML))
	if err != nil {
		t.Fatal(err)
	}
	if p.Name != "com.devsisters.test AdHoc" {
		t.Errorf("name = %q", p.Name)
	}
	if want := time.Date(2027, 3, 25, 7, 0, 22, 0, time.UTC); !p.Expires.Equal(want) {
		t.Errorf("expires = %s", p.Expires)
	}
	if p.AllDevices {
		t.Error("an ad hoc profile reported as enterprise")
	}
	if len(p.Devices) != 2 || p.Devices[1] != "00006021-001C098C11EBC01E" {
		t.Errorf("devices = %v", p.Devices)
	}
	if _, err := parseProfile([]byte("no plist here")); err == nil {
		t.Error("bytes with no plist parsed")
	}
}

func TestEnterpriseProfileProvisionsEveryMac(t *testing.T) {
	xml := strings.Replace(profileXML, "<key>ProvisionedDevices</key>", "<key>ProvisionsAllDevices</key>\n\t<true/>\n\t<key>ProvisionedDevices</key>", 1)
	p, err := parseProfile(signedProfile(xml))
	if err != nil {
		t.Fatal(err)
	}
	if !p.AllDevices {
		t.Error("ProvisionsAllDevices was not read")
	}
	if err := provisionedForThisMac(context.Background(), "x.ipa", signedProfile(xml)); err != nil {
		t.Errorf("an enterprise build was refused: %v", err)
	}
}

// The installer accepts an expired build and the launch then fails with an error naming no cause,
// so the refusal has to happen here and say what to do.
func TestExpiredProfileIsRefusedBeforeInstalling(t *testing.T) {
	xml := strings.Replace(profileXML, "2027-03-25T07:00:22Z", "2025-03-11T04:31:32Z", 1)
	err := provisionedForThisMac(context.Background(), "pot.app", signedProfile(xml))
	if err == nil {
		t.Fatal("an expired profile was accepted")
	}
	for _, want := range []string{"pot.app", "expired on 2025-03-11", "com.devsisters.test AdHoc"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("%q does not say %q", err, want)
		}
	}
}

func TestBuildNotProvisionedForThisMacIsRefused(t *testing.T) {
	udid := provisioningUDID(context.Background())
	if udid == "" {
		t.Skip("this Mac reports no provisioning UDID")
	}
	xml := strings.Replace(profileXML, "00006021-001C098C11EBC01E", "00006021-000000000000001E", 1)
	err := provisionedForThisMac(context.Background(), "x.ipa", signedProfile(xml))
	if err == nil {
		t.Fatal("a build provisioned for two other devices was accepted")
	}
	for _, want := range []string{"2 devices", udid, "TestFlight"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("%q does not say %q", err, want)
		}
	}

	xml = strings.Replace(profileXML, "00006021-001C098C11EBC01E", strings.ToLower(udid), 1)
	if err := provisionedForThisMac(context.Background(), "x.ipa", signedProfile(xml)); err != nil {
		t.Errorf("a build provisioned for this Mac was refused: %v", err)
	}
}

// An App Store build carries no profile; the installer checks that one itself.
func TestBuildWithoutProfileIsLeftToTheInstaller(t *testing.T) {
	if err := provisionedForThisMac(context.Background(), "x.ipa", nil); err != nil {
		t.Error(err)
	}
}

func writeIPA(t *testing.T, dir string, files map[string]string) string {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, body := range files {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, "app.ipa")
	if err := os.WriteFile(p, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

const infoPlist = `<?xml version="1.0" encoding="UTF-8"?>
<plist version="1.0"><dict>
<key>CFBundleIdentifier</key><string>com.devsisters.test</string>
<key>CFBundleDisplayName</key><string>Nightmare</string>
<key>CFBundleExecutable</key><string>NightmareBin</string>
</dict></plist>`

// Only the app's own Info.plist and profile count: an extension a level deeper has both of its own.
func TestIPAIsReadFromTheAppsOwnTopLevel(t *testing.T) {
	ipa := writeIPA(t, t.TempDir(), map[string]string{
		"Payload/Nightmare.app/PlugIns/Widget.appex/Info.plist":               strings.Replace(infoPlist, "com.devsisters.test", "com.devsisters.test.widget", 1),
		"Payload/Nightmare.app/PlugIns/Widget.appex/embedded.mobileprovision": "widget profile",
		"Payload/Nightmare.app/Info.plist":                                    infoPlist,
		"Payload/Nightmare.app/embedded.mobileprovision":                      "app profile",
	})
	b, err := readIPA(ipa)
	if err != nil {
		t.Fatal(err)
	}
	if b.id != "com.devsisters.test" || b.name != "Nightmare" || string(b.profile) != "app profile" {
		t.Errorf("read %+v", b)
	}
}

func TestZipWithoutAnAppIsNotAnIPA(t *testing.T) {
	ipa := writeIPA(t, t.TempDir(), map[string]string{"README": "not an app"})
	if _, err := readIPA(ipa); err == nil || !strings.Contains(err.Error(), "not an iPhone or iPad app") {
		t.Errorf("err = %v", err)
	}
}

// An .app is handed over as an .ipa the installer takes, zipped in place without copying the bundle.
func TestAppBundleIsZippedUnderPayload(t *testing.T) {
	dir := t.TempDir()
	app := filepath.Join(dir, "Nightmare.app")
	if err := os.MkdirAll(filepath.Join(app, "Frameworks"), 0o755); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{"Info.plist": infoPlist, "NightmareBin": "\xcf\xfa\xed\xfe", "Frameworks/A.dylib": "lib"} {
		if err := os.WriteFile(filepath.Join(app, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	ipa, done, err := ipaFor(context.Background(), app)
	if err != nil {
		t.Fatal(err)
	}
	defer done()
	b, err := readIPA(ipa)
	if err != nil {
		t.Fatal(err)
	}
	if b.id != "com.devsisters.test" {
		t.Errorf("read %+v", b)
	}
	zr, err := zip.OpenReader(ipa)
	if err != nil {
		t.Fatal(err)
	}
	defer zr.Close()
	var names []string
	for _, f := range zr.File {
		names = append(names, f.Name)
	}
	for _, want := range []string{"Payload/Nightmare.app/NightmareBin", "Payload/Nightmare.app/Frameworks/A.dylib"} {
		found := false
		for _, n := range names {
			found = found || n == want
		}
		if !found {
			t.Errorf("%s is not in the ipa: %v", want, names)
		}
	}
	done()
	if _, err := os.Stat(ipa); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the temporary ipa was left behind: %v", err)
	}
}

// A wrapper already installed here is zipped from the bundle inside it, which is the app as built.
func TestInstalledWrapperIsZippedFromItsInnerBundle(t *testing.T) {
	dir := t.TempDir()
	inner := filepath.Join(dir, "Nightmare.app", "Wrapper", "Nightmare.app")
	if err := os.MkdirAll(inner, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(inner, "Info.plist"), []byte(infoPlist), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("Wrapper/Nightmare.app", filepath.Join(dir, "Nightmare.app", "WrappedBundle")); err != nil {
		t.Fatal(err)
	}
	ipa, done, err := ipaFor(context.Background(), filepath.Join(dir, "Nightmare.app"))
	if err != nil {
		t.Fatal(err)
	}
	defer done()
	if b, err := readIPA(ipa); err != nil || b.id != "com.devsisters.test" {
		t.Errorf("read %+v, %v", b, err)
	}
}

func TestMacAppAndStrayFilesAreRefused(t *testing.T) {
	dir := t.TempDir()
	mac := filepath.Join(dir, "Finder.app", "Contents")
	if err := os.MkdirAll(mac, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(mac, "Info.plist"), []byte(infoPlist), 0o644); err != nil {
		t.Fatal(err)
	}
	cases := map[string]string{
		filepath.Join(dir, "Finder.app"): "is a Mac app",
		filepath.Join(dir, "a.apk"):      "not an .ipa or an .app",
		filepath.Join(dir, "empty.app"):  "no Info.plist",
	}
	if err := os.Mkdir(filepath.Join(dir, "empty.app"), 0o755); err != nil {
		t.Fatal(err)
	}
	for p, want := range cases {
		_, done, err := ipaFor(context.Background(), p)
		done()
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: err = %v, want %q", filepath.Base(p), err, want)
		}
	}
}

// The identifier a profile lists a Mac under is what System Information shows, and it has the
// shape of a device UDID rather than a UUID.
func TestProvisioningUDIDHasTheShapeOfOne(t *testing.T) {
	udid := provisioningUDID(context.Background())
	if udid == "" {
		t.Skip("this Mac reports no provisioning UDID")
	}
	if len(udid) != 25 || udid[8] != '-' {
		t.Errorf("udid = %q", udid)
	}
}

// Every iPad app installed here is listed with the executable its log is filtered by and marked as
// what it is, so a reader can tell a Designed for iPad row from a Mac app's.
func TestInstalledIPadAppsCarryTheirExecutable(t *testing.T) {
	var wrappers []string
	for _, dir := range applicationDirs() {
		matches, _ := filepath.Glob(filepath.Join(dir, "*.app"))
		for _, app := range matches {
			if isWrapper(app) {
				wrappers = append(wrappers, app)
			}
		}
	}
	if len(wrappers) == 0 {
		t.Skip("no iPad app installed on this Mac")
	}
	apps := installedApps(context.Background())
	perID := map[string]int{}
	for _, w := range wrappers {
		perID[bundleInfo(w).id]++
	}
	for _, w := range wrappers {
		facts := bundleInfo(w)
		found := false
		for _, a := range apps {
			if a.BundleID != facts.id {
				continue
			}
			found = true
			if a.Source != sourceIPad {
				t.Errorf("%s: source = %q", a.Name, a.Source)
			}
			if facts.executable != "" && a.Process != facts.executable {
				t.Errorf("%s: process = %q, want the executable %q", a.Name, a.Process, facts.executable)
			}
		}
		if !found {
			t.Errorf("%s is installed but not listed", filepath.Base(w))
		}
		// the installer leaves "<Name> 2.app" beside an older copy, and then either answers to the id
		if got := findWrapper(facts.id); perID[facts.id] == 1 && got != w {
			t.Errorf("findWrapper(%s) = %q, want %q", facts.id, got, w)
		}
	}
}
