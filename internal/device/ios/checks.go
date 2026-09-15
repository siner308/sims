package ios

import (
	"context"
	"os/exec"
	"runtime"
	"strings"

	"github.com/siner308/sims/internal/device"
)

func (p *Provider) Checks(ctx context.Context) []device.Check {
	if runtime.GOOS != "darwin" {
		return []device.Check{{Name: "Xcode", OK: false, Optional: true, Hint: "iOS simulators and devices need macOS"}}
	}
	xcrun, err := exec.LookPath("xcrun")
	if err != nil {
		return []device.Check{{Name: "xcrun", OK: false, Hint: "xcode-select --install"}}
	}
	checks := []device.Check{{Name: "xcrun", Found: xcrun, OK: true}}
	if out, err := exec.CommandContext(ctx, "xcodebuild", "-version").Output(); err == nil {
		first, _, _ := strings.Cut(string(out), "\n")
		checks = append(checks, device.Check{Name: "Xcode", Found: strings.TrimPrefix(first, "Xcode "), OK: true})
	} else {
		checks = append(checks, device.Check{Name: "Xcode", OK: false, Hint: "install Xcode from the App Store and run sudo xcode-select -s /Applications/Xcode.app"})
	}
	for _, tool := range []string{"simctl", "devicectl"} {
		c := device.Check{Name: tool, Hint: "ships with Xcode; run sudo xcode-select -s /Applications/Xcode.app"}
		if err := exec.CommandContext(ctx, "xcrun", tool, "help").Run(); err == nil {
			c.OK, c.Found = true, "xcrun "+tool
		}
		checks = append(checks, c)
	}
	syslog := device.Check{Name: "idevicesyslog", Optional: true, Hint: "brew install libimobiledevice (needed for logs from physical iPhones)"}
	if path, err := exec.LookPath("idevicesyslog"); err == nil {
		syslog.OK, syslog.Found = true, path
	}
	checks = append(checks, syslog)
	return checks
}
