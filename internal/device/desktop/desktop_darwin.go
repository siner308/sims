package desktop

import (
	"context"
	"os/exec"
	"strings"
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
// their timestamps with the parser it already has. `log` is a zsh builtin, hence the full path.
func hostLogCmd(ctx context.Context) (*exec.Cmd, error) {
	return exec.CommandContext(ctx, "/usr/bin/log", "stream", "--style", "compact"), nil
}
