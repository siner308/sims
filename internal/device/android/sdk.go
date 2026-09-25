package android

import (
	"cmp"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
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

// sdkmanager installs into the SDK that contains the binary, not ANDROID_HOME; when it was found on
// PATH (Homebrew puts it under its own prefix) that would land packages outside the SDK sims uses.
func (s sdk) sdkRootFlag() string { return "--sdk_root=" + s.root }

// avdmanager has no --sdk_root and finds the SDK from its own toolsdir, ignoring ANDROID_HOME, so a Homebrew copy fails with "Valid system image paths are: null".
// The script applies AVDMANAGER_OPTS after its own toolsdir, and the user's own options go last so they still win.
func (s sdk) avdmanagerCmd(ctx context.Context, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, s.avdmanager(), args...)
	opt := "-Dcom.android.sdkmanager.toolsdir=" + filepath.Join(s.root, "cmdline-tools", "latest")
	cmd.Env = append(os.Environ(), "AVDMANAGER_OPTS="+strings.TrimSpace(scriptQuote(opt)+" "+os.Getenv("AVDMANAGER_OPTS")))
	return cmd
}

// The .bat wrapper pastes the value in as is, while the shell script word-splits it through eval.
func scriptQuote(s string) string {
	if isWindows {
		return `"` + s + `"`
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

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

// NewWithADB builds a provider that runs a given adb binary. Tests use it to assert the exact
// command line sims builds without a device attached.
func NewWithADB(adbPath string) *Provider {
	return &Provider{sdk: sdk{root: filepath.Dir(filepath.Dir(adbPath))}, labels: newLabelCache(), adbOverride: adbPath}
}
