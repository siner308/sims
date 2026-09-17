package ios

import (
	"archive/zip"
	"bytes"
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"
)

// macho64 builds the smallest arm64 Mach-O the reader accepts, carrying one LC_BUILD_VERSION.
func macho64(t *testing.T, platform uint32) []byte {
	t.Helper()
	const cmdSize = 24
	var load bytes.Buffer
	binary.Write(&load, binary.LittleEndian, uint32(lcBuildVersion))
	binary.Write(&load, binary.LittleEndian, uint32(cmdSize))
	binary.Write(&load, binary.LittleEndian, platform)
	binary.Write(&load, binary.LittleEndian, uint32(0))
	binary.Write(&load, binary.LittleEndian, uint32(0))
	binary.Write(&load, binary.LittleEndian, uint32(0))

	var b bytes.Buffer
	binary.Write(&b, binary.LittleEndian, uint32(0xfeedfacf)) // Magic64
	binary.Write(&b, binary.LittleEndian, uint32(0x0100000c)) // CpuArm64
	binary.Write(&b, binary.LittleEndian, uint32(0))
	binary.Write(&b, binary.LittleEndian, uint32(2)) // TypeExec
	binary.Write(&b, binary.LittleEndian, uint32(1))
	binary.Write(&b, binary.LittleEndian, uint32(load.Len()))
	binary.Write(&b, binary.LittleEndian, uint32(0))
	binary.Write(&b, binary.LittleEndian, uint32(0))
	b.Write(load.Bytes())
	return b.Bytes()
}

func writeApp(t *testing.T, platform uint32) string {
	t.Helper()
	app := filepath.Join(t.TempDir(), "Demo.app")
	if err := os.MkdirAll(app, 0o755); err != nil {
		t.Fatal(err)
	}
	// A resource sits alongside the executable, and only the Mach-O should be read.
	os.WriteFile(filepath.Join(app, "Info.plist"), []byte("binary plist would go here"), 0o644)
	if err := os.WriteFile(filepath.Join(app, "Demo"), macho64(t, platform), 0o755); err != nil {
		t.Fatal(err)
	}
	return app
}

func writeIPA(t *testing.T, platform uint32) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "Demo.ipa")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	zw := zip.NewWriter(f)
	for name, body := range map[string][]byte{
		"Payload/Demo.app/Demo":                             macho64(t, platform),
		"Payload/Demo.app/Info.plist":                       []byte("plist"),
		"Payload/Demo.app/Frameworks/Other.framework/Other": macho64(t, platformIOS),
	} {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		w.Write(body)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestDeviceOnlyBuild(t *testing.T) {
	const simulator = 7
	if !DeviceOnlyBuild(writeApp(t, platformIOS)) {
		t.Error("an app built for iOS should be reported as device-only")
	}
	if DeviceOnlyBuild(writeApp(t, simulator)) {
		t.Error("a simulator build should install")
	}
	if !DeviceOnlyBuild(writeIPA(t, platformIOS)) {
		t.Error("an ipa built for iOS should be reported as device-only")
	}
	if DeviceOnlyBuild(writeIPA(t, simulator)) {
		t.Error("a simulator ipa should install")
	}
}

func TestDeviceOnlyBuild_UnreadableBundlesInstallAnyway(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "notmacho"), []byte("just bytes"), 0o644)
	for _, path := range []string{dir, filepath.Join(dir, "missing.app"), filepath.Join(dir, "missing.ipa")} {
		if DeviceOnlyBuild(path) {
			t.Errorf("%s cannot be read, so it must not block the install", path)
		}
	}
}

func TestIPAExecutablePath(t *testing.T) {
	yes := []string{"Payload/Demo.app/Demo", "Payload/My Game.app/My Game"}
	no := []string{
		"Payload/Demo.app/Info.plist",
		"Payload/Demo.app/Frameworks/UnityFramework.framework/UnityFramework",
		"Payload/Demo.app/PlugIns/Widget.appex/Widget",
		"Payload/Demo.app/",
		"Symbols/Demo",
	}
	for _, n := range yes {
		if !ipaExecutablePath(n) {
			t.Errorf("%q should be the app executable", n)
		}
	}
	for _, n := range no {
		if ipaExecutablePath(n) {
			t.Errorf("%q should not be taken for the app executable", n)
		}
	}
}
