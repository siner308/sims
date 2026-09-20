package ui

import (
	"context"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// askLaunchServices lists every application registered as able to open a plain text file. The list
// is the system's own, so an editor installed anywhere and named anything is offered without sims
// knowing it exists.
// argv only exists inside a run handler, so the script has to declare one to see the file it was
// asked about.
const askLaunchServices = `use framework "AppKit"
on run argv
set u to current application's |NSURL|'s fileURLWithPath:(item 1 of argv)
set apps to current application's NSWorkspace's sharedWorkspace()'s URLsForApplicationsToOpenURL:u
set out to ""
repeat with a in apps
set out to out & (a's |path|() as text) & linefeed
end repeat
return out
end run`

func guiEditors(sample string) []guiEditor {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "osascript", "-e", askLaunchServices, sample).Output()
	if err != nil {
		return nil
	}
	var found []guiEditor
	seen := map[string]bool{}
	for _, path := range strings.Split(string(out), "\n") {
		path = strings.TrimSpace(path)
		if path == "" || !strings.HasSuffix(path, ".app") {
			continue
		}
		name := strings.TrimSuffix(filepath.Base(path), ".app")
		if seen[name] {
			// the same editor can be registered from more than one copy on disk
			continue
		}
		seen[name] = true
		found = append(found, openWith(name, path))
	}
	return sortEditors(found)
}

// openWith launches the bundle and comes back, leaving the editor running beside sims: the terminal
// stays where it is, so there is nothing to hand over and nothing to restore.
func openWith(name, path string) guiEditor {
	return guiEditor{Name: name, Open: func(file string) error {
		return exec.Command("/usr/bin/open", "-a", path, file).Run()
	}}
}
