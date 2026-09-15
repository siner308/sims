package ios

import (
	"context"
	"os/exec"
	"runtime"
	"strings"

	"github.com/siner308/sims/internal/device"
)

func (p *Provider) Checks(ctx context.Context, emit func(device.Check)) {
	if runtime.GOOS != "darwin" {
		emit(device.Check{Name: "Xcode", Optional: true, Hint: "iOS simulators and devices need macOS"})
		return
	}
	xcrun, err := exec.LookPath("xcrun")
	if err != nil {
		emit(device.Check{Name: "xcrun", Hint: "Xcode command line tools missing", Fix: "xcode-select --install"})
		return
	}
	emit(device.Check{Name: "xcrun", Found: xcrun, OK: true})
	if out, err := exec.CommandContext(ctx, "xcodebuild", "-version").Output(); err == nil {
		first, _, _ := strings.Cut(string(out), "\n")
		emit(device.Check{Name: "Xcode", Found: strings.TrimPrefix(first, "Xcode "), OK: true})
	} else {
		emit(device.Check{Name: "Xcode", Hint: "full Xcode is needed for simulators and devicectl",
			Fix: "install Xcode from the App Store, then: sudo xcode-select -s /Applications/Xcode.app && sudo xcodebuild -license accept"})
	}
	for _, tool := range []string{"simctl", "devicectl"} {
		c := device.Check{Name: tool, Hint: tool + " is not answering; it ships with Xcode", Fix: "sudo xcode-select -s /Applications/Xcode.app"}
		if err := exec.CommandContext(ctx, "xcrun", tool, "help").Run(); err == nil {
			c.OK, c.Found = true, "xcrun "+tool
		}
		emit(c)
	}
	syslog := device.Check{Name: "idevicesyslog", Optional: true, Hint: "needed only for logs from physical iPhones", Fix: "brew install libimobiledevice"}
	if path, err := exec.LookPath("idevicesyslog"); err == nil {
		syslog.OK, syslog.Found = true, path
	}
	emit(syslog)
}
