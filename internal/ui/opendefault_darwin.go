package ui

import (
	"fmt"
	"os/exec"
)

// openWithDefault hands the file to whatever this machine opens that kind of file with, in a window
// of its own. Nothing about which application that is belongs here: the system already knows.
func (a *App) openWithDefault(path string) {
	go func() {
		err := exec.Command("/usr/bin/open", path).Run()
		a.tv.QueueUpdateDraw(func() {
			if err != nil {
				a.flashErr(fmt.Errorf("could not open %s: %w", path, err))
				return
			}
			a.flash("opened " + path)
		})
	}()
}
