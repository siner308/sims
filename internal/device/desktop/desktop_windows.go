package desktop

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/siner308/sims/internal/device"
)

func supported() bool { return true }

func localName() string {
	if name := os.Getenv("COMPUTERNAME"); name != "" {
		return name
	}
	return ""
}

func osVersion(ctx context.Context) string {
	out, err := exec.CommandContext(ctx, "cmd", "/c", "ver").Output()
	if err != nil {
		return "Windows"
	}
	return strings.TrimSpace(string(out))
}

func hardwareModel(ctx context.Context) string {
	out, err := exec.CommandContext(ctx, "powershell", "-NoProfile", "-Command",
		"(Get-CimInstance Win32_ComputerSystem).Model").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// hostLogCmd follows the Windows event log, formatted so the timestamp leads the line the way the
// other platforms' logs do and the merged view can order it.
func hostLogCmd(ctx context.Context, app *device.App) (*exec.Cmd, error) {
	filter := ""
	if app != nil {
		// the event log names its source by provider, which for an app is usually its own name
		filter = fmt.Sprintf(" | Where-Object { $_.ProviderName -like '*%s*' }", app.Name)
	}
	script := `Get-WinEvent -FilterHashtable @{LogName='Application','System'} -MaxEvents 200` + filter + ` |
Sort-Object TimeCreated |
ForEach-Object { '{0} {1} {2}' -f $_.TimeCreated.ToString('yyyy-MM-dd HH:mm:ss.fff'), $_.ProviderName, ($_.Message -replace '\r?\n',' ') }`
	return exec.CommandContext(ctx, "powershell", "-NoProfile", "-Command", script), nil
}
