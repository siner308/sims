package desktop

import (
	"context"
	"fmt"
	"os/exec"
	"strings"

	"github.com/siner308/sims/internal/device"
)

func supported() bool { return true }

// localName is what the user called this Mac, which reads better in a device list than its hostname.
func localName() string {
	out, err := exec.Command("scutil", "--get", "ComputerName").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func osVersion(ctx context.Context) string {
	out, err := exec.CommandContext(ctx, "sw_vers", "-productVersion").Output()
	if err != nil {
		return ""
	}
	return "macOS " + strings.TrimSpace(string(out))
}

func hardwareModel(ctx context.Context) string {
	out, err := exec.CommandContext(ctx, "sysctl", "-n", "hw.model").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// hostLogCmd streams this Mac's log in the same format the simulators use, so the merged view reads
// their timestamps with the parser it already has. A non-nil app narrows it the same way a
// simulator's log is narrowed. `log` is a zsh builtin, hence the full path.
func hostLogCmd(ctx context.Context, app *device.App) (*exec.Cmd, error) {
	args := []string{"stream", "--style", "compact"}
	if app != nil {
		// the logger names a process by its executable, which can differ from the display name; the
		// subsystem is usually the bundle identifier, so either match counts
		args = append(args, "--predicate",
			fmt.Sprintf("process == %q OR subsystem == %q", app.ProcessName(), app.BundleID))
	}
	return exec.CommandContext(ctx, "/usr/bin/log", args...), nil
}
