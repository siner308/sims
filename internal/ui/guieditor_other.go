//go:build !darwin && !windows

package ui

import (
	"context"
	"os/exec"
	"strings"
	"time"
)

// guiEditors asks the desktop which applications handle plain text. The list comes from the
// installed .desktop files, so an editor is offered because the system knows about it rather than
// because sims was told its name.
func guiEditors(string) []guiEditor {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	// gio lists the handlers for a mime type, one desktop file id per line
	out, err := exec.CommandContext(ctx, "gio", "mime", "text/plain").Output()
	if err != nil {
		return nil
	}
	var found []guiEditor
	seen := map[string]bool{}
	for _, id := range desktopIDs(string(out)) {
		if seen[id] {
			continue
		}
		seen[id] = true
		name := strings.TrimSuffix(id, ".desktop")
		found = append(found, guiEditor{Name: name, Open: func(file string) error {
			return exec.Command("gio", "launch", id, file).Start()
		}})
	}
	return sortEditors(found)
}

// desktopIDs pulls the handler ids out of what gio prints, which is prose with the ids indented
// under it rather than a plain list.
func desktopIDs(out string) []string {
	var ids []string
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasSuffix(line, ".desktop") && !strings.Contains(line, " ") {
			ids = append(ids, line)
		}
	}
	return ids
}
