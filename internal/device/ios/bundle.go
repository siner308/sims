package ios

import (
	"archive/zip"
	"debug/macho"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// Load command numbers and platform ids as <mach-o/loader.h> defines them.
const (
	lcVersionMinIPhoneOS = 0x25
	lcBuildVersion       = 0x32

	platformIOS = 2
)

// DeviceOnlyBuild reports whether the app at path is built for a real iPhone, which a simulator installs without complaint and then refuses to launch.
// A bundle whose platform cannot be read is not reported as device-only: a check that cannot see is no reason to block an install that would have worked.
func DeviceOnlyBuild(path string) bool {
	platforms, err := bundlePlatforms(path)
	if err != nil || len(platforms) == 0 {
		return false
	}
	for _, p := range platforms {
		if p != platformIOS {
			return false
		}
	}
	return true
}

func bundlePlatforms(p string) ([]uint32, error) {
	if strings.EqualFold(filepath.Ext(p), ".ipa") {
		return ipaPlatforms(p)
	}
	return appPlatforms(p)
}

func appPlatforms(app string) ([]uint32, error) {
	entries, err := os.ReadDir(app)
	if err != nil {
		return nil, err
	}
	for _, e := range entries {
		if e.IsDir() || strings.Contains(e.Name(), ".") {
			continue
		}
		f, err := os.Open(filepath.Join(app, e.Name()))
		if err != nil {
			continue
		}
		platforms, err := machoPlatforms(f)
		f.Close()
		if err == nil {
			return platforms, nil
		}
	}
	return nil, errors.New("no Mach-O executable in the app bundle")
}

func ipaPlatforms(p string) ([]uint32, error) {
	zr, err := zip.OpenReader(p)
	if err != nil {
		return nil, err
	}
	defer zr.Close()
	for _, f := range zr.File {
		if !ipaExecutablePath(f.Name) {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return nil, err
		}
		body, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			return nil, err
		}
		if platforms, err := machoPlatforms(byteReaderAt(body)); err == nil {
			return platforms, nil
		}
	}
	return nil, errors.New("no Mach-O executable in the ipa")
}

// ipaExecutablePath matches Payload/<Name>.app/<file>: the app's own executable sits directly inside the bundle, while a framework, an extension or a resource sits a directory deeper.
func ipaExecutablePath(name string) bool {
	rest, ok := strings.CutPrefix(name, "Payload/")
	if !ok {
		return false
	}
	dir, file := path.Split(rest)
	if file == "" || strings.Contains(file, ".") {
		return false
	}
	dir = strings.TrimSuffix(dir, "/")
	return strings.HasSuffix(dir, ".app") && !strings.Contains(dir, "/")
}

func machoPlatforms(r io.ReaderAt) ([]uint32, error) {
	if f, err := macho.NewFile(r); err == nil {
		defer f.Close()
		return loadPlatforms(f), nil
	}
	ff, err := macho.NewFatFile(r)
	if err != nil {
		return nil, err
	}
	defer ff.Close()
	var out []uint32
	for _, a := range ff.Arches {
		out = append(out, loadPlatforms(a.File)...)
	}
	return out, nil
}

func loadPlatforms(f *macho.File) []uint32 {
	var out []uint32
	for _, l := range f.Loads {
		raw, ok := l.(macho.LoadBytes)
		if !ok {
			continue
		}
		b := raw.Raw()
		if len(b) < 12 {
			continue
		}
		switch binary.LittleEndian.Uint32(b[0:4]) {
		case lcBuildVersion:
			out = append(out, binary.LittleEndian.Uint32(b[8:12]))
		case lcVersionMinIPhoneOS:
			out = append(out, platformIOS)
		}
	}
	return out
}

type byteReaderAt []byte

func (b byteReaderAt) ReadAt(p []byte, off int64) (int, error) {
	if off < 0 || off >= int64(len(b)) {
		return 0, io.EOF
	}
	n := copy(p, b[off:])
	if n < len(p) {
		return n, io.EOF
	}
	return n, nil
}

func deviceBuildError(path string) error {
	return fmt.Errorf("%s is built for a real iPhone, which a simulator cannot run; build it against the simulator SDK, or install it on a device", filepath.Base(path))
}
