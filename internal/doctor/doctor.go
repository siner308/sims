package doctor

import (
	"context"
	"fmt"
	"io"

	"github.com/siner308/sims/internal/device"
)

// Run prints one line per requirement and reports whether at least one platform is usable.
func Run(ctx context.Context, w io.Writer, providers ...device.Provider) bool {
	usable := false
	for _, p := range providers {
		fmt.Fprintf(w, "%s\n", p.Platform())
		checker, ok := p.(device.Checker)
		if !ok {
			continue
		}
		// a platform counts only when something required is present; a lone optional "skip" is not a platform
		platformOK, anyOK := true, false
		for _, c := range checker.Checks(ctx) {
			mark, detail := "ok", c.Found
			switch {
			case c.OK:
				anyOK = true
			case c.Optional:
				mark, detail = "skip", "optional: "+c.Hint
			default:
				mark, detail = "MISSING", c.Hint
				platformOK = false
			}
			fmt.Fprintf(w, "  %-8s %-14s %s\n", mark, c.Name, detail)
		}
		if platformOK && anyOK {
			usable = true
		}
	}
	if !usable {
		fmt.Fprintln(w, "\nno usable platform: install the Android SDK or Xcode and run sims doctor again")
	}
	return usable
}
