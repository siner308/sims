package android

import (
	"cmp"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
)

var isWindows = runtime.GOOS == "windows"

type sdk struct {
	root string
}

func locateSDK() (sdk, error) {
	root := cmp.Or(os.Getenv("ANDROID_HOME"), os.Getenv("ANDROID_SDK_ROOT"), defaultSDKRoot())
	if root == "" {
		return sdk{}, errors.New("android sdk not found: set ANDROID_HOME")
	}
	if _, err := os.Stat(root); err != nil {
		return sdk{}, errors.New("android sdk not found at " + root)
	}
	return sdk{root: root}, nil
}

func defaultSDKRoot() string {
	switch runtime.GOOS {
	case "darwin":
		home, _ := os.UserHomeDir()
		return filepath.Join(home, "Library", "Android", "sdk")
	case "windows":
		if local := os.Getenv("LOCALAPPDATA"); local != "" {
			return filepath.Join(local, "Android", "Sdk")
		}
	default:
		home, _ := os.UserHomeDir()
		return filepath.Join(home, "Android", "Sdk")
	}
	return ""
}

func (s sdk) adb() string      { return s.bin("platform-tools", "adb") }
func (s sdk) emulator() string { return s.bin("emulator", "emulator") }

// avdmanager and sdkmanager are shell scripts, so the Windows build ships .bat wrappers.
func (s sdk) avdmanager() string { return s.script("cmdline-tools", "latest", "bin", "avdmanager") }
func (s sdk) sdkmanager() string { return s.script("cmdline-tools", "latest", "bin", "sdkmanager") }

func (s sdk) bin(parts ...string) string {
	p := filepath.Join(append([]string{s.root}, parts...)...)
	if runtime.GOOS == "windows" {
		p += ".exe"
	}
	return firstExisting(p, filepath.Base(parts[len(parts)-1]))
}

func (s sdk) script(parts ...string) string {
	p := filepath.Join(append([]string{s.root}, parts...)...)
	if runtime.GOOS == "windows" {
		p += ".bat"
	}
	return firstExisting(p, filepath.Base(parts[len(parts)-1]))
}

func firstExisting(path, fallback string) string {
	if _, err := os.Stat(path); err == nil {
		return path
	}
	if p, err := exec.LookPath(fallback); err == nil {
		return p
	}
	return path
}

func avdHome() string {
	if p := os.Getenv("ANDROID_AVD_HOME"); p != "" {
		return p
	}
	if p := os.Getenv("ANDROID_USER_HOME"); p != "" {
		return filepath.Join(p, "avd")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".android", "avd")
}
