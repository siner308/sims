package ui

import (
	"fmt"
	"os/exec"
)

// openWithDefault hands the file to whatever this machine opens that kind of file with. start is a
// shell builtin, so it is run through cmd rather than executed directly.
func (a *App) openWithDefault(path string) {
	go func() {
		err := exec.Command("cmd", "/c", "start", "", path).Start()
		a.tv.QueueUpdateDraw(func() {
			if err != nil {
				a.flashErr(fmt.Errorf("could not open %s: %w", path, err))
				return
			}
			a.flash("opened " + path)
		})
	}()
}
