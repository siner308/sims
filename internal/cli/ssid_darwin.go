package cli

import (
	"context"
	"os/exec"
	"strings"
	"time"
)

// currentSSID names the wifi network this Mac is on; a physical iPhone's proxy profile attaches to it.
func currentSSID(ctx context.Context) string {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	for _, iface := range []string{"en0", "en1"} {
		out, err := exec.CommandContext(ctx, "networksetup", "-getairportnetwork", iface).Output()
		if err != nil {
			continue
		}
		if name, ok := strings.CutPrefix(strings.TrimSpace(string(out)), "Current Wi-Fi Network: "); ok {
			return name
		}
	}
	return ""
}
