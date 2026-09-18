package ui

import (
	"context"
	"os/exec"
	"strings"
	"time"
)

// currentSSID names the wifi network this Mac is on, which a phone's proxy profile is attached to.
// An empty answer is not an error here: only a physical iPhone needs it, and the capture says so.
func currentSSID(ctx context.Context) string {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "networksetup", "-getairportnetwork", "en0").Output()
	if err == nil {
		if name, ok := strings.CutPrefix(strings.TrimSpace(string(out)), "Current Wi-Fi Network: "); ok {
			return name
		}
	}
	out, err = exec.CommandContext(ctx, "networksetup", "-getairportnetwork", "en1").Output()
	if err != nil {
		return ""
	}
	name, _ := strings.CutPrefix(strings.TrimSpace(string(out)), "Current Wi-Fi Network: ")
	return name
}
