package doctor

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/siner308/sims/internal/device"
)

// Run prints one line per requirement as it is probed, then a copy-paste block for whatever is
// missing, and reports whether at least one platform is usable.
func Run(ctx context.Context, w io.Writer, providers ...device.Provider) bool {
	usable := false
	var fixes []string
	seen := map[string]bool{}
	for _, p := range providers {
		checker, ok := p.(device.Checker)
		if !ok {
			continue
		}
		fmt.Fprintf(w, "%s\n", p.Platform())
		if p.Platform() == device.PlatformIOS {
			fmt.Fprintln(w, "  (the first xcrun call after installing or updating Xcode can take a minute)")
		}
		// a platform counts only when something required is present; a lone optional "skip" is not a platform
		platformOK, anyOK := true, false
		checker.Checks(ctx, func(c device.Check) {
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
			if !c.OK && c.Fix != "" && !seen[c.Fix] {
				seen[c.Fix] = true
				fixes = append(fixes, fmt.Sprintf("# %s\n%s", c.Name, c.Fix))
			}
		})
		if platformOK && anyOK {
			usable = true
		}
	}
	if len(fixes) > 0 {
		fmt.Fprintf(w, "\nto install what is missing:\n%s\n", strings.Join(fixes, "\n"))
	}
	if !usable {
		fmt.Fprintln(w, "\nno usable platform: install the Android SDK or Xcode and run sims doctor again")
	}
	return usable
}
