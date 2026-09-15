package android

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/siner308/sims/internal/device"
)

const cmdlineToolsFix = "brew install --cask android-commandlinetools   # or unzip Google's commandlinetools zip into <sdk>/cmdline-tools/latest"

// The zip puts sdkmanager inside the SDK, so it finds its root on its own; the Homebrew cask does not (see sdkRootFlag).
func (s sdk) cmdlineToolsFix() string {
	return fmt.Sprintf("unzip Google's commandlinetools zip into %s   # or: brew install --cask android-commandlinetools (then pass %s to sdkmanager)",
		filepath.Join(s.root, "cmdline-tools", "latest"), s.sdkRootFlag())
}

func (s sdk) sdkmanagerFix(pkg string) string {
	return fmt.Sprintf("sdkmanager %s %q", s.sdkRootFlag(), pkg)
}

func (p *Provider) Checks(ctx context.Context, emit func(device.Check)) {
	if p.sdkErr != nil {
		emit(device.Check{Name: "Android SDK", Hint: "no SDK found; set ANDROID_HOME or install the command line tools",
			Fix: cmdlineToolsFix + "\nexport ANDROID_HOME=\"$HOME/Library/Android/sdk\"   # or wherever the tools landed"})
		return
	}
	emit(device.Check{Name: "Android SDK", Found: p.sdk.root, OK: true})
	bin := func(name, path, hint, fix string, optional bool) {
		c := device.Check{Name: name, Optional: optional, Hint: hint, Fix: fix}
		if _, err := os.Stat(path); err == nil {
			c.OK, c.Found = true, path
		} else if resolved, err := exec.LookPath(name); err == nil {
			c.OK, c.Found = true, resolved
		}
		if c.OK && name == "adb" {
			if out, err := run(ctx, path, "--version"); err == nil {
				for _, l := range lines(out) {
					if v, ok := strings.CutPrefix(l, "Version "); ok {
						c.Found += " (" + strings.SplitN(v, "-", 2)[0] + ")"
					}
				}
			}
		}
		emit(c)
	}
	bin("adb", p.sdk.adb(), "platform-tools missing", p.sdk.sdkmanagerFix("platform-tools"), false)
	bin("emulator", p.sdk.emulator(), "emulator package missing", p.sdk.sdkmanagerFix("emulator"), false)
	bin("avdmanager", p.sdk.avdmanager(), "command line tools missing", p.sdk.cmdlineToolsFix(), false)
	bin("sdkmanager", p.sdk.sdkmanager(), "command line tools missing", p.sdk.cmdlineToolsFix(), false)
	aapt := device.Check{Name: "aapt2", Optional: true, Hint: "build-tools missing; app names fall back to package ids", Fix: p.sdk.sdkmanagerFix("build-tools;35.0.0")}
	if path, err := p.sdk.aapt2(); err == nil {
		aapt.OK, aapt.Found = true, path
	}
	emit(aapt)
}
