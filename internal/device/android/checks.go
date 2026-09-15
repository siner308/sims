package android

import (
	"context"
	"os"
	"os/exec"
	"strings"

	"github.com/siner308/sims/internal/device"
)

func (p *Provider) Checks(ctx context.Context) []device.Check {
	if p.sdkErr != nil {
		return []device.Check{{Name: "Android SDK", OK: false, Hint: "install Android Studio or the command line tools, then set ANDROID_HOME"}}
	}
	checks := []device.Check{{Name: "Android SDK", Found: p.sdk.root, OK: true}}
	bin := func(name, path, hint string, optional bool) {
		c := device.Check{Name: name, Optional: optional, Hint: hint}
		if _, err := os.Stat(path); err == nil {
			c.OK, c.Found = true, path
		} else if resolved, err := exec.LookPath(name); err == nil {
			c.OK, c.Found = true, resolved
		}
		checks = append(checks, c)
	}
	bin("adb", p.sdk.adb(), "sdkmanager platform-tools", false)
	bin("emulator", p.sdk.emulator(), "sdkmanager emulator", false)
	bin("avdmanager", p.sdk.avdmanager(), "install cmdline-tools into <sdk>/cmdline-tools/latest", false)
	bin("sdkmanager", p.sdk.sdkmanager(), "install cmdline-tools into <sdk>/cmdline-tools/latest", false)
	aapt := device.Check{Name: "aapt2", Optional: true, Hint: "sdkmanager \"build-tools;35.0.0\" (app names fall back to package ids without it)"}
	if path, err := p.sdk.aapt2(); err == nil {
		aapt.OK, aapt.Found = true, path
	}
	checks = append(checks, aapt)
	if out, err := run(ctx, p.sdk.adb(), "--version"); err == nil {
		for _, l := range lines(out) {
			if v, ok := strings.CutPrefix(l, "Version "); ok {
				checks[1].Found += " (" + strings.SplitN(v, "-", 2)[0] + ")"
			}
		}
	}
	return checks
}
