//go:build !darwin && !windows

package ui

import (
	"fmt"
	"os/exec"
)

// openWithDefault hands the file to whatever this desktop opens that kind of file with.
func (a *App) openWithDefault(path string) {
	go func() {
		err := exec.Command("xdg-open", path).Start()
		a.tv.QueueUpdateDraw(func() {
			if err != nil {
				a.flashErr(fmt.Errorf("could not open %s: %w", path, err))
				return
			}
			a.flash("opened " + path)
		})
	}()
}
