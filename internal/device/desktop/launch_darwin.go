package desktop

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
)

// launchApp opens an app by its bundle identifier, the way the Finder would.
func launchApp(ctx context.Context, bundleID string) error {
	out, err := exec.CommandContext(ctx, "open", "-b", bundleID).CombinedOutput()
	if err != nil {
		return fmt.Errorf("open -b %s: %w: %s", bundleID, err, strings.TrimSpace(string(out)))
	}
	return nil
}
